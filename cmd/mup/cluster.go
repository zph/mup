package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/cluster"
	"github.com/zph/mup/pkg/deploy"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/mongo"
	"github.com/zph/mup/pkg/operation"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation" // REQ-SIM-016: Simulation executor
	"github.com/zph/mup/pkg/topology"
	"github.com/zph/mup/pkg/upgrade"
)

var (
	clusterDeployVersion          string
	clusterDeployVariant          string
	clusterDeployYes              bool
	clusterDeployTimeout          time.Duration
	clusterDeployNoMonitoring     bool
	clusterDeployPlanOnly         bool
	clusterDeployAutoApprove      bool
	clusterDeployPlanFile         string
	clusterDeploySimulate         bool   // REQ-SIM-001: Simulation mode flag
	clusterDeploySimulateScenario string // REQ-SIM-041: Scenario file path
	clusterDeploySimulateVerbose  bool   // REQ-SIM-049: Verbose simulation output

	clusterNodeFilter    string
	clusterDisplayFormat string
	clusterKeepData      bool

	// Upgrade command flags [UPG-013]
	clusterUpgradeToVersion         string
	clusterUpgradeVariant           string
	clusterUpgradeFCV               bool
	clusterUpgradeParallelShards    bool
	clusterUpgradePromptLevel       string
	clusterUpgradeResume            bool
	clusterUpgradeRollback          bool
	clusterUpgradeTimeout           time.Duration
	clusterUpgradeResumePromptLevel string
	clusterUpgradeDryRun            bool
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage production MongoDB clusters",
	Long:  `Deploy and manage production MongoDB clusters across multiple hosts.`,
}

var clusterDeployCmd = &cobra.Command{
	Use:   "deploy <cluster-name> <topology-file>",
	Short: "Deploy a new MongoDB cluster",
	Long: `Deploy a new MongoDB cluster from a topology YAML file.

PLAN/APPLY WORKFLOW:
Mup uses a Terraform-inspired plan/apply workflow for safe, predictable deployments:
1. Generate a plan with pre-flight validation (--plan-only)
2. Review the plan (mup plan show <cluster-name>)
3. Apply the plan with confirmation (default) or auto-approve (--auto-approve)
4. Resume if deployment fails (mup cluster resume <cluster-name>)

The topology file defines the cluster structure including:
- mongod servers (standalone or replica set members)
- mongos routers (for sharded clusters)
- config servers (for sharded clusters)

Deployments are local: all nodes run on localhost with auto-allocated ports (30000+).

This command does not deploy monitoring; 'mup playground start' does.

DEPLOYMENT PHASES:
1. Prepare   - Download binaries, create directories, run pre-flight checks
2. Deploy    - Generate configs, start MongoDB processes
3. Initialize - Initialize replica sets, configure sharding
4. Finalize  - Verify health, save cluster metadata

Each phase creates a checkpoint for recovery on failure.

Examples:
  # Generate and review a deployment plan
  mup cluster deploy my-rs replica-set.yaml --version 7.0 --plan-only
  mup plan show my-rs

  # Deploy with interactive confirmation (default)
  mup cluster deploy my-rs replica-set.yaml --version 7.0

  # Deploy without confirmation (auto-approve)
  mup cluster deploy my-rs replica-set.yaml --version 7.0 --auto-approve

  # Deploy with Percona Server for MongoDB
  mup cluster deploy my-rs replica-set.yaml --variant percona --version 8.0.12-4

  # Resume a failed deployment; completed operations are skipped
  mup cluster resume my-rs
  mup state logs my-rs
`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		topologyFile := args[1]

		ctx, cancel := context.WithTimeout(context.Background(), clusterDeployTimeout)
		defer cancel()

		// Parse variant
		variant, err := deploy.ParseVariant(clusterDeployVariant)
		if err != nil {
			return fmt.Errorf("invalid variant: %w", err)
		}

		// Parse topology file
		topo, err := topology.ParseTopologyFile(topologyFile)
		if err != nil {
			return fmt.Errorf("failed to load topology: %w", err)
		}

		// Get cluster storage directories
		clusterDir, err := getClusterDir(clusterName)
		if err != nil {
			return err
		}

		_, err = getStorageDir()
		if err != nil {
			return err
		}
		// Use BinaryManager to get the correct versioned bin path
		// This ensures the path matches what will be downloaded/cached
		bm, err := deploy.NewBinaryManager()
		if err != nil {
			return fmt.Errorf("failed to create binary manager: %w", err)
		}
		platform := deploy.GetCurrentPlatform()
		binPath, err := bm.GetBinPathWithVariant(clusterDeployVersion, variant, platform)
		if err != nil {
			return fmt.Errorf("failed to determine binary path: %w", err)
		}

		// REQ-SIM-027: Display simulation mode indicator
		if clusterDeploySimulate {
			fmt.Println("[SIMULATION] Running in simulation mode - no actual changes will be made")
		}

		// REQ-SIM-016, REQ-SIM-017: Create executors map (simulation or real)
		executors := make(map[string]executor.Executor)
		isLocal := topo.IsLocalDeployment()

		if clusterDeploySimulate {
			// REQ-SIM-001, REQ-SIM-016: Use simulation executor
			var simConfig *simulation.Config
			var err error

			// REQ-SIM-041: Load scenario if specified
			if clusterDeploySimulateScenario != "" {
				fmt.Printf("[SIMULATION] Loading scenario from: %s\n", clusterDeploySimulateScenario)
				simConfig, err = simulation.LoadConfigWithScenario(clusterDeploySimulateScenario)
				if err != nil {
					return fmt.Errorf("failed to load simulation scenario: %w", err)
				}
			} else {
				simConfig = simulation.NewConfig()
			}

			simConfig.AllowRealFileReads = true // REQ-SIM-007: Allow reading topology files

			// Create same simulation executor for all hosts
			simExec := simulation.NewExecutor(simConfig)
			for _, host := range topo.GetAllHosts() {
				executors[host] = simExec
			}
		} else {
			// Create real executors
			for _, host := range topo.GetAllHosts() {
				executors[host] = executor.NewLocalExecutor()
			}
		}

		// Create deploy planner
		plannerConfig := &deploy.PlannerConfig{
			ClusterName: clusterName,
			Version:     clusterDeployVersion,
			Variant:     variant,
			Topology:    topo,
			Executors:   executors,
			MetaDir:     clusterDir,
			IsLocal:     isLocal,
			BinPath:     binPath,
			DryRun:      clusterDeployPlanOnly,
		}

		planner, err := deploy.NewDeployPlanner(plannerConfig)
		if err != nil {
			return fmt.Errorf("failed to create planner: %w", err)
		}

		// Generate plan
		fmt.Printf("\n📋 Generating deployment plan for cluster '%s'...\n\n", clusterName)
		deployPlan, err := planner.GeneratePlan(ctx)
		if err != nil {
			return fmt.Errorf("failed to generate plan: %w", err)
		}

		// Display validation results
		if !deployPlan.Validation.Valid {
			fmt.Println(plan.FormatValidationResult(deployPlan.Validation))
			return fmt.Errorf("validation failed")
		}

		// Display plan summary
		fmt.Println(deployPlan.Summary())

		// LOCAL-008: simulation persists nothing under the real storage directory
		if clusterDeploySimulate {
			if clusterDeployPlanOnly {
				return nil
			}
			return runSimulatedDeploy(ctx, deployPlan, executors, clusterName, topologyFile)
		}

		// Save plan using PlanStore
		storageDir, err2 := getStorageDir()
		if err2 != nil {
			return err2
		}
		planStore, err := plan.NewPlanStore(storageDir)
		if err != nil {
			return fmt.Errorf("failed to create plan store: %w", err)
		}

		planID, err := planStore.SavePlan(deployPlan)
		if err != nil {
			return fmt.Errorf("failed to save plan: %w", err)
		}

		planPath := planStore.GetPlanPath(clusterName, planID)
		fmt.Printf("\n✅ Plan saved: %s\n", planID)
		fmt.Printf("   Path: %s\n", planPath)

		// Verify plan was saved correctly
		verified, err := planStore.VerifyPlan(clusterName, planID)
		if err != nil {
			fmt.Printf("⚠️  Warning: Failed to verify plan: %v\n", err)
		} else if verified {
			fmt.Printf("   ✓ Integrity verified (SHA-256)\n")
		}

		// If plan-only mode, exit here
		if clusterDeployPlanOnly {
			fmt.Printf("\nPlan generated successfully: %s\n", planPath)
			fmt.Printf("Apply by re-running without --plan-only.\n")
			return nil
		}

		// Prompt for confirmation unless auto-approve or yes flag
		if !clusterDeployAutoApprove && !clusterDeployYes {
			fmt.Printf("\n⚠️  Apply plan %s to cluster %s?\n", deployPlan.PlanID, clusterName)
			fmt.Printf("This will:\n")
			fmt.Printf("  • Create %d MongoDB processes\n", len(topo.GetAllHosts()))
			fmt.Printf("  • Use %d operations across %d phases\n", deployPlan.TotalOperations(), len(deployPlan.Phases))
			fmt.Printf("  • Estimated duration: %s\n", deployPlan.EstimatedDuration())
			fmt.Printf("\nDo you want to continue? (yes/no): ")
			var response string
			_, _ = fmt.Scanln(&response)
			if response != "yes" && response != "y" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		// Create lock manager and acquire cluster lock
		lockMgr, err := apply.NewLockManager(storageDir)
		if err != nil {
			return fmt.Errorf("failed to create lock manager: %w", err)
		}

		fmt.Printf("\n🔒 Acquiring cluster lock...\n")
		lock, err := lockMgr.AcquireLock(clusterName, deployPlan.PlanID, "deploy", clusterLockTimeout)
		if err != nil {
			return fmt.Errorf("failed to acquire cluster lock: %w", err)
		}
		defer func() {
			if err := lockMgr.ReleaseLock(clusterName, lock); err != nil {
				fmt.Printf("Warning: failed to release lock: %v\n", err)
			}
		}()

		fmt.Printf("✓ Cluster lock acquired (expires: %s)\n", lock.ExpiresAt.Format(time.RFC3339))

		// Renew the lock in the background; stop renewal and wait for it before
		// the deferred release above runs, so it cannot rewrite a released lock.
		renewCtx, renewCancel := context.WithCancel(ctx)
		renewalStopped := lockMgr.StartLockRenewal(renewCtx, lock, clusterLockRenewInterval, clusterLockTimeout)
		defer func() {
			renewCancel()
			<-renewalStopped
		}()

		// LOCAL-013: the state manager is rooted at the cluster directory so
		// state (<cluster>/state) and plans (<cluster>/plans) resolve together
		stateManager := apply.NewStateManager(clusterDir)

		// Create operation executor
		opExecutor := operation.NewExecutor(executors)

		// Create applier
		applier := apply.NewDefaultApplier(opExecutor, stateManager)

		fmt.Printf("\n🚀 Applying deployment plan...\n\n")

		state, err := applier.Apply(ctx, deployPlan)
		if err != nil {
			fmt.Printf("\n❌ Deployment failed: %v\n", err)
			fmt.Printf("\nState ID: %s\n", state.StateID)
			fmt.Printf("Resume with: mup cluster resume %s\n", clusterName)
			fmt.Printf("Or discard it with: mup cluster destroy %s\n", clusterName)
			return err
		}

		fmt.Printf("\n✅ Deployment completed successfully!\n")
		fmt.Printf("State ID: %s\n", state.StateID)
		fmt.Printf("\nCluster commands:\n")
		fmt.Printf("  mup cluster display %s    # Show cluster status\n", clusterName)
		fmt.Printf("  mup cluster connect %s    # Connect to cluster\n", clusterName)
		fmt.Printf("  mup cluster stop %s       # Stop cluster\n", clusterName)

		return nil
	},
}

// runSimulatedDeploy applies the plan against simulation executors.
// LOCAL-008: state goes to a temporary directory, and no plan, lock or cluster
// files are written under the real storage directory.
func runSimulatedDeploy(ctx context.Context, deployPlan *plan.Plan, executors map[string]executor.Executor, clusterName, topologyFile string) error {
	stateDir, err := os.MkdirTemp("", "mup-simulate-")
	if err != nil {
		return fmt.Errorf("failed to create simulation state directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stateDir); err != nil {
			fmt.Printf("Warning: failed to remove %s: %v\n", stateDir, err)
		}
	}()

	applier := apply.NewDefaultApplier(operation.NewExecutor(executors), apply.NewStateManager(stateDir))

	fmt.Printf("\n🔬 [SIMULATION] Applying deployment plan...\n\n")
	if _, err := applier.Apply(ctx, deployPlan); err != nil {
		fmt.Printf("\n❌ [SIMULATION] Deployment failed: %v\n", err)
		return err
	}

	// Get the simulation executor from the executors map
	var simExec *simulation.SimulationExecutor
	for _, exec := range executors {
		if se, ok := exec.(*simulation.SimulationExecutor); ok {
			simExec = se
			break
		}
	}

	if simExec != nil {
		reporter := simulation.NewReporter(simExec)

		// REQ-SIM-049: Print detailed log if verbose mode
		if clusterDeploySimulateVerbose {
			reporter.PrintDetailed()
		} else {
			reporter.PrintSummary()
		}

		// REQ-SIM-029: Print errors if any occurred
		if reporter.HasErrors() {
			reporter.PrintErrors()
		}
	}

	fmt.Printf("\n✅ [SIMULATION] Simulation completed successfully!\n")
	fmt.Printf("[SIMULATION] No actual changes were made.\n\n")
	fmt.Printf("[SIMULATION] To execute for real, run without --simulate:\n")
	fmt.Printf("  mup cluster deploy %s %s --version %s\n", clusterName, topologyFile, clusterDeployVersion)
	return nil
}

// LOCAL-013: resume continues the most recent failed or paused apply.
var clusterResumeCmd = &cobra.Command{
	Use:   "resume <cluster-name>",
	Short: "Resume a failed or paused deployment",
	Long: `Resume the most recent failed or paused apply for a cluster.

Operations that already completed are skipped; execution continues from the
operation that failed.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		ctx, cancel := context.WithTimeout(context.Background(), clusterDeployTimeout)
		defer cancel()

		clusterDir, err := getClusterDir(clusterName)
		if err != nil {
			return err
		}
		storageDir, err := getStorageDir()
		if err != nil {
			return err
		}

		stateManager := apply.NewStateManager(clusterDir)
		state, err := stateManager.GetCurrentState()
		if err != nil {
			return fmt.Errorf("nothing to resume for cluster %s: %w", clusterName, err)
		}
		if !state.CanResume() {
			return fmt.Errorf("latest apply %s for cluster %s is %s and cannot be resumed", state.StateID, clusterName, state.Status)
		}

		// LOCAL-032: only resume a plan that still matches its checksum
		planStore, err := plan.NewPlanStore(storageDir)
		if err != nil {
			return fmt.Errorf("failed to create plan store: %w", err)
		}
		resumePlan, err := planStore.LoadVerifiedPlan(clusterName, state.PlanID)
		if err != nil {
			return err
		}

		return withClusterLock(ctx, clusterName, state.PlanID, resumePlan.Operation, func() error {
			applier := apply.NewDefaultApplier(operation.NewExecutor(localExecutorsForPlan(resumePlan)), stateManager)

			fmt.Printf("Resuming apply %s for cluster '%s' from phase '%s'...\n\n", state.StateID, clusterName, state.CurrentPhase)
			if _, err := applier.Resume(ctx, state); err != nil {
				fmt.Printf("\n❌ Resume failed: %v\n", err)
				fmt.Printf("Resume again with: mup cluster resume %s\n", clusterName)
				return err
			}

			fmt.Printf("\n✅ Deployment completed successfully!\n")
			return nil
		})
	},
}

// withClusterLock runs fn while holding the cluster lock, renewing the lock
// in the background and releasing it afterwards.
func withClusterLock(ctx context.Context, clusterName, planID, operationName string, fn func() error) error {
	lockMgr, err := newLockManager()
	if err != nil {
		return err
	}

	lock, err := lockMgr.AcquireLock(clusterName, planID, operationName, clusterLockTimeout)
	if err != nil {
		return fmt.Errorf("failed to acquire cluster lock: %w", err)
	}
	defer func() {
		if err := lockMgr.ReleaseLock(clusterName, lock); err != nil {
			fmt.Printf("Warning: failed to release lock: %v\n", err)
		}
	}()

	// Stop renewal and wait for it before the deferred release above runs, so
	// a renewal in flight cannot rewrite a released lock.
	renewCtx, renewCancel := context.WithCancel(ctx)
	renewalStopped := lockMgr.StartLockRenewal(renewCtx, lock, clusterLockRenewInterval, clusterLockTimeout)
	defer func() {
		renewCancel()
		<-renewalStopped
	}()

	return fn()
}

// defaultUpgradeTimeout bounds a whole rolling upgrade. It is much longer
// than the deploy timeout because it covers every node restart, replica set
// settling and time spent waiting at prompts.
const defaultUpgradeTimeout = 12 * time.Hour

const (
	// clusterLockTimeout is how long a cluster lock is held before it expires.
	clusterLockTimeout = 24 * time.Hour
	// clusterLockRenewInterval is how often a held lock is extended.
	clusterLockRenewInterval = time.Hour
)

// localExecutorsForPlan returns a local executor for every host a plan targets.
func localExecutorsForPlan(p *plan.Plan) map[string]executor.Executor {
	executors := map[string]executor.Executor{"localhost": executor.NewLocalExecutor()}
	for _, phase := range p.Phases {
		for _, op := range phase.Operations {
			if host := op.Target.Host; host != "" {
				if _, ok := executors[host]; !ok {
					executors[host] = executor.NewLocalExecutor()
				}
			}
		}
	}
	return executors
}

// LOCAL-018: unlock clears a lock left behind by an interrupted run.
var clusterUnlockCmd = &cobra.Command{
	Use:   "unlock <cluster-name>",
	Short: "Remove a cluster lock left behind by an interrupted operation",
	Long: `Remove the lock on a cluster.

Locks whose owning process has exited are taken over automatically; use this
command when a lock file is unreadable or the owner cannot be checked.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return releaseClusterLock(args[0], clusterDeployYes)
	},
}

var clusterStartCmd = &cobra.Command{
	Use:   "start <cluster-name>",
	Short: "Start a stopped cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		ctx := context.Background()

		mgr, err := cluster.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create manager: %w", err)
		}

		return mgr.Start(ctx, clusterName, clusterNodeFilter)
	},
}

var clusterStopCmd = &cobra.Command{
	Use:   "stop <cluster-name>",
	Short: "Stop a running cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		ctx := context.Background()

		// Require confirmation unless --yes flag is passed
		if !clusterDeployYes {
			fmt.Printf("Are you sure you want to stop cluster '%s'? [y/N]: ", clusterName)
			var response string
			_, _ = fmt.Scanln(&response)
			if response != "y" && response != "Y" && response != "yes" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		mgr, err := cluster.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create manager: %w", err)
		}

		return mgr.Stop(ctx, clusterName, clusterNodeFilter)
	},
}

var clusterDisplayCmd = &cobra.Command{
	Use:   "display <cluster-name>",
	Short: "Show cluster status and information",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		ctx := context.Background()

		mgr, err := cluster.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create manager: %w", err)
		}

		return mgr.Display(ctx, clusterName, clusterDisplayFormat)
	},
}

var clusterDestroyCmd = &cobra.Command{
	Use:   "destroy <cluster-name>",
	Short: "Destroy a cluster and remove all data",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		ctx := context.Background()

		// Require confirmation unless --yes flag is passed
		if !clusterDeployYes {
			action := "destroy"
			if clusterKeepData {
				action = "destroy (keeping data)"
			}
			fmt.Printf("WARNING: This will %s cluster '%s'.\n", action, clusterName)
			if !clusterKeepData {
				fmt.Println("All data will be permanently deleted!")
			}
			fmt.Print("Are you sure? [y/N]: ")
			var response string
			_, _ = fmt.Scanln(&response)
			if response != "y" && response != "Y" && response != "yes" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		mgr, err := cluster.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create manager: %w", err)
		}

		return mgr.Destroy(ctx, clusterName, clusterKeepData)
	},
}

var clusterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all managed clusters",
	Long: `List all MongoDB clusters managed by mup.

Shows cluster name, status, version, topology type, and creation time.

Examples:
  # List all clusters
  mup cluster list

  # List with JSON output
  mup cluster list --format json
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := cluster.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create manager: %w", err)
		}

		return mgr.List(clusterDisplayFormat)
	},
}

var clusterConnectCmd = &cobra.Command{
	Use:   "connect <cluster-name>",
	Short: "Connect to a MongoDB cluster using mongosh",
	Long: `Connect to a running MongoDB cluster using the connection command stored in metadata.

The connection command is automatically generated during deployment and stored in the cluster metadata.
It uses mongosh (or falls back to mongo for older MongoDB versions).

Examples:
  # Connect to a cluster
  mup cluster connect my-cluster
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]

		metaMgr, err := meta.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create meta manager: %w", err)
		}

		// Load cluster metadata
		metadata, err := metaMgr.Load(clusterName)
		if err != nil {
			return fmt.Errorf("failed to load cluster metadata: %w", err)
		}

		if metadata.Status != "running" {
			return fmt.Errorf("cluster '%s' is not running (status: %s). Run 'mup cluster start %s'", clusterName, metadata.Status, clusterName)
		}

		// Construct connection command using cluster-local binaries
		// Get connection string
		connStr := getConnectionString(metadata)
		if connStr == "" {
			return fmt.Errorf("could not determine connection string for cluster '%s'", clusterName)
		}

		// Get correct shell binary based on MongoDB version
		shellBinary, err := mongo.GetShellBinary(metadata.Version)
		if err != nil {
			return fmt.Errorf("failed to select MongoDB shell: %w", err)
		}

		// Construct cluster-local binary path
		clusterDir := metaMgr.GetClusterDir(clusterName)
		clusterBinDir := filepath.Join(clusterDir, fmt.Sprintf("v%s", metadata.Version), "bin")
		shellPath := filepath.Join(clusterBinDir, shellBinary)

		// Build connection command
		connectionCmd := fmt.Sprintf("%s \"%s\"", shellPath, connStr)

		fmt.Printf("Connecting to cluster '%s'...\n", clusterName)
		fmt.Printf("Executing: %s\n\n", connectionCmd)

		// Execute the connection command via shell
		// Use sh -c to properly handle quoted connection strings
		shellCmd := exec.Command("sh", "-c", connectionCmd)
		shellCmd.Stdin = os.Stdin
		shellCmd.Stdout = os.Stdout
		shellCmd.Stderr = os.Stderr

		if err := shellCmd.Run(); err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		return nil
	},
}

// [UPG-013] Upgrade command
var clusterUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster-name>",
	Short: "Upgrade MongoDB cluster to a new version",
	Long: `Upgrade a MongoDB cluster to a new version with zero downtime.

Supports rolling upgrades for replica sets and sharded clusters following
MongoDB best practices:
- Upgrade secondaries first, then step down and upgrade primary
- Upgrade config servers, then shards, then mongos
- Optional Feature Compatibility Version (FCV) upgrade

The upgrade process is fully resumable with automatic checkpointing after
each node. If the upgrade is interrupted, use --resume to continue.

Prompt levels control interaction granularity:
- none: Fully automated (equivalent to --yes)
- phase: Prompt before each major phase (default)
- node: Prompt before each node upgrade
- critical: Prompt only for critical operations (stepdown, FCV)

Examples:
  # Upgrade to MongoDB 7.0 with default prompting
  mup cluster upgrade my-rs --to-version 7.0

  # Upgrade with FCV upgrade
  mup cluster upgrade my-rs --to-version 7.0 --upgrade-fcv

  # Fully automated upgrade
  mup cluster upgrade my-rs --to-version 7.0 --prompt-level none

  # Node-level control for critical systems
  mup cluster upgrade my-rs --to-version 7.0 --prompt-level node

  # Dry run to see the upgrade plan
  mup cluster upgrade my-rs --to-version 7.0 --dry-run

  # Resume interrupted upgrade
  mup cluster upgrade my-rs --resume

  # Undo an unfinished upgrade (before post-upgrade tasks have started)
  mup cluster upgrade my-rs --rollback
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Silence usage on runtime errors (not argument errors)
		cmd.SilenceUsage = true

		clusterName := args[0]

		ctx, cancel := context.WithTimeout(context.Background(), clusterUpgradeTimeout)
		defer cancel()

		// Get metadata directory
		metaDir, err := getClusterDir(clusterName)
		if err != nil {
			return err
		}

		// Load cluster metadata to get topology
		metaMgr, err := meta.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create meta manager: %w", err)
		}
		clusterMeta, err := metaMgr.Load(clusterName)
		if err != nil {
			return fmt.Errorf("failed to load cluster metadata: %w", err)
		}

		// Parse prompt level
		promptLevel, err := parsePromptLevelFromFlags()
		if err != nil {
			return err
		}

		// Create state manager
		stateManager, err := createStateManager(clusterName, metaDir)
		if err != nil {
			return err
		}

		// Initialize or load state
		var state *upgrade.UpgradeState
		if clusterUpgradeResume && clusterUpgradeRollback {
			return fmt.Errorf("--resume and --rollback cannot be used together")
		}
		if clusterUpgradeResume || clusterUpgradeRollback {
			state, err = stateManager.LoadState()
			if err != nil {
				return fmt.Errorf("no unfinished upgrade found: %w", err)
			}

			// Override prompt level if specified
			if clusterUpgradeResumePromptLevel != "" {
				resumePromptLevel, err := parsePromptLevel(clusterUpgradeResumePromptLevel)
				if err != nil {
					return err
				}
				promptLevel = resumePromptLevel
			}
		} else {
			// Validate required flags for new upgrade
			if clusterUpgradeToVersion == "" {
				return fmt.Errorf("--to-version is required for new upgrades")
			}

			state = stateManager.InitializeState(
				clusterName,
				fmt.Sprintf("%s-%s", clusterMeta.Variant, clusterMeta.Version),
				fmt.Sprintf("%s-%s", clusterMeta.Variant, clusterUpgradeToVersion),
			)
			state.PromptLevel = string(promptLevel)
		}

		// Create prompter
		prompter := createPrompter(promptLevel, state)

		// Create upgrade config
		config := createUpgradeConfig(clusterName, metaDir, clusterMeta, promptLevel, stateManager, prompter)

		// Create upgrader (local for now)
		upgrader, err := createLocalUpgrader(config)
		if err != nil {
			return fmt.Errorf("failed to create upgrader: %w", err)
		}

		// UPGSAFE-014
		if clusterUpgradeRollback {
			if promptLevel != upgrade.PromptLevelNone {
				fmt.Printf("Roll cluster %s back to %s, undoing the unfinished upgrade? (yes/no): ", clusterName, clusterMeta.Version)
				var response string
				if _, err := fmt.Scanln(&response); err != nil || (response != "yes" && response != "y") {
					fmt.Println("Cancelled.")
					return nil
				}
			}
			return upgrader.Rollback(ctx)
		}

		// Execute upgrade or resume
		if clusterUpgradeResume {
			return upgrader.Resume(ctx)
		}

		return upgrader.Upgrade(ctx)
	},
}

func init() {
	// Add cluster command to root
	rootCmd.AddCommand(clusterCmd)

	// Add subcommands
	clusterCmd.AddCommand(clusterDeployCmd)
	clusterCmd.AddCommand(clusterUpgradeCmd)
	clusterCmd.AddCommand(clusterResumeCmd)
	clusterCmd.AddCommand(clusterUnlockCmd)
	clusterCmd.AddCommand(clusterStartCmd)
	clusterCmd.AddCommand(clusterStopCmd)
	clusterCmd.AddCommand(clusterDisplayCmd)
	clusterCmd.AddCommand(clusterDestroyCmd)
	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterConnectCmd)

	// Deploy command flags
	clusterDeployCmd.Flags().StringVarP(&clusterDeployVersion, "version", "v", "7.0", "MongoDB version to deploy")
	clusterDeployCmd.Flags().StringVar(&clusterDeployVariant, "variant", "mongo", "MongoDB variant: 'mongo' (official) or 'percona' (Percona Server for MongoDB)")
	clusterDeployCmd.Flags().BoolVar(&clusterDeployYes, "yes", false, "Skip confirmation prompts")
	clusterDeployCmd.Flags().DurationVarP(&clusterDeployTimeout, "timeout", "t", 30*time.Minute, "Deployment timeout")
	clusterDeployCmd.Flags().BoolVar(&clusterDeployNoMonitoring, "no-monitoring", false, "Disable monitoring deployment (Victoria Metrics, Grafana, exporters)")
	clusterDeployCmd.Flags().BoolVar(&clusterDeployPlanOnly, "plan-only", false, "Generate deployment plan without executing (dry-run)")
	clusterDeployCmd.Flags().BoolVar(&clusterDeployAutoApprove, "auto-approve", false, "Skip confirmation and apply plan automatically")
	clusterDeployCmd.Flags().StringVar(&clusterDeployPlanFile, "plan-file", "", "Save plan to specific file path (default: auto-generated)")
	// These flags never had an effect on this command. They stay accepted so
	// existing scripts keep working, but say so when used.
	for flag, reason := range map[string]string{
		"no-monitoring": "cluster deploy does not deploy monitoring",
		"plan-file":     "plans are always saved under the cluster directory; see 'mup plan list'",
	} {
		if err := clusterDeployCmd.Flags().MarkDeprecated(flag, reason); err != nil {
			panic(err)
		}
	}
	clusterDeployCmd.Flags().BoolVar(&clusterDeploySimulate, "simulate", false, "REQ-SIM-001: Run command in simulation mode (no filesystem/process/network changes)")
	clusterDeployCmd.Flags().StringVar(&clusterDeploySimulateScenario, "simulate-scenario", "", "REQ-SIM-041: Path to scenario YAML file for simulation")
	clusterDeployCmd.Flags().BoolVar(&clusterDeploySimulateVerbose, "simulate-verbose", false, "REQ-SIM-049: Show detailed operation log in simulation mode")

	// Start/stop command flags
	clusterStartCmd.Flags().StringVar(&clusterNodeFilter, "node", "", "Start specific node only (host:port)")
	clusterStopCmd.Flags().StringVar(&clusterNodeFilter, "node", "", "Stop specific node only (host:port)")
	clusterStopCmd.Flags().BoolVar(&clusterDeployYes, "yes", false, "Skip confirmation prompt")
	clusterUnlockCmd.Flags().BoolVar(&clusterDeployYes, "yes", false, "Skip confirmation prompt")

	// Display command flags
	clusterDisplayCmd.Flags().StringVar(&clusterDisplayFormat, "format", "text", "Output format: text, json, yaml")

	// List command flags
	clusterListCmd.Flags().StringVar(&clusterDisplayFormat, "format", "text", "Output format: text, json, yaml")

	// Destroy command flags
	clusterDestroyCmd.Flags().BoolVar(&clusterKeepData, "keep-data", false, "Keep data directories")
	clusterDestroyCmd.Flags().BoolVar(&clusterDeployYes, "yes", false, "Skip confirmation prompt")

	// Upgrade command flags [UPG-013]
	clusterUpgradeCmd.Flags().StringVar(&clusterUpgradeToVersion, "to-version", "", "Target MongoDB version (required)")
	clusterUpgradeCmd.Flags().StringVar(&clusterUpgradeVariant, "variant", "", "Target variant (default: current cluster variant)")
	clusterUpgradeCmd.Flags().BoolVar(&clusterUpgradeFCV, "upgrade-fcv", false, "Upgrade Feature Compatibility Version after binary upgrade")
	clusterUpgradeCmd.Flags().BoolVar(&clusterUpgradeParallelShards, "parallel-shards", false, "Upgrade shards in parallel (default: sequential)")
	clusterUpgradeCmd.Flags().StringVar(&clusterUpgradePromptLevel, "prompt-level", "phase", "Prompting granularity: none, phase, node, critical")
	clusterUpgradeCmd.Flags().BoolVar(&clusterUpgradeResume, "resume", false, "Resume a paused or failed upgrade")
	clusterUpgradeCmd.Flags().BoolVar(&clusterUpgradeRollback, "rollback", false, "Undo an unfinished upgrade by moving upgraded nodes back to the old version")
	clusterUpgradeCmd.Flags().DurationVar(&clusterUpgradeTimeout, "timeout", defaultUpgradeTimeout, "Overall upgrade timeout, including time spent at prompts")
	clusterUpgradeCmd.Flags().StringVar(&clusterUpgradeResumePromptLevel, "resume-with-prompt-level", "", "Override prompt level when resuming")
	clusterUpgradeCmd.Flags().BoolVar(&clusterUpgradeDryRun, "dry-run", false, "Show upgrade plan without executing")

}

// Helper functions for upgrade command

func parsePromptLevelFromFlags() (upgrade.PromptLevel, error) {
	level := clusterUpgradePromptLevel
	if clusterDeployYes {
		level = "none"
	}
	return upgrade.ParsePromptLevel(level)
}

func parsePromptLevel(s string) (upgrade.PromptLevel, error) {
	return upgrade.ParsePromptLevel(s)
}

func createStateManager(clusterName, metaDir string) (*upgrade.StateManager, error) {
	return upgrade.NewStateManager(clusterName, metaDir)
}

func createPrompter(level upgrade.PromptLevel, state *upgrade.UpgradeState) *upgrade.Prompter {
	return upgrade.NewPrompter(level, state)
}

func createUpgradeConfig(clusterName, metaDir string, clusterMeta *meta.ClusterMetadata, promptLevel upgrade.PromptLevel, stateManager *upgrade.StateManager, prompter *upgrade.Prompter) upgrade.UpgradeConfig {
	// Parse topology from metadata
	topo := clusterMeta.Topology

	// Create executors (local only for now)
	executors := make(map[string]executor.Executor)
	for _, host := range topo.GetAllHosts() {
		executors[host] = executor.NewLocalExecutor()
	}

	// Parse target variant
	targetVariant := clusterUpgradeVariant
	if targetVariant == "" {
		targetVariant = clusterMeta.Variant
	}

	variant, err := deploy.ParseVariant(targetVariant)
	if err != nil {
		fmt.Printf("Warning: invalid variant %s, using cluster default\n", targetVariant)
		variant, _ = deploy.ParseVariant(clusterMeta.Variant)
	}

	return upgrade.UpgradeConfig{
		ClusterName:    clusterName,
		FromVersion:    clusterMeta.Version,
		ToVersion:      clusterUpgradeToVersion,
		TargetVariant:  variant,
		UpgradeFCV:     clusterUpgradeFCV,
		ParallelShards: clusterUpgradeParallelShards,
		PromptLevel:    promptLevel,
		MetaDir:        metaDir,
		Topology:       topo,
		Executors:      executors,
		StateManager:   stateManager,
		Prompter:       prompter,
		DryRun:         clusterUpgradeDryRun,
	}
}

func createLocalUpgrader(config upgrade.UpgradeConfig) (*upgrade.LocalUpgrader, error) {
	return upgrade.NewLocalUpgrader(config)
}

// getConnectionString builds the MongoDB connection string from metadata
func getConnectionString(metadata *meta.ClusterMetadata) string {
	if metadata.Topology == nil {
		return ""
	}

	topoType := metadata.Topology.GetTopologyType()

	switch topoType {
	case "sharded":
		// Connect via mongos
		for _, node := range metadata.Nodes {
			if node.Type == "mongos" {
				return fmt.Sprintf("mongodb://%s:%d", node.Host, node.Port)
			}
		}

	case "replica_set":
		// Build replica set connection string
		rsName := ""
		var hosts []string

		for _, node := range metadata.Nodes {
			if node.Type == "mongod" && node.ReplicaSet != "" {
				if rsName == "" {
					rsName = node.ReplicaSet
				}
				if rsName == node.ReplicaSet {
					hosts = append(hosts, fmt.Sprintf("%s:%d", node.Host, node.Port))
				}
			}
		}

		if len(hosts) > 0 {
			return fmt.Sprintf("mongodb://%s/?replicaSet=%s", strings.Join(hosts, ","), rsName)
		}

	case "standalone":
		// Connect to single mongod
		for _, node := range metadata.Nodes {
			if node.Type == "mongod" {
				return fmt.Sprintf("mongodb://%s:%d", node.Host, node.Port)
			}
		}
	}

	return "mongodb://localhost:27017"
}
