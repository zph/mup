package upgrade

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const lagTestLimit = 30 * time.Second

func memberAt(port int, state memberState, optime time.Time) ReplicaSetMember {
	m := member(port, state, 1)
	m.OptimeDate = optime
	return m
}

// UPGSAFE-013
func TestCheckReplicationLag(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("caught-up secondaries pass", func(t *testing.T) {
		members := []ReplicaSetMember{
			memberAt(1, memberStatePrimary, now),
			memberAt(2, memberStateSecondary, now.Add(-5*time.Second)),
			memberAt(3, memberStateSecondary, now),
		}
		require.NoError(t, checkReplicationLag(members, lagTestLimit))
	})

	t.Run("a lagging secondary fails and is named", func(t *testing.T) {
		members := []ReplicaSetMember{
			memberAt(1, memberStatePrimary, now),
			memberAt(2, memberStateSecondary, now.Add(-2*time.Minute)),
		}
		err := checkReplicationLag(members, lagTestLimit)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "localhost:2")
		assert.Contains(t, err.Error(), "2m0s")
	})

	t.Run("arbiters hold no data and are ignored", func(t *testing.T) {
		members := []ReplicaSetMember{
			memberAt(1, memberStatePrimary, now),
			memberAt(2, memberStateSecondary, now),
			member(3, memberStateArbiter, 1),
		}
		require.NoError(t, checkReplicationLag(members, lagTestLimit))
	})

	t.Run("a secondary with no optime cannot be judged caught up", func(t *testing.T) {
		members := []ReplicaSetMember{
			memberAt(1, memberStatePrimary, now),
			member(2, memberStateSecondary, 1),
		}
		require.Error(t, checkReplicationLag(members, lagTestLimit))
	})

	t.Run("no primary means lag cannot be measured", func(t *testing.T) {
		members := []ReplicaSetMember{memberAt(2, memberStateSecondary, now)}
		require.Error(t, checkReplicationLag(members, lagTestLimit))
	})

	t.Run("single-member set has nothing to lag", func(t *testing.T) {
		require.NoError(t, checkReplicationLag([]ReplicaSetMember{memberAt(1, memberStatePrimary, now)}, lagTestLimit))
	})
}

// UPGSAFE-013: optimes are read from replSetGetStatus.
func TestParseReplicaSetMembers_ReadsOptimeDate(t *testing.T) {
	optime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	status := bson.M{"members": bson.A{
		bson.M{"name": "127.0.0.1:30000", "state": int32(1), "stateStr": "PRIMARY", "health": float64(1),
			"optimeDate": primitive.NewDateTimeFromTime(optime)},
		bson.M{"name": "127.0.0.1:30001", "state": int32(7), "stateStr": "ARBITER", "health": float64(1)},
	}}

	members, err := parseReplicaSetMembers(status)

	require.NoError(t, err)
	require.Len(t, members, 2)
	assert.True(t, members[0].OptimeDate.Equal(optime))
	assert.True(t, members[1].OptimeDate.IsZero())
}

// UPGSAFE-013: the upgrade does not take a node down while another lags.
func TestReplicaSetUpgrade_WaitsForReplicationLag(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	rs := &laggingReplicaSet{
		fakeReplicaSet: fakeReplicaSet{members: []ReplicaSetMember{
			memberAt(rsTestPortA, memberStatePrimary, now),
			memberAt(rsTestPortB, memberStateSecondary, now),
			memberAt(rsTestPortC, memberStateSecondary, now),
		}},
		laggingCalls: 3,
		lag:          5 * time.Minute,
	}
	h := newRSUpgradeHarness(&rs.fakeReplicaSet)
	h.run.probe = rs
	h.run.maxLag = lagTestLimit

	err := h.run.execute(context.Background(), rsNodes(rsTestPortB))

	require.NoError(t, err)
	assert.Equal(t, []string{"localhost:30001"}, h.upgraded)
	assert.Greater(t, rs.statusCalls, 3)
}

// laggingReplicaSet reports one secondary far behind for its first few
// status calls.
type laggingReplicaSet struct {
	fakeReplicaSet
	laggingCalls int
	lag          time.Duration
}

func (l *laggingReplicaSet) Status(ctx context.Context, rsName string, hosts []string) ([]ReplicaSetMember, error) {
	members, err := l.fakeReplicaSet.Status(ctx, rsName, hosts)
	if err != nil || l.laggingCalls <= 0 {
		return members, err
	}
	l.laggingCalls--
	last := len(members) - 1
	members[last].OptimeDate = members[last].OptimeDate.Add(-l.lag)
	return members, nil
}
