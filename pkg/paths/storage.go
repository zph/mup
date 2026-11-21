package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvStorageDir overrides where mup keeps clusters, cached packages and
// helper binaries. LOCAL-020
const EnvStorageDir = "MUP_STORAGE_DIR"

const (
	mupHomeDirName  = ".mup"
	storageDirName  = "storage"
	clustersDirName = "clusters"
	packagesDirName = "packages"
	binCacheDirName = "bin"
)

// StorageDir returns mup's storage root: $MUP_STORAGE_DIR when set, otherwise
// ~/.mup/storage.
func StorageDir() (string, error) {
	if override := os.Getenv(EnvStorageDir); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("%s must be an absolute path, got %q", EnvStorageDir, override)
		}
		return filepath.Clean(override), nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, mupHomeDirName, storageDirName), nil
}

// ClustersDir returns the directory holding one subdirectory per cluster.
func ClustersDir() (string, error) {
	return storageSubdir(clustersDirName)
}

// ClusterDir returns the directory for a single cluster.
func ClusterDir(clusterName string) (string, error) {
	clusters, err := ClustersDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(clusters, clusterName), nil
}

// PackagesDir returns the MongoDB binary cache directory.
func PackagesDir() (string, error) {
	return storageSubdir(packagesDirName)
}

// BinCacheDir returns the cache directory for helper binaries such as supervisord.
func BinCacheDir() (string, error) {
	return storageSubdir(binCacheDirName)
}

func storageSubdir(name string) (string, error) {
	storage, err := StorageDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(storage, name), nil
}
