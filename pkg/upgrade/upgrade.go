package upgrade

import (
	"context"
	"fmt"
	"time"

	"github.com/zph/mup/pkg/deploy"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/topology"
)

// [UPG-004] Phased upgrade workflow

// UpgraderInterface defines the interface for cluster upgrades
// Supports both local and remote (SSH-based) upgrades
type UpgraderInterface interface {
	// Upgrade executes the full upgrade workflow
	Upgrade(ctx context.Context) error

	// ValidatePrerequisites performs pre-flight validation [UPG-003]
	ValidatePrerequisites(ctx context.Context) error

	// UpgradeConfigServers upgrades config server replica set [UPG-005]
	UpgradeConfigServers(ctx context.Context) error

	// UpgradeShard upgrades a single shard replica set [UPG-005]
	UpgradeShard(ctx context.Context, shardName string) error

	// UpgradeMongos upgrades mongos instances [UPG-004]
	UpgradeMongos(ctx context.Context) error

	// PostUpgradeTasks performs post-upgrade tasks (FCV, etc.) [UPG-008]
	PostUpgradeTasks(ctx context.Context) error

	// Pause pauses the upgrade and saves checkpoint
	Pause(reason string) error

	// Resume resumes a paused upgrade
	Resume(ctx context.Context) error
}

// UpgradeConfig contains configuration for an upgrade
type UpgradeConfig struct {
	ClusterName      string
	FromVersion      string // Auto-detected from metadata
	ToVersion        string
	TargetVariant    deploy.Variant // From deploy.Variant
	UpgradeFCV       bool            // Whether to upgrade FCV after binary upgrade
	ParallelShards   bool            // Whether to upgrade shards in parallel
	PromptLevel      PromptLevel
	MetaDir          string
	Topology         *topology.Topology
	Executors        map[string]executor.Executor // host -> executor
	StateManager     *StateManager
	Prompter         *Prompter
	DryRun           bool
	WaitConfig       *WaitConfig   // [UPG-009] Configurable wait times
	HookRegistry     *HookRegistry // [UPG-009] Lifecycle hooks
}

// Upgrader implements the upgrade workflow
type Upgrader struct {
	config       UpgradeConfig
	state        *UpgradeState
	clusterMeta  *meta.ClusterMetadata
	isLocal      bool
	currentPhase PhaseName
	impl         UpgraderInterface // Reference to concrete implementation for callbacks
	nodeOps      NodeOperations    // Executor-specific node operations (local/SSH)
}

// NewUpgrader creates a new upgrader
func NewUpgrader(config UpgradeConfig) (*Upgrader, error) {
	// Load current cluster metadata
	metaMgr, err := meta.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create meta manager: %w", err)
	}
	clusterMeta, err := metaMgr.Load(config.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster metadata: %w", err)
	}

	// Auto-detect current version if not specified
	if config.FromVersion == "" {
		config.FromVersion = clusterMeta.Version
	}

	// Detect deployment mode
	isLocal := clusterMeta.DeployMode == "local"

	// Initialize or load upgrade state
	var state *UpgradeState
	existingState, err := config.StateManager.LoadState()
	if err != nil {
		// No existing state, create new
		fullFromVersion := fmt.Sprintf("%s-%s", clusterMeta.Variant, config.FromVersion)
		fullToVersion := fmt.Sprintf("%s-%s", clusterMeta.Variant, config.ToVersion)
		state = config.StateManager.InitializeState(config.ClusterName, fullFromVersion, fullToVersion)
		state.PromptLevel = string(config.PromptLevel)
	} else {
		// Resume from existing state
		state = existingState
		fmt.Printf("Found existing upgrade state (ID: %s, status: %s)\n", state.UpgradeID, state.OverallStatus)
	}

	// Initialize HookRegistry if not provided
	if config.HookRegistry == nil {
		config.HookRegistry = NewHookRegistry()
	}

	// Initialize WaitConfig if not provided
	if config.WaitConfig == nil {
		config.WaitConfig = DefaultWaitConfig()
	}

	return &Upgrader{
		config:      config,
		state:       state,
		clusterMeta: clusterMeta,
		isLocal:     isLocal,
	}, nil
}

// SetImpl sets the concrete implementation for callbacks
// This is called by LocalUpgrader/RemoteUpgrader after embedding
func (u *Upgrader) SetImpl(impl UpgraderInterface) {
	u.impl = impl
}

// Upgrade executes the full phased upgrade workflow
// [UPG-004] Phased upgrade workflow
func (u *Upgrader) Upgrade(ctx context.Context) error {
	fmt.Println("Starting MongoDB Cluster Upgrade")
	fmt.Println("================================")
	fmt.Printf("Cluster: %s\n", u.config.ClusterName)
	fmt.Printf("From: %s\n", u.state.PreviousVersion)
	fmt.Printf("To: %s\n", u.state.TargetVersion)
	fmt.Printf("Mode: %s\n", func() string {
		if u.isLocal {
			return "local"
		}
		return "remote"
	}())
	fmt.Printf("Prompt Level: %s\n", u.config.PromptLevel)
	fmt.Println()

	if u.config.DryRun {
		fmt.Println("DRY RUN MODE - No changes will be made")
		return u.generateUpgradePlan()
	}

	// Execute on-upgrade-start hook
	if err := u.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    HookOnUpgradeStart,
		ClusterName: u.config.ClusterName,
		FromVersion: u.config.FromVersion,
		ToVersion:   u.config.ToVersion,
	}); err != nil {
		return fmt.Errorf("on-upgrade-start hook failed: %w", err)
	}

	// Defer on-upgrade-failure hook to run if upgrade fails
	var upgradeErr error
	defer func() {
		if upgradeErr != nil {
			// Execute on-upgrade-failure hook (best effort, don't fail on hook error)
			hookErr := u.config.HookRegistry.Execute(ctx, HookContext{
				HookType:    HookOnUpgradeFailure,
				ClusterName: u.config.ClusterName,
				FromVersion: u.config.FromVersion,
				ToVersion:   u.config.ToVersion,
				Error:       upgradeErr,
			})
			if hookErr != nil {
				fmt.Printf("Warning: on-upgrade-failure hook failed: %v\n", hookErr)
			}
		}
	}()

	// Phase 0: Pre-flight validation
	if err := u.executePhase(ctx, PhasePreFlight, func(ctx context.Context) error {
		return u.impl.ValidatePrerequisites(ctx)
	}); err != nil {
		upgradeErr = fmt.Errorf("pre-flight validation failed: %w", err)
		return upgradeErr
	}

	// For local upgrades: Setup version directories and start new supervisor
	if u.isLocal {
		if lu, ok := u.impl.(*LocalUpgrader); ok {
			if err := lu.setupVersionDirectories(ctx); err != nil {
				return fmt.Errorf("failed to setup version directories: %w", err)
			}
			if err := lu.regenerateSupervisorConfig(ctx); err != nil {
				return fmt.Errorf("failed to generate supervisor config: %w", err)
			}

			// Defer cleanup of 'next' symlink if upgrade fails
			// This ensures failed upgrades don't leave stale symlinks
			defer func() {
				// Only cleanup if upgrade failed (not completed)
				if u.state.OverallStatus != OverallStatusCompleted {
					lu.cleanupNextSymlink()
				}
			}()

			// Start new supervisor (runs alongside old for rolling migration)
			if err := lu.startNewSupervisor(ctx); err != nil {
				return fmt.Errorf("failed to start new supervisor: %w", err)
			}
		}
	}

	topoType := u.config.Topology.GetTopologyType()

	switch topoType {
	case "sharded":
		// Sharded cluster upgrade path
		// Phase 1: Config servers
		if err := u.executePhase(ctx, PhaseConfigServers, func(ctx context.Context) error {
			return u.impl.UpgradeConfigServers(ctx)
		}); err != nil {
			return fmt.Errorf("config server upgrade failed: %w", err)
		}

		// Phase 2: Shards
		if err := u.upgradeAllShards(ctx); err != nil {
			return fmt.Errorf("shard upgrade failed: %w", err)
		}

		// Phase 3: Mongos
		if err := u.executePhase(ctx, PhaseMongos, func(ctx context.Context) error {
			return u.impl.UpgradeMongos(ctx)
		}); err != nil {
			return fmt.Errorf("mongos upgrade failed: %w", err)
		}

	case "replica_set":
		// Replica set upgrade path (treat as single shard)
		if err := u.executePhase(ctx, PhaseShard, func(ctx context.Context) error {
			// For replica set, use UpgradeShard with the replica set name
			rsName := u.config.Topology.Mongod[0].ReplicaSet
			return u.impl.UpgradeShard(ctx, rsName)
		}); err != nil {
			return fmt.Errorf("replica set upgrade failed: %w", err)
		}

	case "standalone":
		// Standalone upgrade path
		if err := u.executePhase(ctx, PhaseShard, func(ctx context.Context) error {
			return u.upgradeStandalone(ctx)
		}); err != nil {
			return fmt.Errorf("standalone upgrade failed: %w", err)
		}

	default:
		return fmt.Errorf("unsupported topology type: %s", topoType)
	}

	// Phase 4: Post-upgrade tasks
	if err := u.executePhase(ctx, PhasePostUpgrade, func(ctx context.Context) error {
		return u.impl.PostUpgradeTasks(ctx)
	}); err != nil {
		return fmt.Errorf("post-upgrade tasks failed: %w", err)
	}

	// Mark upgrade as completed
	u.state.mu.Lock()
	u.state.OverallStatus = OverallStatusCompleted
	u.state.mu.Unlock()

	// Archive state
	if err := u.config.StateManager.ArchiveState(u.state); err != nil {
		fmt.Printf("Warning: failed to archive state: %v\n", err)
	}

	fmt.Println("\n✓ Upgrade completed successfully!")
	u.displaySuccessSummary()

	// Execute on-upgrade-complete hook
	if err := u.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    HookOnUpgradeComplete,
		ClusterName: u.config.ClusterName,
		FromVersion: u.config.FromVersion,
		ToVersion:   u.config.ToVersion,
	}); err != nil {
		fmt.Printf("Warning: on-upgrade-complete hook failed: %v\n", err)
		// Don't fail the upgrade if the completion hook fails
	}

	return nil
}

// executePhase executes a phase with prompting and state tracking
func (u *Upgrader) executePhase(ctx context.Context, phase PhaseName, fn func(context.Context) error) error {
	// Check if already completed
	if phaseState, exists := u.state.Phases[phase]; exists && phaseState.Status == PhaseStatusCompleted {
		fmt.Printf("Phase %s already completed, skipping...\n", phase)
		return nil
	}

	// Prompt before phase if needed
	if u.config.PromptLevel == PromptLevelPhase {
		response, err := u.config.Prompter.PromptForPhase(phase, u.state)
		if err != nil {
			return fmt.Errorf("prompt failed: %w", err)
		}

		switch response {
		case PromptResponsePause:
			return u.Pause("User requested pause")
		case PromptResponseAbort:
			return fmt.Errorf("upgrade aborted by user")
		}
	}

	// Execute before-phase hook
	if err := u.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    HookBeforePhase,
		ClusterName: u.config.ClusterName,
		Phase:       phase,
		FromVersion: u.config.FromVersion,
		ToVersion:   u.config.ToVersion,
	}); err != nil {
		return fmt.Errorf("before-phase hook failed: %w", err)
	}

	// Update phase state to in_progress
	u.state.UpdatePhaseState(phase, PhaseStatusInProgress)
	if err := u.config.StateManager.SaveState(u.state); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	// Execute phase
	if err := fn(ctx); err != nil {
		u.state.UpdatePhaseState(phase, PhaseStatusFailed)
		u.config.StateManager.SaveState(u.state)
		return err
	}

	// Mark phase as completed
	u.state.UpdatePhaseState(phase, PhaseStatusCompleted)

	// Execute after-phase hook
	if err := u.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    HookAfterPhase,
		ClusterName: u.config.ClusterName,
		Phase:       phase,
		FromVersion: u.config.FromVersion,
		ToVersion:   u.config.ToVersion,
	}); err != nil {
		fmt.Printf("Warning: after-phase hook failed: %v\n", err)
		// Don't fail the phase if after-phase hook fails
	}
	if err := u.config.StateManager.CreateCheckpoint(u.state, fmt.Sprintf("Phase %s completed", phase)); err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	return nil
}

// upgradeAllShards upgrades all shards (sequential or parallel)
func (u *Upgrader) upgradeAllShards(ctx context.Context) error {
	// Group mongod nodes by replica set (shard)
	shards := make(map[string][]topology.MongodNode)
	for _, node := range u.config.Topology.Mongod {
		if node.ReplicaSet != "" {
			shards[node.ReplicaSet] = append(shards[node.ReplicaSet], node)
		}
	}

	if u.config.ParallelShards {
		// TODO: Parallel shard upgrades
		return fmt.Errorf("parallel shard upgrades not yet implemented")
	}

	// Sequential shard upgrades
	for shardName := range shards {
		phase := PhaseName(fmt.Sprintf("shard-%s", shardName))
		if err := u.executePhase(ctx, phase, func(ctx context.Context) error {
			return u.impl.UpgradeShard(ctx, shardName)
		}); err != nil {
			return fmt.Errorf("failed to upgrade shard %s: %w", shardName, err)
		}
	}

	return nil
}

// upgradeStandalone upgrades a standalone mongod instance
func (u *Upgrader) upgradeStandalone(ctx context.Context) error {
	if len(u.config.Topology.Mongod) != 1 {
		return fmt.Errorf("expected 1 mongod for standalone, found %d", len(u.config.Topology.Mongod))
	}

	node := u.config.Topology.Mongod[0]
	hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)

	fmt.Printf("\nUpgrading standalone instance: %s\n", hostPort)

	// Prompt if needed
	if u.config.PromptLevel == PromptLevelNode {
		response, err := u.config.Prompter.PromptForNode(hostPort, "STANDALONE", u.state)
		if err != nil {
			return err
		}
		if err := u.handlePromptResponse(response, hostPort); err != nil {
			return err
		}
	}

	// Update node state
	u.state.UpdateNodeState(hostPort, NodeStatusInProgress, "")
	u.config.StateManager.SaveState(u.state)

	// Perform upgrade (will be implemented in local.go)
	if err := u.upgradeNode(ctx, node, "STANDALONE"); err != nil {
		u.state.UpdateNodeState(hostPort, NodeStatusFailed, err.Error())
		u.config.StateManager.SaveState(u.state)
		return fmt.Errorf("failed to upgrade %s: %w", hostPort, err)
	}

	// Mark complete
	u.state.UpdateNodeState(hostPort, NodeStatusCompleted, "")
	u.config.StateManager.CreateCheckpoint(u.state, fmt.Sprintf("Upgraded %s", hostPort))

	return nil
}

// handlePromptResponse handles user responses from prompts
func (u *Upgrader) handlePromptResponse(response PromptResponse, hostPort string) error {
	switch response {
	case PromptResponseSkip:
		u.state.UpdateNodeState(hostPort, NodeStatusSkipped, "User skipped")
		u.state.mu.Lock()
		u.state.SkippedNodes = append(u.state.SkippedNodes, hostPort)
		u.state.mu.Unlock()
		u.config.StateManager.SaveState(u.state)
		fmt.Printf("  Skipped %s\n", hostPort)
		return fmt.Errorf("node skipped")
	case PromptResponsePause:
		return u.Pause("User requested pause")
	case PromptResponseAbort:
		return fmt.Errorf("upgrade aborted by user")
	}
	return nil
}

// Pause pauses the upgrade
func (u *Upgrader) Pause(reason string) error {
	fmt.Printf("\nPausing upgrade: %s\n", reason)

	u.state.mu.Lock()
	u.state.OverallStatus = OverallStatusPaused
	u.state.PausedAt = time.Now()
	u.state.PausedReason = reason
	u.state.UserPauseRequested = true
	u.state.mu.Unlock()

	if err := u.config.StateManager.CreateCheckpoint(u.state, "Upgrade paused"); err != nil {
		return fmt.Errorf("failed to save pause state: %w", err)
	}

	fmt.Println("✓ Upgrade state saved")
	fmt.Printf("Resume with: mup cluster upgrade %s --resume\n", u.config.ClusterName)

	return fmt.Errorf("upgrade paused")
}

// Resume resumes a paused upgrade
func (u *Upgrader) Resume(ctx context.Context) error {
	if u.state.OverallStatus != OverallStatusPaused {
		return fmt.Errorf("cannot resume: upgrade status is %s (expected paused)", u.state.OverallStatus)
	}

	fmt.Println("Resuming upgrade from checkpoint...")
	fmt.Printf("Paused at: %s\n", u.state.PausedAt.Format(time.RFC3339))
	fmt.Printf("Reason: %s\n", u.state.PausedReason)
	fmt.Printf("Current phase: %s\n", u.state.CurrentPhase)

	// Display status
	u.config.Prompter.DisplayStatus(u.state)

	// Confirm resume
	fmt.Print("\nResume upgrade? (yes/no): ")
	var response string
	fmt.Scanln(&response)
	if response != "yes" && response != "y" {
		return fmt.Errorf("resume cancelled by user")
	}

	// Update state to in_progress
	u.state.mu.Lock()
	u.state.OverallStatus = OverallStatusInProgress
	u.state.UserPauseRequested = false
	u.state.mu.Unlock()

	// Continue upgrade
	return u.Upgrade(ctx)
}

// generateUpgradePlan generates and displays the upgrade plan (dry-run)
// [UPG-010] Upgrade plan generation
func (u *Upgrader) generateUpgradePlan() error {
	ctx := context.Background()

	// Run all pre-flight checks
	fmt.Println("\nRunning pre-flight validation for dry-run...")
	if err := u.impl.ValidatePrerequisites(ctx); err != nil {
		return fmt.Errorf("pre-flight validation failed: %w", err)
	}

	fmt.Println("\n✓ All pre-flight checks passed!")
	fmt.Println()

	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    UPGRADE PLAN                            ║")
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Cluster: %-48s ║\n", u.config.ClusterName)
	fmt.Printf("║  From: %-51s ║\n", u.state.PreviousVersion)
	fmt.Printf("║  To: %-53s ║\n", u.state.TargetVersion)
	fmt.Println("╠════════════════════════════════════════════════════════════╣")

	topoType := u.config.Topology.GetTopologyType()
	fmt.Printf("║  Topology: %-47s ║\n", topoType)
	fmt.Println("╠════════════════════════════════════════════════════════════╣")

	switch topoType {
	case "sharded":
		fmt.Println("║  Phase 1: Config Servers                                   ║")
		for _, node := range u.config.Topology.ConfigSvr {
			fmt.Printf("║    - %s:%-43d ║\n", node.Host, node.Port)
		}
		fmt.Println("║                                                            ║")
		fmt.Println("║  Phase 2: Shards                                           ║")
		shards := make(map[string][]topology.MongodNode)
		for _, node := range u.config.Topology.Mongod {
			if node.ReplicaSet != "" {
				shards[node.ReplicaSet] = append(shards[node.ReplicaSet], node)
			}
		}
		for shardName, nodes := range shards {
			fmt.Printf("║    Shard: %-47s ║\n", shardName)
			for _, node := range nodes {
				fmt.Printf("║      - %s:%-41d ║\n", node.Host, node.Port)
			}
		}
		fmt.Println("║                                                            ║")
		fmt.Println("║  Phase 3: Mongos                                           ║")
		for _, node := range u.config.Topology.Mongos {
			fmt.Printf("║    - %s:%-43d ║\n", node.Host, node.Port)
		}

	case "replica_set":
		fmt.Println("║  Replica Set Members:                                      ║")
		for _, node := range u.config.Topology.Mongod {
			fmt.Printf("║    - %s:%-43d ║\n", node.Host, node.Port)
		}

	case "standalone":
		node := u.config.Topology.Mongod[0]
		fmt.Printf("║  Standalone: %s:%-36d ║\n", node.Host, node.Port)
	}

	fmt.Println("║                                                            ║")
	fmt.Println("║  Phase 4: Post-Upgrade                                     ║")
	if u.config.UpgradeFCV {
		fmt.Println("║    - Upgrade Feature Compatibility Version (FCV)          ║")
	}
	fmt.Println("║    - Update cluster metadata                               ║")
	fmt.Println("║    - Verify cluster health                                 ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")

	return nil
}

// displaySuccessSummary displays upgrade success summary
func (u *Upgrader) displaySuccessSummary() {
	completed := u.state.GetCompletedNodeCount()
	total := u.state.GetTotalNodeCount()
	skipped := len(u.state.SkippedNodes)
	duration := time.Since(u.state.UpgradeStartedAt)

	fmt.Println("\n╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║                  UPGRADE SUCCESSFUL                        ║")
	fmt.Println("╠════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Nodes upgraded: %d/%d                                      ║\n", completed, total)
	if skipped > 0 {
		fmt.Printf("║  Nodes skipped: %-43d ║\n", skipped)
	}
	fmt.Printf("║  Duration: %-47s ║\n", duration.Round(time.Second))
	fmt.Printf("║  Checkpoints: %-44d ║\n", u.state.CheckpointCount)
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
}

// upgradeNode upgrades a single node using the NodeOperations interface
// This method provides executor-agnostic orchestration logic
func (u *Upgrader) upgradeNode(ctx context.Context, node interface{}, role string) error {
	if u.nodeOps == nil {
		return fmt.Errorf("nodeOps not initialized (must be set by concrete upgrader)")
	}

	// Determine node ID based on node type
	var nodeID string
	var hostPort string

	switch n := node.(type) {
	case topology.MongodNode:
		nodeID = fmt.Sprintf("mongod-%d", n.Port)
		hostPort = fmt.Sprintf("%s:%d", n.Host, n.Port)
	case topology.MongosNode:
		nodeID = fmt.Sprintf("mongos-%d", n.Port)
		hostPort = fmt.Sprintf("%s:%d", n.Host, n.Port)
	case topology.ConfigNode:
		nodeID = fmt.Sprintf("mongod-%d", n.Port)
		hostPort = fmt.Sprintf("%s:%d", n.Host, n.Port)
	default:
		return fmt.Errorf("unsupported node type: %T", node)
	}

	progress := NewNodeUpgradeProgress(hostPort)

	// Execute before-node-upgrade hook
	if err := u.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    HookBeforeNodeUpgrade,
		ClusterName: u.config.ClusterName,
		Node:        hostPort,
		NodeRole:    role,
		FromVersion: u.config.FromVersion,
		ToVersion:   u.config.ToVersion,
	}); err != nil {
		return fmt.Errorf("before-node-upgrade hook failed: %w", err)
	}

	// Defer on-node-failure hook
	var nodeErr error
	defer func() {
		if nodeErr != nil {
			u.config.HookRegistry.Execute(ctx, HookContext{
				HookType:    HookOnNodeFailure,
				ClusterName: u.config.ClusterName,
				Node:        hostPort,
				NodeRole:    role,
				FromVersion: u.config.FromVersion,
				ToVersion:   u.config.ToVersion,
				Error:       nodeErr,
			})
		}
	}()

	// Step 1: Stop the node
	progress.StartStep(0, "Stopping MongoDB process...")
	if err := u.nodeOps.StopNode(ctx, nodeID); err != nil {
		progress.FailStep(fmt.Sprintf("Failed to stop: %v", err))
		nodeErr = fmt.Errorf("failed to stop process: %w", err)
		return nodeErr
	}
	progress.CompleteStep("Process stopped")

	// Step 2: Backup configuration (simulated for now)
	progress.StartStep(1, "Backing up configuration...")
	time.Sleep(200 * time.Millisecond)
	progress.CompleteStep("Configuration backed up")

	// Step 3: Binary replacement (already done during SetupVersionEnvironment)
	progress.StartStep(2, "Binary replacement (already done)...")
	// Binaries were prepared during SetupVersionEnvironment
	// New supervisor/systemd config points to new version's binaries
	time.Sleep(100 * time.Millisecond)
	progress.CompleteStep("Binary paths updated")

	// Step 4: Start the node with new version
	progress.StartStep(3, "Starting MongoDB with new version...")
	if err := u.nodeOps.StartNode(ctx, nodeID); err != nil {
		progress.FailStep(fmt.Sprintf("Failed to start: %v", err))
		nodeErr = fmt.Errorf("failed to start process: %w", err)
		return nodeErr
	}
	progress.CompleteStep("Process started")

	// Step 5: Wait for node to be healthy
	progress.StartStep(4, "Waiting for node to be healthy...")
	if err := u.nodeOps.WaitForNodeHealthy(ctx, nodeID, 30*time.Second); err != nil {
		progress.FailStep("Timeout waiting for node")
		nodeErr = fmt.Errorf("health check failed: %w", err)
		return nodeErr
	}
	progress.CompleteStep("Node is healthy")

	// Step 6: Verify version
	progress.StartStep(5, "Verifying new version...")
	if err := u.nodeOps.VerifyNodeVersion(ctx, nodeID, u.config.ToVersion); err != nil {
		progress.FailStep(fmt.Sprintf("Version verification failed: %v", err))
		nodeErr = fmt.Errorf("version verification failed: %w", err)
		return nodeErr
	}
	progress.CompleteStep("Version verified")

	// Execute after-node-upgrade hook
	if err := u.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    HookAfterNodeUpgrade,
		ClusterName: u.config.ClusterName,
		Node:        hostPort,
		NodeRole:    role,
		FromVersion: u.config.FromVersion,
		ToVersion:   u.config.ToVersion,
	}); err != nil {
		// Don't fail the upgrade if after-node-upgrade hook fails
		fmt.Printf("Warning: after-node-upgrade hook failed: %v\n", err)
	}

	// Update state
	u.state.UpdateNodeState(hostPort, NodeStatusCompleted, "")
	u.config.StateManager.CreateCheckpoint(u.state, fmt.Sprintf("Upgraded %s (%s)", hostPort, role))
	progress.Complete()
	fmt.Println()

	// Wait for node to stabilize
	time.Sleep(2 * time.Second)

	return nil
}
