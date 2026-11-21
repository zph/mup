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

const (
	rsTestName  = "rs0"
	rsTestPortA = 30000
	rsTestPortB = 30001
	rsTestPortC = 30002
)

func rsNodes(ports ...int) []topology.MongodNode {
	nodes := make([]topology.MongodNode, 0, len(ports))
	for _, port := range ports {
		nodes = append(nodes, topology.MongodNode{Host: "localhost", Port: port, ReplicaSet: rsTestName})
	}
	return nodes
}

// UPGSAFE-005
func TestNextNodeToUpgrade(t *testing.T) {
	healthy := []ReplicaSetMember{
		member(rsTestPortA, memberStatePrimary, 1),
		member(rsTestPortB, memberStateSecondary, 1),
		member(rsTestPortC, memberStateArbiter, 1),
	}

	t.Run("prefers a non-primary", func(t *testing.T) {
		choice, err := nextNodeToUpgrade(rsNodes(rsTestPortA, rsTestPortB, rsTestPortC), healthy)

		require.NoError(t, err)
		assert.Equal(t, 1, choice.index)
		assert.Equal(t, "SECONDARY", choice.role)
		assert.False(t, choice.needsStepDown)
	})

	t.Run("primary goes last and needs a stepdown", func(t *testing.T) {
		choice, err := nextNodeToUpgrade(rsNodes(rsTestPortA), healthy)

		require.NoError(t, err)
		assert.Equal(t, 0, choice.index)
		assert.Equal(t, "PRIMARY", choice.role)
		assert.True(t, choice.needsStepDown)
	})

	t.Run("matches members by port when host names differ", func(t *testing.T) {
		members := []ReplicaSetMember{
			{Host: "127.0.0.1", Port: rsTestPortA, StateNum: int32(memberStatePrimary), StateStr: "PRIMARY", Health: 1, IsPrimary: true},
			{Host: "127.0.0.1", Port: rsTestPortB, StateNum: int32(memberStateSecondary), StateStr: "SECONDARY", Health: 1},
		}

		choice, err := nextNodeToUpgrade(rsNodes(rsTestPortA, rsTestPortB), members)

		require.NoError(t, err)
		assert.Equal(t, 1, choice.index)
	})

	t.Run("single-member set restarts without a stepdown", func(t *testing.T) {
		choice, err := nextNodeToUpgrade(rsNodes(rsTestPortA), []ReplicaSetMember{member(rsTestPortA, memberStatePrimary, 1)})

		require.NoError(t, err)
		assert.False(t, choice.needsStepDown)
	})

	t.Run("fails closed when a node is not a member", func(t *testing.T) {
		_, err := nextNodeToUpgrade(rsNodes(rsTestPortA, 39999), healthy)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "39999")
	})
}

// UPGSAFE-006
func TestValidateFailover(t *testing.T) {
	require.NoError(t, validateFailover(&FailoverEvent{OldPrimary: "localhost:1", NewPrimary: "localhost:2"}))
	require.Error(t, validateFailover(&FailoverEvent{OldPrimary: "localhost:1", NewPrimary: "localhost:1"}))
	require.Error(t, validateFailover(&FailoverEvent{OldPrimary: "localhost:1"}))
	require.Error(t, validateFailover(nil))

	// The replica set may register a member under a different host name than
	// the topology uses; the same port is the same local node.
	require.Error(t, validateFailover(&FailoverEvent{OldPrimary: "localhost:38100", NewPrimary: "127.0.0.1:38100"}))
	require.NoError(t, validateFailover(&FailoverEvent{OldPrimary: "localhost:38100", NewPrimary: "127.0.0.1:38101"}))
}

func TestSameLocalNode(t *testing.T) {
	assert.True(t, sameLocalNode("localhost:1", "localhost:1"))
	assert.True(t, sameLocalNode("localhost:1", "127.0.0.1:1"))
	assert.True(t, sameLocalNode("[::1]:1", "localhost:1"))
	assert.False(t, sameLocalNode("localhost:1", "localhost:2"))
	assert.False(t, sameLocalNode("localhost", "localhost:2"))
	assert.False(t, sameLocalNode("", ""))
}

// UPGSAFE-007
func TestVersionMatches(t *testing.T) {
	tests := []struct {
		actual, expected string
		want             bool
	}{
		{"7.0.14", "7.0.14", true},
		{"7.0.14", "7.0", true},
		{"7.0.5-4", "7.0.5-4", true},
		{"7.0.5-4", "7.0.5", true},
		{"7.0.14", "7.0.1", false},
		{"6.0.15", "7.0", false},
		{"17.0.1", "7.0", false},
		{"", "7.0", false},
		{"7.0.14", "", false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_vs_%s", tt.actual, tt.expected), func(t *testing.T) {
			assert.Equal(t, tt.want, versionMatches(tt.actual, tt.expected))
		})
	}
}

// fakeReplicaSet simulates a replica set whose members are restarted by the
// upgrader. Each Status call returns the next scripted snapshot, then keeps
// returning the last one.
type fakeReplicaSet struct {
	members      []ReplicaSetMember
	statusErr    error
	unsettled    int // Status calls that report a RECOVERING member first
	statusCalls  int
	stepDowns    []string
	stepDownErr  error
	stepDownNoop bool
}

func (f *fakeReplicaSet) Status(context.Context, string, []string) ([]ReplicaSetMember, error) {
	f.statusCalls++
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	snapshot := append([]ReplicaSetMember(nil), f.members...)
	if f.unsettled > 0 {
		f.unsettled--
		for i := range snapshot {
			if !snapshot[i].IsPrimary {
				snapshot[i].StateNum = int32(memberStateRecovering)
				snapshot[i].StateStr = memberStateRecovering.String()
				break
			}
		}
	}
	return snapshot, nil
}

func (f *fakeReplicaSet) StepDown(_ context.Context, primaryHost, _ string, _ []string) (*FailoverEvent, error) {
	f.stepDowns = append(f.stepDowns, primaryHost)
	if f.stepDownErr != nil {
		return nil, f.stepDownErr
	}
	if f.stepDownNoop {
		return &FailoverEvent{OldPrimary: primaryHost, NewPrimary: primaryHost}, nil
	}

	var newPrimary string
	for i := range f.members {
		switch {
		case f.members[i].Address() == primaryHost:
			f.members[i].StateNum, f.members[i].StateStr, f.members[i].IsPrimary = int32(memberStateSecondary), "SECONDARY", false
		case newPrimary == "" && memberState(f.members[i].StateNum) == memberStateSecondary:
			f.members[i].StateNum, f.members[i].StateStr, f.members[i].IsPrimary = int32(memberStatePrimary), "PRIMARY", true
			newPrimary = f.members[i].Address()
		}
	}
	return &FailoverEvent{OldPrimary: primaryHost, NewPrimary: newPrimary}, nil
}

type rsUpgradeHarness struct {
	rs       *fakeReplicaSet
	upgraded []string
	roles    map[string]string
	failOn   string
	run      *replicaSetUpgrade
}

func newRSUpgradeHarness(rs *fakeReplicaSet) *rsUpgradeHarness {
	h := &rsUpgradeHarness{rs: rs, roles: map[string]string{}}
	h.run = &replicaSetUpgrade{
		probe:         rs,
		rsName:        rsTestName,
		settleTimeout: time.Minute,
		pollInterval:  time.Millisecond,
		isDone:        func(string) bool { return false },
		upgradeNode: func(_ context.Context, node topology.MongodNode, role string, needsStepDown bool) error {
			hostPort := fmt.Sprintf("%s:%d", node.Host, node.Port)
			if hostPort == h.failOn {
				return errors.New("node upgrade failed")
			}
			if needsStepDown {
				event, err := rs.StepDown(context.Background(), hostPort, rsTestName, nil)
				if err != nil {
					return err
				}
				if err := validateFailover(event); err != nil {
					return err
				}
			}
			h.upgraded = append(h.upgraded, hostPort)
			h.roles[hostPort] = role
			return nil
		},
	}
	return h
}

func threeMemberSet() *fakeReplicaSet {
	return &fakeReplicaSet{members: []ReplicaSetMember{
		member(rsTestPortA, memberStatePrimary, 1),
		member(rsTestPortB, memberStateSecondary, 1),
		member(rsTestPortC, memberStateSecondary, 1),
	}}
}

// UPGSAFE-005: secondaries first, the primary last after a stepdown.
func TestReplicaSetUpgrade_SecondariesThenPrimary(t *testing.T) {
	h := newRSUpgradeHarness(threeMemberSet())

	err := h.run.execute(context.Background(), rsNodes(rsTestPortA, rsTestPortB, rsTestPortC))

	require.NoError(t, err)
	assert.Equal(t, []string{"localhost:30001", "localhost:30002", "localhost:30000"}, h.upgraded)
	assert.Equal(t, []string{"localhost:30000"}, h.rs.stepDowns)
	assert.Equal(t, "PRIMARY", h.roles["localhost:30000"])
}

// UPGSAFE-005: a node is only taken down once every member has settled.
func TestReplicaSetUpgrade_WaitsForSetToSettle(t *testing.T) {
	rs := threeMemberSet()
	rs.unsettled = 3
	h := newRSUpgradeHarness(rs)

	err := h.run.execute(context.Background(), rsNodes(rsTestPortB))

	require.NoError(t, err)
	assert.Equal(t, []string{"localhost:30001"}, h.upgraded)
	assert.Greater(t, rs.statusCalls, 3)
}

// UPGSAFE-005: an unsettled set stops the upgrade rather than proceeding.
func TestReplicaSetUpgrade_TimesOutWhenSetNeverSettles(t *testing.T) {
	rs := threeMemberSet()
	rs.members[2] = member(rsTestPortC, memberStateRecovering, 1)
	h := newRSUpgradeHarness(rs)
	h.run.settleTimeout = time.Millisecond

	err := h.run.execute(context.Background(), rsNodes(rsTestPortB))

	require.Error(t, err)
	assert.Empty(t, h.upgraded)
	assert.Contains(t, err.Error(), "localhost:30002")
}

// UPGSAFE-005: role detection failure stops the upgrade; it never assumes SECONDARY.
func TestReplicaSetUpgrade_FailsClosedWhenStatusUnavailable(t *testing.T) {
	rs := threeMemberSet()
	rs.statusErr = errors.New("connection refused")
	h := newRSUpgradeHarness(rs)
	h.run.settleTimeout = time.Millisecond

	err := h.run.execute(context.Background(), rsNodes(rsTestPortA, rsTestPortB))

	require.Error(t, err)
	assert.Empty(t, h.upgraded)
	assert.Empty(t, rs.stepDowns)
}

// UPGSAFE-005: roles are re-read before each node, so a primary that moved
// during the upgrade is still upgraded last, after a stepdown.
func TestReplicaSetUpgrade_FollowsPrimaryAfterElection(t *testing.T) {
	rs := threeMemberSet()
	h := newRSUpgradeHarness(rs)
	inner := h.run.upgradeNode
	h.run.upgradeNode = func(ctx context.Context, node topology.MongodNode, role string, needsStepDown bool) error {
		if err := inner(ctx, node, role, needsStepDown); err != nil {
			return err
		}
		if node.Port == rsTestPortB {
			// An election moves the primary to the node just upgraded.
			rs.members[0] = member(rsTestPortA, memberStateSecondary, 1)
			rs.members[1] = member(rsTestPortB, memberStatePrimary, 1)
		}
		return nil
	}

	err := h.run.execute(context.Background(), rsNodes(rsTestPortA, rsTestPortB, rsTestPortC))

	require.NoError(t, err)
	assert.Equal(t, []string{"localhost:30001", "localhost:30000", "localhost:30002"}, h.upgraded)
	assert.Empty(t, rs.stepDowns, "the only remaining primary was already upgraded")
}

// UPGSAFE-006: a stepdown that leaves the node primary stops the upgrade.
func TestReplicaSetUpgrade_StopsWhenStepDownDoesNotMovePrimary(t *testing.T) {
	rs := threeMemberSet()
	rs.stepDownNoop = true
	h := newRSUpgradeHarness(rs)

	err := h.run.execute(context.Background(), rsNodes(rsTestPortA))

	require.Error(t, err)
	assert.Empty(t, h.upgraded)
}

// Already-upgraded nodes are skipped on a rerun.
func TestReplicaSetUpgrade_SkipsCompletedNodes(t *testing.T) {
	h := newRSUpgradeHarness(threeMemberSet())
	h.run.isDone = func(hostPort string) bool { return hostPort == "localhost:30001" }

	err := h.run.execute(context.Background(), rsNodes(rsTestPortA, rsTestPortB, rsTestPortC))

	require.NoError(t, err)
	assert.Equal(t, []string{"localhost:30002", "localhost:30000"}, h.upgraded)
}

func TestReplicaSetUpgrade_StopsOnNodeFailure(t *testing.T) {
	h := newRSUpgradeHarness(threeMemberSet())
	h.failOn = "localhost:30001"

	err := h.run.execute(context.Background(), rsNodes(rsTestPortA, rsTestPortB, rsTestPortC))

	require.Error(t, err)
	assert.Empty(t, h.upgraded)
	assert.Empty(t, h.rs.stepDowns)
}
