package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/zph/mup/pkg/playground"
)

var (
	playgroundMongoVersion string
	playgroundTimeout      time.Duration
)

var playgroundCmd = &cobra.Command{
	Use:   "playground",
	Short: "Manage local MongoDB playground cluster",
	Long: `Quick local test cluster for development and testing.
Uses mongo-scaffold to spin up a local MongoDB instance.`,
}

var playgroundStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a local playground MongoDB cluster",
	Long: `Start a local MongoDB playground cluster for development and testing.
The cluster runs on your local machine and is automatically managed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(context.Background(), playgroundTimeout)
		defer cancel()

		mgr, err := playground.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create playground manager: %w", err)
		}

		fmt.Printf("Starting playground cluster (MongoDB %s)...\n", playgroundMongoVersion)
		if err := mgr.Start(ctx, playgroundMongoVersion); err != nil {
			return fmt.Errorf("failed to start playground: %w", err)
		}

		state, err := mgr.Status()
		if err != nil {
			return fmt.Errorf("failed to get status: %w", err)
		}

		fmt.Println("\n✓ Playground cluster started successfully!")
		fmt.Printf("\nConnection URI: %s\n", state.ConnectionURI)
		fmt.Printf("Data directory: %s\n", state.DataDir)
		fmt.Printf("\nTo connect: mongosh \"%s\"\n", state.ConnectionURI)

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

		mgr, err := playground.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create playground manager: %w", err)
		}

		fmt.Println("Stopping playground cluster...")
		if err := mgr.Stop(ctx); err != nil {
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
		mgr, err := playground.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create playground manager: %w", err)
		}

		state, err := mgr.Status()
		if err != nil {
			fmt.Println("No playground cluster found")
			return nil
		}

		fmt.Printf("Playground Cluster Status\n")
		fmt.Printf("========================\n")
		fmt.Printf("Name:           %s\n", state.Name)
		fmt.Printf("Status:         %s\n", state.Status)
		fmt.Printf("MongoDB:        %s\n", state.MongoVersion)
		fmt.Printf("Started:        %s\n", state.StartedAt.Format(time.RFC3339))
		fmt.Printf("Connection URI: %s\n", state.ConnectionURI)
		fmt.Printf("Data directory: %s\n", state.DataDir)

		if state.Status == "running" {
			uptime := time.Since(state.StartedAt)
			fmt.Printf("Uptime:         %s\n", uptime.Round(time.Second))
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

		mgr, err := playground.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create playground manager: %w", err)
		}

		fmt.Println("Destroying playground cluster and removing all data...")
		if err := mgr.Destroy(ctx); err != nil {
			return fmt.Errorf("failed to destroy playground: %w", err)
		}

		fmt.Println("✓ Playground cluster destroyed successfully!")
		return nil
	},
}

var playgroundConnectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Connect to the playground cluster using mongo/mongosh",
	Long: `Connect to the running playground MongoDB cluster.
This uses the connection command from the cluster info file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := playground.NewManager()
		if err != nil {
			return fmt.Errorf("failed to create playground manager: %w", err)
		}

		// Get cluster info
		clusterInfo, err := mgr.GetClusterInfo()
		if err != nil {
			return fmt.Errorf("failed to get cluster info: %w", err)
		}

		// Check if cluster is running
		state, err := mgr.Status()
		if err != nil {
			return fmt.Errorf("no playground cluster found")
		}

		if state.Status != "running" {
			return fmt.Errorf("playground cluster is not running (status: %s)", state.Status)
		}

		// Use the connection command from cluster info
		if clusterInfo.ConnectionCommand == "" {
			return fmt.Errorf("no connection command found in cluster info")
		}

		fmt.Printf("Connecting to playground cluster...\n")
		fmt.Printf("Connection: %s\n\n", clusterInfo.ConnectionString)

		// Execute the connection command using sh -c to handle the full command line
		shellCmd := exec.Command("sh", "-c", clusterInfo.ConnectionCommand)

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

	// Add to root command
	rootCmd.AddCommand(playgroundCmd)
}
