package apply

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckpointer_CreateCheckpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-checkpointer-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stateMgr := NewStateManager(tmpDir)
	checkpointer := NewCheckpointer(stateMgr)

	state := NewApplyState("plan-123", "test-cluster", "deploy")
	state.CurrentPhase = "prepare"

	err = checkpointer.CreateCheckpoint(state, "Completed prepare phase")
	require.NoError(t, err)

	// Verify checkpoint was added to state
	assert.Len(t, state.Checkpoints, 1)
	checkpoint := state.Checkpoints[0]
	assert.Equal(t, "Completed prepare phase", checkpoint.Description)
	assert.Equal(t, "prepare", checkpoint.Phase)
	assert.NotEmpty(t, checkpoint.ID)
	assert.NotZero(t, checkpoint.Timestamp)

	// Verify checkpoint file was created
	_, err = os.Stat(checkpoint.StatePath)
	assert.NoError(t, err, "Checkpoint file should exist")
}

func TestCheckpointer_LoadCheckpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-checkpointer-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stateMgr := NewStateManager(tmpDir)
	checkpointer := NewCheckpointer(stateMgr)

	// Create state with some data
	state := NewApplyState("plan-123", "test-cluster", "deploy")
	state.CurrentPhase = "prepare"
	state.Log("info", "prepare", "", "Test log entry")

	// Create checkpoint
	err = checkpointer.CreateCheckpoint(state, "Test checkpoint")
	require.NoError(t, err)

	checkpointID := state.Checkpoints[0].ID

	// Load checkpoint
	loadedState, err := checkpointer.LoadCheckpoint(state.StateID, checkpointID)
	require.NoError(t, err)

	// Verify loaded state matches original
	assert.Equal(t, state.StateID, loadedState.StateID)
	assert.Equal(t, state.ClusterName, loadedState.ClusterName)
	assert.Equal(t, state.CurrentPhase, loadedState.CurrentPhase)
	assert.Len(t, loadedState.ExecutionLog, 1)
}

func TestCheckpointer_ListCheckpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-checkpointer-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stateMgr := NewStateManager(tmpDir)
	checkpointer := NewCheckpointer(stateMgr)

	state := NewApplyState("plan-123", "test-cluster", "deploy")

	// Save state first
	require.NoError(t, stateMgr.SaveState(state))

	// No checkpoints initially
	checkpoints, err := checkpointer.ListCheckpoints(state.StateID)
	require.NoError(t, err)
	assert.Empty(t, checkpoints)

	// Create checkpoints
	state.CurrentPhase = "prepare"
	err = checkpointer.CreateCheckpoint(state, "Checkpoint 1")
	require.NoError(t, err)

	state.CurrentPhase = "deploy"
	err = checkpointer.CreateCheckpoint(state, "Checkpoint 2")
	require.NoError(t, err)

	// List checkpoints
	checkpoints, err = checkpointer.ListCheckpoints(state.StateID)
	require.NoError(t, err)
	assert.Len(t, checkpoints, 2)
	assert.Equal(t, "Checkpoint 1", checkpoints[0].Description)
	assert.Equal(t, "Checkpoint 2", checkpoints[1].Description)
}

func TestCheckpointer_GetLatestCheckpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-checkpointer-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stateMgr := NewStateManager(tmpDir)
	checkpointer := NewCheckpointer(stateMgr)

	state := NewApplyState("plan-123", "test-cluster", "deploy")
	require.NoError(t, stateMgr.SaveState(state))

	// Create checkpoints with delays to ensure different timestamps
	state.CurrentPhase = "prepare"
	err = checkpointer.CreateCheckpoint(state, "Checkpoint 1")
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	state.CurrentPhase = "deploy"
	err = checkpointer.CreateCheckpoint(state, "Checkpoint 2")
	require.NoError(t, err)
	checkpoint2ID := state.Checkpoints[1].ID

	// Get latest checkpoint
	latest, err := checkpointer.GetLatestCheckpoint(state.StateID)
	require.NoError(t, err)
	assert.Equal(t, checkpoint2ID, latest.ID)
	assert.Equal(t, "deploy", latest.Phase)
}

func TestCheckpointer_GetLatestCheckpoint_NoCheckpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-checkpointer-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stateMgr := NewStateManager(tmpDir)
	checkpointer := NewCheckpointer(stateMgr)

	state := NewApplyState("plan-123", "test-cluster", "deploy")
	require.NoError(t, stateMgr.SaveState(state))

	_, err = checkpointer.GetLatestCheckpoint(state.StateID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no checkpoints found")
}

func TestCheckpointer_RestoreFromCheckpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-checkpointer-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stateMgr := NewStateManager(tmpDir)
	checkpointer := NewCheckpointer(stateMgr)

	// Create state and checkpoint
	state := NewApplyState("plan-123", "test-cluster", "deploy")
	state.CurrentPhase = "prepare"
	state.UpdateStatus(StatusRunning)

	err = checkpointer.CreateCheckpoint(state, "Test checkpoint")
	require.NoError(t, err)
	checkpointID := state.Checkpoints[0].ID

	// Modify state after checkpoint
	state.CurrentPhase = "deploy"
	state.UpdateStatus(StatusFailed)

	// Restore from checkpoint
	restoredState, err := checkpointer.RestoreFromCheckpoint(state.StateID, checkpointID)
	require.NoError(t, err)

	// Verify restored state
	assert.Equal(t, state.StateID, restoredState.StateID)
	assert.Equal(t, "prepare", restoredState.CurrentPhase)
	assert.Equal(t, StatusPaused, restoredState.Status) // Restored states are paused
}

func TestCheckpointer_CleanupOldCheckpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-checkpointer-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stateMgr := NewStateManager(tmpDir)
	checkpointer := NewCheckpointer(stateMgr)

	state := NewApplyState("plan-123", "test-cluster", "deploy")
	state.CurrentPhase = "prepare"

	// Create a checkpoint
	err = checkpointer.CreateCheckpoint(state, "Old checkpoint")
	require.NoError(t, err)

	checkpointDir := stateMgr.GetCheckpointDir(state.StateID)

	// Verify checkpoint exists
	entries, err := os.ReadDir(checkpointDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries)

	// Clean up checkpoints older than 0 (should delete all)
	err = checkpointer.CleanupOldCheckpoints(state.StateID, 0)
	require.NoError(t, err)

	// Verify checkpoints were cleaned up
	entries, err = os.ReadDir(checkpointDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestCheckpointer_GetCheckpointInfo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-checkpointer-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	stateMgr := NewStateManager(tmpDir)
	checkpointer := NewCheckpointer(stateMgr)

	state := NewApplyState("plan-123", "test-cluster", "deploy")
	state.CurrentPhase = "prepare"
	state.StartOperation("op-001")
	state.CompleteOperation("op-001", &OperationResult{Success: true})

	err = checkpointer.CreateCheckpoint(state, "Test checkpoint")
	require.NoError(t, err)
	checkpointID := state.Checkpoints[0].ID

	// Get checkpoint info
	// This loads the current state for metadata and checkpoint state for statistics
	info, err := checkpointer.GetCheckpointInfo(state.StateID, checkpointID)
	require.NoError(t, err)

	assert.Equal(t, checkpointID, info.ID)
	assert.Equal(t, "Test checkpoint", info.Description)
	assert.Equal(t, "prepare", info.Phase)
	assert.Equal(t, 1, info.Completed)
	assert.Equal(t, 1, info.Total)
	assert.Equal(t, 0, info.Errors)
}

func TestCheckpointInfo_String(t *testing.T) {
	info := &CheckpointInfo{
		ID:          "checkpoint-123",
		Description: "Test checkpoint",
		Timestamp:   time.Now(),
		Phase:       "prepare",
		Status:      StatusCompleted,
		Errors:      0,
		Completed:   5,
		Total:       10,
	}

	str := info.String()
	assert.Contains(t, str, "checkpoint-123")
	assert.Contains(t, str, "Test checkpoint")
	assert.Contains(t, str, "prepare")
	assert.Contains(t, str, "5/10")
}
