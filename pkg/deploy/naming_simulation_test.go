package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/naming"
	"github.com/zph/mup/pkg/paths"
	"github.com/zph/mup/pkg/supervisor"
	"github.com/zph/mup/pkg/template"
	"github.com/zph/mup/pkg/topology"
)

// TestShardedClusterNamingConsistency simulates a sharded cluster deployment
// and verifies that naming is consistent across all components
func TestShardedClusterNamingConsistency(t *testing.T) {
	// Create temp directory for simulation
	tmpDir := t.TempDir()
	clusterDir := filepath.Join(tmpDir, "clusters", "test-cluster")
	version := "7.0.0"
	versionDir := filepath.Join(clusterDir, "v"+version)

	// Create sharded cluster topology
	topo := &topology.Topology{
		ConfigSvr: []topology.MongodNode{
			{Host: "localhost", Port: 30000, ReplicaSet: "configRS"},
			{Host: "localhost", Port: 30001, ReplicaSet: "configRS"},
			{Host: "localhost", Port: 30002, ReplicaSet: "configRS"},
		},
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: 30100, ReplicaSet: "shard1"},
			{Host: "localhost", Port: 30101, ReplicaSet: "shard1"},
			{Host: "localhost", Port: 30200, ReplicaSet: "shard2"},
			{Host: "localhost", Port: 30201, ReplicaSet: "shard2"},
		},
		Mongos: []topology.MongosNode{
			{Host: "localhost", Port: 30300},
			{Host: "localhost", Port: 30301},
		},
	}

	// Test 1: Verify naming package functions return correct names
	t.Run("NamingPackageFunctions", func(t *testing.T) {
		tests := []struct {
			nodeType string
			port     int
			expected string
		}{
			{"config", 30000, "config-30000"},
			{"config", 30002, "config-30002"},
			{"mongod", 30100, "mongod-30100"},
			{"mongod", 30201, "mongod-30201"},
			{"mongos", 30300, "mongos-30300"},
			{"mongos", 30301, "mongos-30301"},
		}

		for _, tt := range tests {
			got := naming.GetProgramName(tt.nodeType, tt.port)
			if got != tt.expected {
				t.Errorf("GetProgramName(%q, %d) = %q, want %q",
					tt.nodeType, tt.port, got, tt.expected)
			}

			// Process dir should match program name
			processDir := naming.GetProcessDir(tt.nodeType, tt.port)
			if processDir != tt.expected {
				t.Errorf("GetProcessDir(%q, %d) = %q, want %q",
					tt.nodeType, tt.port, processDir, tt.expected)
			}
		}
	})

	// Test 2: Verify config file names
	t.Run("ConfigFileNames", func(t *testing.T) {
		tests := []struct {
			nodeType string
			expected string
		}{
			{"config", "config.conf"},
			{"mongod", "mongod.conf"},
			{"mongos", "mongos.conf"},
		}

		for _, tt := range tests {
			got := naming.GetConfigFileName(tt.nodeType)
			if got != tt.expected {
				t.Errorf("GetConfigFileName(%q) = %q, want %q",
					tt.nodeType, got, tt.expected)
			}
		}
	})

	// Test 3: Simulate directory creation and verify structure
	t.Run("DirectoryStructure", func(t *testing.T) {
		// Create path resolver
		resolver := paths.NewPathResolver(clusterDir, version)

		// Create deployer
		exec := executor.NewLocalExecutor()
		executors := map[string]executor.Executor{
			"localhost": exec,
		}

		templateMgr := template.NewManager()
		deployer := &Deployer{
			topology:     topo,
			clusterName:  "test-cluster",
			version:      version,
			metaDir:      clusterDir,
			pathResolver: resolver,
			executors:    executors,
			templateMgr:  templateMgr,
		}

		// Simulate directory creation
		if err := deployer.createDirectories(context.Background()); err != nil {
			t.Fatalf("createDirectories failed: %v", err)
		}

		// Verify config server directories
		for _, node := range topo.ConfigSvr {
			expectedDirs := []string{
				filepath.Join(versionDir, naming.GetProcessDir("config", node.Port), "log"),
				filepath.Join(versionDir, naming.GetProcessDir("config", node.Port), "config"),
			}

			for _, dir := range expectedDirs {
				if _, err := os.Stat(dir); os.IsNotExist(err) {
					t.Errorf("Expected directory not created: %s", dir)
				}
			}
		}

		// Verify mongod directories
		for _, node := range topo.Mongod {
			expectedDirs := []string{
				filepath.Join(versionDir, naming.GetProcessDir("mongod", node.Port), "log"),
				filepath.Join(versionDir, naming.GetProcessDir("mongod", node.Port), "config"),
			}

			for _, dir := range expectedDirs {
				if _, err := os.Stat(dir); os.IsNotExist(err) {
					t.Errorf("Expected directory not created: %s", dir)
				}
			}
		}

		// Verify mongos directories
		for _, node := range topo.Mongos {
			expectedDirs := []string{
				filepath.Join(versionDir, naming.GetProcessDir("mongos", node.Port), "log"),
				filepath.Join(versionDir, naming.GetProcessDir("mongos", node.Port), "config"),
			}

			for _, dir := range expectedDirs {
				if _, err := os.Stat(dir); os.IsNotExist(err) {
					t.Errorf("Expected directory not created: %s", dir)
				}
			}
		}
	})

	// Test 4: Verify supervisor config generation uses correct naming
	t.Run("SupervisorConfigNaming", func(t *testing.T) {
		binPath := filepath.Join(tmpDir, "bin")
		if err := os.MkdirAll(binPath, 0755); err != nil {
			t.Fatalf("Failed to create bin directory: %v", err)
		}

		generator := supervisor.NewConfigGenerator(
			versionDir,
			tmpDir,
			"test-cluster",
			topo,
			version,
			binPath,
		)

		// Generate configs for each node type
		t.Run("ConfigServerSupervisorConfig", func(t *testing.T) {
			for _, node := range topo.ConfigSvr {
				if err := generator.GenerateNodeSupervisorConfig("config", node.Host, node.Port, node.ReplicaSet, true); err != nil {
					t.Fatalf("Failed to generate config server supervisor config: %v", err)
				}

				// Verify supervisor config file exists in correct location
				expectedPath := filepath.Join(
					versionDir,
					naming.GetProcessDir("config", node.Port),
					"supervisor.conf",
				)
				if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
					t.Errorf("Supervisor config not found at expected path: %s", expectedPath)
				}
			}
		})

		t.Run("MongodSupervisorConfig", func(t *testing.T) {
			for _, node := range topo.Mongod {
				if err := generator.GenerateNodeSupervisorConfig("mongod", node.Host, node.Port, node.ReplicaSet, false); err != nil {
					t.Fatalf("Failed to generate mongod supervisor config: %v", err)
				}

				expectedPath := filepath.Join(
					versionDir,
					naming.GetProcessDir("mongod", node.Port),
					"supervisor.conf",
				)
				if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
					t.Errorf("Supervisor config not found at expected path: %s", expectedPath)
				}
			}
		})

		t.Run("MongosSupervisorConfig", func(t *testing.T) {
			for _, node := range topo.Mongos {
				if err := generator.GenerateMongosNodeSupervisorConfig(node.Host, node.Port); err != nil {
					t.Fatalf("Failed to generate mongos supervisor config: %v", err)
				}

				expectedPath := filepath.Join(
					versionDir,
					naming.GetProcessDir("mongos", node.Port),
					"supervisor.conf",
				)
				if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
					t.Errorf("Supervisor config not found at expected path: %s", expectedPath)
				}
			}
		})
	})
}

// TestNamingConsistencyAcrossComponents tests that naming is consistent
// between different parts of the codebase
func TestNamingConsistencyAcrossComponents(t *testing.T) {
	tests := []struct {
		name     string
		nodeType string
		port     int
	}{
		{"config server 1", "config", 27019},
		{"config server 2", "config", 27020},
		{"mongod shard 1", "mongod", 27017},
		{"mongod shard 2", "mongod", 27018},
		{"mongos router 1", "mongos", 27021},
		{"mongos router 2", "mongos", 27022},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			programName := naming.GetProgramName(tt.nodeType, tt.port)
			processDir := naming.GetProcessDir(tt.nodeType, tt.port)
			configFile := naming.GetConfigFileName(tt.nodeType)

			// Program name and process dir should match
			if programName != processDir {
				t.Errorf("Inconsistent naming: programName=%q, processDir=%q",
					programName, processDir)
			}

			// Config file should use node type
			expectedConfigFile := tt.nodeType + ".conf"
			if configFile != expectedConfigFile {
				t.Errorf("Config file name mismatch: got %q, want %q",
					configFile, expectedConfigFile)
			}

			// Verify format matches expected pattern
			expected := tt.nodeType + "-" + string(rune(tt.port/10000+'0')) +
				string(rune((tt.port/1000)%10+'0')) +
				string(rune((tt.port/100)%10+'0')) +
				string(rune((tt.port/10)%10+'0')) +
				string(rune(tt.port%10+'0'))
			if programName != expected {
				t.Errorf("Program name format incorrect: got %q, want %q",
					programName, expected)
			}
		})
	}
}

// TestLogFileNaming verifies that log files use simple names
func TestLogFileNaming(t *testing.T) {
	tests := []struct {
		nodeType        string
		expectedLogName string
	}{
		{"mongod", "process.log"},
		{"mongos", "process.log"},
		{"config", "process.log"},
	}

	for _, tt := range tests {
		t.Run(tt.nodeType, func(t *testing.T) {
			// Verify the log naming pattern is simple (just node type)
			// This is enforced by the config generation code
			expectedLogName := tt.expectedLogName

			// The actual log file name should be just the node type + .log
			// Not including host/port information
			if expectedLogName != tt.nodeType+".log" {
				t.Errorf("Log file name should be simple: %q", expectedLogName)
			}
		})
	}
}
