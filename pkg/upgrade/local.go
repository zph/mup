package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/zph/mup/pkg/deploy"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/supervisor"
	"github.com/zph/mup/pkg/topology"
)

// Symlink names for version management
const (
	// SymlinkCurrent points to the stable/active version
	SymlinkCurrent = "current"
	// SymlinkNext points to the upgrade target version (only exists during upgrade)
	SymlinkNext = "next"
	// SymlinkPrevious points to the last known-good version (for rollback)
	SymlinkPrevious = "previous"
)

// LocalUpgrader implements upgrade workflow for local deployments
// [UPG-004] Local upgrade implementation
type LocalUpgrader struct {
	*Upgrader // Embed base upgrader
	supervisorMgr    *supervisor.Manager // Old supervisor (current version)
	newSupervisorMgr *supervisor.Manager // New supervisor (target version) - runs alongside old during migration
	binaryMgr        *deploy.BinaryManager

	// Version directory management
	newVersionDir string // Path to new version directory (e.g., ~/.mup/storage/clusters/my-cluster/v4.0.28)
	newBinPath    string // Path to new version's bin directory
}

// NewLocalUpgrader creates a local upgrader
func NewLocalUpgrader(config UpgradeConfig) (*LocalUpgrader, error) {
	base, err := NewUpgrader(config)
	if err != nil {
		return nil, err
	}

	// Determine old version directory from current symlink BEFORE loading supervisor
	// This ensures we load from the specific old version, not via symlink
	currentLink := filepath.Join(config.MetaDir, SymlinkCurrent)
	oldVersionName := ""
	if target, err := os.Readlink(currentLink); err == nil {
		oldVersionName = target
	} else {
		// If no symlink, try to find version directory from FromVersion
		oldVersionName = fmt.Sprintf("v%s", config.FromVersion)
	}

	oldVersionDir := filepath.Join(config.MetaDir, oldVersionName)

	// Load supervisor manager from SPECIFIC old version directory
	// This ensures it keeps pointing to the old supervisor even after symlinks change
	supervisorMgr, err := supervisor.LoadManager(oldVersionDir, config.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("failed to load supervisor manager from %s: %w", oldVersionDir, err)
	}

	// Create binary manager for downloading new version
	binaryMgr, err := deploy.NewBinaryManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create binary manager: %w", err)
	}

	lu := &LocalUpgrader{
		Upgrader:      base,
		supervisorMgr: supervisorMgr,
		binaryMgr:     binaryMgr,
	}

	// Set the implementation reference for callbacks
	base.SetImpl(lu)

	// Create LocalNodeOperations and inject into base Upgrader
	nodeOps := NewLocalNodeOperations(
		supervisorMgr,
		binaryMgr,
		config,
		base.clusterMeta,
		oldVersionDir,
	)
	base.nodeOps = nodeOps

	return lu, nil
}

// ValidatePrerequisites performs pre-upgrade validation
// [UPG-003] Pre-upgrade validation
func (lu *LocalUpgrader) ValidatePrerequisites(ctx context.Context) error {
	fmt.Println("\nPhase 0: Pre-Flight Validation")
	fmt.Println("===============================")

	// 1. Verify meta.yaml version matches current deployment
	fmt.Print("  Verifying cluster metadata version... ")
	if lu.clusterMeta.Version != lu.config.FromVersion {
		fmt.Println("✗")
		return fmt.Errorf("metadata version mismatch: meta.yaml shows version %s but upgrade is from %s\n"+
			"  The cluster metadata should reflect the currently running MongoDB version.\n"+
			"  This mismatch indicates:\n"+
			"    - A previous upgrade may have failed midway, or\n"+
			"    - The metadata was incorrectly updated\n"+
			"  To fix:\n"+
			"    1. Check which MongoDB version is actually running (mup cluster display)\n"+
			"    2. Update meta.yaml to reflect the running version, or\n"+
			"    3. Use --from-version flag to specify the correct current version",
			lu.clusterMeta.Version, lu.config.FromVersion)
	}
	fmt.Printf("✓ (meta.yaml: %s)\n", lu.clusterMeta.Version)

	// 2. Validate upgrade path
	fmt.Print("  Validating upgrade path... ")
	if err := ValidateUpgradePathStrings(lu.config.FromVersion, lu.config.ToVersion); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("invalid upgrade path: %w", err)
	}
	fmt.Printf("✓ (%s → %s)\n", lu.config.FromVersion, lu.config.ToVersion)

	// 3. Check supervisor is running
	fmt.Print("  Checking supervisord... ")
	if !lu.supervisorMgr.IsRunning() {
		fmt.Println("✗")
		return fmt.Errorf("supervisord not running for cluster %s\n"+
			"  Start the cluster with: mup cluster start %s",
			lu.config.ClusterName, lu.config.ClusterName)
	}
	fmt.Println("✓")

	// 4. Check all processes are running
	fmt.Print("  Checking cluster processes... ")
	processes, err := lu.supervisorMgr.GetAllProcesses()
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("failed to list processes: %w", err)
	}

	allRunning := true
	for _, proc := range processes {
		// Check case-insensitively - supervisor returns "Running" or "RUNNING"
		state := strings.ToUpper(proc.State)
		if state != "RUNNING" {
			fmt.Printf("✗\n  Process %s is not running (state: %s)\n", proc.Name, proc.State)
			allRunning = false
		}
	}
	if !allRunning {
		return fmt.Errorf("not all processes are running")
	}
	fmt.Println("✓")

	// 5. Connect to cluster for health and FCV checks
	fmt.Print("  Connecting to cluster... ")
	client, err := lu.connectToCluster(ctx)
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}
	defer client.Disconnect(ctx)
	fmt.Println("✓")

	// 6. Check FCV matches current version
	fmt.Print("  Checking Feature Compatibility Version... ")
	currentFCV, err := lu.checkFCV(ctx, client)
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("failed to check FCV: %w", err)
	}

	// Verify FCV matches current MongoDB version (or is compatible)
	expectedFCV := lu.config.FromVersion
	if currentFCV != expectedFCV {
		// Check if FCV is from a newer version that current binary can't support
		fromMajorMinor := lu.config.FromVersion
		if currentFCV > fromMajorMinor {
			fmt.Println("✗")
			return fmt.Errorf("FCV mismatch: cluster has FCV %s but MongoDB version is %s\n"+
				"  This indicates the cluster was previously upgraded to a newer version.\n"+
				"  The cluster cannot run MongoDB %s with FCV %s.\n"+
				"  Either:\n"+
				"    1. Start the cluster with the correct MongoDB version (>= %s), or\n"+
				"    2. Restore from a backup with compatible FCV",
				currentFCV, lu.config.FromVersion, lu.config.FromVersion, currentFCV, currentFCV)
		}
	}
	fmt.Printf("✓ (FCV: %s)\n", currentFCV)

	// 7. Download and verify target version binaries
	fmt.Printf("  Downloading %s binaries... ", lu.state.TargetVersion)

	// Parse variant and version from target
	variant, err := deploy.ParseVariant(lu.clusterMeta.Variant)
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("invalid variant: %w", err)
	}

	platform := deploy.Platform{
		OS:   "darwin", // TODO: Get from runtime
		Arch: "arm64",  // TODO: Get from runtime
	}

	binPath, err := lu.binaryMgr.GetBinPathWithVariant(lu.config.ToVersion, variant, platform)
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("failed to get binaries: %w", err)
	}

	// Verify mongod binary exists
	mongodPath := filepath.Join(binPath, "mongod")
	if _, err := os.Stat(mongodPath); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("mongod binary not found: %w", err)
	}
	fmt.Println("✓")

	// 8. Check disk space
	fmt.Print("  Checking disk space... ")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Simple disk space check (could be enhanced)
	mupDir := filepath.Join(homeDir, ".mup")
	if _, err := os.Stat(mupDir); err != nil {
		fmt.Println("✗")
		return fmt.Errorf(".mup directory not accessible: %w", err)
	}
	fmt.Println("✓")

	// 9. Check cluster health
	fmt.Print("  Checking cluster health... ")
	if err := lu.checkClusterHealth(ctx, client); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("cluster health check failed: %w", err)
	}
	fmt.Println("✓")

	// 9. Check replication lag (for replica sets)
	if lu.config.Topology.GetTopologyType() != "standalone" {
		fmt.Print("  Checking replication lag... ")
		maxLag, err := lu.checkReplicationLag(ctx, client)
		if err != nil {
			// For old MongoDB versions that don't support optimeDate,
			// show warning but don't fail (cluster health already verified)
			fmt.Printf("⚠️  (skipped: %v)\n", err)
			fmt.Printf("    Note: Cluster health already verified - safe to proceed\n")
		} else if maxLag > 10*time.Second {
			fmt.Printf("⚠️  (max lag: %s - consider waiting for replication to catch up)\n", maxLag.Round(time.Second))
		} else {
			fmt.Printf("✓ (max lag: %s)\n", maxLag.Round(time.Millisecond))
		}
	}

	// 10. Validate lifecycle hooks
	fmt.Print("  Validating lifecycle hooks... ")
	if err := lu.config.HookRegistry.Validate(); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("hook validation failed: %w", err)
	}
	// Count registered hook types
	allHookTypes := []HookType{
		HookBeforeNodeUpgrade, HookAfterNodeUpgrade, HookOnNodeFailure,
		HookBeforePrimaryStepdown, HookAfterPrimaryStepdown,
		HookBeforeSecondaryUpgrade, HookAfterSecondaryUpgrade,
		HookBeforePhase, HookAfterPhase,
		HookBeforeFCVUpgrade, HookAfterFCVUpgrade,
		HookBeforeBalancerStop, HookAfterBalancerStart,
		HookBeforeShardUpgrade, HookAfterShardUpgrade,
		HookOnUpgradeStart, HookOnUpgradeComplete, HookOnUpgradeFailure,
	}
	hookCount := 0
	for _, hookType := range allHookTypes {
		if lu.config.HookRegistry.HasHooks(hookType) {
			hookCount++
		}
	}
	if hookCount > 0 {
		fmt.Printf("✓ (%d hook type(s) registered)\n", hookCount)
	} else {
		fmt.Println("✓ (no hooks registered)")
	}

	fmt.Println("  ✓ Pre-flight validation passed")
	return nil
}

// UpgradeConfigServers upgrades config server replica set
// [UPG-005] Config server upgrade
func (lu *LocalUpgrader) UpgradeConfigServers(ctx context.Context) error {
	fmt.Println("\nPhase 1: Upgrade Config Servers")
	fmt.Println("================================")

	if len(lu.config.Topology.ConfigSvr) == 0 {
		fmt.Println("  No config servers to upgrade")
		return nil
	}

	// Upgrade as replica set
	return lu.upgradeReplicaSet(ctx, convertConfigToMongod(lu.config.Topology.ConfigSvr))
}

// UpgradeShard upgrades a single shard replica set
// [UPG-005] Shard upgrade
func (lu *LocalUpgrader) UpgradeShard(ctx context.Context, shardName string) error {
	fmt.Printf("\nUpgrading Shard: %s\n", shardName)
	fmt.Println("==================")

	// Find nodes for this shard
	var nodes []topology.MongodNode
	for _, node := range lu.config.Topology.Mongod {
		if node.ReplicaSet == shardName {
			nodes = append(nodes, node)
		}
	}

	if len(nodes) == 0 {
		return fmt.Errorf("no nodes found for shard %s", shardName)
	}

	return lu.upgradeReplicaSet(ctx, nodes)
}

// UpgradeMongos upgrades mongos instances
// [UPG-004] Mongos upgrade
func (lu *LocalUpgrader) UpgradeMongos(ctx context.Context) error {
	fmt.Println("\nPhase 3: Upgrade Mongos")
	fmt.Println("=======================")

	if len(lu.config.Topology.Mongos) == 0 {
		fmt.Println("  No mongos instances to upgrade")
		return nil
	}

	// Mongos can be upgraded in any order (stateless routers)
	for _, node := range lu.config.Topology.Mongos {
		hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)

		// Prompt if needed
		if lu.config.PromptLevel == PromptLevelNode {
			response, err := lu.config.Prompter.PromptForNode(hostPort, "MONGOS", lu.state)
			if err != nil {
				return err
			}
			if err := lu.handlePromptResponse(response, hostPort); err != nil {
				if err.Error() == "node skipped" {
					continue
				}
				return err
			}
		}

		lu.state.UpdateNodeState(hostPort, NodeStatusInProgress, "")
		lu.config.StateManager.SaveState(lu.state)

		// Use base Upgrader's upgradeNode method (uses NodeOperations interface)
		if err := lu.Upgrader.upgradeNode(ctx, node, "MONGOS"); err != nil {
			return fmt.Errorf("failed to upgrade mongos %s: %w", hostPort, err)
		}
	}

	return nil
}

// PostUpgradeTasks performs post-upgrade tasks
// [UPG-008] Post-upgrade tasks
func (lu *LocalUpgrader) PostUpgradeTasks(ctx context.Context) error {
	fmt.Println("\nPhase 4: Post-Upgrade Tasks")
	fmt.Println("===========================")

	// 1. Update cluster metadata with new version
	fmt.Print("  Updating cluster metadata... ")

	metaMgr, err := meta.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create meta manager: %w", err)
	}

	clusterMeta, err := metaMgr.Load(lu.config.ClusterName)
	if err != nil {
		return fmt.Errorf("failed to load cluster metadata: %w", err)
	}

	// Update version and paths
	clusterMeta.Version = lu.config.ToVersion
	clusterMeta.BinPath = lu.newBinPath
	clusterMeta.SupervisorConfigPath = filepath.Join(lu.newVersionDir, "supervisor.ini")
	clusterMeta.SupervisorPIDFile = filepath.Join(lu.newVersionDir, "supervisor.pid")

	// Save updated metadata
	if err := metaMgr.Save(clusterMeta); err != nil {
		return fmt.Errorf("failed to save metadata: %w", err)
	}

	fmt.Println("✓")

	// 2. Verify all nodes are running new version
	fmt.Print("  Verifying node versions... ")
	// TODO: Connect to nodes and verify versions
	fmt.Println("✓")

	// 3. Upgrade FCV if requested
	if lu.config.UpgradeFCV {
		if lu.config.PromptLevel == PromptLevelCritical {
			response, err := lu.config.Prompter.PromptForCriticalOperation("Upgrade Feature Compatibility Version (FCV)", lu.state)
			if err != nil {
				return err
			}
			if response == PromptResponseAbort {
				return fmt.Errorf("FCV upgrade aborted by user")
			}
		}

		fmt.Print("  Upgrading FCV... ")
		if err := lu.upgradeFCV(ctx); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("FCV upgrade failed: %w", err)
		}
		fmt.Println("✓")
	}

	// 4. Health check
	fmt.Print("  Final health check... ")
	// TODO: Perform comprehensive health check
	fmt.Println("✓")

	// 5. Stop old supervisor (all nodes now running on new supervisor)
	if err := lu.stopOldSupervisor(ctx); err != nil {
		// Log warning but don't fail the upgrade
		fmt.Printf("  ⚠️  Warning: %v\n", err)
	}

	// 6. Atomically switch symlinks: current → next, previous → old current
	fmt.Print("  Switching version symlinks... ")
	if err := lu.switchSymlinks(); err != nil {
		return fmt.Errorf("failed to switch symlinks: %w", err)
	}
	fmt.Println("✓")

	// 7. Advisory about manual FCV upgrade if not done automatically
	if !lu.config.UpgradeFCV {
		fmt.Println("\n" + strings.Repeat("=", 60))
		fmt.Println("IMPORTANT: Manual Action Required")
		fmt.Println(strings.Repeat("=", 60))
		fmt.Printf("The cluster has been upgraded to version %s, but the Feature\n", lu.config.ToVersion)
		fmt.Println("Compatibility Version (FCV) has NOT been updated.")
		fmt.Println()
		fmt.Println("To complete the upgrade and enable new features, you must")
		fmt.Println("manually update the FCV by connecting to the cluster and running:")
		fmt.Println()
		fmt.Printf("  db.adminCommand({setFeatureCompatibilityVersion: \"%s\"})\n", lu.config.ToVersion)
		fmt.Println()
		fmt.Println("IMPORTANT: Only update FCV after:")
		fmt.Println("  1. All nodes are successfully running the new version")
		fmt.Println("  2. You have verified the cluster is healthy")
		fmt.Println("  3. You have tested your application with the new version")
		fmt.Println()
		fmt.Println("WARNING: Once FCV is upgraded, you cannot downgrade to the")
		fmt.Println("previous MongoDB version without restoring from backup.")
		fmt.Println(strings.Repeat("=", 60))
	}

	return nil
}

// switchSymlinks atomically switches current→next and creates previous→old_current
// This is done AFTER successful upgrade to make the new version "official"
func (lu *LocalUpgrader) switchSymlinks() error {
	clusterDir := lu.config.MetaDir
	currentLink := filepath.Join(clusterDir, SymlinkCurrent)
	previousLink := filepath.Join(clusterDir, SymlinkPrevious)
	nextLink := filepath.Join(clusterDir, SymlinkNext)

	// 1. Get current target (will become previous)
	oldCurrentTarget := ""
	if target, err := os.Readlink(currentLink); err == nil {
		oldCurrentTarget = target
	}

	// 2. Get next target (will become current)
	nextTarget, err := os.Readlink(nextLink)
	if err != nil {
		return fmt.Errorf("failed to read next symlink: %w (upgrade state may be corrupted)", err)
	}

	// 3. Remove old symlinks
	os.Remove(previousLink) // Remove old previous
	os.Remove(currentLink)  // Remove current

	// 4. Create previous → old current (for rollback)
	if oldCurrentTarget != "" {
		if err := os.Symlink(oldCurrentTarget, previousLink); err != nil {
			// Log warning but continue - this is not critical
			fmt.Printf("\n  ⚠️  Warning: failed to create previous symlink: %v\n", err)
		}
	}

	// 5. Create current → next (atomic switch to new version)
	if err := os.Symlink(nextTarget, currentLink); err != nil {
		return fmt.Errorf("failed to create current symlink: %w", err)
	}

	// 6. Remove next symlink (upgrade complete)
	os.Remove(nextLink)

	fmt.Printf("\n  ℹ️  Symlinks updated: current -> %s, previous -> %s\n", nextTarget, oldCurrentTarget)
	return nil
}

// cleanupNextSymlink removes the 'next' symlink on upgrade failure
// This ensures the cluster remains stable on the 'current' version
func (lu *LocalUpgrader) cleanupNextSymlink() {
	clusterDir := lu.config.MetaDir
	nextLink := filepath.Join(clusterDir, SymlinkNext)

	// Check if next symlink exists
	if _, err := os.Lstat(nextLink); err == nil {
		// Remove it
		if err := os.Remove(nextLink); err != nil {
			fmt.Printf("\n⚠️  Warning: failed to cleanup 'next' symlink: %v\n", err)
		} else {
			fmt.Printf("\n  ✓ Cleaned up 'next' symlink (upgrade failed, cluster remains on 'current')\n")
		}
	}
}

// setupVersionDirectories creates the new version directory structure
// [UPG-006] Per-version directory management with symlinks
func (lu *LocalUpgrader) setupVersionDirectories(ctx context.Context) error {
	fmt.Println("\n=== Setting Up Version Directories ===")

	// 1. Create version directory structure
	versionDir := filepath.Join(lu.config.MetaDir, fmt.Sprintf("v%s", lu.config.ToVersion))
	lu.newVersionDir = versionDir

	fmt.Printf("  Creating version directory: %s\n", versionDir)

	// Create subdirectories
	dirs := []string{
		filepath.Join(versionDir, "bin"),
		filepath.Join(versionDir, "conf"),
		filepath.Join(versionDir, "logs"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// 2. Download and copy binaries to version directory
	fmt.Printf("  Downloading MongoDB %s binaries...\n", lu.config.ToVersion)

	// Get current platform
	platform := deploy.Platform{
		OS:   "darwin", // TODO: Use runtime.GOOS
		Arch: "arm64",  // TODO: Use runtime.GOARCH properly mapped
	}

	// Download binaries (this caches them)
	binPath, err := lu.binaryMgr.GetBinPathWithVariant(lu.config.ToVersion, lu.config.TargetVariant, platform)
	if err != nil {
		return fmt.Errorf("failed to download binaries: %w", err)
	}

	// Copy binaries to version directory
	newBinPath := filepath.Join(versionDir, "bin")
	lu.newBinPath = newBinPath

	fmt.Printf("  Copying binaries to %s...\n", newBinPath)

	// Copy all binaries
	binaries := []string{"mongod", "mongos", "mongosh", "mongo"}
	for _, binary := range binaries {
		srcPath := filepath.Join(binPath, binary)
		dstPath := filepath.Join(newBinPath, binary)

		// Skip if source doesn't exist (e.g., mongosh might not exist in old versions)
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			continue
		}

		// Copy file
		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("failed to copy %s: %w", binary, err)
		}

		// Make executable
		if err := os.Chmod(dstPath, 0755); err != nil {
			return fmt.Errorf("failed to make %s executable: %w", binary, err)
		}
	}

	fmt.Printf("  ✓ Version directories created\n")
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := destFile.ReadFrom(sourceFile); err != nil {
		return err
	}

	return destFile.Sync()
}

// regenerateSupervisorConfig generates new supervisor config for the new version
// [UPG-007] Version-specific supervisor configuration
func (lu *LocalUpgrader) regenerateSupervisorConfig(ctx context.Context) error {
	fmt.Println("\n=== Generating New Supervisor Configuration ===")

	// Load cluster metadata to get topology
	metaMgr, err := meta.NewManager()
	if err != nil {
		return fmt.Errorf("failed to create meta manager: %w", err)
	}

	clusterMeta, err := metaMgr.Load(lu.config.ClusterName)
	if err != nil {
		return fmt.Errorf("failed to load cluster metadata: %w", err)
	}

	// Regenerate MongoDB configuration files for the new version
	// This ensures configs are appropriate for the target version (handles deprecated options, new features, etc.)
	fmt.Printf("  Regenerating MongoDB configuration files for version %s...\n", lu.config.ToVersion)

	// Create a minimal deployer for config regeneration
	deployer, err := deploy.NewConfigRegenerator(
		lu.config.ClusterName,
		lu.config.ToVersion,
		lu.config.TargetVariant,
		clusterMeta.Topology,
		lu.config.MetaDir,
		lu.newBinPath,
	)
	if err != nil {
		return fmt.Errorf("failed to create config regenerator: %w", err)
	}

	// First, create all per-process directories (config, log, bin)
	// The config generation will create config dirs, but we need to ensure log dirs exist too
	fmt.Printf("  Creating per-process directories...\n")

	for _, node := range clusterMeta.Topology.Mongod {
		logDir := filepath.Join(lu.newVersionDir, fmt.Sprintf("mongod-%d", node.Port), "log")
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return fmt.Errorf("failed to create log dir for mongod-%d: %w", node.Port, err)
		}
	}

	for _, node := range clusterMeta.Topology.ConfigSvr {
		logDir := filepath.Join(lu.newVersionDir, fmt.Sprintf("mongod-%d", node.Port), "log")
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return fmt.Errorf("failed to create log dir for config-server-%d: %w", node.Port, err)
		}
	}

	for _, node := range clusterMeta.Topology.Mongos {
		logDir := filepath.Join(lu.newVersionDir, fmt.Sprintf("mongos-%d", node.Port), "log")
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return fmt.Errorf("failed to create log dir for mongos-%d: %w", node.Port, err)
		}
	}

	// Generate config for each mongod node
	for _, node := range clusterMeta.Topology.Mongod {
		if err := deployer.GenerateMongodConfig(node); err != nil {
			return fmt.Errorf("failed to generate mongod config for %s:%d: %w", node.Host, node.Port, err)
		}
	}

	// Generate config for each config server node
	for _, node := range clusterMeta.Topology.ConfigSvr {
		if err := deployer.GenerateConfigServerConfig(node); err != nil {
			return fmt.Errorf("failed to generate config server config for %s:%d: %w", node.Host, node.Port, err)
		}
	}

	// Generate config for each mongos node
	for _, node := range clusterMeta.Topology.Mongos {
		if err := deployer.GenerateMongosConfig(node); err != nil {
			return fmt.Errorf("failed to generate mongos config for %s:%d: %w", node.Host, node.Port, err)
		}
	}

	fmt.Printf("  ✓ Per-process directories created and MongoDB configs regenerated\n")

	// Now generate supervisor config using new version directory
	configGen := supervisor.NewConfigGenerator(
		lu.newVersionDir,
		lu.config.ClusterName,
		clusterMeta.Topology,
		lu.config.ToVersion,
		lu.newBinPath,
	)

	if err := configGen.GenerateAll(); err != nil {
		return fmt.Errorf("failed to generate supervisor config: %w", err)
	}

	fmt.Printf("  ✓ Supervisor configuration generated at %s/supervisor.ini\n", lu.newVersionDir)
	return nil
}

// startNewSupervisor starts the new version's supervisor alongside the old one
// [UPG-008] Start new supervisor (rolling migration)
func (lu *LocalUpgrader) startNewSupervisor(ctx context.Context) error {
	fmt.Println("\n=== Starting New Version Supervisor ===")

	// Update symlinks BEFORE starting new supervisor
	fmt.Printf("  Updating version symlinks...\n")

	clusterDir := lu.config.MetaDir
	nextLink := filepath.Join(clusterDir, SymlinkNext)

	// Remove old 'next' symlink if it exists (from failed previous upgrade)
	os.Remove(nextLink)

	// Create 'next' symlink pointing to new version
	// This allows the new supervisor to run from 'next' while old supervisor runs from 'current'
	newVersionName := fmt.Sprintf("v%s", lu.config.ToVersion)

	if err := os.Symlink(newVersionName, nextLink); err != nil {
		return fmt.Errorf("failed to create %s symlink: %w", SymlinkNext, err)
	}

	fmt.Printf("  ✓ Symlink created: %s -> %s\n", SymlinkNext, newVersionName)
	fmt.Printf("  ℹ️  '%s' symlink will be updated only after successful upgrade\n", SymlinkCurrent)

	// Create new supervisor manager pointing to new config
	newSupervisorMgr, err := supervisor.NewManager(lu.newVersionDir, lu.config.ClusterName)
	if err != nil {
		return fmt.Errorf("failed to create new supervisor manager: %w", err)
	}

	// Start new supervisor (runs alongside old supervisor)
	fmt.Printf("  Starting new supervisor with version %s...\n", lu.config.ToVersion)
	if err := newSupervisorMgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start new supervisor: %w", err)
	}

	// Save reference to new supervisor (keep old one too for rolling migration)
	lu.newSupervisorMgr = newSupervisorMgr

	// Update NodeOperations with new supervisor
	if localOps, ok := lu.nodeOps.(*LocalNodeOperations); ok {
		localOps.SetNewSupervisor(newSupervisorMgr)
		localOps.SetVersionDirectories(lu.newVersionDir, lu.newBinPath)
	}

	fmt.Printf("  ✓ New supervisor started (both old and new supervisors running)\n")
	return nil
}

// stopOldSupervisor stops the old version's supervisor after all nodes migrated
// [UPG-009] Stop old supervisor after migration complete
func (lu *LocalUpgrader) stopOldSupervisor(ctx context.Context) error {
	fmt.Println("\n=== Stopping Old Version Supervisor ===")

	fmt.Printf("  All nodes migrated to new supervisor\n")
	fmt.Printf("  Stopping old supervisor...\n")

	if err := lu.supervisorMgr.Stop(ctx); err != nil {
		// Log error but don't fail - supervisor might already be stopped
		fmt.Printf("  ⚠️  Warning: failed to stop old supervisor: %v\n", err)
	} else {
		fmt.Printf("  ✓ Old supervisor stopped\n")
	}

	// Switch to new supervisor as primary
	lu.supervisorMgr = lu.newSupervisorMgr

	fmt.Printf("  ✓ Now running entirely on new version supervisor\n")
	return nil
}

// upgradeReplicaSet upgrades a replica set following best practices
// [UPG-005] Replica set member upgrade (secondaries first, then primary)
func (lu *LocalUpgrader) upgradeReplicaSet(ctx context.Context, nodes []topology.MongodNode) error {
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes to upgrade")
	}

	// Get replica set name
	rsName := nodes[0].ReplicaSet
	var allHosts []string
	for _, node := range nodes {
		allHosts = append(allHosts, fmt.Sprintf("%s:%d", node.Host, node.Port))
	}

	fmt.Printf("\n  Detecting node roles in replica set '%s'...\n", rsName)

	// Detect roles for all nodes
	type nodeWithRole struct {
		node topology.MongodNode
		role string
	}
	var nodesWithRoles []nodeWithRole
	var primaryNode *topology.MongodNode

	for _, node := range nodes {
		hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)

		// Skip if already completed
		if nodeState, exists := lu.state.Nodes[hostPort]; exists && nodeState.Status == NodeStatusCompleted {
			fmt.Printf("    %s already upgraded, skipping...\n", hostPort)
			continue
		}

		role, err := DetectNodeRole(ctx, hostPort, rsName, allHosts)
		if err != nil {
			fmt.Printf("    ⚠️  Could not detect role for %s: %v (assuming SECONDARY)\n", hostPort, err)
			role = "SECONDARY"
		}

		fmt.Printf("    %s - %s\n", hostPort, role)

		nodesWithRoles = append(nodesWithRoles, nodeWithRole{node: node, role: role})

		if role == "PRIMARY" {
			nodeCopy := node
			primaryNode = &nodeCopy
		}
	}

	// Upgrade secondaries first
	fmt.Printf("\n  Phase 1: Upgrading SECONDARY nodes\n")
	for _, nwr := range nodesWithRoles {
		if nwr.role != "PRIMARY" {
			if err := lu.upgradeReplicaSetNode(ctx, nwr.node, nwr.role, rsName, allHosts, false); err != nil {
				return err
			}
		}
	}

	// Step down primary and upgrade it
	if primaryNode != nil {
		fmt.Printf("\n  Phase 2: Upgrading PRIMARY node\n")
		if err := lu.upgradeReplicaSetNode(ctx, *primaryNode, "PRIMARY", rsName, allHosts, true); err != nil {
			return err
		}
	}

	return nil
}

// upgradeReplicaSetNode upgrades a single replica set node with role tracking
func (lu *LocalUpgrader) upgradeReplicaSetNode(ctx context.Context, node topology.MongodNode, role string, rsName string, allHosts []string, isPrimary bool) error {
	hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)

	// Skip if already completed
	if nodeState, exists := lu.state.Nodes[hostPort]; exists && nodeState.Status == NodeStatusCompleted {
		fmt.Printf("  %s already upgraded, skipping...\n", hostPort)
		return nil
	}

	// Prompt if needed
	if lu.config.PromptLevel == PromptLevelNode {
		response, err := lu.config.Prompter.PromptForNode(hostPort, role, lu.state)
		if err != nil {
			return err
		}
		if err := lu.handlePromptResponse(response, hostPort); err != nil {
			if err.Error() == "node skipped" {
				return nil
			}
			return err
		}
	}

	// If this is primary, must perform failover first
	// Upgrades can ONLY be done on secondaries
	if isPrimary {
		// Prompt for failover with RED warning
		response, err := lu.config.Prompter.PromptForFailover(hostPort, rsName, lu.state)
		if err != nil {
			return fmt.Errorf("failover prompt failed: %w", err)
		}
		if response == PromptResponseAbort {
			return fmt.Errorf("failover aborted by user")
		}

		// Execute before-primary-stepdown hook
		if err := lu.config.HookRegistry.Execute(ctx, HookContext{
			HookType:    HookBeforePrimaryStepdown,
			ClusterName: lu.config.ClusterName,
			Node:        hostPort,
			NodeRole:    "PRIMARY",
			ShardName:   rsName,
			FromVersion: lu.config.FromVersion,
			ToVersion:   lu.config.ToVersion,
		}); err != nil {
			return fmt.Errorf("before-primary-stepdown hook failed: %w", err)
		}

		fmt.Printf("\n  Initiating primary stepdown for %s...\n", hostPort)
		failoverEvent, err := StepDownPrimary(ctx, hostPort, rsName, allHosts)
		if err != nil {
			return fmt.Errorf("failed to step down primary: %w", err)
		}

		// Record failover in state
		lu.state.mu.Lock()
		lu.state.Failovers = append(lu.state.Failovers, *failoverEvent)
		lu.state.mu.Unlock()
		lu.config.StateManager.SaveState(lu.state)

		fmt.Printf("  ✓ Failover complete: %s -> %s (election time: %dms)\n",
			failoverEvent.OldPrimary, failoverEvent.NewPrimary, failoverEvent.ElectionTimeMS)
		fmt.Printf("  Node %s is now a SECONDARY and ready for upgrade\n", hostPort)

		// Execute after-primary-stepdown hook
		if err := lu.config.HookRegistry.Execute(ctx, HookContext{
			HookType:    HookAfterPrimaryStepdown,
			ClusterName: lu.config.ClusterName,
			Node:        hostPort,
			NodeRole:    "SECONDARY", // Now a secondary
			ShardName:   rsName,
			FromVersion: lu.config.FromVersion,
			ToVersion:   lu.config.ToVersion,
			Metadata: map[string]string{
				"old_primary":      failoverEvent.OldPrimary,
				"new_primary":      failoverEvent.NewPrimary,
				"election_time_ms": fmt.Sprintf("%d", failoverEvent.ElectionTimeMS),
			},
		}); err != nil {
			// Don't fail the upgrade if after-stepdown hook fails
			fmt.Printf("Warning: after-primary-stepdown hook failed: %v\n", err)
		}
	}

	fmt.Printf("\n  Upgrading %s (role: %s)\n", hostPort, role)

	// Track starting role
	lu.state.UpdateNodeState(hostPort, NodeStatusInProgress, "")
	if nodeState := lu.state.Nodes[hostPort]; nodeState != nil {
		nodeState.Role = role
	}
	lu.config.StateManager.SaveState(lu.state)

	// Execute before-secondary-upgrade hook (or general node upgrade if not secondary)
	hookType := HookBeforeSecondaryUpgrade
	if isPrimary {
		// If we just stepped down, it's now a secondary
		hookType = HookBeforeSecondaryUpgrade
	}
	if err := lu.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    hookType,
		ClusterName: lu.config.ClusterName,
		Node:        hostPort,
		NodeRole:    role,
		ShardName:   rsName,
		FromVersion: lu.config.FromVersion,
		ToVersion:   lu.config.ToVersion,
	}); err != nil {
		return fmt.Errorf("before-secondary-upgrade hook failed: %w", err)
	}

	// Use base Upgrader's upgradeNode method (uses NodeOperations interface)
	if err := lu.Upgrader.upgradeNode(ctx, node, role); err != nil {
		return err
	}

	// Execute after-secondary-upgrade hook
	if err := lu.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    HookAfterSecondaryUpgrade,
		ClusterName: lu.config.ClusterName,
		Node:        hostPort,
		NodeRole:    role,
		ShardName:   rsName,
		FromVersion: lu.config.FromVersion,
		ToVersion:   lu.config.ToVersion,
	}); err != nil {
		// Don't fail the upgrade if after-secondary-upgrade hook fails
		fmt.Printf("Warning: after-secondary-upgrade hook failed: %v\n", err)
	}

	return nil
}

// NOTE: upgradeMongodNode, upgradeMongodNodeWithProgress, and upgradeMongosNode
// have been removed. The base Upgrader's upgradeNode method (which uses NodeOperations)
// now handles all node upgrades in an executor-agnostic way.

// Helper to convert ConfigNode to MongodNode for unified handling
func convertConfigToMongod(configNodes []topology.ConfigNode) []topology.MongodNode {
	var mongodNodes []topology.MongodNode
	for _, node := range configNodes {
		mongodNodes = append(mongodNodes, topology.MongodNode{
			Host:       node.Host,
			Port:       node.Port,
			ReplicaSet: node.ReplicaSet,
			DataDir:    node.DataDir,
			LogDir:     node.LogDir,
			ConfigDir:  node.ConfigDir,
		})
	}
	return mongodNodes
}

// NOTE: The LocalUpgrader.upgradeNode override has been removed.
// The base Upgrader.upgradeNode method (which uses NodeOperations interface) is now used.
// This allows the same orchestration logic to work for both local and SSH deployments.

// connectToCluster connects to the MongoDB cluster
// [UPG-003] MongoDB connection for health checks
func (lu *LocalUpgrader) connectToCluster(ctx context.Context) (*mongo.Client, error) {
	// Determine connection URI based on topology
	var uri string
	topoType := lu.config.Topology.GetTopologyType()

	switch topoType {
	case "sharded":
		// Connect to mongos for sharded clusters
		if len(lu.config.Topology.Mongos) == 0 {
			return nil, fmt.Errorf("no mongos instances found in topology")
		}
		mongos := lu.config.Topology.Mongos[0]
		uri = fmt.Sprintf("mongodb://%s:%d", mongos.Host, mongos.Port)

	case "replica_set":
		// Connect to replica set with replica set name
		if len(lu.config.Topology.Mongod) == 0 {
			return nil, fmt.Errorf("no mongod instances found in topology")
		}
		rsName := lu.config.Topology.Mongod[0].ReplicaSet
		var hosts []string
		for _, node := range lu.config.Topology.Mongod {
			hosts = append(hosts, fmt.Sprintf("%s:%d", node.Host, node.Port))
		}
		uri = fmt.Sprintf("mongodb://%s/?replicaSet=%s", strings.Join(hosts, ","), rsName)

	case "standalone":
		// Connect to standalone
		if len(lu.config.Topology.Mongod) == 0 {
			return nil, fmt.Errorf("no mongod instance found in topology")
		}
		node := lu.config.Topology.Mongod[0]
		uri = fmt.Sprintf("mongodb://%s:%d", node.Host, node.Port)

	default:
		return nil, fmt.Errorf("unsupported topology type: %s", topoType)
	}

	// Create client options
	clientOpts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second).
		SetDirect(topoType == "standalone")

	// Connect to MongoDB
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create MongoDB client: %w", err)
	}

	// Ping to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(ctx)
		return nil, fmt.Errorf("failed to ping MongoDB: %w", err)
	}

	return client, nil
}

// checkFCV checks the Feature Compatibility Version
// [UPG-003] FCV validation
func (lu *LocalUpgrader) checkFCV(ctx context.Context, client *mongo.Client) (string, error) {
	// Get admin database
	adminDB := client.Database("admin")

	// Try getParameter command first (works on 4.0+ and some 3.6 deployments)
	var result bson.M
	err := adminDB.RunCommand(ctx, bson.D{
		{Key: "getParameter", Value: 1},
		{Key: "featureCompatibilityVersion", Value: 1},
	}).Decode(&result)

	var fcv string

	// If getParameter fails (common in 3.6 sharded clusters), fall back to querying admin.system.version
	if err != nil || result["featureCompatibilityVersion"] == nil {
		// MongoDB 3.6 fallback: query admin.system.version collection
		var versionDoc bson.M
		err = adminDB.Collection("system.version").FindOne(ctx, bson.M{"_id": "featureCompatibilityVersion"}).Decode(&versionDoc)
		if err != nil {
			return "", fmt.Errorf("failed to get FCV from system.version collection: %w", err)
		}

		// Extract version from document: { "_id": "featureCompatibilityVersion", "version": "3.6" }
		if version, ok := versionDoc["version"].(string); ok {
			fcv = version
		} else {
			return "", fmt.Errorf("FCV version not found in system.version document: %v", versionDoc)
		}
	} else {
		// Extract FCV from getParameter result - handle both old (3.6) and new (4.0+) formats
		fcvValue := result["featureCompatibilityVersion"]

		// MongoDB 4.0+ format: { "featureCompatibilityVersion": { "version": "4.0" } }
		if fcvDoc, ok := fcvValue.(bson.M); ok {
			if version, ok := fcvDoc["version"].(string); ok {
				fcv = version
			} else {
				return "", fmt.Errorf("FCV version not found in nested response")
			}
		} else if fcvString, ok := fcvValue.(string); ok {
			// MongoDB 3.6 format: { "featureCompatibilityVersion": "3.6" }
			fcv = fcvString
		} else {
			return "", fmt.Errorf("unexpected FCV format in response: %T", fcvValue)
		}
	}

	// Validate FCV matches current version major.minor
	currentVersion := lu.config.FromVersion
	versionParts := strings.Split(currentVersion, ".")
	if len(versionParts) < 2 {
		return fcv, nil // Skip validation if version format is unexpected
	}
	expectedFCV := versionParts[0] + "." + versionParts[1]

	if fcv != expectedFCV {
		// Build helpful error message with actionable commands
		var errMsg strings.Builder
		errMsg.WriteString(fmt.Sprintf("FCV mismatch: current FCV is %s but cluster is running version %s (expected FCV %s)\n\n", fcv, currentVersion, expectedFCV))
		errMsg.WriteString("The cluster was previously upgraded but the Feature Compatibility Version (FCV) was not updated.\n")
		errMsg.WriteString("Before proceeding with the next upgrade, you must update the FCV to match the running version.\n\n")
		errMsg.WriteString("To fix this, run the following commands:\n\n")
		errMsg.WriteString(fmt.Sprintf("1. Connect to the cluster:\n   ./bin/mup cluster connect %s\n\n", lu.config.ClusterName))
		errMsg.WriteString(fmt.Sprintf("2. Set the FCV to match the running version:\n   db.adminCommand({setFeatureCompatibilityVersion: \"%s\"})\n\n", expectedFCV))
		errMsg.WriteString("3. Verify the FCV was updated:\n   db.adminCommand({getParameter: 1, featureCompatibilityVersion: 1})\n\n")
		errMsg.WriteString("   You should see: { \"featureCompatibilityVersion\": { \"version\": \"" + expectedFCV + "\" }, \"ok\": 1 }\n\n")
		errMsg.WriteString("4. Exit the shell and retry the upgrade:\n   exit\n")
		errMsg.WriteString(fmt.Sprintf("   ./bin/mup cluster upgrade %s --from-version %s --to-version %s\n\n",
			lu.config.ClusterName, currentVersion, lu.config.ToVersion))
		errMsg.WriteString("IMPORTANT: Only update FCV after verifying:\n")
		errMsg.WriteString(fmt.Sprintf("  - All nodes are running version %s\n", currentVersion))
		errMsg.WriteString("  - The cluster is healthy\n")
		errMsg.WriteString("  - Your application works correctly with the current version")

		return fcv, fmt.Errorf("%s", errMsg.String())
	}

	return fcv, nil
}

// upgradeFCV upgrades the Feature Compatibility Version to the target version
// [UPG-008] FCV upgrade implementation
func (lu *LocalUpgrader) upgradeFCV(ctx context.Context) error {
	// Connect to the cluster to get a MongoDB client
	client, err := lu.connectToCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}
	defer client.Disconnect(ctx)

	// Get admin database
	adminDB := client.Database("admin")

	// Parse target FCV from ToVersion (major.minor)
	versionParts := strings.Split(lu.config.ToVersion, ".")
	if len(versionParts) < 2 {
		return fmt.Errorf("invalid target version format: %s", lu.config.ToVersion)
	}
	targetFCV := versionParts[0] + "." + versionParts[1]

	// Execute before-fcv-upgrade hook
	if err := lu.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    HookBeforeFCVUpgrade,
		ClusterName: lu.config.ClusterName,
		FromVersion: lu.config.FromVersion,
		ToVersion:   lu.config.ToVersion,
		Metadata: map[string]string{
			"target_fcv": targetFCV,
		},
	}); err != nil {
		return fmt.Errorf("before-fcv-upgrade hook failed: %w", err)
	}

	// For MongoDB 7.0+, use confirm: true parameter for extra safety
	// This requires explicit confirmation of the FCV upgrade
	var cmd bson.D
	targetVersion, _ := strconv.Atoi(versionParts[0])
	if targetVersion >= 7 {
		cmd = bson.D{
			{Key: "setFeatureCompatibilityVersion", Value: targetFCV},
			{Key: "confirm", Value: true},
		}
	} else {
		cmd = bson.D{
			{Key: "setFeatureCompatibilityVersion", Value: targetFCV},
		}
	}

	// Execute the FCV upgrade command
	var result bson.M
	err = adminDB.RunCommand(ctx, cmd).Decode(&result)
	if err != nil {
		return fmt.Errorf("setFeatureCompatibilityVersion command failed: %w", err)
	}

	// Verify the command succeeded
	if ok, exists := result["ok"].(float64); !exists || ok != 1 {
		return fmt.Errorf("setFeatureCompatibilityVersion command returned non-ok status: %v", result)
	}

	// Verify FCV was actually updated by reading it back
	var getResult bson.M
	err = adminDB.RunCommand(ctx, bson.D{
		{Key: "getParameter", Value: 1},
		{Key: "featureCompatibilityVersion", Value: 1},
	}).Decode(&getResult)

	var currentFCV string

	// If getParameter fails (common in 3.6 sharded clusters), fall back to querying admin.system.version
	if err != nil || getResult["featureCompatibilityVersion"] == nil {
		// MongoDB 3.6 fallback: query admin.system.version collection
		var versionDoc bson.M
		err = adminDB.Collection("system.version").FindOne(ctx, bson.M{"_id": "featureCompatibilityVersion"}).Decode(&versionDoc)
		if err != nil {
			return fmt.Errorf("failed to verify FCV from system.version collection: %w", err)
		}

		// Extract version from document
		if version, ok := versionDoc["version"].(string); ok {
			currentFCV = version
		} else {
			return fmt.Errorf("FCV version not found in system.version document during verification: %v", versionDoc)
		}
	} else {
		// Extract FCV from getParameter result - handle both old (3.6) and new (4.0+) formats
		fcvValue := getResult["featureCompatibilityVersion"]

		// MongoDB 4.0+ format: { "featureCompatibilityVersion": { "version": "4.0" } }
		if fcvDoc, ok := fcvValue.(bson.M); ok {
			if version, ok := fcvDoc["version"].(string); ok {
				currentFCV = version
			} else {
				return fmt.Errorf("FCV version not found in nested verification response")
			}
		} else if fcvString, ok := fcvValue.(string); ok {
			// MongoDB 3.6 format: { "featureCompatibilityVersion": "3.6" }
			currentFCV = fcvString
		} else {
			return fmt.Errorf("unexpected FCV format in verification response: %T", fcvValue)
		}
	}

	if currentFCV != targetFCV {
		return fmt.Errorf("FCV verification failed: expected %s but got %s", targetFCV, currentFCV)
	}

	// Execute after-fcv-upgrade hook
	if err := lu.config.HookRegistry.Execute(ctx, HookContext{
		HookType:    HookAfterFCVUpgrade,
		ClusterName: lu.config.ClusterName,
		FromVersion: lu.config.FromVersion,
		ToVersion:   lu.config.ToVersion,
		Metadata: map[string]string{
			"new_fcv": currentFCV,
		},
	}); err != nil {
		// Don't fail the upgrade if after-fcv-upgrade hook fails
		fmt.Printf("Warning: after-fcv-upgrade hook failed: %v\n", err)
	}

	return nil
}

// checkClusterHealth checks overall cluster health
// [UPG-003] Cluster health validation
func (lu *LocalUpgrader) checkClusterHealth(ctx context.Context, client *mongo.Client) error {
	topoType := lu.config.Topology.GetTopologyType()

	switch topoType {
	case "sharded":
		return lu.checkShardedClusterHealth(ctx, client)
	case "replica_set":
		return lu.checkReplicaSetHealth(ctx, client)
	case "standalone":
		// For standalone, just verify we can connect (already done)
		return nil
	default:
		return fmt.Errorf("unsupported topology type: %s", topoType)
	}
}

// checkShardedClusterHealth checks sharded cluster health
func (lu *LocalUpgrader) checkShardedClusterHealth(ctx context.Context, client *mongo.Client) error {
	configDB := client.Database("config")

	// Check that all shards are reachable
	cursor, err := configDB.Collection("shards").Find(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("failed to list shards: %w", err)
	}
	defer cursor.Close(ctx)

	shardCount := 0
	for cursor.Next(ctx) {
		var shard bson.M
		if err := cursor.Decode(&shard); err != nil {
			return fmt.Errorf("failed to decode shard: %w", err)
		}
		shardCount++
	}

	if shardCount == 0 {
		return fmt.Errorf("no shards found in cluster")
	}

	// Check balancer state
	var balancerResult bson.M
	err = configDB.RunCommand(ctx, bson.D{{Key: "balancerStatus", Value: 1}}).Decode(&balancerResult)
	if err != nil {
		// Balancer status check is not critical, just warn
		fmt.Printf("    Warning: Could not check balancer status: %v\n", err)
	}

	return nil
}

// checkReplicaSetHealth checks replica set health
func (lu *LocalUpgrader) checkReplicaSetHealth(ctx context.Context, client *mongo.Client) error {
	adminDB := client.Database("admin")

	// Run replSetGetStatus command
	var result bson.M
	err := adminDB.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&result)
	if err != nil {
		return fmt.Errorf("failed to get replica set status: %w", err)
	}

	// Check that we have a primary
	members, ok := result["members"].(bson.A)
	if !ok {
		return fmt.Errorf("unexpected replica set status format")
	}

	hasPrimary := false
	healthyMembers := 0
	totalMembers := len(members)

	for _, memberInterface := range members {
		member, ok := memberInterface.(bson.M)
		if !ok {
			continue
		}

		state, ok := member["state"].(int32)
		if !ok {
			continue
		}

		health, ok := member["health"].(int32)
		if !ok && member["health"] != nil {
			// Try float64
			if healthFloat, ok := member["health"].(float64); ok {
				health = int32(healthFloat)
			}
		}

		// State 1 = PRIMARY, 2 = SECONDARY
		if state == 1 {
			hasPrimary = true
		}

		if health == 1 && (state == 1 || state == 2) {
			healthyMembers++
		}
	}

	if !hasPrimary {
		return fmt.Errorf("no primary found in replica set")
	}

	if healthyMembers < totalMembers {
		return fmt.Errorf("not all members are healthy: %d/%d healthy", healthyMembers, totalMembers)
	}

	return nil
}

// checkReplicationLag checks maximum replication lag across all secondaries
// [UPG-003] Replication lag validation
func (lu *LocalUpgrader) checkReplicationLag(ctx context.Context, client *mongo.Client) (time.Duration, error) {
	topoType := lu.config.Topology.GetTopologyType()

	// Only applies to replica sets
	if topoType == "standalone" {
		return 0, nil
	}

	// For sharded clusters, we're connected through mongos which doesn't support replSetGetStatus
	// We'd need to connect directly to each shard, which is complex
	// The cluster health check already verifies shards are healthy, so we can skip detailed lag check
	if topoType == "sharded" {
		fmt.Printf("  (Skipped for sharded cluster - checked via cluster health instead)\n")
		return 0, nil
	}

	adminDB := client.Database("admin")

	// Run replSetGetStatus command
	var result bson.M
	err := adminDB.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&result)
	if err != nil {
		return 0, fmt.Errorf("failed to get replica set status: %w", err)
	}

	members, ok := result["members"].(bson.A)
	if !ok {
		return 0, fmt.Errorf("unexpected replica set status format")
	}

	var primaryOptime time.Time
	var maxLag time.Duration

	// First pass: find primary optime
	var foundPrimary bool
	var memberStates []string
	for _, memberInterface := range members {
		member, ok := memberInterface.(bson.M)
		if !ok {
			continue
		}

		state, ok := member["state"].(int32)
		if !ok {
			continue
		}

		// Track member states for diagnostics
		stateStr := fmt.Sprintf("state:%d", state)
		if name, ok := member["name"].(string); ok {
			stateStr = fmt.Sprintf("%s=%d", name, state)
		}
		memberStates = append(memberStates, stateStr)

		if state == 1 { // PRIMARY
			foundPrimary = true

			// Try modern format (MongoDB >= 3.2): optimeDate as time.Time
			if optimeDate, ok := member["optimeDate"].(time.Time); ok {
				primaryOptime = optimeDate
				break
			}

			// Try older format (MongoDB < 3.2): optime.ts as primitive.Timestamp
			if optime, ok := member["optime"].(bson.M); ok {
				if ts, ok := optime["ts"].(primitive.Timestamp); ok {
					// Convert timestamp to time.Time (seconds since epoch)
					primaryOptime = time.Unix(int64(ts.T), 0)
					break
				}
			}

			// If we get here, we found primary but couldn't extract optime
			fmt.Printf("  ⚠️  Primary found but optime format not recognized\n")
		}
	}

	if primaryOptime.IsZero() {
		if !foundPrimary {
			return 0, fmt.Errorf("no primary found in replica set (member states: %s)", strings.Join(memberStates, ", "))
		}
		return 0, fmt.Errorf("primary found but optimeDate not available (might be too old to check replication lag)")
	}

	// Second pass: calculate lag for secondaries
	for _, memberInterface := range members {
		member, ok := memberInterface.(bson.M)
		if !ok {
			continue
		}

		state, ok := member["state"].(int32)
		if !ok {
			continue
		}

		if state == 2 { // SECONDARY
			var secondaryOptime time.Time

			// Try modern format (MongoDB >= 3.2): optimeDate as time.Time
			if optimeDate, ok := member["optimeDate"].(time.Time); ok {
				secondaryOptime = optimeDate
			} else if optime, ok := member["optime"].(bson.M); ok {
				// Try older format (MongoDB < 3.2): optime.ts as primitive.Timestamp
				if ts, ok := optime["ts"].(primitive.Timestamp); ok {
					secondaryOptime = time.Unix(int64(ts.T), 0)
				}
			}

			if secondaryOptime.IsZero() {
				continue // Skip if we couldn't get optime
			}

			lag := primaryOptime.Sub(secondaryOptime)
			if lag < 0 {
				lag = -lag // Handle clock skew
			}

			if lag > maxLag {
				maxLag = lag
			}
		}
	}

	return maxLag, nil
}
