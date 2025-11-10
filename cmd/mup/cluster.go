package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/zph/mup/pkg/cluster"
	"github.com/zph/mup/pkg/deploy"
)

var (
	clusterDeployVersion      string
	clusterDeployUser         string
	clusterDeployIdentityFile string
	clusterDeployYes          bool
	clusterDeployTimeout      time.Duration

	clusterNodeFilter    string
	clusterDisplayFormat string
	clusterKeepData      bool
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

Examples:
  # Deploy a local 3-node replica set
  mup cluster deploy my-rs replica-set.yaml

  # Deploy a remote sharded cluster
  mup cluster deploy prod-cluster sharded.yaml --user admin

  # Deploy with specific MongoDB version
  mup cluster deploy test-rs replica-set.yaml --version 7.0.5
`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		topologyFile := args[1]

		ctx, cancel := context.WithTimeout(context.Background(), clusterDeployTimeout)
		defer cancel()

		// Create deployer
		cfg := deploy.DeployConfig{
			ClusterName:  clusterName,
			Version:      clusterDeployVersion,
			TopologyFile: topologyFile,
			SSHUser:      clusterDeployUser,
			IdentityFile: clusterDeployIdentityFile,
			SkipConfirm:  clusterDeployYes,
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

func init() {
	// Add cluster command to root
	rootCmd.AddCommand(clusterCmd)

	// Add subcommands
	clusterCmd.AddCommand(clusterDeployCmd)
	clusterCmd.AddCommand(clusterStartCmd)
	clusterCmd.AddCommand(clusterStopCmd)
	clusterCmd.AddCommand(clusterDisplayCmd)
	clusterCmd.AddCommand(clusterDestroyCmd)

	// Deploy command flags
	clusterDeployCmd.Flags().StringVarP(&clusterDeployVersion, "version", "v", "7.0", "MongoDB version to deploy")
	clusterDeployCmd.Flags().StringVar(&clusterDeployUser, "user", "", "SSH user (default: from topology file)")
	clusterDeployCmd.Flags().StringVar(&clusterDeployIdentityFile, "identity-file", "", "SSH private key path")
	clusterDeployCmd.Flags().BoolVar(&clusterDeployYes, "yes", false, "Skip confirmation prompts")
	clusterDeployCmd.Flags().DurationVarP(&clusterDeployTimeout, "timeout", "t", 30*time.Minute, "Deployment timeout")

	// Start/stop command flags
	clusterStartCmd.Flags().StringVar(&clusterNodeFilter, "node", "", "Start specific node only (host:port)")
	clusterStopCmd.Flags().StringVar(&clusterNodeFilter, "node", "", "Stop specific node only (host:port)")
	clusterStopCmd.Flags().BoolVar(&clusterDeployYes, "yes", false, "Skip confirmation prompt")

	// Display command flags
	clusterDisplayCmd.Flags().StringVar(&clusterDisplayFormat, "format", "text", "Output format: text, json, yaml")

	// Destroy command flags
	clusterDestroyCmd.Flags().BoolVar(&clusterKeepData, "keep-data", false, "Keep data directories")
	clusterDestroyCmd.Flags().BoolVar(&clusterDeployYes, "yes", false, "Skip confirmation prompt")
}
