package apply

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/plan"
)

func TestNewApplyState(t *testing.T) {
	state := NewApplyState("plan-123", "test-cluster", "deploy")

	assert.NotEmpty(t, state.StateID)
	assert.Equal(t, "plan-123", state.PlanID)
	assert.Equal(t, "test-cluster", state.ClusterName)
	assert.Equal(t, "deploy", state.Operation)
	assert.Equal(t, StatusPending, state.Status)
	assert.NotNil(t, state.PhaseStates)
	assert.NotNil(t, state.OperationStates)
	assert.NotNil(t, state.Checkpoints)
	assert.NotNil(t, state.Errors)
	assert.NotNil(t, state.ExecutionLog)
}

func TestApplyState_UpdateStatus(t *testing.T) {
	state := NewApplyState("plan-123", "test-cluster", "deploy")

	state.UpdateStatus(StatusRunning)
	assert.Equal(t, StatusRunning, state.Status)
	assert.Nil(t, state.CompletedAt)

	state.UpdateStatus(StatusCompleted)
	assert.Equal(t, StatusCompleted, state.Status)
	assert.NotNil(t, state.CompletedAt)
}

func TestApplyState_PhaseLifecycle(t *testing.T) {
	state := NewApplyState("plan-123", "test-cluster", "deploy")

	// Start phase
	state.StartPhase("prepare")
	assert.Equal(t, "prepare", state.CurrentPhase)
	require.Contains(t, state.PhaseStates, "prepare")
	assert.Equal(t, StatusRunning, state.PhaseStates["prepare"].Status)
	assert.NotNil(t, state.PhaseStates["prepare"].StartedAt)

	// Complete phase
	state.CompletePhase("prepare")
	assert.Equal(t, StatusCompleted, state.PhaseStates["prepare"].Status)
	assert.NotNil(t, state.PhaseStates["prepare"].CompletedAt)

	// Fail phase
	state.StartPhase("deploy")
	state.FailPhase("deploy", assert.AnError)
	assert.Equal(t, StatusFailed, state.PhaseStates["deploy"].Status)
	assert.Contains(t, state.PhaseStates["deploy"].Error, "assert.AnError")
}

func TestApplyState_OperationLifecycle(t *testing.T) {
	state := NewApplyState("plan-123", "test-cluster", "deploy")

	// Start operation
	state.StartOperation("op-001")
	require.Contains(t, state.OperationStates, "op-001")
	assert.Equal(t, StatusRunning, state.OperationStates["op-001"].Status)

	// Complete operation
	result := &OperationResult{
		Success: true,
		Output:  "Operation completed",
		Changes: []plan.Change{},
	}
	state.CompleteOperation("op-001", result)
	assert.Equal(t, StatusCompleted, state.OperationStates["op-001"].Status)
	assert.Equal(t, result, state.OperationStates["op-001"].Result)

	// Fail operation
	state.StartOperation("op-002")
	state.FailOperation("op-002", assert.AnError, true)
	assert.Equal(t, StatusFailed, state.OperationStates["op-002"].Status)
	assert.Len(t, state.Errors, 1)
	assert.True(t, state.Errors[0].Recoverable)
}

func TestApplyState_Checkpoints(t *testing.T) {
	state := NewApplyState("plan-123", "test-cluster", "deploy")
	state.CurrentPhase = "prepare"

	// Create checkpoint
	state.CreateCheckpoint("Completed prepare phase", "/tmp/checkpoint-1.json")
	assert.Len(t, state.Checkpoints, 1)
	assert.Equal(t, "Completed prepare phase", state.Checkpoints[0].Description)
	assert.Equal(t, "prepare", state.Checkpoints[0].Phase)

	// Get last checkpoint
	lastCP := state.GetLastCheckpoint()
	require.NotNil(t, lastCP)
	assert.Equal(t, state.Checkpoints[0].ID, lastCP.ID)

	// Create another checkpoint
	state.CurrentPhase = "deploy"
	state.CreateCheckpoint("Completed deploy phase", "/tmp/checkpoint-2.json")
	assert.Len(t, state.Checkpoints, 2)

	lastCP = state.GetLastCheckpoint()
	assert.Equal(t, "deploy", lastCP.Phase)
}

func TestApplyState_GetLastCheckpoint_Empty(t *testing.T) {
	state := NewApplyState("plan-123", "test-cluster", "deploy")
	assert.Nil(t, state.GetLastCheckpoint())
}

func TestApplyState_Log(t *testing.T) {
	state := NewApplyState("plan-123", "test-cluster", "deploy")
	state.CurrentPhase = "prepare"

	state.Log("info", "prepare", "op-001", "Starting operation")
	assert.Len(t, state.ExecutionLog, 1)

	entry := state.ExecutionLog[0]
	assert.Equal(t, "info", entry.Level)
	assert.Equal(t, "prepare", entry.Phase)
	assert.Equal(t, "op-001", entry.Operation)
	assert.Equal(t, "Starting operation", entry.Message)
	assert.NotZero(t, entry.Timestamp)
}

func TestApplyState_IsComplete(t *testing.T) {
	state := NewApplyState("plan-123", "test-cluster", "deploy")

	state.Status = StatusRunning
	assert.False(t, state.IsComplete())

	state.Status = StatusCompleted
	assert.True(t, state.IsComplete())

	state.Status = StatusFailed
	assert.True(t, state.IsComplete())

	state.Status = StatusRolledBack
	assert.True(t, state.IsComplete())
}

func TestApplyState_CanResume(t *testing.T) {
	state := NewApplyState("plan-123", "test-cluster", "deploy")

	state.Status = StatusRunning
	assert.False(t, state.CanResume())

	state.Status = StatusPaused
	assert.True(t, state.CanResume())

	state.Status = StatusFailed
	assert.True(t, state.CanResume())

	state.Status = StatusCompleted
	assert.False(t, state.CanResume())
}

func TestApplyState_SaveAndLoad(t *testing.T) {
	// Create test state
	state := NewApplyState("plan-123", "test-cluster", "deploy")
	state.UpdateStatus(StatusRunning)
	state.StartPhase("prepare")
	// Note: StartPhase already logs an entry, so we have 1 log entry

	// Save to temp file
	tmpFile, err := os.CreateTemp("", "test-state-*.json")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	err = state.SaveToFile(tmpPath)
	require.NoError(t, err)

	// Load from file
	loadedState, err := LoadStateFromFile(tmpPath)
	require.NoError(t, err)

	// Verify
	assert.Equal(t, state.StateID, loadedState.StateID)
	assert.Equal(t, state.PlanID, loadedState.PlanID)
	assert.Equal(t, state.ClusterName, loadedState.ClusterName)
	assert.Equal(t, state.Operation, loadedState.Operation)
	assert.Equal(t, state.Status, loadedState.Status)
	assert.Equal(t, state.CurrentPhase, loadedState.CurrentPhase)
	assert.NotEmpty(t, loadedState.ExecutionLog)
}

func TestStateManager_SaveAndLoad(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "test-state-manager-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mgr := NewStateManager(tmpDir)

	// Create and save state
	state := NewApplyState("plan-123", "test-cluster", "deploy")
	err = mgr.SaveState(state)
	require.NoError(t, err)

	// Load state
	loadedState, err := mgr.LoadState(state.StateID)
	require.NoError(t, err)
	assert.Equal(t, state.StateID, loadedState.StateID)
}

func TestStateManager_ListStates(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-state-manager-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mgr := NewStateManager(tmpDir)

	// Initially empty
	states, err := mgr.ListStates()
	require.NoError(t, err)
	assert.Empty(t, states)

	// Save multiple states
	state1 := NewApplyState("plan-1", "cluster-1", "deploy")
	state2 := NewApplyState("plan-2", "cluster-2", "deploy")

	require.NoError(t, mgr.SaveState(state1))
	require.NoError(t, mgr.SaveState(state2))

	// List states
	states, err = mgr.ListStates()
	require.NoError(t, err)
	assert.Len(t, states, 2)
}

func TestStateManager_GetCurrentState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-state-manager-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mgr := NewStateManager(tmpDir)

	// No state initially
	_, err = mgr.GetCurrentState()
	assert.Error(t, err)

	// Save states with different timestamps
	state1 := NewApplyState("plan-1", "cluster-1", "deploy")
	state1.StartedAt = time.Now().Add(-1 * time.Hour)
	require.NoError(t, mgr.SaveState(state1))

	state2 := NewApplyState("plan-2", "cluster-1", "deploy")
	state2.StartedAt = time.Now()
	require.NoError(t, mgr.SaveState(state2))

	// Get current (most recent) state
	current, err := mgr.GetCurrentState()
	require.NoError(t, err)
	assert.Equal(t, state2.StateID, current.StateID)
}

func TestStateManager_SaveCheckpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-state-manager-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mgr := NewStateManager(tmpDir)

	state := NewApplyState("plan-123", "test-cluster", "deploy")
	state.CurrentPhase = "prepare"

	err = mgr.SaveCheckpoint(state, "Completed prepare phase")
	require.NoError(t, err)

	// Verify checkpoint was added
	assert.Len(t, state.Checkpoints, 1)
	assert.Equal(t, "Completed prepare phase", state.Checkpoints[0].Description)

	// Verify checkpoint file exists
	checkpointDir := mgr.GetCheckpointDir(state.StateID)
	entries, err := os.ReadDir(checkpointDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries)
}

func TestStateManager_GetStatePath(t *testing.T) {
	mgr := NewStateManager("/test/cluster")
	path := mgr.GetStatePath("state-123")

	expected := filepath.Join("/test/cluster", "state", "state-123.json")
	assert.Equal(t, expected, path)
}

func TestStateManager_GetCheckpointDir(t *testing.T) {
	mgr := NewStateManager("/test/cluster")
	dir := mgr.GetCheckpointDir("state-123")

	expected := filepath.Join("/test/cluster", "state", "state-123-checkpoints")
	assert.Equal(t, expected, dir)
}

func TestStateManager_GetCheckpointPath(t *testing.T) {
	mgr := NewStateManager("/test/cluster")
	path := mgr.GetCheckpointPath("state-123", "checkpoint-456")

	expected := filepath.Join("/test/cluster", "state", "state-123-checkpoints", "checkpoint-456.json")
	assert.Equal(t, expected, path)
}
