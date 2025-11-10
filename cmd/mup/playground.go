package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/zph/mup/pkg/cluster"
	"github.com/zph/mup/pkg/deploy"
	"github.com/zph/mup/pkg/meta"
)

const (
	playgroundClusterName = "playground"
	playgroundTopology    = `# Playground topology - 3-node replica set
global:
  user: mongodb
  deploy_dir: ~/.mup/playground
  data_dir: ~/.mup/playground/data
  log_dir: ~/.mup/playground/logs
  config_dir: ~/.mup/playground/conf

mongod_servers:
  - host: localhost
    port: 0  # Auto-allocated
    replica_set: rs0

  - host: localhost
    port: 0
    replica_set: rs0

  - host: localhost
    port: 0
    replica_set: rs0
`
)

var (
	playgroundMongoVersion string
	playgroundTimeout      time.Duration
	playgroundYes          bool
)

var playgroundCmd = &cobra.Command{
	Use:   "playground",
	Short: "Manage local MongoDB playground cluster",
	Long: `Quick local test cluster for development and testing.
Creates a 3-node replica set on localhost with auto-allocated ports.`,
}

var playgroundStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a local playground MongoDB cluster",
	Long: `Start a local MongoDB playground cluster for development and testing.
The cluster runs on your local machine as a 3-node replica set.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), playgroundTimeout)
		defer cancel()

		// Check if playground already exists
		metaMgr, err := meta.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create meta manager: %w", err)
		}

		existing, err := metaMgr.Load(playgroundClusterName)
		if err == nil {
			// Playground exists, just start it
			fmt.Printf("Playground cluster already exists, starting...\n")

			clusterMgr, err := cluster.NewManager()
			if err != nil {
				return fmt.Errorf("failed to create cluster manager: %w", err)
			}

			if err := clusterMgr.Start(ctx, playgroundClusterName, ""); err != nil {
				return fmt.Errorf("failed to start playground: %w", err)
			}

			fmt.Println("\n✓ Playground cluster started successfully!")
			displayPlaygroundInfo(existing)
			return nil
		}

		// Playground doesn't exist, deploy it
		fmt.Printf("Creating playground cluster (MongoDB %s)...\n\n", playgroundMongoVersion)

		// Create temporary topology file
		tmpDir, err := os.MkdirTemp("", "mup-playground-")
		if err != nil {
			return fmt.Errorf("failed to create temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		topologyFile := filepath.Join(tmpDir, "topology.yaml")
		if err := os.WriteFile(topologyFile, []byte(playgroundTopology), 0644); err != nil {
			return fmt.Errorf("failed to write topology file: %w", err)
		}

		// Deploy the cluster
		cfg := deploy.DeployConfig{
			ClusterName:  playgroundClusterName,
			Version:      playgroundMongoVersion,
			TopologyFile: topologyFile,
			SkipConfirm:  true,
		}

		deployer, err := deploy.NewDeployer(cfg)
		if err != nil {
			return fmt.Errorf("failed to create deployer: %w", err)
		}
		defer deployer.Close()

		if err := deployer.Deploy(ctx); err != nil {
			return fmt.Errorf("failed to deploy playground: %w", err)
		}

		// Load metadata to display info
		metadata, err := metaMgr.Load(playgroundClusterName)
		if err != nil {
			return fmt.Errorf("failed to load metadata: %w", err)
		}

		fmt.Println("\n✓ Playground cluster created successfully!")
		displayPlaygroundInfo(metadata)

		return nil
	},
}

var playgroundStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the playground cluster",
	Long:  `Stop the running playground MongoDB cluster.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), playgroundTimeout)
		defer cancel()

		// Require confirmation unless --yes flag is passed
		if !playgroundYes {
			fmt.Printf("Are you sure you want to stop the playground cluster? [y/N]: ")
			var response string
			fmt.Scanln(&response)
			if response != "y" && response != "Y" && response != "yes" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		mgr, err := cluster.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create cluster manager: %w", err)
		}

		fmt.Println("Stopping playground cluster...")
		if err := mgr.Stop(ctx, playgroundClusterName, ""); err != nil {
			return fmt.Errorf("failed to stop playground: %w", err)
		}

		fmt.Println("✓ Playground cluster stopped successfully!")
		return nil
	},
}

var playgroundStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show playground cluster status",
	Long:  `Display the current status of the playground MongoDB cluster.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := cluster.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create cluster manager: %w", err)
		}

		if err := mgr.Display(context.Background(), playgroundClusterName, "text"); err != nil {
			if err.Error() == fmt.Sprintf("cluster '%s' not found", playgroundClusterName) {
				fmt.Println("No playground cluster found")
				fmt.Println("Run 'mup playground start' to create one")
				return nil
			}
			return err
		}

		return nil
	},
}

var playgroundDestroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Destroy the playground cluster and remove all data",
	Long: `Completely remove the playground cluster including all data.
This operation cannot be undone.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), playgroundTimeout)
		defer cancel()

		// Require confirmation unless --yes flag is passed
		if !playgroundYes {
			fmt.Printf("Are you sure you want to destroy the playground cluster and remove all data? [y/N]: ")
			var response string
			fmt.Scanln(&response)
			if response != "y" && response != "Y" && response != "yes" {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		mgr, err := cluster.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create cluster manager: %w", err)
		}

		fmt.Println("Destroying playground cluster and removing all data...")
		if err := mgr.Destroy(ctx, playgroundClusterName, false); err != nil {
			return fmt.Errorf("failed to destroy playground: %w", err)
		}

		fmt.Println("✓ Playground cluster destroyed successfully!")
		return nil
	},
}

var playgroundConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect to the playground cluster using mongosh",
	Long:  `Connect to the running playground MongoDB cluster.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		metaMgr, err := meta.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create meta manager: %w", err)
		}

		// Get cluster metadata
		metadata, err := metaMgr.Load(playgroundClusterName)
		if err != nil {
			return fmt.Errorf("no playground cluster found. Run 'mup playground start' first")
		}

		if metadata.Status != "running" {
			return fmt.Errorf("playground cluster is not running (status: %s). Run 'mup playground start'", metadata.Status)
		}

		// Build connection string
		var hosts []string
		rsName := ""
		for _, node := range metadata.Nodes {
			if node.Type == "mongod" {
				hosts = append(hosts, fmt.Sprintf("%s:%d", node.Host, node.Port))
				if rsName == "" && node.ReplicaSet != "" {
					rsName = node.ReplicaSet
				}
			}
		}

		if len(hosts) == 0 {
			return fmt.Errorf("no mongod nodes found in playground cluster")
		}

		var connectionString string
		if rsName != "" {
			connectionString = fmt.Sprintf("mongodb://%s/?replicaSet=%s", hosts[0], rsName)
		} else {
			connectionString = fmt.Sprintf("mongodb://%s", hosts[0])
		}

		fmt.Printf("Connecting to playground cluster...\n")
		fmt.Printf("Connection: %s\n\n", connectionString)

		// Try mongosh first, fall back to mongo
		mongoshPath, err := exec.LookPath("mongosh")
		var shellCmd *exec.Cmd

		if err == nil {
			shellCmd = exec.Command(mongoshPath, connectionString)
		} else {
			mongoPath, err := exec.LookPath("mongo")
			if err != nil {
				return fmt.Errorf("neither mongosh nor mongo found in PATH. Please install MongoDB shell")
			}
			shellCmd = exec.Command(mongoPath, connectionString)
		}

		// Set up stdin/stdout/stderr to be interactive
		shellCmd.Stdin = os.Stdin
		shellCmd.Stdout = os.Stdout
		shellCmd.Stderr = os.Stderr

		// Execute the command
		err = shellCmd.Run()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				// Get the exit code
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					os.Exit(status.ExitStatus())
				}
			}
			return fmt.Errorf("failed to run connection command: %w", err)
		}

		return nil
	},
}

func displayPlaygroundInfo(metadata *meta.ClusterMetadata) {
	// Build connection string
	var hosts []string
	rsName := ""
	for _, node := range metadata.Nodes {
		if node.Type == "mongod" {
			hosts = append(hosts, fmt.Sprintf("%s:%d", node.Host, node.Port))
			if rsName == "" && node.ReplicaSet != "" {
				rsName = node.ReplicaSet
			}
		}
	}

	var connectionString string
	if rsName != "" {
		connectionString = fmt.Sprintf("mongodb://%s/?replicaSet=%s", hosts[0], rsName)
	} else {
		connectionString = fmt.Sprintf("mongodb://%s", hosts[0])
	}

	fmt.Printf("\nConnection URI: %s\n", connectionString)
	fmt.Printf("MongoDB version: %s\n", metadata.Version)
	fmt.Printf("Nodes: %d\n", len(metadata.Nodes))
	fmt.Printf("\nTo connect: mongosh \"%s\"\n", connectionString)
	fmt.Printf("To view status: mup playground status\n")
	fmt.Printf("To stop: mup playground stop\n")
}

func init() {
	// Add subcommands
	playgroundCmd.AddCommand(playgroundStartCmd)
	playgroundCmd.AddCommand(playgroundStopCmd)
	playgroundCmd.AddCommand(playgroundStatusCmd)
	playgroundCmd.AddCommand(playgroundConnectCmd)
	playgroundCmd.AddCommand(playgroundDestroyCmd)

	// Flags
	playgroundStartCmd.Flags().StringVarP(&playgroundMongoVersion, "version", "v", "7.0", "MongoDB version to use")
	playgroundCmd.PersistentFlags().DurationVarP(&playgroundTimeout, "timeout", "t", 5*time.Minute, "Operation timeout")
	playgroundStopCmd.Flags().BoolVarP(&playgroundYes, "yes", "y", false, "Skip confirmation prompt")
	playgroundDestroyCmd.Flags().BoolVarP(&playgroundYes, "yes", "y", false, "Skip confirmation prompt")

	// Add to root command
	rootCmd.AddCommand(playgroundCmd)
}
