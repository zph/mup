package simulation

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REQ-SIM-001, REQ-SIM-016: SimulationExecutor must implement Executor interface
func TestSimulationExecutor_ImplementsInterface(t *testing.T) {
	config := NewConfig()
	executor := NewExecutor(config)

	// Verify executor is not nil
	require.NotNil(t, executor)
}

// REQ-SIM-004: Record filesystem operations without modifying actual filesystem
func TestSimulationExecutor_CreateDirectory(t *testing.T) {
	config := NewConfig()
	executor := NewExecutor(config)

	// REQ-SIM-004: Create directory should not actually create on disk
	testPath := "/tmp/simulation-test-dir-" + t.Name()

	err := executor.CreateDirectory(testPath, 0755)
	assert.NoError(t, err)

	// Verify directory was NOT created on actual filesystem
	_, statErr := os.Stat(testPath)
	assert.True(t, os.IsNotExist(statErr), "Directory should not exist on actual filesystem")

	// REQ-SIM-002: Operation should be recorded in simulation state
	ops := executor.GetOperations()
	require.Len(t, ops, 1)
	assert.Equal(t, "create_directory", ops[0].Type)
	assert.Equal(t, testPath, ops[0].Target)
}

// REQ-SIM-005: File existence checks should use simulated state
func TestSimulationExecutor_FileExists(t *testing.T) {
	config := NewConfig()
	executor := NewExecutor(config)

	testPath := "/tmp/simulated-file.txt"

	// Initially, file should not exist in simulated state
	exists, err := executor.FileExists(testPath)
	assert.NoError(t, err)
	assert.False(t, exists)

	// REQ-SIM-004: Upload content to simulated filesystem
	err = executor.UploadContent([]byte("test content"), testPath)
	assert.NoError(t, err)

	// REQ-SIM-005: Now file should exist in simulated state
	exists, err = executor.FileExists(testPath)
	assert.NoError(t, err)
	assert.True(t, exists, "File should exist in simulated filesystem")

	// Verify file was NOT created on actual filesystem
	_, statErr := os.Stat(testPath)
	assert.True(t, os.IsNotExist(statErr), "File should not exist on actual filesystem")
}

// REQ-SIM-008: Record process operations without starting actual processes
func TestSimulationExecutor_Background(t *testing.T) {
	config := NewConfig()
	executor := NewExecutor(config)

	// REQ-SIM-008: Start process in background
	pid, err := executor.Background("mongod --config /etc/mongod.conf")
	assert.NoError(t, err)
	assert.Greater(t, pid, 0, "Should return a simulated PID")

	// REQ-SIM-002: Operation should be recorded
	ops := executor.GetOperations()
	require.Len(t, ops, 1)
	assert.Equal(t, "start_process", ops[0].Type)
	assert.Contains(t, ops[0].Details, "mongod")

	// REQ-SIM-009: Process should be tracked as running in simulated state
	running, err := executor.IsProcessRunning(pid)
	assert.NoError(t, err)
	assert.True(t, running, "Process should be tracked as running in simulation")
}

// REQ-SIM-011: Process status queries should use simulated state
func TestSimulationExecutor_ProcessLifecycle(t *testing.T) {
	config := NewConfig()
	executor := NewExecutor(config)

	// Start process
	pid, err := executor.Background("mongod")
	require.NoError(t, err)

	// Verify running
	running, err := executor.IsProcessRunning(pid)
	assert.NoError(t, err)
	assert.True(t, running)

	// REQ-SIM-008: Stop process
	err = executor.StopProcess(pid)
	assert.NoError(t, err)

	// REQ-SIM-011: Verify stopped
	running, err = executor.IsProcessRunning(pid)
	assert.NoError(t, err)
	assert.False(t, running, "Process should be stopped")

	// Verify operation was recorded
	ops := executor.GetOperations()
	assert.GreaterOrEqual(t, len(ops), 2) // start + stop
}

// REQ-SIM-013: SSH commands should return configurable responses
func TestSimulationExecutor_Execute_WithConfiguredResponse(t *testing.T) {
	config := NewConfig()

	// REQ-SIM-043: Configure default response
	config.SetResponse("mongod --version", "db version v7.0.5")

	executor := NewExecutor(config)

	// REQ-SIM-013: Execute command should return configured response
	output, err := executor.Execute("mongod --version")
	assert.NoError(t, err)
	assert.Equal(t, "db version v7.0.5", output)

	// REQ-SIM-002: Operation should be recorded
	ops := executor.GetOperations()
	require.Len(t, ops, 1)
	assert.Equal(t, "execute", ops[0].Type)
	assert.Equal(t, "mongod --version", ops[0].Details)
}

// REQ-SIM-014: Connectivity checks should always succeed
func TestSimulationExecutor_CheckConnectivity(t *testing.T) {
	config := NewConfig()
	executor := NewExecutor(config)

	// REQ-SIM-014: Should always succeed in simulation
	err := executor.CheckConnectivity()
	assert.NoError(t, err)
}

// REQ-SIM-018: Support configurable behaviors including failures
func TestSimulationExecutor_ConfiguredFailure(t *testing.T) {
	config := NewConfig()

	// REQ-SIM-018: Configure a failure scenario
	config.SetFailure("upload_file", "/etc/mongod.conf", "permission denied")

	executor := NewExecutor(config)

	// This should succeed (no failure configured)
	err := executor.UploadFile("local.conf", "/tmp/mongod.conf")
	assert.NoError(t, err)

	// This should fail (failure configured)
	err = executor.UploadFile("local.conf", "/etc/mongod.conf")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

// REQ-SIM-023: Simulation should be deterministic
func TestSimulationExecutor_Deterministic(t *testing.T) {
	// Run same operations multiple times
	for i := 0; i < 3; i++ {
		config := NewConfig()
		config.SetResponse("test", "response")
		executor := NewExecutor(config)

		err := executor.CreateDirectory("/test/dir", 0755)
		assert.NoError(t, err)

		pid, err := executor.Background("mongod")
		assert.NoError(t, err)
		assert.Greater(t, pid, 0)

		output, err := executor.Execute("test")
		assert.NoError(t, err)
		assert.Equal(t, "response", output)

		// Results should be consistent across runs
		ops := executor.GetOperations()
		assert.Len(t, ops, 3)
	}
}

// REQ-SIM-002: Track all operations for reporting
func TestSimulationExecutor_GetOperations(t *testing.T) {
	config := NewConfig()
	executor := NewExecutor(config)

	// Perform various operations
	_ = executor.CreateDirectory("/data/mongodb", 0755)
	_ = executor.UploadContent([]byte("config"), "/etc/mongod.conf")
	_, _ = executor.Background("mongod")
	_, _ = executor.Execute("systemctl status mongod")

	// REQ-SIM-002: All operations should be tracked
	ops := executor.GetOperations()
	assert.Len(t, ops, 4)

	// Verify operation types
	assert.Equal(t, "create_directory", ops[0].Type)
	assert.Equal(t, "upload_content", ops[1].Type)
	assert.Equal(t, "start_process", ops[2].Type)
	assert.Equal(t, "execute", ops[3].Type)
}

// REQ-SIM-035: Memory usage should remain under 100MB
func TestSimulationExecutor_MemoryUsage(t *testing.T) {
	config := NewConfig()
	executor := NewExecutor(config)

	// Simulate a large deployment with many operations
	for i := 0; i < 1000; i++ {
		_ = executor.CreateDirectory("/data/dir-"+string(rune(i)), 0755)
		_ = executor.UploadContent([]byte("content"), "/data/file-"+string(rune(i)))
		_, _ = executor.Background("process-" + string(rune(i)))
	}

	ops := executor.GetOperations()
	assert.Len(t, ops, 3000)

	// This test passes if it doesn't crash with OOM
	// Actual memory profiling would be done separately
}

// REQ-SIM-007: Support reading actual files while preventing writes
func TestSimulationExecutor_RealFileReads(t *testing.T) {
	config := NewConfig()
	config.AllowRealFileReads = true
	executor := NewExecutor(config)

	// Create a temporary file for testing
	tmpfile, err := os.CreateTemp("", "simulation-test-*.txt")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	content := []byte("test topology content")
	_, err = tmpfile.Write(content)
	require.NoError(t, err)
	tmpfile.Close()

	// REQ-SIM-007: Should be able to check if real file exists
	exists, err := executor.FileExists(tmpfile.Name())
	assert.NoError(t, err)
	assert.True(t, exists, "Should detect actual file existence when AllowRealFileReads is true")
}

// REQ-SIM-010: Process readiness checks should succeed instantly
func TestSimulationExecutor_InstantReadiness(t *testing.T) {
	config := NewConfig()
	executor := NewExecutor(config)

	// Start a process
	pid, err := executor.Background("mongod")
	require.NoError(t, err)

	// REQ-SIM-010: Readiness check should succeed immediately
	running, err := executor.IsProcessRunning(pid)
	assert.NoError(t, err)
	assert.True(t, running)

	// In real execution, this would take seconds to minutes
	// In simulation, it's instant
}
