package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zph/mup/pkg/paths"
)

// Destroy stops a cluster and removes what mup created for it.
//
// LOCAL-001: only paths inside the cluster directory are removed. Node paths
// recorded in metadata are never used as deletion targets because they can
// hold raw topology values that point outside mup's storage.
func (m *Manager) Destroy(ctx context.Context, clusterName string, keepData bool) error {
	if _, err := m.metaMgr.Load(clusterName); err != nil {
		return err
	}

	fmt.Printf("Destroying cluster '%s'...\n", clusterName)

	fmt.Println("Stopping all MongoDB processes...")
	if err := m.Stop(ctx, clusterName, ""); err != nil {
		fmt.Printf("Warning: failed to stop some nodes: %v\n", err)
	}

	clusterDir := m.metaMgr.GetClusterDir(clusterName)

	// LOCAL-002
	if !keepData {
		fmt.Println("Removing cluster directory...")
		if err := m.metaMgr.Delete(clusterName); err != nil {
			return fmt.Errorf("failed to delete cluster directory: %w", err)
		}
		fmt.Printf("\n✓ Cluster '%s' destroyed\n", clusterName)
		return nil
	}

	// LOCAL-003
	dataDir := paths.NewClusterLayout(clusterDir).DataDir()
	fmt.Println("Removing cluster files (keeping data)...")
	if err := removeAllExcept(clusterDir, dataDir); err != nil {
		return err
	}

	fmt.Printf("\n✓ Cluster '%s' destroyed; data kept at %s\n", clusterName, dataDir)
	return nil
}

// removeAllExcept removes every entry directly under dir other than keep.
func removeAllExcept(dir, keep string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", dir, err)
	}

	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())
		if entryPath == keep {
			continue
		}
		if err := os.RemoveAll(entryPath); err != nil {
			return fmt.Errorf("failed to remove %s: %w", entryPath, err)
		}
	}
	return nil
}
