package deploy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/topology"
)

const (
	plannerTestCluster = "planner-validation"
	plannerTestVersion = "7.0"
	plannerTestPort    = 30000
)

func newValidationPlanner(t *testing.T, topo *topology.Topology) *DeployPlanner {
	t.Helper()
	planner, err := NewDeployPlanner(&PlannerConfig{
		ClusterName: plannerTestCluster,
		Version:     plannerTestVersion,
		Topology:    topo,
		MetaDir:     t.TempDir(),
		IsLocal:     topo.IsLocalDeployment(),
	})
	require.NoError(t, err)
	return planner
}

// LOCAL-005: topologies with non-local hosts are rejected before planning.
func TestGeneratePlan_RejectsNonLocalHosts(t *testing.T) {
	topo := &topology.Topology{
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: plannerTestPort},
			{Host: "10.0.0.2", Port: plannerTestPort},
		},
	}

	_, err := newValidationPlanner(t, topo).GeneratePlan(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "10.0.0.2")
}

// LOCAL-006: invalid topologies are rejected before planning.
func TestGeneratePlan_RejectsInvalidTopology(t *testing.T) {
	tests := []struct {
		name    string
		topo    *topology.Topology
		wantErr string
	}{
		{
			name: "duplicate port on one host",
			topo: &topology.Topology{Mongod: []topology.MongodNode{
				{Host: "localhost", Port: plannerTestPort},
				{Host: "localhost", Port: plannerTestPort},
			}},
			wantErr: "duplicate port",
		},
		{
			name:    "no nodes",
			topo:    &topology.Topology{},
			wantErr: "at least one node",
		},
		{
			name: "config server without replica set",
			topo: &topology.Topology{ConfigSvr: []topology.ConfigNode{
				{Host: "localhost", Port: plannerTestPort},
			}},
			wantErr: "replica_set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newValidationPlanner(t, tt.topo).GeneratePlan(context.Background())

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// LOCAL-010: every health verification names the ports it must check.
func TestGeneratePlan_HealthChecksCarryAllPorts(t *testing.T) {
	topo := &topology.Topology{
		ConfigSvr: []topology.ConfigNode{{Host: "localhost", Port: plannerTestPort, ReplicaSet: "cfg"}},
		Mongod:    []topology.MongodNode{{Host: "localhost", Port: plannerTestPort + 1, ReplicaSet: "shard1"}},
		Mongos:    []topology.MongosNode{{Host: "localhost", Port: plannerTestPort + 2}},
	}
	wantPorts := []int{plannerTestPort, plannerTestPort + 1, plannerTestPort + 2}
	planner := newValidationPlanner(t, topo)

	var healthOps int
	for _, phase := range []plan.PlannedPhase{planner.generateInitializePhase(), planner.generateFinalizePhase()} {
		for _, op := range phase.Operations {
			if op.Type != plan.OpVerifyHealth {
				continue
			}
			healthOps++
			assert.ElementsMatch(t, wantPorts, op.Params["ports"], "phase %s", phase.Name)
		}
	}
	assert.Equal(t, 2, healthOps)
}

// LOCAL-022: member settings from the topology reach replica set initiation.
func TestGeneratePlan_InitCarriesMemberOptions(t *testing.T) {
	zero := 0.0
	hidden := true
	noVotes := 0
	topo := &topology.Topology{
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: plannerTestPort, ReplicaSet: "rs0"},
			{Host: "localhost", Port: plannerTestPort + 1, ReplicaSet: "rs0", Priority: &zero, Hidden: &hidden, Votes: &noVotes},
		},
	}

	phase := newValidationPlanner(t, topo).generateInitializePhase()

	var initOps []plan.PlannedOperation
	for _, op := range phase.Operations {
		if op.Type == plan.OpInitReplicaSet {
			initOps = append(initOps, op)
		}
	}
	require.Len(t, initOps, 1)
	assert.Equal(t, map[string]map[string]interface{}{
		"127.0.0.1:30001": {"priority": 0.0, "hidden": true, "votes": 0},
	}, initOps[0].Params["member_options"])
}

// Replica sets are planned in a stable order so the same topology always
// yields the same operation IDs.
func TestGeneratePlan_ReplicaSetOrderIsStable(t *testing.T) {
	topo := &topology.Topology{
		ConfigSvr: []topology.ConfigNode{{Host: "localhost", Port: plannerTestPort, ReplicaSet: "cfg"}},
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: plannerTestPort + 1, ReplicaSet: "shardB"},
			{Host: "localhost", Port: plannerTestPort + 2, ReplicaSet: "shardA"},
		},
		Mongos: []topology.MongosNode{{Host: "localhost", Port: plannerTestPort + 3}},
	}

	initOrder := func() []string {
		var names []string
		for _, op := range newValidationPlanner(t, topo).generateInitializePhase().Operations {
			if op.Type == plan.OpInitReplicaSet {
				names = append(names, op.Target.Name)
			}
		}
		return names
	}

	first := initOrder()
	assert.Equal(t, []string{"cfg", "shardA", "shardB"}, first)
	for i := 0; i < 10; i++ {
		assert.Equal(t, first, initOrder())
	}
}
