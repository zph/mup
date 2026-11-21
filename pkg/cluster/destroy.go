package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/zph/mup/pkg/paths"
	"github.com/zph/mup/pkg/supervisor"
)

const (
	supervisorConfigFile = "supervisor.ini"
	versionDirGlob       = "v*"
)

// Destroy stops a cluster and removes what mup created for it.
//
// LOCAL-001: only paths inside the cluster directory are removed. Node paths
// recorded in metadata are never used as deletion targets because they can
// hold raw topology values that point outside mup's storage.
func (m *Manager) Destroy(ctx context.Context, clusterName string, keepData bool) error {
	clusterDir := m.metaMgr.GetClusterDir(clusterName)

	if _, err := m.metaMgr.Load(clusterName); err != nil {
		// LOCAL-040: a deploy that failed before saving metadata leaves a
		// directory, and possibly running processes, with no metadata.
		if _, statErr := os.Stat(clusterDir); statErr != nil || metadataExists(m.metaMgr.GetMetaFile(clusterName)) {
			return err
		}
		fmt.Printf("Cluster '%s' has no metadata (a deployment did not finish); cleaning up what it left...\n", clusterName)
		if err := stopSupervisorsIn(ctx, clusterDir, clusterName); err != nil {
			return err
		}
		return m.removeClusterFiles(clusterName, clusterDir, keepData)
	}

	fmt.Printf("Destroying cluster '%s'...\n", clusterName)

	fmt.Println("Stopping all MongoDB processes...")
	if err := m.Stop(ctx, clusterName, ""); err != nil {
		fmt.Printf("Warning: failed to stop some nodes: %v\n", err)
	}

	return m.removeClusterFiles(clusterName, clusterDir, keepData)
}

// metadataExists reports whether a metadata file is present, even if unreadable.
func metadataExists(metaFile string) bool {
	_, err := os.Stat(metaFile)
	return err == nil
}

// removeClusterFiles removes the cluster directory, or everything in it
// except data when keepData is set.
func (m *Manager) removeClusterFiles(clusterName, clusterDir string, keepData bool) error {
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

// supervisorDirs returns the version directories of a cluster that hold a
// supervisor configuration, in name order.
// LOCAL-040
func supervisorDirs(clusterDir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(clusterDir, versionDirGlob, supervisorConfigFile))
	if err != nil {
		return nil, fmt.Errorf("failed to look for supervisors in %s: %w", clusterDir, err)
	}
	dirs := make([]string, 0, len(matches))
	for _, match := range matches {
		dirs = append(dirs, filepath.Dir(match))
	}
	sort.Strings(dirs)
	return dirs, nil
}

// stopSupervisorsIn shuts down every supervisor running from a cluster
// directory, and with it the processes each one manages. Files are only
// removed once this succeeds, so nothing is deleted under a live process.
// LOCAL-040
func stopSupervisorsIn(ctx context.Context, clusterDir, clusterName string) error {
	dirs, err := supervisorDirs(clusterDir)
	if err != nil {
		return err
	}

	var failures []error
	for _, dir := range dirs {
		supMgr, err := supervisor.LoadManager(dir, clusterName)
		if err != nil {
			failures = append(failures, fmt.Errorf("failed to load supervisor in %s: %w", dir, err))
			continue
		}
		if !supMgr.IsRunning() {
			continue
		}
		if err := supMgr.Stop(ctx); err != nil {
			failures = append(failures, fmt.Errorf("failed to stop supervisor in %s: %w", dir, err))
		}
	}
	return errors.Join(failures...)
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
