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
	importer "github.com/zph/mup/pkg/import"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/mongo"
	"github.com/zph/mup/pkg/operation"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation" // REQ-SIM-016: Simulation executor
	"github.com/zph/mup/pkg/topology"
	"github.com/zph/mup/pkg/upgrade"
)

var (
	clusterDeployVersion      string
	clusterDeployVariant      string
	clusterDeployUser         string
	clusterDeployIdentityFile string
	clusterDeployYes          bool
	clusterDeployTimeout      time.Duration
	clusterDeployNoMonitoring bool
	clusterDeployPlanOnly     bool
	clusterDeployAutoApprove  bool
	clusterDeployPlanFile         string
	clusterDeploySimulate         bool   // REQ-SIM-001: Simulation mode flag
	clusterDeploySimulateScenario string // REQ-SIM-041: Scenario file path
	clusterDeploySimulateVerbose  bool   // REQ-SIM-049: Verbose simulation output

	clusterNodeFilter    string
	clusterDisplayFormat string
	clusterKeepData      bool

	// Upgrade command flags [UPG-013]
	clusterUpgradeToVersion      string
	clusterUpgradeVariant        string
	clusterUpgradeFCV            bool
	clusterUpgradeParallelShards bool
	clusterUpgradePromptLevel    string
	clusterUpgradeResume         bool
	clusterUpgradeResumePromptLevel string
	clusterUpgradeDryRun         bool

	// Import command flags
	clusterImportAutoDetect      bool
	clusterImportConfigFile      string
	clusterImportDataDir         string
	clusterImportPort            int
	clusterImportHost            string
	clusterImportDryRun          bool
	clusterImportSkipRestart     bool
	clusterImportKeepSystemd     bool
	clusterImportSSHHost         string
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
4. Resume from checkpoints if deployment fails (mup plan resume <cluster-name>)

The topology file defines the cluster structure including:
- mongod servers (standalone or replica set members)
- mongos routers (for sharded clusters)
- config servers (for sharded clusters)

The deployment supports two modes:
- LOCAL: All nodes on localhost with auto-allocated ports (30000+)
- REMOTE: Nodes on remote hosts via SSH with standard ports

Monitoring is enabled by default and includes:
- Victoria Metrics (time-series database)
- Grafana (visualization with pre-configured dashboards)
- node_exporter (OS and hardware metrics)
- mongodb_exporter (MongoDB-specific metrics)

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

  # Deploy without monitoring
  mup cluster deploy my-rs replica-set.yaml --version 7.0 --no-monitoring

  # Deploy a remote sharded cluster
  mup cluster deploy prod-cluster sharded.yaml --version 7.0 --user admin

  # Deploy with Percona Server for MongoDB
  mup cluster deploy my-rs replica-set.yaml --variant percona --version 8.0.12-4

  # Resume failed deployment from last checkpoint
  mup plan resume my-rs

  # Monitor deployment progress
  mup state show my-rs
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

		storageDir, err := getStorageDir()
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
			fmt.Printf("\nPlan generated successfully. Review with:\n")
			fmt.Printf("  mup plan show %s --plan-id=%s\n\n", clusterName, planID)
			fmt.Printf("Apply with:\n")
			fmt.Printf("  mup plan apply %s --plan-id=%s\n", clusterName, planID)
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
			fmt.Scanln(&response)
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
		lock, err := lockMgr.AcquireLock(clusterName, planID, "deploy", 24*time.Hour)
		if err != nil {
			return fmt.Errorf("failed to acquire cluster lock: %w", err)
		}
		defer func() {
			if err := lockMgr.ReleaseLock(clusterName, lock); err != nil {
				fmt.Printf("Warning: failed to release lock: %v\n", err)
			}
		}()

		fmt.Printf("✓ Cluster lock acquired (expires: %s)\n", lock.ExpiresAt.Format(time.RFC3339))

		// Start lock renewal in background
		renewCtx, renewCancel := context.WithCancel(ctx)
		defer renewCancel()
		lockMgr.StartLockRenewal(renewCtx, lock, 1*time.Hour, 24*time.Hour)

		// Create state manager
		stateDir := filepath.Join(clusterDir, "state")
		stateManager := apply.NewStateManager(stateDir)

		// Create operation executor
		opExecutor := operation.NewExecutor(executors)

		// Create applier
		applier := apply.NewDefaultApplier(opExecutor, stateManager)

		// Execute deployment
		if clusterDeploySimulate {
			fmt.Printf("\n🔬 [SIMULATION] Applying deployment plan...\n\n")
		} else {
			fmt.Printf("\n🚀 Applying deployment plan...\n\n")
		}

		state, err := applier.Apply(ctx, deployPlan)
		if err != nil {
			fmt.Printf("\n❌ Deployment failed: %v\n", err)
			fmt.Printf("\nState ID: %s\n", state.StateID)
			fmt.Printf("Resume with: mup plan resume %s\n", clusterName)
			return err
		}

		// REQ-SIM-028: Print simulation report if in simulation mode
		if clusterDeploySimulate {
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
		} else {
			fmt.Printf("\n✅ Deployment completed successfully!\n")
			fmt.Printf("State ID: %s\n", state.StateID)
			fmt.Printf("\nCluster commands:\n")
			fmt.Printf("  mup cluster display %s    # Show cluster status\n", clusterName)
			fmt.Printf("  mup cluster connect %s    # Connect to cluster\n", clusterName)
			fmt.Printf("  mup cluster stop %s       # Stop cluster\n", clusterName)
		}

		return nil
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
			fmt.Scanln(&response)
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
			fmt.Scanln(&response)
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
		shellBinary := mongo.GetShellBinary(metadata.Version)

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
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Silence usage on runtime errors (not argument errors)
		cmd.SilenceUsage = true

		clusterName := args[0]

		ctx, cancel := context.WithTimeout(context.Background(), clusterDeployTimeout)
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
		if clusterUpgradeResume {
			state, err = stateManager.LoadState()
			if err != nil {
				return fmt.Errorf("no upgrade state found to resume: %w", err)
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

		// Execute upgrade or resume
		if clusterUpgradeResume {
			return upgrader.Resume(ctx)
		}

		return upgrader.Upgrade(ctx)
	},
}

var clusterImportCmd = &cobra.Command{
	Use:   "import <cluster-name>",
	Short: "Import an existing MongoDB cluster into mup management",
	Long: `Import an existing MongoDB cluster (especially systemd-managed) into mup's management.

The import process:
1. Discovers running MongoDB instances (auto-detect or manual)
2. Parses existing configurations and systemd services
3. Creates mup's directory structure with symlinks to existing data
4. Migrates from systemd to supervisord management
5. Uses rolling restart for replica sets (SECONDARY → PRIMARY)

Supports both local and remote (SSH) clusters.

Examples:
  # Auto-detect local MongoDB cluster
  mup cluster import my-cluster --auto-detect

  # Import with manual configuration
  mup cluster import my-cluster \
    --config /etc/mongod.conf \
    --data-dir /var/lib/mongodb \
    --port 27017

  # Import remote cluster via SSH
  mup cluster import prod-cluster \
    --auto-detect \
    --ssh-host user@production-server

  # Dry-run to preview changes
  mup cluster import my-cluster --auto-detect --dry-run

  # Import without restarting processes
  mup cluster import my-cluster --auto-detect --skip-restart
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		ctx := context.Background()

		fmt.Printf("Importing cluster: %s\n\n", clusterName)

		// Get cluster storage directory
		clusterDir, err := getClusterDir(clusterName)
		if err != nil {
			return err
		}

		// Create executor (local or SSH)
		var exec executor.Executor
		if clusterImportSSHHost != "" {
			fmt.Printf("Using SSH executor for remote host: %s\n", clusterImportSSHHost)
			// SSH executor would be created here
			// For now, use local executor
			exec = executor.NewLocalExecutor()
		} else {
			exec = executor.NewLocalExecutor()
		}

		// Create orchestrator
		orchestrator := importer.NewImportOrchestrator(exec)

		// Build import options
		importOpts := importer.ImportOptions{
			ClusterName:      clusterName,
			ClusterDir:       clusterDir,
			AutoDetect:       clusterImportAutoDetect,
			ConfigFile:       clusterImportConfigFile,
			DataDir:          clusterImportDataDir,
			Port:             clusterImportPort,
			Host:             clusterImportHost,
			DryRun:           clusterImportDryRun,
			SkipRestart:      clusterImportSkipRestart,
			KeepSystemdFiles: clusterImportKeepSystemd,
		}

		// Validate options
		if !importOpts.AutoDetect && importOpts.ConfigFile == "" {
			return fmt.Errorf("either --auto-detect or --config must be specified")
		}

		// Display dry-run notice
		if importOpts.DryRun {
			fmt.Println("🔍 DRY-RUN MODE: No changes will be made")
		}

		// Execute import
		result, err := orchestrator.Import(ctx, importOpts)
		if err != nil {
			fmt.Printf("\n❌ Import failed: %v\n", err)
			return err
		}

		// Display results
		fmt.Println("\n" + strings.Repeat("=", 60))
		if result.Success {
			fmt.Println("✅ Import completed successfully!")
		} else {
			fmt.Println("⚠️  Import completed with warnings")
		}
		fmt.Println(strings.Repeat("=", 60))
		fmt.Printf("\nCluster: %s\n", result.ClusterName)
		fmt.Printf("Version: %s (%s)\n", result.Version, result.Variant)
		fmt.Printf("Topology: %s\n", result.TopologyType)
		fmt.Printf("Nodes imported: %d\n", result.NodesImported)

		if len(result.ServicesDisabled) > 0 {
			fmt.Printf("\nSystemd services disabled:\n")
			for _, svc := range result.ServicesDisabled {
				fmt.Printf("  - %s\n", svc)
			}
		}

		if !importOpts.DryRun {
			fmt.Printf("\nCluster data: %s\n", clusterDir)
			fmt.Printf("\nNext steps:\n")
			fmt.Printf("  - View cluster status: mup cluster display %s\n", clusterName)
			fmt.Printf("  - Connect to cluster:  mup cluster connect %s\n", clusterName)
			fmt.Printf("  - Start cluster:       mup cluster start %s\n", clusterName)
			fmt.Printf("  - Stop cluster:        mup cluster stop %s\n", clusterName)
		}

		return nil
	},
}

func init() {
	// Add cluster command to root
	rootCmd.AddCommand(clusterCmd)

	// Add subcommands
	clusterCmd.AddCommand(clusterDeployCmd)
	clusterCmd.AddCommand(clusterUpgradeCmd)
	clusterCmd.AddCommand(clusterImportCmd)
	clusterCmd.AddCommand(clusterStartCmd)
	clusterCmd.AddCommand(clusterStopCmd)
	clusterCmd.AddCommand(clusterDisplayCmd)
	clusterCmd.AddCommand(clusterDestroyCmd)
	clusterCmd.AddCommand(clusterListCmd)
	clusterCmd.AddCommand(clusterConnectCmd)

	// Deploy command flags
	clusterDeployCmd.Flags().StringVarP(&clusterDeployVersion, "version", "v", "7.0", "MongoDB version to deploy")
	clusterDeployCmd.Flags().StringVar(&clusterDeployVariant, "variant", "mongo", "MongoDB variant: 'mongo' (official) or 'percona' (Percona Server for MongoDB)")
	clusterDeployCmd.Flags().StringVar(&clusterDeployUser, "user", "", "SSH user (default: from topology file)")
	clusterDeployCmd.Flags().StringVar(&clusterDeployIdentityFile, "identity-file", "", "SSH private key path")
	clusterDeployCmd.Flags().BoolVar(&clusterDeployYes, "yes", false, "Skip confirmation prompts")
	clusterDeployCmd.Flags().DurationVarP(&clusterDeployTimeout, "timeout", "t", 30*time.Minute, "Deployment timeout")
	clusterDeployCmd.Flags().BoolVar(&clusterDeployNoMonitoring, "no-monitoring", false, "Disable monitoring deployment (Victoria Metrics, Grafana, exporters)")
	clusterDeployCmd.Flags().BoolVar(&clusterDeployPlanOnly, "plan-only", false, "Generate deployment plan without executing (dry-run)")
	clusterDeployCmd.Flags().BoolVar(&clusterDeployAutoApprove, "auto-approve", false, "Skip confirmation and apply plan automatically")
	clusterDeployCmd.Flags().StringVar(&clusterDeployPlanFile, "plan-file", "", "Save plan to specific file path (default: auto-generated)")
	clusterDeployCmd.Flags().BoolVar(&clusterDeploySimulate, "simulate", false, "REQ-SIM-001: Run command in simulation mode (no filesystem/process/network changes)")
	clusterDeployCmd.Flags().StringVar(&clusterDeploySimulateScenario, "simulate-scenario", "", "REQ-SIM-041: Path to scenario YAML file for simulation")
	clusterDeployCmd.Flags().BoolVar(&clusterDeploySimulateVerbose, "simulate-verbose", false, "REQ-SIM-049: Show detailed operation log in simulation mode")

	// Start/stop command flags
	clusterStartCmd.Flags().StringVar(&clusterNodeFilter, "node", "", "Start specific node only (host:port)")
	clusterStopCmd.Flags().StringVar(&clusterNodeFilter, "node", "", "Stop specific node only (host:port)")
	clusterStopCmd.Flags().BoolVar(&clusterDeployYes, "yes", false, "Skip confirmation prompt")

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
	clusterUpgradeCmd.Flags().StringVar(&clusterUpgradeResumePromptLevel, "resume-with-prompt-level", "", "Override prompt level when resuming")
	clusterUpgradeCmd.Flags().BoolVar(&clusterUpgradeDryRun, "dry-run", false, "Show upgrade plan without executing")

	// Import command flags
	clusterImportCmd.Flags().BoolVar(&clusterImportAutoDetect, "auto-detect", false, "Auto-detect running MongoDB instances")
	clusterImportCmd.Flags().StringVar(&clusterImportConfigFile, "config", "", "MongoDB config file path (for manual mode)")
	clusterImportCmd.Flags().StringVar(&clusterImportDataDir, "data-dir", "", "MongoDB data directory path (for manual mode)")
	clusterImportCmd.Flags().IntVar(&clusterImportPort, "port", 27017, "MongoDB port (for manual mode)")
	clusterImportCmd.Flags().StringVar(&clusterImportHost, "host", "localhost", "MongoDB host (for manual mode)")
	clusterImportCmd.Flags().BoolVar(&clusterImportDryRun, "dry-run", false, "Preview changes without executing")
	clusterImportCmd.Flags().BoolVar(&clusterImportSkipRestart, "skip-restart", false, "Skip process restart (structure only)")
	clusterImportCmd.Flags().BoolVar(&clusterImportKeepSystemd, "keep-systemd-files", false, "Keep systemd unit files (don't remove)")
	clusterImportCmd.Flags().StringVar(&clusterImportSSHHost, "ssh-host", "", "Remote host for import (user@host format)")
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
		ClusterName:      clusterName,
		FromVersion:      clusterMeta.Version,
		ToVersion:        clusterUpgradeToVersion,
		TargetVariant:    variant,
		UpgradeFCV:       clusterUpgradeFCV,
		ParallelShards:   clusterUpgradeParallelShards,
		PromptLevel:      promptLevel,
		MetaDir:          metaDir,
		Topology:         topo,
		Executors:        executors,
		StateManager:     stateManager,
		Prompter:         prompter,
		DryRun:           clusterUpgradeDryRun,
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
