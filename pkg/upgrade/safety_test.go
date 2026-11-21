package upgrade

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func member(port int, state memberState, health int32) ReplicaSetMember {
	return ReplicaSetMember{
		Host:      "localhost",
		Port:      port,
		StateNum:  int32(state),
		StateStr:  state.String(),
		Health:    health,
		IsPrimary: state == memberStatePrimary,
	}
}

// UPGSAFE-001
func TestEvaluateReplicaSetHealth(t *testing.T) {
	tests := []struct {
		name    string
		members []ReplicaSetMember
		wantErr string
	}{
		{
			name:    "primary and secondaries",
			members: []ReplicaSetMember{member(1, memberStatePrimary, 1), member(2, memberStateSecondary, 1), member(3, memberStateSecondary, 1)},
		},
		{
			name:    "primary secondary arbiter",
			members: []ReplicaSetMember{member(1, memberStatePrimary, 1), member(2, memberStateSecondary, 1), member(3, memberStateArbiter, 1)},
		},
		{
			name:    "no primary",
			members: []ReplicaSetMember{member(1, memberStateSecondary, 1), member(2, memberStateSecondary, 1)},
			wantErr: "no primary",
		},
		{
			name:    "recovering member",
			members: []ReplicaSetMember{member(1, memberStatePrimary, 1), member(2, memberStateRecovering, 1)},
			wantErr: "localhost:2",
		},
		{
			name:    "unreachable member",
			members: []ReplicaSetMember{member(1, memberStatePrimary, 1), member(2, memberStateSecondary, 0)},
			wantErr: "localhost:2",
		},
		{
			name:    "no members",
			wantErr: "no members",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := evaluateReplicaSetHealth(tt.members)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// UPGSAFE-002: with prompts disabled the failover confirmation is not read
// from stdin, so unattended upgrades reach the primary.
func TestPromptForFailover_NoPromptLevelNeverReadsInput(t *testing.T) {
	prompter := &Prompter{level: PromptLevelNone, reader: bufio.NewReader(strings.NewReader(""))}

	response, err := prompter.PromptForFailover("localhost:1", "rs0", &UpgradeState{})

	require.NoError(t, err)
	assert.Equal(t, PromptResponseContinue, response)
}

// UPGSAFE-002: other levels still require an explicit answer.
func TestPromptForFailover_InteractiveLevelsRequireAnswer(t *testing.T) {
	prompter := &Prompter{level: PromptLevelPhase, reader: bufio.NewReader(strings.NewReader("no\n"))}

	response, err := prompter.PromptForFailover("localhost:1", "rs0", &UpgradeState{})

	require.NoError(t, err)
	assert.Equal(t, PromptResponseAbort, response)
}

// UPGSAFE-003
func TestCriticalPromptAction(t *testing.T) {
	tests := []struct {
		response PromptResponse
		want     criticalAction
	}{
		{PromptResponseContinue, criticalProceed},
		{PromptResponsePause, criticalPause},
		{PromptResponseAbort, criticalAbort},
		{PromptResponseSkip, criticalAbort},
		{PromptResponse("unexpected"), criticalAbort},
	}
	for _, tt := range tests {
		t.Run(string(tt.response), func(t *testing.T) {
			assert.Equal(t, tt.want, criticalPromptAction(tt.response))
		})
	}
}

// UPGSAFE-004
func TestEnsureNoSkippedNodes(t *testing.T) {
	require.NoError(t, ensureNoSkippedNodes(&UpgradeState{}))

	err := ensureNoSkippedNodes(&UpgradeState{SkippedNodes: []string{"localhost:2", "localhost:3"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "localhost:2")
	assert.Contains(t, err.Error(), "localhost:3")
}
