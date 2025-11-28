package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zph/mup/pkg/executor"
)

// IMP-008: Disable systemd services during import
func TestDisableSystemdService(t *testing.T) {
	// Note: These tests use mocked executor commands in real scenarios
	// For now, we test the logic and command generation

	t.Run("generate disable command", func(t *testing.T) {
		exec := executor.NewLocalExecutor()
		manager := NewSystemdManager(exec)

		serviceName := "mongod"
		command := manager.generateDisableCommand(serviceName)

		assert.Contains(t, command, "systemctl disable")
		assert.Contains(t, command, serviceName)
	})

	t.Run("generate stop command", func(t *testing.T) {
		exec := executor.NewLocalExecutor()
		manager := NewSystemdManager(exec)

		serviceName := "mongod"
		command := manager.generateStopCommand(serviceName)

		assert.Contains(t, command, "systemctl stop")
		assert.Contains(t, command, serviceName)
	})
}

// IMP-009: Re-enable systemd services on failure
func TestEnableSystemdService(t *testing.T) {
	t.Run("generate enable command", func(t *testing.T) {
		exec := executor.NewLocalExecutor()
		manager := NewSystemdManager(exec)

		serviceName := "mongod"
		command := manager.generateEnableCommand(serviceName)

		assert.Contains(t, command, "systemctl enable")
		assert.Contains(t, command, serviceName)
	})

	t.Run("generate start command", func(t *testing.T) {
		exec := executor.NewLocalExecutor()
		manager := NewSystemdManager(exec)

		serviceName := "mongod"
		command := manager.generateStartCommand(serviceName)

		assert.Contains(t, command, "systemctl start")
		assert.Contains(t, command, serviceName)
	})
}

// Test systemd service state tracking
func TestSystemdStateTracking(t *testing.T) {
	t.Run("track disabled services", func(t *testing.T) {
		exec := executor.NewLocalExecutor()
		manager := NewSystemdManager(exec)

		services := []string{"mongod", "mongod-shard1", "mongod-shard2"}

		for _, svc := range services {
			manager.TrackDisabledService(svc)
		}

		disabledServices := manager.GetDisabledServices()
		assert.Len(t, disabledServices, 3)
		assert.Contains(t, disabledServices, "mongod")
		assert.Contains(t, disabledServices, "mongod-shard1")
		assert.Contains(t, disabledServices, "mongod-shard2")
	})

	t.Run("clear tracked services", func(t *testing.T) {
		exec := executor.NewLocalExecutor()
		manager := NewSystemdManager(exec)

		manager.TrackDisabledService("test-service")
		assert.Len(t, manager.GetDisabledServices(), 1)

		manager.ClearTrackedServices()
		assert.Len(t, manager.GetDisabledServices(), 0)
	})
}

// Test rollback functionality
func TestSystemdRollback(t *testing.T) {
	exec := executor.NewLocalExecutor()
	manager := NewSystemdManager(exec)

	t.Run("rollback plan includes all disabled services", func(t *testing.T) {
		services := []string{"mongod-1", "mongod-2", "mongod-3"}

		for _, svc := range services {
			manager.TrackDisabledService(svc)
		}

		// IMP-009: Rollback should re-enable all disabled services
		rollbackCmds := manager.GenerateRollbackCommands()

		// Should have enable + start commands for each service
		assert.Len(t, rollbackCmds, len(services)*2)

		// Verify commands are correct
		for _, svc := range services {
			foundEnable := false
			foundStart := false
			for _, cmd := range rollbackCmds {
				if cmd.Contains("enable") && cmd.Contains(svc) {
					foundEnable = true
				}
				if cmd.Contains("start") && cmd.Contains(svc) {
					foundStart = true
				}
			}
			assert.True(t, foundEnable, "Should have enable command for %s", svc)
			assert.True(t, foundStart, "Should have start command for %s", svc)
		}
	})
}

// Test service status checking
func TestCheckServiceStatus(t *testing.T) {
	exec := executor.NewLocalExecutor()
	manager := NewSystemdManager(exec)

	t.Run("generate status check command", func(t *testing.T) {
		serviceName := "mongod"
		command := manager.generateStatusCommand(serviceName)

		assert.Contains(t, command, "systemctl is-active")
		assert.Contains(t, command, serviceName)
	})
}

// Test systemd service validation
func TestValidateSystemdService(t *testing.T) {
	t.Run("validate service name format", func(t *testing.T) {
		validNames := []string{
			"mongod",
			"mongod-shard1",
			"mongod.service",
			"mongodb-27017",
		}

		for _, name := range validNames {
			normalized := normalizeServiceName(name)
			assert.NotEmpty(t, normalized)
			assert.NotContains(t, normalized, ".service", "Should strip .service suffix")
		}
	})
}

// Helper function for testing
func normalizeServiceName(name string) string {
	// Remove .service suffix if present
	if len(name) > 8 && name[len(name)-8:] == ".service" {
		return name[:len(name)-8]
	}
	return name
}
