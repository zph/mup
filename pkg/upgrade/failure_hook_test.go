package upgrade

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const failureTestCluster = "failure-hook-test"

func newFailureTestUpgrader(t *testing.T, hook func(context.Context, HookContext) error) *Upgrader {
	t.Helper()
	stateManager, err := NewStateManager(failureTestCluster, t.TempDir())
	require.NoError(t, err)
	state := stateManager.InitializeState(failureTestCluster, "mongo-7.0.0", "mongo-7.0.48")

	registry := NewHookRegistry()
	registry.Register(NewFunctionHook("record", HookOnUpgradeFailure, hook))

	return &Upgrader{
		config: UpgradeConfig{
			ClusterName:  failureTestCluster,
			FromVersion:  "7.0.0",
			ToVersion:    "7.0.48",
			StateManager: stateManager,
			HookRegistry: registry,
		},
		state: state,
	}
}

// UPGSAFE-011: any failure runs the on-upgrade-failure hook and is recorded.
func TestHandleUpgradeFailure_RunsHookAndRecordsFailure(t *testing.T) {
	var seen []HookContext
	var hookCtxErr error
	upgrader := newFailureTestUpgrader(t, func(ctx context.Context, hookCtx HookContext) error {
		seen = append(seen, hookCtx)
		hookCtxErr = ctx.Err()
		return nil
	})

	// The upgrade's own context is already cancelled, as after a timeout.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cause := errors.New("shard upgrade failed")

	upgrader.handleUpgradeFailure(ctx, cause)

	require.Len(t, seen, 1)
	assert.Equal(t, cause, seen[0].Error)
	assert.Equal(t, failureTestCluster, seen[0].ClusterName)
	assert.NoError(t, hookCtxErr, "the failure hook must not inherit a cancelled context")

	saved, err := upgrader.config.StateManager.LoadState()
	require.NoError(t, err)
	assert.Equal(t, OverallStatusFailed, saved.OverallStatus)
}

// UPGSAFE-011: a pause is not a failure.
func TestHandleUpgradeFailure_PauseIsNotAFailure(t *testing.T) {
	var calls int
	upgrader := newFailureTestUpgrader(t, func(context.Context, HookContext) error {
		calls++
		return nil
	})
	upgrader.state.OverallStatus = OverallStatusPaused

	upgrader.handleUpgradeFailure(context.Background(), errors.New("upgrade paused"))

	assert.Zero(t, calls)
	assert.Equal(t, OverallStatusPaused, upgrader.state.OverallStatus)
}
