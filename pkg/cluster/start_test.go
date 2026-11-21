package cluster

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/supervisor"
)

// LOCAL-012: start reports every node that did not reach RUNNING.
func TestNodesNotRunning(t *testing.T) {
	nodes := []*meta.NodeMetadata{
		{Type: "mongod", Host: "localhost", Port: 30000, SupervisorProgramName: "mongod-30000"},
		{Type: "mongod", Host: "localhost", Port: 30001, SupervisorProgramName: "mongod-30001"},
		{Type: "mongod", Host: "localhost", Port: 30002, SupervisorProgramName: "mongod-30002"},
		{Type: "mongos", Host: "localhost", Port: 30003, SupervisorProgramName: "mongos-30003"},
	}
	statuses := map[string]*supervisor.ProcessStatus{
		"mongod-30000": {State: "Running", PID: 11},
		"mongod-30001": {State: "FATAL"},
		"mongod-30002": {State: "Backoff"},
	}
	getStatus := func(name string) (*supervisor.ProcessStatus, error) {
		if status, ok := statuses[name]; ok {
			return status, nil
		}
		return nil, errors.New("no such process")
	}

	failed := nodesNotRunning(nodes, getStatus)

	assert.Len(t, failed, 3)
	assert.Contains(t, failed[0], "localhost:30001")
	assert.Contains(t, failed[0], "FATAL")
	assert.Contains(t, failed[1], "localhost:30002")
	assert.Contains(t, failed[1], "BACKOFF")
	assert.Contains(t, failed[2], "localhost:30003")
	assert.Equal(t, 11, nodes[0].PID)
}

func nodeTypes(groups [][]*meta.NodeMetadata) [][]string {
	var result [][]string
	for _, group := range groups {
		var names []string
		for _, node := range group {
			names = append(names, node.SupervisorProgramName)
		}
		result = append(result, names)
	}
	return result
}

// LOCAL-021: config servers start first and stop last; mongos the reverse.
func TestNodeGroupOrdering(t *testing.T) {
	nodes := []*meta.NodeMetadata{
		{Type: "mongos", SupervisorProgramName: "mongos-1"},
		{Type: "mongod", SupervisorProgramName: "mongod-1"},
		{Type: "config", SupervisorProgramName: "config-1"},
		{Type: "mongod", SupervisorProgramName: "mongod-2"},
		{Type: "config", SupervisorProgramName: "config-2"},
	}

	assert.Equal(t, [][]string{
		{"config-1", "config-2"},
		{"mongod-1", "mongod-2"},
		{"mongos-1"},
	}, nodeTypes(nodeGroupsForStart(nodes)))

	assert.Equal(t, [][]string{
		{"mongos-1"},
		{"mongod-1", "mongod-2"},
		{"config-1", "config-2"},
	}, nodeTypes(nodeGroupsForStop(nodes)))
}

// A replica set has a single group, so it still starts in parallel.
func TestNodeGroupOrdering_ReplicaSetIsOneGroup(t *testing.T) {
	nodes := []*meta.NodeMetadata{
		{Type: "mongod", SupervisorProgramName: "mongod-1"},
		{Type: "mongod", SupervisorProgramName: "mongod-2"},
	}

	assert.Len(t, nodeGroupsForStart(nodes), 1)
	assert.Empty(t, nodeGroupsForStart(nil))
}
