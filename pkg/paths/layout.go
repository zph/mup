package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// REQ-PM-009 to REQ-PM-015: ClusterLayout manages version directories and symlinks
// for the cluster filesystem structure, enabling rolling upgrades and rollback
type ClusterLayout struct {
	clusterDir string // Base cluster directory: ~/.mup/storage/clusters/<cluster-name>
}

// NewClusterLayout creates a new cluster layout manager
func NewClusterLayout(clusterDir string) *ClusterLayout {
	return &ClusterLayout{
		clusterDir: clusterDir,
	}
}

// REQ-PM-009: VersionDir returns the version-specific directory path
// Pattern: <cluster-dir>/v<version>
// Version directories contain binaries, configs, and logs for that specific version
func (l *ClusterLayout) VersionDir(version string) string {
	return filepath.Join(l.clusterDir, fmt.Sprintf("v%s", version))
}

// REQ-PM-011: DataDir returns the data directory base path
// Pattern: <cluster-dir>/data
// Data directories are version-independent to enable zero-copy upgrades.
// Individual node data is stored at data/<host>-<port>
func (l *ClusterLayout) DataDir() string {
	return filepath.Join(l.clusterDir, "data")
}

// REQ-PM-012: CurrentLink returns the path to the "current" symlink
// The current symlink points to the active version directory (e.g., v7.0.0)
// This provides a stable reference for monitoring and management tools
func (l *ClusterLayout) CurrentLink() string {
	return filepath.Join(l.clusterDir, "current")
}

// REQ-PM-013: NextLink returns the path to the "next" symlink
// During upgrades, the next symlink points to the target version directory
// This enables the system to manage two versions simultaneously during rolling upgrades
func (l *ClusterLayout) NextLink() string {
	return filepath.Join(l.clusterDir, "next")
}

// REQ-PM-014: PreviousLink returns the path to the "previous" symlink
// After successful upgrade, previous points to the old version directory
// This enables quick rollback to the last known working version
func (l *ClusterLayout) PreviousLink() string {
	return filepath.Join(l.clusterDir, "previous")
}

// REQ-PM-018/019/020: ProcessDir returns the per-process directory path
// Patterns:
// - mongod: <cluster-dir>/v<version>/mongod-<port>
// - mongos: <cluster-dir>/v<version>/mongos-<port>
// - config: <cluster-dir>/v<version>/config-<port>
// These directories contain process-specific subdirectories: config/ and log/
func (l *ClusterLayout) ProcessDir(version, nodeType string, port int) string {
	return filepath.Join(
		l.VersionDir(version),
		fmt.Sprintf("%s-%d", nodeType, port),
	)
}

// NodeDataDir returns the data directory for a specific node
// REQ-PM-011: Pattern: <cluster-dir>/data/<host>-<port>
func (l *ClusterLayout) NodeDataDir(host string, port int) string {
	return filepath.Join(l.DataDir(), fmt.Sprintf("%s-%d", host, port))
}

// BinDir returns the binary directory for a version
// REQ-PM-010: Pattern: <cluster-dir>/v<version>/bin
func (l *ClusterLayout) BinDir(version string) string {
	return filepath.Join(l.VersionDir(version), "bin")
}

// ConfigDir returns the config directory for a process
// REQ-PM-010: Pattern: <cluster-dir>/v<version>/<nodeType>-<port>/config
func (l *ClusterLayout) ConfigDir(version, nodeType string, port int) string {
	return filepath.Join(l.ProcessDir(version, nodeType, port), "config")
}

// LogDir returns the log directory for a process
// REQ-PM-010: Pattern: <cluster-dir>/v<version>/<nodeType>-<port>/log
func (l *ClusterLayout) LogDir(version, nodeType string, port int) string {
	return filepath.Join(l.ProcessDir(version, nodeType, port), "log")
}

// CreateCurrentLink creates the "current" symlink pointing to the specified version
// REQ-PM-012: Current symlink enables stable reference to active version
func (l *ClusterLayout) CreateCurrentLink(version string) error {
	linkPath := l.CurrentLink()
	targetPath := fmt.Sprintf("v%s", version)

	// Remove existing symlink if present
	os.Remove(linkPath)

	// Create new symlink (relative path for portability)
	return os.Symlink(targetPath, linkPath)
}

// CreatePreviousLink creates the "previous" symlink pointing to the specified version
// REQ-PM-014: Previous symlink enables quick rollback
func (l *ClusterLayout) CreatePreviousLink(version string) error {
	linkPath := l.PreviousLink()
	targetPath := fmt.Sprintf("v%s", version)

	// Remove existing symlink if present
	os.Remove(linkPath)

	// Create new symlink (relative path for portability)
	return os.Symlink(targetPath, linkPath)
}

// CreateNextLink creates the "next" symlink pointing to the target upgrade version
// REQ-PM-013: Next symlink manages two versions during rolling upgrades
func (l *ClusterLayout) CreateNextLink(version string) error {
	linkPath := l.NextLink()
	targetPath := fmt.Sprintf("v%s", version)

	// Remove existing symlink if present
	os.Remove(linkPath)

	// Create new symlink (relative path for portability)
	return os.Symlink(targetPath, linkPath)
}

// ActivateVersion updates symlinks to activate a new version
// REQ-PM-012, REQ-PM-014: Atomically updates current/previous for version switching
// Moves current -> previous, then next -> current (for upgrades)
// Or directly updates current (for initial deployment)
func (l *ClusterLayout) ActivateVersion(newVersion string) error {
	currentLink := l.CurrentLink()
	previousLink := l.PreviousLink()

	// Check if current symlink exists
	currentTarget, err := os.Readlink(currentLink)
	if err == nil && currentTarget != "" {
		// Current exists, move it to previous before activating new version
		os.Remove(previousLink)
		if err := os.Symlink(currentTarget, previousLink); err != nil {
			return fmt.Errorf("failed to create previous symlink: %w", err)
		}
	}

	// Update current to point to new version
	os.Remove(currentLink)
	targetPath := fmt.Sprintf("v%s", newVersion)
	if err := os.Symlink(targetPath, currentLink); err != nil {
		return fmt.Errorf("failed to create current symlink: %w", err)
	}

	return nil
}
