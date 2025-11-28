package paths

import (
	"path/filepath"
	"testing"

	"github.com/zph/mup/pkg/topology"
)

// REQ-PM-024: Unit tests for LocalPathResolver and RemotePathResolver
// covering all path types using test simulation harness

func TestLocalPathResolver_DataDir(t *testing.T) {
	// REQ-PM-002: LocalPathResolver resolves all paths relative to cluster directory
	// REQ-PM-011: Data directories use pattern data/<host>-<port>

	clusterDir := "/home/user/.mup/storage/clusters/test-cluster"
	resolver := NewLocalPathResolver(clusterDir, "7.0.0")

	node := &topology.MongodNode{
		Host: "localhost",
		Port: 27017,
		// DeployDir, DataDir should be ignored for local mode
		DeployDir: "/opt/mongodb",
		DataDir:   "/var/lib/mongodb",
	}

	dataDir, err := resolver.DataDir(node.Host, node.Port)
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	expected := filepath.Join(clusterDir, "data", "localhost-27017")
	if dataDir != expected {
		t.Errorf("DataDir() = %v, want %v", dataDir, expected)
	}
}

func TestLocalPathResolver_LogDir(t *testing.T) {
	// REQ-PM-002: LocalPathResolver resolves all paths relative to cluster directory
	// REQ-PM-009: Version-specific directories use pattern v<version>
	// REQ-PM-010: Per-version subdirectories for logs

	clusterDir := "/home/user/.mup/storage/clusters/test-cluster"
	version := "7.0.0"
	resolver := NewLocalPathResolver(clusterDir, version)

	tests := []struct {
		name     string
		nodeType string
		host     string
		port     int
		want     string
	}{
		{
			name:     "mongod log dir",
			nodeType: "mongod",
			host:     "localhost",
			port:     27017,
			want:     filepath.Join(clusterDir, "v7.0.0", "mongod-27017", "log"),
		},
		{
			name:     "mongos log dir",
			nodeType: "mongos",
			host:     "localhost",
			port:     27016,
			want:     filepath.Join(clusterDir, "v7.0.0", "mongos-27016", "log"),
		},
		{
			name:     "config server log dir",
			nodeType: "config",
			host:     "localhost",
			port:     27019,
			want:     filepath.Join(clusterDir, "v7.0.0", "config-27019", "log"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logDir, err := resolver.LogDir(tt.nodeType, tt.host, tt.port)
			if err != nil {
				t.Fatalf("LogDir() error = %v", err)
			}

			if logDir != tt.want {
				t.Errorf("LogDir() = %v, want %v", logDir, tt.want)
			}
		})
	}
}

func TestLocalPathResolver_ConfigDir(t *testing.T) {
	// REQ-PM-002: LocalPathResolver resolves all paths relative to cluster directory
	// REQ-PM-010: Per-version subdirectories for configs

	clusterDir := "/home/user/.mup/storage/clusters/test-cluster"
	version := "8.0.0"
	resolver := NewLocalPathResolver(clusterDir, version)

	tests := []struct {
		name     string
		nodeType string
		host     string
		port     int
		want     string
	}{
		{
			name:     "mongod config dir",
			nodeType: "mongod",
			host:     "localhost",
			port:     27017,
			want:     filepath.Join(clusterDir, "v8.0.0", "mongod-27017", "config"),
		},
		{
			name:     "mongos config dir",
			nodeType: "mongos",
			host:     "localhost",
			port:     27016,
			want:     filepath.Join(clusterDir, "v8.0.0", "mongos-27016", "config"),
		},
		{
			name:     "config server config dir",
			nodeType: "config",
			host:     "localhost",
			port:     27019,
			want:     filepath.Join(clusterDir, "v8.0.0", "config-27019", "config"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir, err := resolver.ConfigDir(tt.nodeType, tt.host, tt.port)
			if err != nil {
				t.Fatalf("ConfigDir() error = %v", err)
			}

			if configDir != tt.want {
				t.Errorf("ConfigDir() = %v, want %v", configDir, tt.want)
			}
		})
	}
}

func TestLocalPathResolver_BinDir(t *testing.T) {
	// REQ-PM-010: Per-version bin directory

	clusterDir := "/home/user/.mup/storage/clusters/test-cluster"
	version := "7.0.0"
	resolver := NewLocalPathResolver(clusterDir, version)

	binDir, err := resolver.BinDir()
	if err != nil {
		t.Fatalf("BinDir() error = %v", err)
	}

	expected := filepath.Join(clusterDir, "v7.0.0", "bin")
	if binDir != expected {
		t.Errorf("BinDir() = %v, want %v", binDir, expected)
	}
}

func TestRemotePathResolver_AbsolutePaths(t *testing.T) {
	// REQ-PM-004: Absolute paths at instance level override all defaults

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "/opt/tidb",
		DataDir:   "/data/tidb",
		LogDir:    "/logs/tidb",
	}

	resolver := NewRemotePathResolver(global)

	// Instance with absolute paths
	instanceDeployDir := "/opt/mongodb/mongod-27017"
	instanceDataDir := "/data/mongodb/data1"

	dataDir, err := resolver.DataDir("10.0.1.10", 27017, instanceDeployDir, instanceDataDir)
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	// Absolute path should be used as-is
	if dataDir != instanceDataDir {
		t.Errorf("DataDir() = %v, want %v", dataDir, instanceDataDir)
	}
}

func TestRemotePathResolver_RelativePathNesting(t *testing.T) {
	// REQ-PM-005: Relative paths nest within global deploy directory
	// TiUP: /home/<user>/<global.deploy_dir>/<instance.deploy_dir>

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "tidb-deploy",
		DataDir:   "",
		LogDir:    "",
	}

	resolver := NewRemotePathResolver(global)

	// Instance with relative deploy_dir
	instanceDeployDir := "mongod-27017"

	// DataDir should be resolved relative to instance deploy dir
	// If no instance data_dir specified, should use default pattern
	dataDir, err := resolver.DataDir("10.0.1.10", 27017, instanceDeployDir, "")
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	// REQ-PM-007: Relative data_dir nests within deploy_dir
	// Expected: /home/mongodb/tidb-deploy/mongod-27017/data
	expected := filepath.Join("/home", global.User, global.DeployDir, instanceDeployDir, "data")
	if dataDir != expected {
		t.Errorf("DataDir() = %v, want %v", dataDir, expected)
	}
}

func TestRemotePathResolver_GlobalDefaults(t *testing.T) {
	// REQ-PM-006: Instance-level missing values cascade from global defaults

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "/opt/mongodb",
		DataDir:   "/data/mongodb",
		LogDir:    "/logs/mongodb",
	}

	resolver := NewRemotePathResolver(global)

	// Instance with no overrides - should use global values
	dataDir, err := resolver.DataDir("10.0.1.10", 27017, "", "")
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	// Should use global data_dir as base
	expected := filepath.Join(global.DataDir, "mongod-27017")
	if dataDir != expected {
		t.Errorf("DataDir() = %v, want %v", dataDir, expected)
	}
}

func TestRemotePathResolver_LogDirNesting(t *testing.T) {
	// REQ-PM-008: Relative log_dir nests within deploy_dir

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "/opt/mongodb",
		DataDir:   "",
		LogDir:    "",
	}

	resolver := NewRemotePathResolver(global)

	instanceDeployDir := "/opt/mongodb/mongod-27017"

	logDir, err := resolver.LogDir("mongod", "10.0.1.10", 27017, instanceDeployDir, "")
	if err != nil {
		t.Fatalf("LogDir() error = %v", err)
	}

	// Log should be within deploy dir
	expected := filepath.Join(instanceDeployDir, "log")
	if logDir != expected {
		t.Errorf("LogDir() = %v, want %v", logDir, expected)
	}
}

func TestPathResolver_ErrorHandling(t *testing.T) {
	// REQ-PM-027: Empty path resolution should return descriptive error

	clusterDir := ""
	resolver := NewLocalPathResolver(clusterDir, "7.0.0")

	_, err := resolver.DataDir("localhost", 27017)
	if err == nil {
		t.Error("DataDir() with empty clusterDir should return error")
	}

	// Error should be descriptive
	if err != nil && err.Error() == "" {
		t.Error("Error should have descriptive message")
	}
}

func TestLocalPathResolver_ProcessDirectoryNaming(t *testing.T) {
	// REQ-PM-018: mongod directories use pattern mongod-<port>
	// REQ-PM-019: mongos directories use pattern mongos-<port>
	// REQ-PM-020: config server directories use pattern config-<port>

	clusterDir := "/home/user/.mup/storage/clusters/test"
	resolver := NewLocalPathResolver(clusterDir, "7.0.0")

	tests := []struct {
		nodeType     string
		port         int
		wantContains string
	}{
		{"mongod", 27017, "mongod-27017"},
		{"mongos", 27016, "mongos-27016"},
		{"config", 27019, "config-27019"},
	}

	for _, tt := range tests {
		t.Run(tt.nodeType, func(t *testing.T) {
			logDir, err := resolver.LogDir(tt.nodeType, "localhost", tt.port)
			if err != nil {
				t.Fatalf("LogDir() error = %v", err)
			}

			if filepath.Base(filepath.Dir(logDir)) != tt.wantContains {
				t.Errorf("LogDir() = %v, should contain %v in path, got %v",
					logDir, tt.wantContains, filepath.Base(filepath.Dir(logDir)))
			}
		})
	}
}
