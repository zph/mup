package paths

import (
	"fmt"
	"path/filepath"

	"github.com/zph/mup/pkg/topology"
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

// REQ-PM-003: RemotePathResolver resolves paths according to TiUP topology conventions
// for SSH deployments. Implements TiUP path resolution rules from:
// https://docs.pingcap.com/tidb/stable/tiup-cluster-topology-reference/
// (referenced in CLAUDE.md)
type RemotePathResolver struct {
	global *topology.GlobalConfig
}

// NewRemotePathResolver creates a resolver for remote SSH deployments
func NewRemotePathResolver(global *topology.GlobalConfig) *RemotePathResolver {
	return &RemotePathResolver{
		global: global,
	}
}

// DataDir implements TiUP path resolution rules for data directories
// REQ-PM-004: Absolute paths at instance level override all defaults
// REQ-PM-005: Relative paths nest within /home/<user>/<global.deploy_dir>/<instance.deploy_dir>
// REQ-PM-006: Missing instance-level values cascade from global defaults
// REQ-PM-007: Relative data_dir nests within deploy_dir
func (r *RemotePathResolver) DataDir(host string, port int, instanceDeployDir, instanceDataDir string) (string, error) {
	// REQ-PM-004: If instance has absolute data_dir, use it directly
	if instanceDataDir != "" && filepath.IsAbs(instanceDataDir) {
		return instanceDataDir, nil
	}

	// Determine base deploy directory
	var deployDir string
	if instanceDeployDir != "" && filepath.IsAbs(instanceDeployDir) {
		// Instance has absolute deploy_dir
		deployDir = instanceDeployDir
	} else if instanceDeployDir != "" {
		// REQ-PM-005: Relative instance deploy_dir nests within global
		deployDir = filepath.Join("/home", r.global.User, r.global.DeployDir, instanceDeployDir)
	} else {
		// Use global deploy_dir
		deployDir = r.global.DeployDir
	}

	// Determine data directory
	if instanceDataDir != "" {
		// REQ-PM-007: Relative data_dir nests within deploy_dir
		return filepath.Join(deployDir, instanceDataDir), nil
	}

	// REQ-PM-006: Use global data_dir as default if available
	if r.global.DataDir != "" && filepath.IsAbs(r.global.DataDir) {
		return filepath.Join(r.global.DataDir, fmt.Sprintf("mongod-%d", port)), nil
	}

	// Default pattern within deploy_dir
	return filepath.Join(deployDir, "data"), nil
}

// LogDir implements TiUP path resolution rules for log directories
// REQ-PM-008: Relative log_dir nests within deploy_dir
func (r *RemotePathResolver) LogDir(nodeType, host string, port int, instanceDeployDir, instanceLogDir string) (string, error) {
	// REQ-PM-004: If instance has absolute log_dir, use it directly
	if instanceLogDir != "" && filepath.IsAbs(instanceLogDir) {
		return instanceLogDir, nil
	}

	// Determine base deploy directory
	var deployDir string
	if instanceDeployDir != "" && filepath.IsAbs(instanceDeployDir) {
		deployDir = instanceDeployDir
	} else if instanceDeployDir != "" {
		// REQ-PM-005: Relative instance deploy_dir nests within global
		deployDir = filepath.Join("/home", r.global.User, r.global.DeployDir, instanceDeployDir)
	} else {
		deployDir = r.global.DeployDir
	}

	// Determine log directory
	if instanceLogDir != "" {
		// REQ-PM-008: Relative log_dir nests within deploy_dir
		return filepath.Join(deployDir, instanceLogDir), nil
	}

	// REQ-PM-006: Use global log_dir if available
	if r.global.LogDir != "" && filepath.IsAbs(r.global.LogDir) {
		return filepath.Join(r.global.LogDir, fmt.Sprintf("%s-%d", nodeType, port)), nil
	}

	// Default pattern within deploy_dir
	return filepath.Join(deployDir, "log"), nil
}

// ConfigDir implements TiUP path resolution rules for config directories
func (r *RemotePathResolver) ConfigDir(nodeType, host string, port int, instanceDeployDir, instanceConfigDir string) (string, error) {
	// REQ-PM-004: If instance has absolute config_dir, use it directly
	if instanceConfigDir != "" && filepath.IsAbs(instanceConfigDir) {
		return instanceConfigDir, nil
	}

	// Determine base deploy directory
	var deployDir string
	if instanceDeployDir != "" && filepath.IsAbs(instanceDeployDir) {
		deployDir = instanceDeployDir
	} else if instanceDeployDir != "" {
		// REQ-PM-005: Relative instance deploy_dir nests within global
		deployDir = filepath.Join("/home", r.global.User, r.global.DeployDir, instanceDeployDir)
	} else {
		deployDir = r.global.DeployDir
	}

	// Determine config directory
	if instanceConfigDir != "" {
		// Similar to log_dir, config_dir nests within deploy_dir when relative
		return filepath.Join(deployDir, instanceConfigDir), nil
	}

	// REQ-PM-006: Use global config_dir if available
	if r.global.ConfigDir != "" && filepath.IsAbs(r.global.ConfigDir) {
		return filepath.Join(r.global.ConfigDir, fmt.Sprintf("%s-%d", nodeType, port)), nil
	}

	// Default pattern within deploy_dir
	return filepath.Join(deployDir, "conf"), nil
}

// BinDir returns the binary directory for remote deployments
func (r *RemotePathResolver) BinDir() (string, error) {
	if r.global.DeployDir == "" {
		return "", fmt.Errorf("failed to resolve bin_dir: global deploy_dir is empty")
	}

	return filepath.Join(r.global.DeployDir, "bin"), nil
}

// ConfigFile returns the full path to the config file for a remote node
// Pattern: <config-dir>/<nodeType>.conf
// File name is simple since directory path includes instance-specific information
func (r *RemotePathResolver) ConfigFile(nodeType, host string, port int, instanceDeployDir, instanceConfigDir string) (string, error) {
	configDir, err := r.ConfigDir(nodeType, host, port, instanceDeployDir, instanceConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, fmt.Sprintf("%s.conf", nodeType)), nil
}

// LogFile returns the full path to the log file for a remote node
// Pattern: <log-dir>/<nodeType>.log
// File name is simple since directory path includes instance-specific information
func (r *RemotePathResolver) LogFile(nodeType, host string, port int, instanceDeployDir, instanceLogDir string) (string, error) {
	logDir, err := r.LogDir(nodeType, host, port, instanceDeployDir, instanceLogDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(logDir, fmt.Sprintf("%s.log", nodeType)), nil
}
