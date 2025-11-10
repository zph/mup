package cluster

import (
	"context"
	"fmt"
)

// Destroy destroys a cluster
func (m *Manager) Destroy(ctx context.Context, clusterName string, keepData bool) error {
	// Load metadata
	metadata, err := m.metaMgr.Load(clusterName)
	if err != nil {
		return err
	}

	fmt.Printf("Destroying cluster '%s'...\n", clusterName)

	// Stop all nodes first
	fmt.Println("Stopping all MongoDB processes...")
	if err := m.Stop(ctx, clusterName, ""); err != nil {
		fmt.Printf("Warning: failed to stop some nodes: %v\n", err)
	}

	// Remove data directories if requested
	if !keepData && metadata.DeployMode == "local" {
		fmt.Println("Removing data directories...")

		// Create executors
		executors, err := m.createExecutors(metadata)
		if err != nil {
			return fmt.Errorf("failed to create executors: %w", err)
		}
		defer m.closeExecutors(executors)

		for _, node := range metadata.Nodes {
			exec := executors[node.Host]

			// Remove data directory
			if node.DataDir != "" {
				if err := exec.RemoveDirectory(node.DataDir); err != nil {
					fmt.Printf("  ! Failed to remove data directory %s: %v\n", node.DataDir, err)
				} else {
					fmt.Printf("  ✓ Removed %s\n", node.DataDir)
				}
			}

			// Remove log directory
			if err := exec.RemoveDirectory(node.LogDir); err != nil {
				fmt.Printf("  ! Failed to remove log directory %s: %v\n", node.LogDir, err)
			}

			// Remove config directory
			if err := exec.RemoveDirectory(node.ConfigDir); err != nil {
				fmt.Printf("  ! Failed to remove config directory %s: %v\n", node.ConfigDir, err)
			}
		}
	}

	// Delete metadata
	fmt.Println("Removing cluster metadata...")
	if err := m.metaMgr.Delete(clusterName); err != nil {
		return fmt.Errorf("failed to delete metadata: %w", err)
	}

	fmt.Printf("\n✓ Cluster '%s' destroyed\n", clusterName)
	return nil
}
