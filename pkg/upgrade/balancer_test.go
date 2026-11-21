package upgrade

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeBalancer struct {
	enabled    bool
	calls      []string
	enabledErr error
	stopErr    error
}

func (f *fakeBalancer) Enabled(context.Context) (bool, error) {
	f.calls = append(f.calls, "status")
	return f.enabled, f.enabledErr
}

func (f *fakeBalancer) Stop(context.Context) error {
	f.calls = append(f.calls, "stop")
	if f.stopErr != nil {
		return f.stopErr
	}
	f.enabled = false
	return nil
}

func (f *fakeBalancer) Start(context.Context) error {
	f.calls = append(f.calls, "start")
	f.enabled = true
	return nil
}

func newBalancerGuard(ctl *fakeBalancer, state *UpgradeState, hooks *[]HookType) *balancerGuard {
	return &balancerGuard{
		control: ctl,
		state:   state,
		save:    func() error { return nil },
		runHook: func(_ context.Context, hook HookType) error {
			*hooks = append(*hooks, hook)
			return nil
		},
	}
}

// UPGSAFE-008: an enabled balancer is stopped before the upgrade and started after it.
func TestBalancerGuard_StopsAndRestoresEnabledBalancer(t *testing.T) {
	ctl := &fakeBalancer{enabled: true}
	state := &UpgradeState{}
	var hooks []HookType
	guard := newBalancerGuard(ctl, state, &hooks)

	require.NoError(t, guard.pause(context.Background()))
	assert.False(t, ctl.enabled)
	assert.True(t, state.BalancerStoppedByUpgrade)

	require.NoError(t, guard.restore(context.Background()))
	assert.True(t, ctl.enabled)
	assert.False(t, state.BalancerStoppedByUpgrade)
	assert.Equal(t, []HookType{HookBeforeBalancerStop, HookAfterBalancerStart}, hooks)
}

// UPGSAFE-008: a balancer the operator had already disabled is left disabled.
func TestBalancerGuard_LeavesDisabledBalancerAlone(t *testing.T) {
	ctl := &fakeBalancer{enabled: false}
	state := &UpgradeState{}
	var hooks []HookType
	guard := newBalancerGuard(ctl, state, &hooks)

	require.NoError(t, guard.pause(context.Background()))
	require.NoError(t, guard.restore(context.Background()))

	assert.False(t, ctl.enabled)
	assert.NotContains(t, ctl.calls, "stop")
	assert.NotContains(t, ctl.calls, "start")
	assert.Empty(t, hooks)
}

// UPGSAFE-008: a rerun after an interrupted upgrade still restores the balancer.
func TestBalancerGuard_ResumedUpgradeRestoresBalancer(t *testing.T) {
	ctl := &fakeBalancer{enabled: false}
	state := &UpgradeState{BalancerStoppedByUpgrade: true}
	var hooks []HookType
	guard := newBalancerGuard(ctl, state, &hooks)

	require.NoError(t, guard.pause(context.Background()))
	assert.True(t, state.BalancerStoppedByUpgrade)

	require.NoError(t, guard.restore(context.Background()))
	assert.True(t, ctl.enabled)
}

// UPGSAFE-008: the upgrade does not proceed if the balancer cannot be stopped.
func TestBalancerGuard_PauseFailureStopsUpgrade(t *testing.T) {
	var hooks []HookType

	stopFails := newBalancerGuard(&fakeBalancer{enabled: true, stopErr: errors.New("boom")}, &UpgradeState{}, &hooks)
	require.Error(t, stopFails.pause(context.Background()))
	assert.False(t, stopFails.state.BalancerStoppedByUpgrade)

	statusFails := newBalancerGuard(&fakeBalancer{enabledErr: errors.New("boom")}, &UpgradeState{}, &hooks)
	require.Error(t, statusFails.pause(context.Background()))
}
