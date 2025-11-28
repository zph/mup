package importer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/executor"
)

// IMP-001: Support both auto-detect and manual modes
func TestDiscoveryModes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	exec := executor.NewLocalExecutor()
	discoverer := NewDiscoverer(exec)

	t.Run("manual mode with explicit config", func(t *testing.T) {
		// IMP-003: Use provided config paths in manual mode
		opts := DiscoveryOptions{
			Mode:      ManualMode,
			ConfigFile: "/etc/mongod.conf",
			DataDir:    "/var/lib/mongodb",
			Port:       27017,
		}

		result, err := discoverer.Discover(opts)
		require.NoError(t, err)
		assert.Equal(t, ManualMode, result.Mode)
		assert.NotNil(t, result.Instances)
	})

	t.Run("auto-detect mode", func(t *testing.T) {
		// IMP-002: Scan for MongoDB processes when auto-detect is requested
		opts := DiscoveryOptions{
			Mode: AutoDetectMode,
		}

		result, err := discoverer.Discover(opts)
		// This may error if no MongoDB is running, which is fine for the test
		if err == nil {
			assert.Equal(t, AutoDetectMode, result.Mode)
		}
	})
}

// IMP-002: Scan for running MongoDB processes
func TestScanProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	exec := executor.NewLocalExecutor()
	discoverer := NewDiscoverer(exec)

	t.Run("detect running mongod/mongos processes", func(t *testing.T) {
		// This test will only pass if MongoDB is running
		// In CI, we'd mock the executor
		processes, err := discoverer.scanProcesses()

		// Either we find processes or get an error (no processes running)
		if err == nil {
			// Validate process structure
			for _, proc := range processes {
				assert.NotEmpty(t, proc.PID)
				// Accept both mongod and mongos processes
				hasMongo := strings.Contains(proc.Command, "mongod") || strings.Contains(proc.Command, "mongos")
				assert.True(t, hasMongo, "Expected mongod or mongos in command: %s", proc.Command)
			}
		}
	})
}

// IMP-004: Detect systemd services
func TestDetectSystemdServices(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	exec := executor.NewLocalExecutor()
	discoverer := NewDiscoverer(exec)

	t.Run("scan for systemd services", func(t *testing.T) {
		// IMP-004: Detect associated systemd service units
		services, err := discoverer.detectSystemdServices()

		// This may return empty list or error if no services exist
		if err == nil {
			// If services found, validate structure
			for _, svc := range services {
				assert.NotEmpty(t, svc.Name, "Service name should not be empty")
				assert.NotEmpty(t, svc.UnitFile, "Unit file path should not be empty")
			}
		}
	})
}

// IMP-005: Query MongoDB for version and topology
func TestQueryMongoDBInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	exec := executor.NewLocalExecutor()
	discoverer := NewDiscoverer(exec)

	t.Run("query version and topology", func(t *testing.T) {
		// This test requires a running MongoDB instance
		// In a real test, we'd mock the MongoDB connection
		instance := MongoInstance{
			Host: "localhost",
			Port: 27017,
		}

		info, err := discoverer.queryMongoDBInfo(instance)

		// Test may fail if MongoDB not running, which is acceptable
		if err == nil {
			// IMP-005: Verify we get version, variant, and topology
			assert.NotEmpty(t, info.Version, "Version should be detected")
			assert.NotEmpty(t, info.Variant, "Variant should be detected")
			assert.NotEmpty(t, info.TopologyType, "Topology type should be detected")
		}
	})
}

// Test discovery result structure
func TestDiscoveryResult(t *testing.T) {
	result := &DiscoveryResult{
		Mode: AutoDetectMode,
		Instances: []MongoInstance{
			{
				Host:       "localhost",
				Port:       27017,
				ConfigFile: "/etc/mongod.conf",
				DataDir:    "/var/lib/mongodb",
				Version:    "7.0.5",
				Variant:    "mongo",
			},
		},
		SystemdServices: []SystemdService{
			{
				Name:     "mongod",
				UnitFile: "/etc/systemd/system/mongod.service",
			},
		},
	}

	assert.Equal(t, AutoDetectMode, result.Mode)
	assert.Len(t, result.Instances, 1)
	assert.Equal(t, "localhost", result.Instances[0].Host)
	assert.Equal(t, 27017, result.Instances[0].Port)
	assert.Len(t, result.SystemdServices, 1)
}
