package importer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zph/mup/pkg/executor"
)

// StructureBuilder handles creating mup's directory structure for imported clusters
type StructureBuilder struct {
	executor executor.Executor
}

// StructureSetupConfig contains configuration for setting up directory structure
type StructureSetupConfig struct {
	ClusterDir       string            // Base cluster directory (e.g., ~/.mup/storage/clusters/my-cluster)
	Version          string            // MongoDB version (e.g., "7.0.5")
	ExistingDataDirs map[string]string // Map of nodeID -> existing data directory path
}

// NewStructureBuilder creates a new StructureBuilder
func NewStructureBuilder(exec executor.Executor) *StructureBuilder {
	return &StructureBuilder{
		executor: exec,
	}
}

// CreateVersionDirectories creates the version-specific directory structure
// IMP-013: Create version-specific directory structure
func (sb *StructureBuilder) CreateVersionDirectories(clusterDir, version string) error {
	versionDir := filepath.Join(clusterDir, "v"+version)

	// IMP-015: Create bin/, conf/, logs/ directories
	dirs := []string{
		versionDir,
		filepath.Join(versionDir, "bin"),
		filepath.Join(versionDir, "conf"),
		filepath.Join(versionDir, "logs"),
	}

	for _, dir := range dirs {
		if err := sb.executor.CreateDirectory(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// CreateDataSymlink creates a symlink from mup's data directory to an existing data directory
// IMP-014: Create symlinks to existing data directories without moving data files
func (sb *StructureBuilder) CreateDataSymlink(dataDir, nodeID, existingDataDir string) error {
	// Ensure data directory exists
	if err := sb.executor.CreateDirectory(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	symlinkPath := filepath.Join(dataDir, nodeID)

	// Resolve absolute paths to check if they're the same location
	absSymlinkPath, err := filepath.Abs(symlinkPath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlink path: %w", err)
	}
	absExistingDir, err := filepath.Abs(existingDataDir)
	if err != nil {
		return fmt.Errorf("failed to resolve existing data dir path: %w", err)
	}

	// Check if we're trying to symlink to the same location
	if absSymlinkPath == absExistingDir {
		// Symlink and target are the same - skip symlink creation
		// This can happen when re-importing a cluster already in mup's structure
		return nil
	}

	// Check if symlink already exists
	exists, err := sb.executor.FileExists(symlinkPath)
	if err != nil {
		return fmt.Errorf("failed to check if symlink exists: %w", err)
	}

	if exists {
		// Check if it's already pointing to the right place
		if info, err := os.Lstat(symlinkPath); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(symlinkPath)
				if err == nil && target == existingDataDir {
					// Already correctly symlinked
					return nil
				}
			}
		}
		// Remove existing symlink/file
		if err := sb.executor.RemoveFile(symlinkPath); err != nil {
			return fmt.Errorf("failed to remove existing symlink: %w", err)
		}
	}

	// IMP-014: Create symlink (no data movement)
	if err := os.Symlink(existingDataDir, symlinkPath); err != nil {
		return fmt.Errorf("failed to create symlink: %w", err)
	}

	return nil
}

// CreateCurrentSymlink creates the 'current' symlink pointing to the active version
// IMP-016: Create 'current' symlink pointing to version-specific directory
func (sb *StructureBuilder) CreateCurrentSymlink(clusterDir, version string) error {
	currentLink := filepath.Join(clusterDir, "current")
	versionDirName := "v" + version

	// Check if current symlink already exists
	exists, err := sb.executor.FileExists(currentLink)
	if err != nil {
		return fmt.Errorf("failed to check if current symlink exists: %w", err)
	}

	if exists {
		// Remove existing symlink
		if err := sb.executor.RemoveFile(currentLink); err != nil {
			return fmt.Errorf("failed to remove existing current symlink: %w", err)
		}
	}

	// Create symlink (relative path for portability)
	if err := os.Symlink(versionDirName, currentLink); err != nil {
		return fmt.Errorf("failed to create current symlink: %w", err)
	}

	return nil
}

// CreatePreviousSymlink creates the 'previous' symlink for rollback
// This is used during upgrades, but for initial import there is no previous version
func (sb *StructureBuilder) CreatePreviousSymlink(clusterDir, version string) error {
	previousLink := filepath.Join(clusterDir, "previous")
	versionDirName := "v" + version

	// Check if previous symlink already exists
	exists, err := sb.executor.FileExists(previousLink)
	if err != nil {
		return fmt.Errorf("failed to check if previous symlink exists: %w", err)
	}

	if exists {
		// Remove existing symlink
		if err := sb.executor.RemoveFile(previousLink); err != nil {
			return fmt.Errorf("failed to remove existing previous symlink: %w", err)
		}
	}

	// Create symlink
	if err := os.Symlink(versionDirName, previousLink); err != nil {
		return fmt.Errorf("failed to create previous symlink: %w", err)
	}

	return nil
}

// SetupImportStructure sets up the complete directory structure for an imported cluster
// This is the main entry point that orchestrates all structure creation
func (sb *StructureBuilder) SetupImportStructure(config StructureSetupConfig) error {
	// IMP-013: Create version-specific directories
	if err := sb.CreateVersionDirectories(config.ClusterDir, config.Version); err != nil {
		return fmt.Errorf("failed to create version directories: %w", err)
	}

	// IMP-014: Create data symlinks for each node
	dataDir := filepath.Join(config.ClusterDir, "data")
	for nodeID, existingDataDir := range config.ExistingDataDirs {
		if err := sb.CreateDataSymlink(dataDir, nodeID, existingDataDir); err != nil {
			return fmt.Errorf("failed to create data symlink for %s: %w", nodeID, err)
		}
	}

	// IMP-016: Create 'current' symlink
	if err := sb.CreateCurrentSymlink(config.ClusterDir, config.Version); err != nil {
		return fmt.Errorf("failed to create current symlink: %w", err)
	}

	// Note: We don't create 'previous' symlink during initial import
	// It will be created during the first upgrade

	return nil
}

// GetNodeConfDir returns the configuration directory path for a specific node
func (sb *StructureBuilder) GetNodeConfDir(clusterDir, version, nodeID string) string {
	return filepath.Join(clusterDir, "v"+version, "conf", nodeID)
}

// GetNodeLogPath returns the log file path for a specific node
func (sb *StructureBuilder) GetNodeLogPath(clusterDir, version, nodeID string) string {
	return filepath.Join(clusterDir, "v"+version, "logs", fmt.Sprintf("%s.log", nodeID))
}

// GetBinDir returns the binary directory path for a specific version
func (sb *StructureBuilder) GetBinDir(clusterDir, version string) string {
	return filepath.Join(clusterDir, "v"+version, "bin")
}

// GetVersionDir returns the version directory path
func (sb *StructureBuilder) GetVersionDir(clusterDir, version string) string {
	return filepath.Join(clusterDir, "v"+version)
}

// GetDataDir returns the data directory path for a specific node (via symlink)
func (sb *StructureBuilder) GetDataDir(clusterDir, nodeID string) string {
	return filepath.Join(clusterDir, "data", nodeID)
}

// ValidateStructure validates that the directory structure is correctly set up
func (sb *StructureBuilder) ValidateStructure(config StructureSetupConfig) error {
	// Check version directory exists
	versionDir := filepath.Join(config.ClusterDir, "v"+config.Version)
	exists, err := sb.executor.FileExists(versionDir)
	if err != nil || !exists {
		return fmt.Errorf("version directory does not exist: %s", versionDir)
	}

	// Check required subdirectories
	requiredDirs := []string{"bin", "conf", "logs"}
	for _, dir := range requiredDirs {
		dirPath := filepath.Join(versionDir, dir)
		exists, err := sb.executor.FileExists(dirPath)
		if err != nil || !exists {
			return fmt.Errorf("required directory does not exist: %s", dirPath)
		}
	}

	// Check data symlinks
	dataDir := filepath.Join(config.ClusterDir, "data")
	for nodeID, expectedTarget := range config.ExistingDataDirs {
		symlinkPath := filepath.Join(dataDir, nodeID)
		exists, err := sb.executor.FileExists(symlinkPath)
		if err != nil || !exists {
			return fmt.Errorf("data symlink does not exist: %s", symlinkPath)
		}

		// Verify it's actually a symlink
		info, err := os.Lstat(symlinkPath)
		if err != nil {
			return fmt.Errorf("failed to stat symlink: %w", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("expected symlink but found regular file/dir: %s", symlinkPath)
		}

		// Verify target
		target, err := os.Readlink(symlinkPath)
		if err != nil {
			return fmt.Errorf("failed to read symlink: %w", err)
		}
		if target != expectedTarget {
			return fmt.Errorf("symlink points to wrong target: expected %s, got %s", expectedTarget, target)
		}
	}

	// Check current symlink
	currentLink := filepath.Join(config.ClusterDir, "current")
	exists, err = sb.executor.FileExists(currentLink)
	if err != nil || !exists {
		return fmt.Errorf("current symlink does not exist")
	}

	return nil
}
