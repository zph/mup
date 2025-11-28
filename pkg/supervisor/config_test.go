package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zph/mup/pkg/topology"
)

func TestConfigGenerator_DataDirectoryPath(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "mup-supervisor-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create cluster structure
	clusterDir := filepath.Join(tmpDir, "test-cluster")
	versionDir := filepath.Join(clusterDir, "v7.0")
	binPath := filepath.Join(versionDir, "bin")

	// Create necessary directories
	if err := os.MkdirAll(binPath, 0755); err != nil {
		t.Fatalf("Failed to create bin dir: %v", err)
	}

	// Create test topology
	topo := &topology.Topology{
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: 27017, ReplicaSet: "rs0"},
		},
	}

	// Generate config and wrapper scripts
	gen := NewConfigGenerator(versionDir, "test-cluster", topo, "7.0", binPath)
	if err := gen.GenerateAll(); err != nil {
		t.Fatalf("Failed to generate config: %v", err)
	}

	// Read per-node config file (not the main supervisor.ini which only has includes)
	nodeConfigPath := filepath.Join(versionDir, "mongod-27017", "supervisor.conf")
	content, err := os.ReadFile(nodeConfigPath)
	if err != nil {
		t.Fatalf("Failed to read node config: %v", err)
	}
	configStr := string(content)

	// Verify data directory path is at cluster root, not version dir
	// Expected: /tmp/xxx/test-cluster/data/localhost-27017
	// NOT: /tmp/xxx/test-cluster/v7.0/data/localhost-27017
	expectedDataDir := filepath.Join(clusterDir, "data", "localhost-27017")
	if !strings.Contains(configStr, "directory = "+expectedDataDir) {
		t.Errorf("Config does not contain correct data directory path\nExpected: directory = %s\nConfig:\n%s",
			expectedDataDir, configStr)
	}

	// Verify it does NOT use version-specific data path
	wrongDataDir := filepath.Join(versionDir, "data", "localhost-27017")
	if strings.Contains(configStr, "directory = "+wrongDataDir) {
		t.Errorf("Config incorrectly uses version-specific data directory path\nFound: directory = %s\nConfig:\n%s",
			wrongDataDir, configStr)
	}

	// Verify config path IS version-specific and uses per-process structure
	// New structure: {versionDir}/mongod-{port}/config/mongod.conf
	expectedConfigPath := filepath.Join(versionDir, "mongod-27017", "config", "mongod.conf")
	if !strings.Contains(configStr, "--config "+expectedConfigPath) {
		t.Errorf("Config does not contain correct config path\nExpected: --config %s\nConfig:\n%s",
			expectedConfigPath, configStr)
	}

	// Verify log path IS version-specific and uses per-process structure
	// New structure: {versionDir}/mongod-{port}/log/supervisor-mongod-{port}.log
	expectedLogPath := filepath.Join(versionDir, "mongod-27017", "log", "supervisor-mongod-27017.log")
	if !strings.Contains(configStr, "stdout_logfile = "+expectedLogPath) {
		t.Errorf("Config does not contain correct log path\nExpected: stdout_logfile = %s\nConfig:\n%s",
			expectedLogPath, configStr)
	}

	// Verify wrapper scripts directory exists
	expectedBinDir := filepath.Join(versionDir, "mongod-27017", "bin")
	if _, err := os.Stat(expectedBinDir); os.IsNotExist(err) {
		t.Errorf("Bin directory does not exist: %s", expectedBinDir)
	}

	// Verify wrapper scripts exist
	for _, script := range []string{"start", "stop", "status"} {
		scriptPath := filepath.Join(expectedBinDir, script)
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			t.Errorf("Wrapper script does not exist: %s", scriptPath)
		}
	}
}

func TestConfigGenerator_UniqueHTTPPort(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "mup-supervisor-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testCases := []struct {
		clusterName string
		version     string
		description string
	}{
		{"test-cluster", "v7.0", "Test cluster v7.0"},
		{"test-cluster", "v8.0", "Test cluster v8.0 (different version should give different port)"},
		{"prod-cluster", "v7.0", "Prod cluster v7.0"},
		{"test-cluster", "v7.0", "Test cluster v7.0 again (same dir should give same port)"},
	}

	seenPorts := make(map[string]int) // key: clusterName-version

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			versionDir := filepath.Join(tmpDir, tc.clusterName, tc.version)
			binPath := filepath.Join(versionDir, "bin")

			if err := os.MkdirAll(binPath, 0755); err != nil {
				t.Fatalf("Failed to create bin dir: %v", err)
			}

			topo := &topology.Topology{
				Mongod: []topology.MongodNode{
					{Host: "localhost", Port: 27017},
				},
			}

			gen := NewConfigGenerator(versionDir, tc.clusterName, topo, tc.version, binPath)
			if err := gen.GenerateUnifiedConfig(); err != nil {
				t.Fatalf("Failed to generate config: %v", err)
			}

			configPath := filepath.Join(versionDir, "supervisor.ini")
			content, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("Failed to read config: %v", err)
			}

			actualPort := gen.getSupervisorHTTPPort()

			// Verify port is in expected range (19000-19999)
			if actualPort < 19000 || actualPort > 19999 {
				t.Errorf("Port %d is outside expected range 19000-19999", actualPort)
			}

			// Verify same directory gives same port
			key := tc.clusterName + "-" + tc.version
			if prevPort, exists := seenPorts[key]; exists {
				if prevPort != actualPort {
					t.Errorf("Same directory gave different ports: %d vs %d", prevPort, actualPort)
				}
			} else {
				seenPorts[key] = actualPort
			}

			// Verify different versions of same cluster get different ports
			if tc.version == "v8.0" {
				v70Port := seenPorts[tc.clusterName+"-v7.0"]
				if v70Port == actualPort {
					t.Errorf("Different versions of same cluster got same port: %d", actualPort)
				}
			}

			// Verify config contains the correct port
			portStr := fmt.Sprintf("port = 127.0.0.1:%d", actualPort)
			if !strings.Contains(string(content), portStr) {
				t.Errorf("Config does not contain expected port\nExpected: %s\nConfig:\n%s",
					portStr, string(content))
			}
		})
	}
}

func TestConfigGenerator_ClusterRootCalculation(t *testing.T) {
	testCases := []struct {
		versionDir  string
		wantRoot    string
		description string
	}{
		{
			versionDir:  "/home/user/.mup/storage/clusters/test/v7.0",
			wantRoot:    "/home/user/.mup/storage/clusters/test",
			description: "Standard Linux path",
		},
		{
			versionDir:  "/Users/zph/.mup/storage/clusters/my-cluster/v8.0",
			wantRoot:    "/Users/zph/.mup/storage/clusters/my-cluster",
			description: "macOS path",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			topo := &topology.Topology{}
			gen := NewConfigGenerator(tc.versionDir, "test", topo, "7.0", "/bin")

			if gen.clusterRoot != tc.wantRoot {
				t.Errorf("Incorrect cluster root\nInput: %s\nExpected: %s\nGot: %s",
					tc.versionDir, tc.wantRoot, gen.clusterRoot)
			}
		})
	}
}
