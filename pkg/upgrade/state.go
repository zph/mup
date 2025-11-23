package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// [UPG-011] Upgrade state tracking and checkpointing

// NodeStatus represents the status of a node upgrade
type NodeStatus string

const (
	NodeStatusPending    NodeStatus = "pending"
	NodeStatusInProgress NodeStatus = "in_progress"
	NodeStatusCompleted  NodeStatus = "completed"
	NodeStatusFailed     NodeStatus = "failed"
	NodeStatusRolledBack NodeStatus = "rolled_back"
	NodeStatusSkipped    NodeStatus = "skipped" // User explicitly skipped this node
)

// PhaseStatus represents the status of an upgrade phase
type PhaseStatus string

const (
	PhaseStatusNotStarted PhaseStatus = "not_started"
	PhaseStatusInProgress PhaseStatus = "in_progress"
	PhaseStatusCompleted  PhaseStatus = "completed"
	PhaseStatusFailed     PhaseStatus = "failed"
)

// OverallStatus represents the overall upgrade status
type OverallStatus string

const (
	OverallStatusInProgress OverallStatus = "in_progress"
	OverallStatusCompleted  OverallStatus = "completed"
	OverallStatusFailed     OverallStatus = "failed"
	OverallStatusRolledBack OverallStatus = "rolled_back"
	OverallStatusPaused     OverallStatus = "paused"
)

// PhaseName represents an upgrade phase
type PhaseName string

const (
	PhasePreFlight     PhaseName = "pre-flight"
	PhaseConfigServers PhaseName = "config-servers"
	PhaseShard         PhaseName = "shard"      // Will be "shard-N" in practice
	PhaseMongos        PhaseName = "mongos"
	PhasePostUpgrade   PhaseName = "post-upgrade"
)

// NodeState represents the state of a single node upgrade
// [UPG-011] Node-level state tracking
type NodeState struct {
	Status              NodeStatus `yaml:"status"`
	HostPort            string     `yaml:"host_port"`
	StartTimestamp      time.Time  `yaml:"start_timestamp,omitempty"`
	CompletionTimestamp time.Time  `yaml:"completion_timestamp,omitempty"`
	PreviousVersion     string     `yaml:"previous_version"`
	TargetVersion       string     `yaml:"target_version"`
	ErrorDetails        string     `yaml:"error_details,omitempty"`
	RetryCount          int        `yaml:"retry_count"`
	Role                string     `yaml:"role,omitempty"` // PRIMARY, SECONDARY, ARBITER, MONGOS
}

// PhaseState represents the state of an upgrade phase
// [UPG-011] Phase-level state tracking
type PhaseState struct {
	Name                  PhaseName   `yaml:"name"`
	Status                PhaseStatus `yaml:"status"`
	NodesInPhase          []string    `yaml:"nodes_in_phase"` // List of host:port
	PhaseStartTimestamp   time.Time   `yaml:"phase_start_timestamp,omitempty"`
	LastCheckpointTime    time.Time   `yaml:"last_checkpoint_time,omitempty"`
}

// UpgradeState represents the complete upgrade state
// [UPG-011] Global upgrade state with checkpointing
type UpgradeState struct {
	// Global identifiers
	UpgradeID       string        `yaml:"upgrade_id"`
	ClusterName     string        `yaml:"cluster_name"`
	PreviousVersion string        `yaml:"previous_version"` // e.g., "mongo-6.0.15"
	TargetVersion   string        `yaml:"target_version"`   // e.g., "mongo-7.0.0"
	UpgradeStartedAt time.Time    `yaml:"upgrade_started_at"`
	LastUpdatedAt   time.Time     `yaml:"last_updated_at"`
	OverallStatus   OverallStatus `yaml:"overall_status"`

	// Phase tracking
	CurrentPhase PhaseName              `yaml:"current_phase"`
	Phases       map[PhaseName]*PhaseState `yaml:"phases"`

	// Node tracking
	Nodes map[string]*NodeState `yaml:"nodes"` // Key: host:port

	// Checkpoint metadata
	CheckpointCount int       `yaml:"checkpoint_count"`
	LastCheckpoint  time.Time `yaml:"last_checkpoint"`

	// Pause/resume metadata
	PausedAt     time.Time `yaml:"paused_at,omitempty"`
	PausedReason string    `yaml:"paused_reason,omitempty"`

	// User interaction tracking [UPG-016]
	PromptLevel        string   `yaml:"prompt_level,omitempty"`         // none, phase, node, critical
	SkippedNodes       []string `yaml:"skipped_nodes,omitempty"`        // Nodes user chose to skip
	UserPauseRequested bool     `yaml:"user_pause_requested,omitempty"` // User requested pause

	// Failover tracking
	Failovers []FailoverEvent `yaml:"failovers,omitempty"` // Track all failovers during upgrade

	mu sync.RWMutex `yaml:"-"` // Protects concurrent access
}

// FailoverEvent represents a failover that occurred during upgrade
type FailoverEvent struct {
	Timestamp      time.Time `yaml:"timestamp"`
	ReplicaSet     string    `yaml:"replica_set"`
	OldPrimary     string    `yaml:"old_primary"`      // host:port
	NewPrimary     string    `yaml:"new_primary"`      // host:port
	Reason         string    `yaml:"reason"`           // "stepdown", "automatic", "election"
	ElectionTimeMS int       `yaml:"election_time_ms"` // Time taken for election
}

// StateManager manages upgrade state persistence
// [UPG-011] State persistence with atomic writes
type StateManager struct {
	stateFile    string
	historyDir   string
	currentState *UpgradeState
	mu           sync.RWMutex
}

// NewStateManager creates a new state manager
func NewStateManager(clusterName string, metaDir string) (*StateManager, error) {
	stateFile := filepath.Join(metaDir, "upgrade-state.yaml")
	historyDir := filepath.Join(metaDir, "upgrade-history")

	// Create history directory if it doesn't exist
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create history directory: %w", err)
	}

	return &StateManager{
		stateFile:  stateFile,
		historyDir: historyDir,
	}, nil
}

// InitializeState creates a new upgrade state
// [UPG-011] Initialize state for new upgrade
func (sm *StateManager) InitializeState(clusterName, previousVersion, targetVersion string) *UpgradeState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	state := &UpgradeState{
		UpgradeID:        generateUpgradeID(),
		ClusterName:      clusterName,
		PreviousVersion:  previousVersion,
		TargetVersion:    targetVersion,
		UpgradeStartedAt: time.Now(),
		LastUpdatedAt:    time.Now(),
		OverallStatus:    OverallStatusInProgress,
		CurrentPhase:     PhasePreFlight,
		Phases:           make(map[PhaseName]*PhaseState),
		Nodes:            make(map[string]*NodeState),
		CheckpointCount:  0,
	}

	sm.currentState = state
	return state
}

// LoadState loads state from disk
// [UPG-011] Load checkpoint state for resume
func (sm *StateManager) LoadState() (*UpgradeState, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	data, err := os.ReadFile(sm.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no upgrade state found: %w", err)
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state UpgradeState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	sm.currentState = &state
	return &state, nil
}

// SaveState persists state to disk atomically
// [UPG-011] Atomic checkpoint persistence
func (sm *StateManager) SaveState(state *UpgradeState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	state.mu.Lock()
	state.LastUpdatedAt = time.Now()
	state.LastCheckpoint = time.Now()
	state.CheckpointCount++
	state.mu.Unlock()

	// Marshal to YAML
	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to temporary file first (atomic operation)
	tmpFile := sm.stateFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	// Rename to final location (atomic on POSIX systems)
	if err := os.Rename(tmpFile, sm.stateFile); err != nil {
		return fmt.Errorf("failed to rename temp state file: %w", err)
	}

	sm.currentState = state
	return nil
}

// CreateCheckpoint creates a checkpoint of current state
// [UPG-011] Checkpoint mechanism
func (sm *StateManager) CreateCheckpoint(state *UpgradeState, reason string) error {
	// Save current state
	if err := sm.SaveState(state); err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	// Optionally log checkpoint
	fmt.Printf("✓ Checkpoint #%d created: %s\n", state.CheckpointCount, reason)
	return nil
}

// ArchiveState archives completed upgrade state
// [UPG-011] Archive successful upgrades
func (sm *StateManager) ArchiveState(state *UpgradeState) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Create archive filename with timestamp
	timestamp := time.Now().Format("20060102-150405")
	archiveFile := filepath.Join(sm.historyDir, fmt.Sprintf("upgrade-%s-%s.yaml", state.UpgradeID, timestamp))

	// Marshal state
	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state for archive: %w", err)
	}

	// Write archive
	if err := os.WriteFile(archiveFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write archive: %w", err)
	}

	// Remove current state file
	if err := os.Remove(sm.stateFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove state file: %w", err)
	}

	fmt.Printf("✓ Upgrade state archived: %s\n", archiveFile)
	return nil
}

// GetState returns the current state
func (sm *StateManager) GetState() *UpgradeState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentState
}

// UpdateNodeState updates a node's state and creates checkpoint
// [UPG-011] Node state update with checkpoint
func (state *UpgradeState) UpdateNodeState(hostPort string, status NodeStatus, errorMsg string) {
	state.mu.Lock()
	defer state.mu.Unlock()

	node, exists := state.Nodes[hostPort]
	if !exists {
		node = &NodeState{
			HostPort:        hostPort,
			PreviousVersion: state.PreviousVersion,
			TargetVersion:   state.TargetVersion,
		}
		state.Nodes[hostPort] = node
	}

	node.Status = status
	if status == NodeStatusInProgress {
		node.StartTimestamp = time.Now()
	}
	if status == NodeStatusCompleted || status == NodeStatusFailed || status == NodeStatusSkipped {
		node.CompletionTimestamp = time.Now()
	}
	if errorMsg != "" {
		node.ErrorDetails = errorMsg
	}
	if status == NodeStatusFailed {
		node.RetryCount++
	}

	state.LastUpdatedAt = time.Now()
}

// UpdatePhaseState updates a phase's state
// [UPG-011] Phase state update
func (state *UpgradeState) UpdatePhaseState(phase PhaseName, status PhaseStatus) {
	state.mu.Lock()
	defer state.mu.Unlock()

	phaseState, exists := state.Phases[phase]
	if !exists {
		phaseState = &PhaseState{
			Name:   phase,
			Status: PhaseStatusNotStarted,
		}
		state.Phases[phase] = phaseState
	}

	phaseState.Status = status
	if status == PhaseStatusInProgress {
		phaseState.PhaseStartTimestamp = time.Now()
	}

	state.CurrentPhase = phase
	state.LastUpdatedAt = time.Now()
}

// GetNodesByStatus returns all nodes with given status
func (state *UpgradeState) GetNodesByStatus(status NodeStatus) []*NodeState {
	state.mu.RLock()
	defer state.mu.RUnlock()

	var nodes []*NodeState
	for _, node := range state.Nodes {
		if node.Status == status {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// GetCompletedNodeCount returns count of completed nodes
func (state *UpgradeState) GetCompletedNodeCount() int {
	state.mu.RLock()
	defer state.mu.RUnlock()

	count := 0
	for _, node := range state.Nodes {
		if node.Status == NodeStatusCompleted {
			count++
		}
	}
	return count
}

// GetTotalNodeCount returns total number of nodes
func (state *UpgradeState) GetTotalNodeCount() int {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return len(state.Nodes)
}

// generateUpgradeID generates a unique upgrade ID
func generateUpgradeID() string {
	return fmt.Sprintf("upg-%d", time.Now().Unix())
}
