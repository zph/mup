package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Checkpointer manages checkpoints
type Checkpointer struct {
	stateManager *StateManager
}

// NewCheckpointer creates a new checkpointer
func NewCheckpointer(stateManager *StateManager) *Checkpointer {
	return &Checkpointer{
		stateManager: stateManager,
	}
}

// CreateCheckpoint creates a new checkpoint
func (c *Checkpointer) CreateCheckpoint(state *ApplyState, description string) error {
	// Create checkpoint directory if it doesn't exist
	checkpointDir := c.stateManager.GetCheckpointDir(state.StateID)
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Generate checkpoint ID based on timestamp
	checkpointID := fmt.Sprintf("checkpoint-%d", time.Now().Unix())
	checkpointPath := c.stateManager.GetCheckpointPath(state.StateID, checkpointID)

	// Save state snapshot
	if err := state.SaveToFile(checkpointPath); err != nil {
		return fmt.Errorf("failed to save checkpoint: %w", err)
	}

	// Add checkpoint to state
	checkpoint := Checkpoint{
		ID:          checkpointID,
		Description: description,
		Timestamp:   time.Now(),
		Phase:       state.CurrentPhase,
		StatePath:   checkpointPath,
	}
	state.Checkpoints = append(state.Checkpoints, checkpoint)

	// Save updated state
	if err := c.stateManager.SaveState(state); err != nil {
		return fmt.Errorf("failed to save state after checkpoint: %w", err)
	}

	return nil
}

// LoadCheckpoint loads a checkpoint by ID
func (c *Checkpointer) LoadCheckpoint(stateID, checkpointID string) (*ApplyState, error) {
	checkpointPath := c.stateManager.GetCheckpointPath(stateID, checkpointID)
	state, err := LoadStateFromFile(checkpointPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint: %w", err)
	}
	return state, nil
}

// ListCheckpoints lists all checkpoints for a state
func (c *Checkpointer) ListCheckpoints(stateID string) ([]Checkpoint, error) {
	// Load the state to get checkpoint references
	state, err := c.stateManager.LoadState(stateID)
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	return state.Checkpoints, nil
}

// GetLatestCheckpoint returns the most recent checkpoint for a state
func (c *Checkpointer) GetLatestCheckpoint(stateID string) (*Checkpoint, error) {
	checkpoints, err := c.ListCheckpoints(stateID)
	if err != nil {
		return nil, err
	}

	if len(checkpoints) == 0 {
		return nil, fmt.Errorf("no checkpoints found")
	}

	// Sort by timestamp (newest first)
	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].Timestamp.After(checkpoints[j].Timestamp)
	})

	return &checkpoints[0], nil
}

// RestoreFromCheckpoint restores state from a checkpoint
func (c *Checkpointer) RestoreFromCheckpoint(stateID, checkpointID string) (*ApplyState, error) {
	// Load the checkpoint
	state, err := c.LoadCheckpoint(stateID, checkpointID)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint: %w", err)
	}

	// Mark state as resumed
	state.UpdateStatus(StatusPaused)
	state.Log("info", state.CurrentPhase, "", fmt.Sprintf("Restored from checkpoint: %s", checkpointID))

	// Save the restored state
	if err := c.stateManager.SaveState(state); err != nil {
		return nil, fmt.Errorf("failed to save restored state: %w", err)
	}

	return state, nil
}

// CleanupOldCheckpoints removes checkpoints older than the specified duration
func (c *Checkpointer) CleanupOldCheckpoints(stateID string, olderThan time.Duration) error {
	checkpointDir := c.stateManager.GetCheckpointDir(stateID)
	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No checkpoints to clean
		}
		return fmt.Errorf("failed to read checkpoint directory: %w", err)
	}

	cutoff := time.Now().Add(-olderThan)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			checkpointPath := filepath.Join(checkpointDir, entry.Name())
			if err := os.Remove(checkpointPath); err != nil {
				// Log but don't fail
				fmt.Printf("Warning: failed to remove old checkpoint %s: %v\n", checkpointPath, err)
			}
		}
	}

	return nil
}

// GetCheckpointInfo returns information about a checkpoint
func (c *Checkpointer) GetCheckpointInfo(stateID, checkpointID string) (*CheckpointInfo, error) {
	// Load the checkpoint state
	checkpointState, err := c.LoadCheckpoint(stateID, checkpointID)
	if err != nil {
		return nil, err
	}

	// Load the current state to get checkpoint metadata
	// (checkpoint files don't contain their own metadata)
	currentState, err := c.stateManager.LoadState(stateID)
	if err != nil {
		return nil, err
	}

	// Find the checkpoint metadata in the current state
	var checkpoint *Checkpoint
	for i := range currentState.Checkpoints {
		if currentState.Checkpoints[i].ID == checkpointID {
			checkpoint = &currentState.Checkpoints[i]
			break
		}
	}

	if checkpoint == nil {
		return nil, fmt.Errorf("checkpoint metadata not found")
	}

	info := &CheckpointInfo{
		ID:          checkpoint.ID,
		Description: checkpoint.Description,
		Timestamp:   checkpoint.Timestamp,
		Phase:       checkpoint.Phase,
		Operation:   checkpoint.Operation,
		Status:      checkpointState.Status,
		Errors:      len(checkpointState.Errors),
		Completed:   c.countCompletedOperations(checkpointState),
		Total:       len(checkpointState.OperationStates),
	}

	return info, nil
}

// countCompletedOperations counts how many operations are completed
func (c *Checkpointer) countCompletedOperations(state *ApplyState) int {
	count := 0
	for _, opState := range state.OperationStates {
		if opState.Status == StatusCompleted {
			count++
		}
	}
	return count
}

// CheckpointInfo contains summary information about a checkpoint
type CheckpointInfo struct {
	ID          string
	Description string
	Timestamp   time.Time
	Phase       string
	Operation   string
	Status      ApplyStatus
	Errors      int
	Completed   int
	Total       int
}

// String returns a string representation of checkpoint info
func (i *CheckpointInfo) String() string {
	return fmt.Sprintf("Checkpoint %s: %s (Phase: %s, Status: %s, Progress: %d/%d, Errors: %d)",
		i.ID, i.Description, i.Phase, i.Status, i.Completed, i.Total, i.Errors)
}
