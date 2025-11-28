package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/topology"
)

func TestNewManager(t *testing.T) {
	tempDir := t.TempDir()
	clusterName := "test-cluster"

	mgr, err := NewManager(tempDir, clusterName)
	require.NoError(t, err)
	assert.NotNil(t, mgr)
	assert.Equal(t, clusterName, mgr.clusterName)
	assert.Equal(t, tempDir, mgr.clusterDir)
	assert.Equal(t, filepath.Join(tempDir, "supervisor.ini"), mgr.configPath)
}

func TestConfigGenerator_GenerateMainConfig(t *testing.T) {
	tempDir := t.TempDir()
	clusterName := "test-cluster"

	// Create a minimal topology
	topo := &topology.Topology{
		Global: topology.GlobalConfig{
			User:       "test",
			DeployDir:  tempDir,
			DataDir:    filepath.Join(tempDir, "data"),
			LogDir:     filepath.Join(tempDir, "logs"),
			ConfigDir:  filepath.Join(tempDir, "conf"),
		},
		Mongod: []topology.MongodNode{
			{
				Host:       "localhost",
				Port:       27017,
				ReplicaSet: "rs0",
			},
		},
	}

	gen := NewConfigGenerator(tempDir, clusterName, topo, "7.0", "/tmp/mongodb-7.0/bin")

	err := gen.GenerateMainConfig()
	require.NoError(t, err)

	// Verify the config file was created
	configPath := filepath.Join(tempDir, "supervisor.ini")
	_, err = os.Stat(configPath)
	require.NoError(t, err)

	// Read and verify content
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)

	configStr := string(content)
	assert.Contains(t, configStr, "[supervisord]")
	assert.Contains(t, configStr, "[inet_http_server]")
	assert.Contains(t, configStr, "identifier = test-cluster")
	assert.Contains(t, configStr, "[include]")
}

func TestConfigGenerator_GenerateMongodConfig(t *testing.T) {
	tempDir := t.TempDir()
	clusterName := "test-cluster"

	// Create necessary directories
	require.NoError(t, os.MkdirAll(filepath.Join(tempDir, "conf"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(tempDir, "logs"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(tempDir, "data"), 0755))

	topo := &topology.Topology{
		Global: topology.GlobalConfig{
			User:       "test",
			DeployDir:  tempDir,
			DataDir:    filepath.Join(tempDir, "data"),
			LogDir:     filepath.Join(tempDir, "logs"),
			ConfigDir:  filepath.Join(tempDir, "conf"),
		},
		Mongod: []topology.MongodNode{
			{
				Host:       "localhost",
				Port:       27017,
				ReplicaSet: "rs0",
			},
		},
	}

	gen := NewConfigGenerator(tempDir, clusterName, topo, "7.0", "/tmp/mongodb-7.0/bin")

	err := gen.GenerateMongodConfig(topo.Mongod[0])
	require.NoError(t, err)

	// Verify the config file was created
	configPath := filepath.Join(tempDir, "conf", "localhost-27017", "supervisor-mongod.ini")
	_, err = os.Stat(configPath)
	require.NoError(t, err)

	// Read and verify content
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)

	configStr := string(content)
	assert.Contains(t, configStr, "[program:mongod-27017]")
	assert.Contains(t, configStr, "command = /tmp/mongodb-7.0/bin/mongod")
	assert.Contains(t, configStr, "autostart = false")
	assert.Contains(t, configStr, "autorestart = unexpected")
	assert.Contains(t, configStr, "stopsignal = INT")
	assert.Contains(t, configStr, "Replica Set: rs0")
}

func TestConfigGenerator_GenerateAll(t *testing.T) {
	tempDir := t.TempDir()
	clusterName := "test-cluster"

	// Create necessary directories
	require.NoError(t, os.MkdirAll(filepath.Join(tempDir, "conf"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(tempDir, "logs"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(tempDir, "data"), 0755))

	// Create a 3-node replica set topology
	topo := &topology.Topology{
		Global: topology.GlobalConfig{
			User:       "test",
			DeployDir:  tempDir,
			DataDir:    filepath.Join(tempDir, "data"),
			LogDir:     filepath.Join(tempDir, "logs"),
			ConfigDir:  filepath.Join(tempDir, "conf"),
		},
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: 27017, ReplicaSet: "rs0"},
			{Host: "localhost", Port: 27018, ReplicaSet: "rs0"},
			{Host: "localhost", Port: 27019, ReplicaSet: "rs0"},
		},
	}

	gen := NewConfigGenerator(tempDir, clusterName, topo, "7.0", "/tmp/mongodb-7.0/bin")

	err := gen.GenerateAll()
	require.NoError(t, err)

	// Verify main unified config created
	configPath := filepath.Join(tempDir, "supervisor.ini")
	_, err = os.Stat(configPath)
	require.NoError(t, err)

	// Read main config and verify it includes all per-node configs
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	configStr := string(content)

	// Verify main config includes all per-node config files
	for _, node := range topo.Mongod {
		includePattern := fmt.Sprintf("mongod-%d/supervisor.conf", node.Port)
		assert.Contains(t, configStr, includePattern, "Main config should include per-node config for %s:%d", node.Host, node.Port)
	}

	// Verify each per-node config file exists and contains the program definition
	for _, node := range topo.Mongod {
		nodeConfigPath := filepath.Join(tempDir, fmt.Sprintf("mongod-%d", node.Port), "supervisor.conf")
		_, err = os.Stat(nodeConfigPath)
		require.NoError(t, err, "Per-node config should exist for %s:%d", node.Host, node.Port)

		// Read and verify the program definition is in the per-node config
		nodeContent, err := os.ReadFile(nodeConfigPath)
		require.NoError(t, err)
		nodeConfigStr := string(nodeContent)

		programName := fmt.Sprintf("[program:mongod-%d]", node.Port)
		assert.Contains(t, nodeConfigStr, programName, "Per-node config should include program definition for %s:%d", node.Host, node.Port)
	}
}

func TestManager_IsRunning(t *testing.T) {
	tempDir := t.TempDir()
	clusterName := "test-cluster"

	mgr, err := NewManager(tempDir, clusterName)
	require.NoError(t, err)

	// Should not be running (no config yet)
	assert.False(t, mgr.IsRunning())
}

func TestLoadManager(t *testing.T) {
	tempDir := t.TempDir()
	clusterName := "test-cluster"

	// Create a config file first
	topo := &topology.Topology{
		Global: topology.GlobalConfig{
			DeployDir: tempDir,
		},
		Mongod: []topology.MongodNode{},
	}

	gen := NewConfigGenerator(tempDir, clusterName, topo, "7.0", "/tmp/bin")
	require.NoError(t, gen.GenerateMainConfig())

	// Now load the manager
	mgr, err := LoadManager(tempDir, clusterName)
	require.NoError(t, err)
	assert.NotNil(t, mgr)
}

func TestManager_StartStop(t *testing.T) {
	t.Skip("Skipping integration test - requires full supervisord setup")

	tempDir := t.TempDir()
	clusterName := "test-cluster"
	ctx := context.Background()

	mgr, err := NewManager(tempDir, clusterName)
	require.NoError(t, err)

	// Generate config first
	topo := &topology.Topology{
		Global: topology.GlobalConfig{
			DeployDir: tempDir,
		},
	}
	gen := NewConfigGenerator(tempDir, clusterName, topo, "7.0", "/tmp/bin")
	require.NoError(t, gen.GenerateAll())

	// Load config
	mgr, err = LoadManager(tempDir, clusterName)
	require.NoError(t, err)

	// Start
	err = mgr.Start(ctx)
	assert.NoError(t, err)

	// Stop
	err = mgr.Stop(ctx)
	assert.NoError(t, err)
}
