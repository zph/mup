package upgrade

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/topology"
)

func stateWithNodes(status OverallStatus, nodes map[string]NodeStatus) *UpgradeState {
	state := &UpgradeState{
		OverallStatus: status,
		Phases:        map[PhaseName]*PhaseState{},
		Nodes:         map[string]*NodeState{},
	}
	for hostPort, nodeStatus := range nodes {
		state.Nodes[hostPort] = &NodeState{Status: nodeStatus}
	}
	return state
}

// UPGSAFE-014
func TestCanRollBack(t *testing.T) {
	for _, status := range []OverallStatus{OverallStatusFailed, OverallStatusPaused, OverallStatusInProgress} {
		require.NoError(t, canRollBack(stateWithNodes(status, nil)), "status %s", status)
	}

	err := canRollBack(stateWithNodes(OverallStatusCompleted, nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "completed")

	require.Error(t, canRollBack(stateWithNodes(OverallStatusRolledBack, nil)))

	// Once post-upgrade tasks have begun the FCV may have changed and the old
	// supervisor may be gone; that is a downgrade, not a rollback.
	for _, phaseStatus := range []PhaseStatus{PhaseStatusInProgress, PhaseStatusCompleted, PhaseStatusFailed} {
		state := stateWithNodes(OverallStatusFailed, nil)
		state.Phases[PhasePostUpgrade] = &PhaseState{Status: phaseStatus}
		require.Error(t, canRollBack(state), "post-upgrade %s", phaseStatus)
	}

	notStarted := stateWithNodes(OverallStatusFailed, nil)
	notStarted.Phases[PhasePostUpgrade] = &PhaseState{Status: PhaseStatusNotStarted}
	require.NoError(t, canRollBack(notStarted))
}

// UPGSAFE-014: only nodes the upgrade touched are moved back.
func TestNeedsRollback(t *testing.T) {
	state := stateWithNodes(OverallStatusFailed, map[string]NodeStatus{
		"localhost:1": NodeStatusCompleted,
		"localhost:2": NodeStatusInProgress,
		"localhost:3": NodeStatusFailed,
		"localhost:4": NodeStatusPending,
		"localhost:5": NodeStatusRolledBack,
		"localhost:6": NodeStatusSkipped,
	})

	assert.True(t, needsRollback(state, "localhost:1"))
	assert.True(t, needsRollback(state, "localhost:2"))
	assert.True(t, needsRollback(state, "localhost:3"))
	assert.False(t, needsRollback(state, "localhost:4"))
	assert.False(t, needsRollback(state, "localhost:5"))
	assert.False(t, needsRollback(state, "localhost:6"))
	assert.False(t, needsRollback(state, "localhost:7"), "a node the upgrade never reached")
}

type fakeNodeMover struct {
	calls     []string
	stopErr   error
	startErr  error
	verifyErr error
}

func (f *fakeNodeMover) StopOnNew(_ context.Context, program string) error {
	f.calls = append(f.calls, "stop-new "+program)
	return f.stopErr
}

func (f *fakeNodeMover) StartOnOld(_ context.Context, program string) error {
	f.calls = append(f.calls, "start-old "+program)
	return f.startErr
}

func (f *fakeNodeMover) VerifyVersion(_ context.Context, program, version string) error {
	f.calls = append(f.calls, "verify "+program+" "+version)
	return f.verifyErr
}

// UPGSAFE-014
func TestRollbackNode(t *testing.T) {
	t.Run("stops on new, starts on old, verifies the old version", func(t *testing.T) {
		mover := &fakeNodeMover{}

		require.NoError(t, rollbackNode(context.Background(), mover, "mongod-30000", "7.0.0"))

		assert.Equal(t, []string{"stop-new mongod-30000", "start-old mongod-30000", "verify mongod-30000 7.0.0"}, mover.calls)
	})

	t.Run("does not start the old binary if the new one could not be stopped", func(t *testing.T) {
		mover := &fakeNodeMover{stopErr: errors.New("boom")}

		require.Error(t, rollbackNode(context.Background(), mover, "mongod-30000", "7.0.0"))

		assert.Equal(t, []string{"stop-new mongod-30000"}, mover.calls)
	})

	t.Run("reports a node that came back on the wrong version", func(t *testing.T) {
		mover := &fakeNodeMover{verifyErr: errors.New("node is running version 7.0.48, expected 7.0.0")}

		err := rollbackNode(context.Background(), mover, "mongod-30000", "7.0.0")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "7.0.48")
	})
}

type rollbackHarness struct {
	rs    *fakeReplicaSet
	mover *fakeNodeMover
	state *UpgradeState
	run   *replicaSetRollback
}

func newRollbackHarness(rs *fakeReplicaSet, nodes map[string]NodeStatus) *rollbackHarness {
	h := &rollbackHarness{rs: rs, mover: &fakeNodeMover{}, state: stateWithNodes(OverallStatusFailed, nodes)}
	h.run = &replicaSetRollback{
		probe:         rs,
		mover:         h.mover,
		state:         h.state,
		rsName:        rsTestName,
		fromVersion:   "7.0.0",
		programPrefix: "mongod",
		settleTimeout: time.Minute,
		pollInterval:  time.Millisecond,
		save:          func() error { return nil },
	}
	return h
}

func (h *rollbackHarness) startedOnOld() []string {
	var started []string
	for _, call := range h.mover.calls {
		var program string
		if _, err := fmt.Sscanf(call, "start-old %s", &program); err == nil {
			started = append(started, program)
		}
	}
	return started
}

// UPGSAFE-014: upgraded secondaries go back first, an upgraded primary last
// after a stepdown, and untouched nodes are left alone.
func TestReplicaSetRollback_OrderAndScope(t *testing.T) {
	h := newRollbackHarness(threeMemberSet(), map[string]NodeStatus{
		"localhost:30000": NodeStatusCompleted, // currently PRIMARY
		"localhost:30001": NodeStatusCompleted,
		"localhost:30002": NodeStatusPending,
	})

	err := h.run.execute(context.Background(), rsNodes(rsTestPortA, rsTestPortB, rsTestPortC))

	require.NoError(t, err)
	assert.Equal(t, []string{"mongod-30001", "mongod-30000"}, h.startedOnOld())
	assert.Equal(t, []string{"localhost:30000"}, h.rs.stepDowns)
	assert.Equal(t, NodeStatusRolledBack, h.state.Nodes["localhost:30000"].Status)
	assert.Equal(t, NodeStatusRolledBack, h.state.Nodes["localhost:30001"].Status)
	assert.Equal(t, NodeStatusPending, h.state.Nodes["localhost:30002"].Status)
}

// UPGSAFE-014: the node whose upgrade failed is down, so the set is not
// healthy; it is moved back first instead of waiting for a health that only
// its own rollback can restore.
func TestReplicaSetRollback_RestoresFailedNodeFirst(t *testing.T) {
	rs := threeMemberSet()
	rs.members[2] = member(rsTestPortC, memberStateDown, 0)
	h := newRollbackHarness(rs, map[string]NodeStatus{
		"localhost:30001": NodeStatusCompleted,
		"localhost:30002": NodeStatusFailed,
	})
	// Starting the failed node on the old binary brings it back.
	h.run.afterNode = func(node topology.MongodNode) {
		if node.Port == rsTestPortC {
			rs.members[2] = member(rsTestPortC, memberStateSecondary, 1)
		}
	}

	err := h.run.execute(context.Background(), rsNodes(rsTestPortA, rsTestPortB, rsTestPortC))

	require.NoError(t, err)
	assert.Equal(t, []string{"mongod-30002", "mongod-30001"}, h.startedOnOld())
}

// UPGSAFE-014: nothing touched means nothing to do.
func TestReplicaSetRollback_NothingToDo(t *testing.T) {
	h := newRollbackHarness(threeMemberSet(), map[string]NodeStatus{"localhost:30000": NodeStatusPending})

	require.NoError(t, h.run.execute(context.Background(), rsNodes(rsTestPortA, rsTestPortB, rsTestPortC)))

	assert.Empty(t, h.mover.calls)
	assert.Zero(t, h.rs.statusCalls)
}

// UPGSAFE-014: a failure stops the rollback and leaves the node marked for retry.
func TestReplicaSetRollback_StopsOnFailure(t *testing.T) {
	h := newRollbackHarness(threeMemberSet(), map[string]NodeStatus{
		"localhost:30001": NodeStatusCompleted,
		"localhost:30002": NodeStatusCompleted,
	})
	h.mover.startErr = errors.New("boom")

	err := h.run.execute(context.Background(), rsNodes(rsTestPortA, rsTestPortB, rsTestPortC))

	require.Error(t, err)
	assert.Equal(t, NodeStatusCompleted, h.state.Nodes["localhost:30001"].Status)
	assert.Len(t, h.startedOnOld(), 1)
}
