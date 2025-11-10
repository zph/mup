package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "mup",
	Short: "MongoDB Cluster Management Tool",
	Long: `Mup (MongoDB Utility Platform) is a cluster management tool for MongoDB.
It simplifies deployment, configuration, and lifecycle management of MongoDB
clusters (standalone, replica sets, and sharded clusters).`,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
