package upgrade

import (
	"strings"
	"testing"
)

// [UPG-016] Tests for configurable user prompting

func TestParsePromptLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected PromptLevel
		wantErr  bool
	}{
		{"none", PromptLevelNone, false},
		{"phase", PromptLevelPhase, false},
		{"node", PromptLevelNode, false},
		{"critical", PromptLevelCritical, false},
		{"", PromptLevelNone, false}, // Empty string defaults to none
		{"NONE", PromptLevelNone, false}, // Case insensitive
		{"Phase", PromptLevelPhase, false},
		{"invalid", "", true},
		{"all", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParsePromptLevel(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error for input %s, got none", tt.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input %s: %v", tt.input, err)
				}
				if result != tt.expected {
					t.Errorf("Expected %s, got %s", tt.expected, result)
				}
			}
		})
	}
}

func TestPrompter_ShouldPrompt(t *testing.T) {
	state := &UpgradeState{
		Nodes: make(map[string]*NodeState),
	}

	tests := []struct {
		name         string
		level        PromptLevel
		context      PromptContext
		shouldPrompt bool
	}{
		{
			name:  "none-level-never-prompts",
			level: PromptLevelNone,
			context: PromptContext{
				Phase:        PhaseConfigServers,
				NodeHostPort: "localhost:27017",
			},
			shouldPrompt: false,
		},
		{
			name:  "phase-level-prompts-at-phase-boundary",
			level: PromptLevelPhase,
			context: PromptContext{
				Phase:        PhaseConfigServers,
				NodeHostPort: "", // Empty means phase boundary
			},
			shouldPrompt: true,
		},
		{
			name:  "phase-level-no-prompt-for-nodes",
			level: PromptLevelPhase,
			context: PromptContext{
				Phase:        PhaseConfigServers,
				NodeHostPort: "localhost:27017",
			},
			shouldPrompt: false,
		},
		{
			name:  "node-level-prompts-for-every-node",
			level: PromptLevelNode,
			context: PromptContext{
				Phase:        PhaseConfigServers,
				NodeHostPort: "localhost:27017",
			},
			shouldPrompt: true,
		},
		{
			name:  "node-level-no-prompt-at-phase-boundary",
			level: PromptLevelNode,
			context: PromptContext{
				Phase:        PhaseConfigServers,
				NodeHostPort: "",
			},
			shouldPrompt: false,
		},
		{
			name:  "critical-level-prompts-for-critical-ops",
			level: PromptLevelCritical,
			context: PromptContext{
				Phase:      PhaseConfigServers,
				IsCritical: true,
			},
			shouldPrompt: true,
		},
		{
			name:  "critical-level-no-prompt-for-normal-ops",
			level: PromptLevelCritical,
			context: PromptContext{
				Phase:        PhaseConfigServers,
				NodeHostPort: "localhost:27017",
				IsCritical:   false,
			},
			shouldPrompt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompter := NewPrompter(tt.level, state)
			result := prompter.ShouldPrompt(tt.context)
			if result != tt.shouldPrompt {
				t.Errorf("Expected ShouldPrompt to return %v, got %v", tt.shouldPrompt, result)
			}
		})
	}
}

func TestPrompter_PromptForPhase(t *testing.T) {
	state := &UpgradeState{
		Nodes:           make(map[string]*NodeState),
		PreviousVersion: "mongo-6.0.15",
		TargetVersion:   "mongo-7.0.0",
		CurrentPhase:    PhaseConfigServers,
	}

	state.UpdateNodeState("localhost:27017", NodeStatusCompleted, "")
	state.UpdateNodeState("localhost:27018", NodeStatusPending, "")

	// Test with none level - should not prompt
	prompter := NewPrompter(PromptLevelNone, state)
	response, err := prompter.PromptForPhase(PhaseMongos, state)
	if err != nil {
		t.Fatalf("PromptForPhase failed: %v", err)
	}
	if response != PromptResponseContinue {
		t.Errorf("Expected continue response, got %s", response)
	}
}

func TestPrompter_PromptForNode(t *testing.T) {
	state := &UpgradeState{
		Nodes:           make(map[string]*NodeState),
		PreviousVersion: "mongo-6.0.15",
		TargetVersion:   "mongo-7.0.0",
		CurrentPhase:    PhaseConfigServers,
	}

	// Test with none level - should not prompt
	prompter := NewPrompter(PromptLevelNone, state)
	response, err := prompter.PromptForNode("localhost:27017", "SECONDARY", state)
	if err != nil {
		t.Fatalf("PromptForNode failed: %v", err)
	}
	if response != PromptResponseContinue {
		t.Errorf("Expected continue response, got %s", response)
	}
}

func TestPrompter_PromptForCriticalOperation(t *testing.T) {
	state := &UpgradeState{
		Nodes:           make(map[string]*NodeState),
		PreviousVersion: "mongo-6.0.15",
		TargetVersion:   "mongo-7.0.0",
		CurrentPhase:    PhaseConfigServers,
	}

	// Test with none level - should not prompt
	prompter := NewPrompter(PromptLevelNone, state)
	response, err := prompter.PromptForCriticalOperation("Primary Stepdown", state)
	if err != nil {
		t.Fatalf("PromptForCriticalOperation failed: %v", err)
	}
	if response != PromptResponseContinue {
		t.Errorf("Expected continue response, got %s", response)
	}
}

func TestPrompter_DisplayStatus(t *testing.T) {
	state := &UpgradeState{
		ClusterName:     "test-cluster",
		PreviousVersion: "mongo-6.0.15",
		TargetVersion:   "mongo-7.0.0",
		OverallStatus:   OverallStatusInProgress,
		CurrentPhase:    PhaseConfigServers,
		Nodes:           make(map[string]*NodeState),
		CheckpointCount: 3,
	}

	state.UpdateNodeState("localhost:27017", NodeStatusCompleted, "")
	state.UpdateNodeState("localhost:27018", NodeStatusInProgress, "")
	state.UpdateNodeState("localhost:27019", NodeStatusPending, "")

	prompter := NewPrompter(PromptLevelNone, state)

	// This is mainly a smoke test - we can't easily test terminal output
	// Just ensure it doesn't panic
	prompter.DisplayStatus(state)

	// Test with nil state
	prompter.DisplayStatus(nil)
}

func TestFormatHelpers(t *testing.T) {
	// Test center
	if center("test", 10) != "   test   " {
		t.Errorf("center() failed: got %q", center("test", 10))
	}

	// Test pad
	if pad("test", 10) != "test      " {
		t.Errorf("pad() failed: got %q", pad("test", 10))
	}

	// Test formatPhaseName
	if formatPhaseName(PhasePreFlight) != "Pre-Flight Validation" {
		t.Errorf("formatPhaseName() failed")
	}

	// Test formatDuration
	if !strings.Contains(formatDuration(30*60*1000000000), "30m") {
		t.Errorf("formatDuration() failed for 30 minutes")
	}
}

func TestPromptContext_Creation(t *testing.T) {
	context := PromptContext{
		Phase:            PhaseConfigServers,
		NodeHostPort:     "localhost:27017",
		NodeRole:         "PRIMARY",
		CompletedNodes:   3,
		TotalNodes:       6,
		EstimatedTimeMin: 15,
		IsCritical:       false,
		OperationName:    "",
	}

	if context.Phase != PhaseConfigServers {
		t.Errorf("Phase mismatch")
	}
	if context.NodeHostPort != "localhost:27017" {
		t.Errorf("NodeHostPort mismatch")
	}
	if context.CompletedNodes != 3 {
		t.Errorf("CompletedNodes mismatch")
	}
}

func TestPromptResponse_Types(t *testing.T) {
	responses := []PromptResponse{
		PromptResponseContinue,
		PromptResponseSkip,
		PromptResponsePause,
		PromptResponseAbort,
		PromptResponseHealthCheck,
		PromptResponseViewStatus,
	}

	if len(responses) != 6 {
		t.Errorf("Expected 6 response types, got %d", len(responses))
	}

	// Ensure they're distinct
	seen := make(map[PromptResponse]bool)
	for _, r := range responses {
		if seen[r] {
			t.Errorf("Duplicate response type: %s", r)
		}
		seen[r] = true
	}
}

func TestPromptLevel_Types(t *testing.T) {
	levels := []PromptLevel{
		PromptLevelNone,
		PromptLevelPhase,
		PromptLevelNode,
		PromptLevelCritical,
	}

	if len(levels) != 4 {
		t.Errorf("Expected 4 prompt levels, got %d", len(levels))
	}

	// Ensure they're distinct
	seen := make(map[PromptLevel]bool)
	for _, l := range levels {
		if seen[l] {
			t.Errorf("Duplicate prompt level: %s", l)
		}
		seen[l] = true
	}
}
