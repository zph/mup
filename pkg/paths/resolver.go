package paths

import (
	"fmt"
	"path/filepath"
)

// REQ-PM-001: PathResolver interface provides consistent path resolution methods
// across all operations (deploy, upgrade, import) for different deployment modes
type PathResolver interface {
	// DataDir returns the data directory path for a node
	// REQ-PM-011: Data directories are version-independent
	DataDir(host string, port int) (string, error)

	// LogDir returns the log directory path for a node
	// REQ-PM-010: Log directories are version-specific
	LogDir(nodeType, host string, port int) (string, error)

	// ConfigDir returns the config directory path for a node
	// REQ-PM-010: Config directories are version-specific
	ConfigDir(nodeType, host string, port int) (string, error)

	// BinDir returns the binary directory path
	// REQ-PM-010: Bin directories are version-specific
	BinDir() (string, error)

	// ConfigFile returns the full path to the config file for a node
	// REQ-PM-010: Config files are version-specific
	ConfigFile(nodeType, host string, port int) (string, error)

	// LogFile returns the full path to the log file for a node
	// REQ-PM-010: Log files are version-specific
	LogFile(nodeType, host string, port int) (string, error)
}

// REQ-PM-002: LocalPathResolver resolves all paths relative to cluster directory
// for local deployments (playground mode), ignoring topology-specified paths.
// Reference: CLAUDE.md states local deployments use ~/.mup/storage/clusters/<name>/
type LocalPathResolver struct {
	clusterDir string // Base directory: ~/.mup/storage/clusters/<cluster-name>
	version    string // MongoDB version for version-specific paths
}

// NewLocalPathResolver creates a resolver for local deployments
func NewLocalPathResolver(clusterDir, version string) *LocalPathResolver {
	return &LocalPathResolver{
		clusterDir: clusterDir,
		version:    version,
	}
}

// REQ-PM-011: Data directories use pattern data/<host>-<port> and are version-independent
// This enables MongoDB data files to be reused across upgrades without copying
func (r *LocalPathResolver) DataDir(host string, port int) (string, error) {
	// REQ-PM-027: Validate cluster directory is not empty
	if r.clusterDir == "" {
		return "", fmt.Errorf("failed to resolve data_dir: cluster directory is empty")
	}

	// Version-independent data directory
	return filepath.Join(r.clusterDir, "data", fmt.Sprintf("%s-%d", host, port)), nil
}

// REQ-PM-010: Log directories are version-specific to enable side-by-side versions
// REQ-PM-009: Version directories use pattern v<version>
// REQ-PM-018/019/020: Process directories use patterns mongod-<port>, mongos-<port>, config-<port>
func (r *LocalPathResolver) LogDir(nodeType, host string, port int) (string, error) {
	if r.clusterDir == "" {
		return "", fmt.Errorf("failed to resolve log_dir for %s:%d: cluster directory is empty", host, port)
	}
	if r.version == "" {
		return "", fmt.Errorf("failed to resolve log_dir for %s:%d: version is empty", host, port)
	}

	// Pattern: <cluster-dir>/v<version>/<nodeType>-<port>/log
	processDir := fmt.Sprintf("%s-%d", nodeType, port)
	return filepath.Join(r.clusterDir, fmt.Sprintf("v%s", r.version), processDir, "log"), nil
}

// REQ-PM-010: Config directories are version-specific
func (r *LocalPathResolver) ConfigDir(nodeType, host string, port int) (string, error) {
	if r.clusterDir == "" {
		return "", fmt.Errorf("failed to resolve config_dir for %s:%d: cluster directory is empty", host, port)
	}
	if r.version == "" {
		return "", fmt.Errorf("failed to resolve config_dir for %s:%d: version is empty", host, port)
	}

	// Pattern: <cluster-dir>/v<version>/<nodeType>-<port>/config
	processDir := fmt.Sprintf("%s-%d", nodeType, port)
	return filepath.Join(r.clusterDir, fmt.Sprintf("v%s", r.version), processDir, "config"), nil
}

// REQ-PM-010: Bin directories are version-specific
func (r *LocalPathResolver) BinDir() (string, error) {
	if r.clusterDir == "" {
		return "", fmt.Errorf("failed to resolve bin_dir: cluster directory is empty")
	}
	if r.version == "" {
		return "", fmt.Errorf("failed to resolve bin_dir: version is empty")
	}

	return filepath.Join(r.clusterDir, fmt.Sprintf("v%s", r.version), "bin"), nil
}

// ConfigFile returns the full path to the config file for a node
// REQ-PM-010: Config files are version-specific
// Pattern: <cluster-dir>/v<version>/<nodeType>-<port>/config/<nodeType>.conf
// File name is simple since directory already identifies node uniquely
func (r *LocalPathResolver) ConfigFile(nodeType, host string, port int) (string, error) {
	configDir, err := r.ConfigDir(nodeType, host, port)
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, fmt.Sprintf("%s.conf", nodeType)), nil
}

// LogFile returns the full path to the log file for a node
// REQ-PM-010: Log files are version-specific
// Pattern: <cluster-dir>/v<version>/<nodeType>-<port>/log/<nodeType>.log
// File name is simple since directory already identifies node uniquely
func (r *LocalPathResolver) LogFile(nodeType, host string, port int) (string, error) {
	logDir, err := r.LogDir(nodeType, host, port)
	if err != nil {
		return "", err
	}
	return filepath.Join(logDir, fmt.Sprintf("%s.log", nodeType)), nil
}
