package apply

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zph/mup/pkg/plan"
)

// ApplyState tracks execution progress
type ApplyState struct {
	// Identity
	StateID     string    `json:"state_id"`      // UUID for this apply
	PlanID      string    `json:"plan_id"`       // Which plan we're executing
	ClusterName string    `json:"cluster_name"`
	Operation   string    `json:"operation"`     // "deploy", "upgrade", "import", etc.

	// Status
	Status      ApplyStatus `json:"status"`      // "pending", "running", "paused", "completed", "failed"
	StartedAt   time.Time   `json:"started_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`

	// Progress
	CurrentPhase    string                     `json:"current_phase"`
	PhaseStates     map[string]*PhaseState     `json:"phase_states"`
	OperationStates map[string]*OperationState `json:"operation_states"`

	// Checkpoints
	Checkpoints []Checkpoint `json:"checkpoints"`

	// Errors
	Errors []ExecutionError `json:"errors"`

	// Runtime Info
	ExecutionLog []LogEntry `json:"execution_log"`

	// Mutex for concurrent access protection (not serialized)
	mu sync.Mutex `json:"-"`
}

// ApplyStatus represents the status of an apply operation
type ApplyStatus string

const (
	StatusPending    ApplyStatus = "pending"
	StatusRunning    ApplyStatus = "running"
	StatusPaused     ApplyStatus = "paused"
	StatusCompleted  ApplyStatus = "completed"
	StatusFailed     ApplyStatus = "failed"
	StatusRolledBack ApplyStatus = "rolled_back"
)

// PhaseState tracks the state of a phase
type PhaseState struct {
	Name        string      `json:"name"`
	Status      ApplyStatus `json:"status"`
	StartedAt   *time.Time  `json:"started_at,omitempty"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`
	Error       string      `json:"error,omitempty"`
}

// OperationState tracks the state of an operation
type OperationState struct {
	ID          string               `json:"id"`
	Status      ApplyStatus          `json:"status"`
	StartedAt   *time.Time           `json:"started_at,omitempty"`
	CompletedAt *time.Time           `json:"completed_at,omitempty"`
	Error       string               `json:"error,omitempty"`
	Result      *OperationResult     `json:"result,omitempty"`
	Retries     int                  `json:"retries"`
}

// OperationResult contains the result of an operation
type OperationResult struct {
	Success  bool                   `json:"success"`
	Output   string                 `json:"output,omitempty"`
	Changes  []plan.Change          `json:"changes"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Checkpoint represents a point in time snapshot
type Checkpoint struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Timestamp   time.Time `json:"timestamp"`
	Phase       string    `json:"phase"`
	Operation   string    `json:"operation,omitempty"`
	StatePath   string    `json:"state_path"` // Path to full state snapshot
}

// ExecutionError represents an error during execution
type ExecutionError struct {
	Timestamp   time.Time `json:"timestamp"`
	Phase       string    `json:"phase"`
	Operation   string    `json:"operation"`
	Error       string    `json:"error"`
	Recoverable bool      `json:"recoverable"`
}

// LogEntry represents a log entry
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"` // "info", "warn", "error", "debug"
	Phase     string    `json:"phase"`
	Operation string    `json:"operation,omitempty"`
	Message   string    `json:"message"`
}

// NewApplyState creates a new apply state
func NewApplyState(planID, clusterName, operation string) *ApplyState {
	now := time.Now()
	return &ApplyState{
		StateID:         uuid.New().String(),
		PlanID:          planID,
		ClusterName:     clusterName,
		Operation:       operation,
		Status:          StatusPending,
		StartedAt:       now,
		UpdatedAt:       now,
		PhaseStates:     make(map[string]*PhaseState),
		OperationStates: make(map[string]*OperationState),
		Checkpoints:     make([]Checkpoint, 0),
		Errors:          make([]ExecutionError, 0),
		ExecutionLog:    make([]LogEntry, 0),
	}
}

// UpdateStatus updates the overall status
func (s *ApplyState) UpdateStatus(status ApplyStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Status = status
	s.UpdatedAt = time.Now()

	if status == StatusCompleted || status == StatusFailed || status == StatusRolledBack {
		now := time.Now()
		s.CompletedAt = &now
	}
}

// StartPhase marks a phase as started
func (s *ApplyState) StartPhase(phaseName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.CurrentPhase = phaseName
	s.PhaseStates[phaseName] = &PhaseState{
		Name:      phaseName,
		Status:    StatusRunning,
		StartedAt: &now,
	}
	s.UpdatedAt = now
	s.logUnsafe("info", phaseName, "", fmt.Sprintf("Starting phase: %s", phaseName))
}

// CompletePhase marks a phase as completed
func (s *ApplyState) CompletePhase(phaseName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if state, ok := s.PhaseStates[phaseName]; ok {
		state.Status = StatusCompleted
		state.CompletedAt = &now
	}
	s.UpdatedAt = now
	s.logUnsafe("info", phaseName, "", fmt.Sprintf("Completed phase: %s", phaseName))
}

// FailPhase marks a phase as failed
func (s *ApplyState) FailPhase(phaseName string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if state, ok := s.PhaseStates[phaseName]; ok {
		state.Status = StatusFailed
		state.CompletedAt = &now
		state.Error = err.Error()
	}
	s.UpdatedAt = now
	s.logUnsafe("error", phaseName, "", fmt.Sprintf("Phase failed: %s - %v", phaseName, err))
}

// StartOperation marks an operation as started
func (s *ApplyState) StartOperation(operationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	s.OperationStates[operationID] = &OperationState{
		ID:        operationID,
		Status:    StatusRunning,
		StartedAt: &now,
	}
	s.UpdatedAt = now
}

// CompleteOperation marks an operation as completed
func (s *ApplyState) CompleteOperation(operationID string, result *OperationResult) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if state, ok := s.OperationStates[operationID]; ok {
		state.Status = StatusCompleted
		state.CompletedAt = &now
		state.Result = result
	}
	s.UpdatedAt = now
}

// FailOperation marks an operation as failed
func (s *ApplyState) FailOperation(operationID string, err error, recoverable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if state, ok := s.OperationStates[operationID]; ok {
		state.Status = StatusFailed
		state.CompletedAt = &now
		state.Error = err.Error()
	}

	s.Errors = append(s.Errors, ExecutionError{
		Timestamp:   now,
		Phase:       s.CurrentPhase,
		Operation:   operationID,
		Error:       err.Error(),
		Recoverable: recoverable,
	})
	s.UpdatedAt = now
}

// CreateCheckpoint creates a new checkpoint
func (s *ApplyState) CreateCheckpoint(description string, statePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	checkpoint := Checkpoint{
		ID:          uuid.New().String(),
		Description: description,
		Timestamp:   time.Now(),
		Phase:       s.CurrentPhase,
		StatePath:   statePath,
	}
	s.Checkpoints = append(s.Checkpoints, checkpoint)
	s.logUnsafe("info", s.CurrentPhase, "", fmt.Sprintf("Checkpoint created: %s", description))
}

// GetLastCheckpoint returns the most recent checkpoint
func (s *ApplyState) GetLastCheckpoint() *Checkpoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.Checkpoints) == 0 {
		return nil
	}
	return &s.Checkpoints[len(s.Checkpoints)-1]
}

// Log adds a log entry (thread-safe)
func (s *ApplyState) Log(level, phase, operation, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logUnsafe(level, phase, operation, message)
}

// logUnsafe adds a log entry without locking (must be called with lock held)
func (s *ApplyState) logUnsafe(level, phase, operation, message string) {
	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Phase:     phase,
		Operation: operation,
		Message:   message,
	}
	s.ExecutionLog = append(s.ExecutionLog, entry)
	s.UpdatedAt = time.Now()
}

// IsComplete returns whether the apply is complete
func (s *ApplyState) IsComplete() bool {
	return s.Status == StatusCompleted || s.Status == StatusFailed || s.Status == StatusRolledBack
}

// CanResume returns whether the apply can be resumed
func (s *ApplyState) CanResume() bool {
	return s.Status == StatusPaused || s.Status == StatusFailed
}

// GetPhaseProgress returns the number of completed operations in a phase
func (s *ApplyState) GetPhaseProgress(phaseName string) (completed, total int) {
	// This will be calculated based on operations
	// For now, return simple phase state
	if state, ok := s.PhaseStates[phaseName]; ok {
		if state.Status == StatusCompleted {
			return 1, 1
		}
	}
	return 0, 1
}

// SaveToFile saves the state to a file
func (s *ApplyState) SaveToFile(path string) error {
	// Lock while marshaling to prevent concurrent modifications
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// LoadStateFromFile loads state from a file
func LoadStateFromFile(path string) (*ApplyState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state ApplyState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return &state, nil
}

// StateManager manages apply state persistence
type StateManager struct {
	clusterDir string
}

// NewStateManager creates a new state manager
func NewStateManager(clusterDir string) *StateManager {
	return &StateManager{
		clusterDir: clusterDir,
	}
}

// GetStateDir returns the state directory for the cluster
func (m *StateManager) GetStateDir() string {
	return filepath.Join(m.clusterDir, "state")
}

// GetStatePath returns the path to a state file
func (m *StateManager) GetStatePath(stateID string) string {
	return filepath.Join(m.GetStateDir(), fmt.Sprintf("%s.json", stateID))
}

// GetCheckpointDir returns the checkpoint directory for a state
func (m *StateManager) GetCheckpointDir(stateID string) string {
	return filepath.Join(m.GetStateDir(), fmt.Sprintf("%s-checkpoints", stateID))
}

// GetCheckpointPath returns the path to a checkpoint file
func (m *StateManager) GetCheckpointPath(stateID, checkpointID string) string {
	return filepath.Join(m.GetCheckpointDir(stateID), fmt.Sprintf("%s.json", checkpointID))
}

// SaveState saves the state to disk
func (m *StateManager) SaveState(state *ApplyState) error {
	path := m.GetStatePath(state.StateID)
	return state.SaveToFile(path)
}

// LoadState loads a state from disk
func (m *StateManager) LoadState(stateID string) (*ApplyState, error) {
	path := m.GetStatePath(stateID)
	return LoadStateFromFile(path)
}

// SaveCheckpoint saves a checkpoint
func (m *StateManager) SaveCheckpoint(state *ApplyState, description string) error {
	checkpointDir := m.GetCheckpointDir(state.StateID)
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	checkpointID := uuid.New().String()
	checkpointPath := m.GetCheckpointPath(state.StateID, checkpointID)

	// Save full state as checkpoint
	if err := state.SaveToFile(checkpointPath); err != nil {
		return fmt.Errorf("failed to save checkpoint: %w", err)
	}

	// Add checkpoint reference to state
	state.CreateCheckpoint(description, checkpointPath)

	// Save updated state
	return m.SaveState(state)
}

// ListStates lists all states for the cluster
func (m *StateManager) ListStates() ([]*ApplyState, error) {
	stateDir := m.GetStateDir()
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*ApplyState{}, nil
		}
		return nil, fmt.Errorf("failed to read state directory: %w", err)
	}

	states := make([]*ApplyState, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		statePath := filepath.Join(stateDir, entry.Name())
		state, err := LoadStateFromFile(statePath)
		if err != nil {
			// Skip invalid state files
			continue
		}
		states = append(states, state)
	}

	return states, nil
}

// GetCurrentState returns the current/latest state for the cluster
func (m *StateManager) GetCurrentState() (*ApplyState, error) {
	states, err := m.ListStates()
	if err != nil {
		return nil, err
	}

	if len(states) == 0 {
		return nil, fmt.Errorf("no state found")
	}

	// Return the most recent state
	var latest *ApplyState
	for _, state := range states {
		if latest == nil || state.StartedAt.After(latest.StartedAt) {
			latest = state
		}
	}

	return latest, nil
}
