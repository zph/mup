package upgrade

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// [UPG-011] Tests for upgrade state tracking and checkpointing

func TestStateManager_InitializeState(t *testing.T) {
	tmpDir := t.TempDir()
	sm, err := NewStateManager("test-cluster", tmpDir)
	if err != nil {
		t.Fatalf("Failed to create state manager: %v", err)
	}

	state := sm.InitializeState("test-cluster", "mongo-6.0.15", "mongo-7.0.0")

	// Verify state fields
	if state.ClusterName != "test-cluster" {
		t.Errorf("Expected cluster name 'test-cluster', got %s", state.ClusterName)
	}
	if state.PreviousVersion != "mongo-6.0.15" {
		t.Errorf("Expected previous version 'mongo-6.0.15', got %s", state.PreviousVersion)
	}
	if state.TargetVersion != "mongo-7.0.0" {
		t.Errorf("Expected target version 'mongo-7.0.0', got %s", state.TargetVersion)
	}
	if state.OverallStatus != OverallStatusInProgress {
		t.Errorf("Expected status in_progress, got %s", state.OverallStatus)
	}
	if state.CurrentPhase != PhasePreFlight {
		t.Errorf("Expected phase pre-flight, got %s", state.CurrentPhase)
	}
	if state.Nodes == nil {
		t.Error("Nodes map should be initialized")
	}
	if state.Phases == nil {
		t.Error("Phases map should be initialized")
	}
}

func TestStateManager_SaveAndLoadState(t *testing.T) {
	tmpDir := t.TempDir()
	sm, err := NewStateManager("test-cluster", tmpDir)
	if err != nil {
		t.Fatalf("Failed to create state manager: %v", err)
	}

	// Initialize and save state
	state := sm.InitializeState("test-cluster", "mongo-6.0.15", "mongo-7.0.0")
	state.UpdateNodeState("localhost:27017", NodeStatusPending, "")
	state.UpdatePhaseState(PhaseConfigServers, PhaseStatusInProgress)

	if err := sm.SaveState(state); err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Verify state file exists
	stateFile := filepath.Join(tmpDir, "upgrade-state.yaml")
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		t.Fatal("State file was not created")
	}

	// Load state
	sm2, err := NewStateManager("test-cluster", tmpDir)
	if err != nil {
		t.Fatalf("Failed to create second state manager: %v", err)
	}

	loadedState, err := sm2.LoadState()
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}

	// Verify loaded state
	if loadedState.ClusterName != state.ClusterName {
		t.Errorf("Loaded cluster name mismatch: expected %s, got %s", state.ClusterName, loadedState.ClusterName)
	}
	if loadedState.PreviousVersion != state.PreviousVersion {
		t.Errorf("Loaded previous version mismatch")
	}
	if loadedState.CurrentPhase != PhaseConfigServers {
		t.Errorf("Expected phase config-servers, got %s", loadedState.CurrentPhase)
	}

	// Verify node state
	node, exists := loadedState.Nodes["localhost:27017"]
	if !exists {
		t.Fatal("Node localhost:27017 not found in loaded state")
	}
	if node.Status != NodeStatusPending {
		t.Errorf("Expected node status pending, got %s", node.Status)
	}
}

func TestStateManager_CreateCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	sm, err := NewStateManager("test-cluster", tmpDir)
	if err != nil {
		t.Fatalf("Failed to create state manager: %v", err)
	}

	state := sm.InitializeState("test-cluster", "mongo-6.0.15", "mongo-7.0.0")

	// Create first checkpoint
	if err := sm.CreateCheckpoint(state, "after node 1"); err != nil {
		t.Fatalf("Failed to create checkpoint: %v", err)
	}

	if state.CheckpointCount != 1 {
		t.Errorf("Expected checkpoint count 1, got %d", state.CheckpointCount)
	}

	// Create second checkpoint
	if err := sm.CreateCheckpoint(state, "after node 2"); err != nil {
		t.Fatalf("Failed to create second checkpoint: %v", err)
	}

	if state.CheckpointCount != 2 {
		t.Errorf("Expected checkpoint count 2, got %d", state.CheckpointCount)
	}
}

func TestStateManager_ArchiveState(t *testing.T) {
	tmpDir := t.TempDir()
	sm, err := NewStateManager("test-cluster", tmpDir)
	if err != nil {
		t.Fatalf("Failed to create state manager: %v", err)
	}

	state := sm.InitializeState("test-cluster", "mongo-6.0.15", "mongo-7.0.0")
	state.OverallStatus = OverallStatusCompleted

	if err := sm.SaveState(state); err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Archive state
	if err := sm.ArchiveState(state); err != nil {
		t.Fatalf("Failed to archive state: %v", err)
	}

	// Verify current state file is removed
	stateFile := filepath.Join(tmpDir, "upgrade-state.yaml")
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Error("State file should be removed after archiving")
	}

	// Verify archive file exists
	historyDir := filepath.Join(tmpDir, "upgrade-history")
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		t.Fatalf("Failed to read history directory: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("Expected 1 archive file, found %d", len(entries))
	}
}

func TestUpgradeState_UpdateNodeState(t *testing.T) {
	state := &UpgradeState{
		Nodes:           make(map[string]*NodeState),
		PreviousVersion: "mongo-6.0.15",
		TargetVersion:   "mongo-7.0.0",
	}

	// Update node to in_progress
	state.UpdateNodeState("localhost:27017", NodeStatusInProgress, "")

	node := state.Nodes["localhost:27017"]
	if node == nil {
		t.Fatal("Node not created")
	}
	if node.Status != NodeStatusInProgress {
		t.Errorf("Expected status in_progress, got %s", node.Status)
	}
	if node.StartTimestamp.IsZero() {
		t.Error("Start timestamp should be set")
	}

	// Update node to completed
	time.Sleep(10 * time.Millisecond) // Small delay to ensure different timestamps
	state.UpdateNodeState("localhost:27017", NodeStatusCompleted, "")

	if node.Status != NodeStatusCompleted {
		t.Errorf("Expected status completed, got %s", node.Status)
	}
	if node.CompletionTimestamp.IsZero() {
		t.Error("Completion timestamp should be set")
	}
	if node.CompletionTimestamp.Before(node.StartTimestamp) {
		t.Error("Completion timestamp should be after start timestamp")
	}

	// Update node to failed with error
	state.UpdateNodeState("localhost:27018", NodeStatusFailed, "connection timeout")

	failedNode := state.Nodes["localhost:27018"]
	if failedNode.ErrorDetails != "connection timeout" {
		t.Errorf("Expected error details 'connection timeout', got %s", failedNode.ErrorDetails)
	}
	if failedNode.RetryCount != 1 {
		t.Errorf("Expected retry count 1, got %d", failedNode.RetryCount)
	}
}

func TestUpgradeState_UpdatePhaseState(t *testing.T) {
	state := &UpgradeState{
		Phases: make(map[PhaseName]*PhaseState),
	}

	// Update phase to in_progress
	state.UpdatePhaseState(PhaseConfigServers, PhaseStatusInProgress)

	phase := state.Phases[PhaseConfigServers]
	if phase == nil {
		t.Fatal("Phase not created")
	}
	if phase.Status != PhaseStatusInProgress {
		t.Errorf("Expected status in_progress, got %s", phase.Status)
	}
	if phase.PhaseStartTimestamp.IsZero() {
		t.Error("Phase start timestamp should be set")
	}
	if state.CurrentPhase != PhaseConfigServers {
		t.Errorf("Expected current phase config-servers, got %s", state.CurrentPhase)
	}
}

func TestUpgradeState_GetNodesByStatus(t *testing.T) {
	state := &UpgradeState{
		Nodes:           make(map[string]*NodeState),
		PreviousVersion: "mongo-6.0.15",
		TargetVersion:   "mongo-7.0.0",
	}

	// Add nodes with different statuses
	state.UpdateNodeState("localhost:27017", NodeStatusCompleted, "")
	state.UpdateNodeState("localhost:27018", NodeStatusInProgress, "")
	state.UpdateNodeState("localhost:27019", NodeStatusCompleted, "")
	state.UpdateNodeState("localhost:27020", NodeStatusPending, "")

	// Get completed nodes
	completed := state.GetNodesByStatus(NodeStatusCompleted)
	if len(completed) != 2 {
		t.Errorf("Expected 2 completed nodes, got %d", len(completed))
	}

	// Get in progress nodes
	inProgress := state.GetNodesByStatus(NodeStatusInProgress)
	if len(inProgress) != 1 {
		t.Errorf("Expected 1 in_progress node, got %d", len(inProgress))
	}

	// Get pending nodes
	pending := state.GetNodesByStatus(NodeStatusPending)
	if len(pending) != 1 {
		t.Errorf("Expected 1 pending node, got %d", len(pending))
	}
}

func TestUpgradeState_GetCompletedNodeCount(t *testing.T) {
	state := &UpgradeState{
		Nodes:           make(map[string]*NodeState),
		PreviousVersion: "mongo-6.0.15",
		TargetVersion:   "mongo-7.0.0",
	}

	if state.GetCompletedNodeCount() != 0 {
		t.Error("Expected 0 completed nodes initially")
	}

	state.UpdateNodeState("localhost:27017", NodeStatusCompleted, "")
	state.UpdateNodeState("localhost:27018", NodeStatusInProgress, "")
	state.UpdateNodeState("localhost:27019", NodeStatusCompleted, "")

	if state.GetCompletedNodeCount() != 2 {
		t.Errorf("Expected 2 completed nodes, got %d", state.GetCompletedNodeCount())
	}

	if state.GetTotalNodeCount() != 3 {
		t.Errorf("Expected 3 total nodes, got %d", state.GetTotalNodeCount())
	}
}

func TestStateManager_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	sm, err := NewStateManager("test-cluster", tmpDir)
	if err != nil {
		t.Fatalf("Failed to create state manager: %v", err)
	}

	state := sm.InitializeState("test-cluster", "mongo-6.0.15", "mongo-7.0.0")

	// Save state
	if err := sm.SaveState(state); err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Verify temp file doesn't exist (should be cleaned up)
	tmpFile := filepath.Join(tmpDir, "upgrade-state.yaml.tmp")
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("Temp file should not exist after save")
	}

	// Verify final file exists
	stateFile := filepath.Join(tmpDir, "upgrade-state.yaml")
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		t.Error("State file should exist")
	}
}

func TestUpgradeState_PauseResume(t *testing.T) {
	state := &UpgradeState{
		Nodes:           make(map[string]*NodeState),
		PreviousVersion: "mongo-6.0.15",
		TargetVersion:   "mongo-7.0.0",
		OverallStatus:   OverallStatusInProgress,
	}

	// Pause upgrade
	state.mu.Lock()
	state.OverallStatus = OverallStatusPaused
	state.PausedAt = time.Now()
	state.PausedReason = "User requested pause"
	state.UserPauseRequested = true
	state.mu.Unlock()

	if state.OverallStatus != OverallStatusPaused {
		t.Errorf("Expected status paused, got %s", state.OverallStatus)
	}
	if state.PausedAt.IsZero() {
		t.Error("PausedAt should be set")
	}
	if !state.UserPauseRequested {
		t.Error("UserPauseRequested should be true")
	}

	// Resume upgrade
	state.mu.Lock()
	state.OverallStatus = OverallStatusInProgress
	state.UserPauseRequested = false
	state.mu.Unlock()

	if state.OverallStatus != OverallStatusInProgress {
		t.Errorf("Expected status in_progress after resume, got %s", state.OverallStatus)
	}
}

func TestUpgradeState_SkippedNodes(t *testing.T) {
	state := &UpgradeState{
		Nodes:           make(map[string]*NodeState),
		PreviousVersion: "mongo-6.0.15",
		TargetVersion:   "mongo-7.0.0",
		SkippedNodes:    []string{},
	}

	// Skip a node
	state.UpdateNodeState("localhost:27017", NodeStatusSkipped, "User skipped")
	state.mu.Lock()
	state.SkippedNodes = append(state.SkippedNodes, "localhost:27017")
	state.mu.Unlock()

	if len(state.SkippedNodes) != 1 {
		t.Errorf("Expected 1 skipped node, got %d", len(state.SkippedNodes))
	}

	node := state.Nodes["localhost:27017"]
	if node.Status != NodeStatusSkipped {
		t.Errorf("Expected node status skipped, got %s", node.Status)
	}
}
