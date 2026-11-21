package main

import (
	"path/filepath"

	"github.com/zph/mup/pkg/paths"
)

// getStorageDir returns the base mup storage directory (~/.mup/storage)
func getStorageDir() (string, error) {
	return paths.StorageDir()
}

// getClusterDir returns the storage directory for a specific cluster
func getClusterDir(clusterName string) (string, error) {
	storageDir, err := getStorageDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(storageDir, "clusters", clusterName), nil
}
