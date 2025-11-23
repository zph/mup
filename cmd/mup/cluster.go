package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
	"github.com/zph/mup/pkg/cluster"
	"github.com/zph/mup/pkg/deploy"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
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

Examples:
  # Deploy a local 3-node replica set with monitoring
  mup cluster deploy my-rs replica-set.yaml

  # Deploy without monitoring
  mup cluster deploy my-rs replica-set.yaml --no-monitoring

  # Deploy a remote sharded cluster
  mup cluster deploy prod-cluster sharded.yaml --user admin

  # Deploy with specific MongoDB version
  mup cluster deploy test-rs replica-set.yaml --version 7.0.5

  # Deploy with Percona Server for MongoDB
  mup cluster deploy my-rs replica-set.yaml --variant percona --version 8.0.12-4
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

		// Create deployer
		cfg := deploy.DeployConfig{
			ClusterName:       clusterName,
			Version:           clusterDeployVersion,
			Variant:           variant,
			TopologyFile:      topologyFile,
			SSHUser:           clusterDeployUser,
			IdentityFile:      clusterDeployIdentityFile,
			SkipConfirm:       clusterDeployYes,
			DisableMonitoring: clusterDeployNoMonitoring,
		}

		deployer, err := deploy.NewDeployer(cfg)
		if err != nil {
			return fmt.Errorf("failed to create deployer: %w", err)
		}
		defer deployer.Close()

		// Execute deployment
		fmt.Printf("\nDeploying cluster '%s'...\n\n", clusterName)

		if err := deployer.Deploy(ctx); err != nil {
			return fmt.Errorf("deployment failed: %w", err)
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

		if metadata.ConnectionCommand == "" {
			return fmt.Errorf("no connection command found in metadata for cluster '%s'", clusterName)
		}

		fmt.Printf("Connecting to cluster '%s'...\n", clusterName)
		fmt.Printf("Executing: %s\n\n", metadata.ConnectionCommand)

		// Execute the connection command via shell
		// Use sh -c to properly handle quoted connection strings
		shellCmd := exec.Command("sh", "-c", metadata.ConnectionCommand)
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
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		metaDir := fmt.Sprintf("%s/.mup/storage/clusters/%s", homeDir, clusterName)

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

func init() {
	// Add cluster command to root
	rootCmd.AddCommand(clusterCmd)

	// Add subcommands
	clusterCmd.AddCommand(clusterDeployCmd)
	clusterCmd.AddCommand(clusterUpgradeCmd)
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
