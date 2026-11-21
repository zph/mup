package operation

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/topology"
)

const (
	saveMetaCluster = "save-meta-test"
	saveMetaVersion = "7.0"
	saveMetaPort    = 30000
)

// LOCAL-004: metadata records the paths mup actually deployed to, not the
// raw topology values.
func TestSaveMetadataHandler_RecordsResolvedLocalPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	topo := &topology.Topology{
		Mongod: []topology.MongodNode{{
			Host:      "localhost",
			Port:      saveMetaPort,
			DataDir:   "/data/mongodb",
			LogDir:    "/var/log/mongodb",
			ConfigDir: "/etc/mongodb",
		}},
	}
	op := &plan.PlannedOperation{
		Type:   plan.OpSaveMetadata,
		Target: plan.OperationTarget{Type: "cluster", Name: saveMetaCluster},
		Params: map[string]interface{}{
			"version":     saveMetaVersion,
			"deploy_mode": "local",
			"topology":    topo,
		},
	}

	_, err := (&SaveMetadataHandler{}).Execute(context.Background(), op, executor.NewLocalExecutor())
	require.NoError(t, err)

	metaMgr, err := meta.NewManager()
	require.NoError(t, err)
	saved, err := metaMgr.Load(saveMetaCluster)
	require.NoError(t, err)
	require.Len(t, saved.Nodes, 1)

	clusterDir := metaMgr.GetClusterDir(saveMetaCluster)
	node := saved.Nodes[0]
	assert.Equal(t, filepath.Join(clusterDir, "data", "localhost-30000"), node.DataDir)
	assert.Equal(t, filepath.Join(clusterDir, "v7.0", "mongod-30000", "log"), node.LogDir)
	assert.Equal(t, filepath.Join(clusterDir, "v7.0", "mongod-30000", "config"), node.ConfigDir)
}

// LOCAL-016: a plan reloaded from JSON (as on resume) carries the topology as
// a generic map; metadata must still record every node.
func TestSaveMetadataHandler_AcceptsJSONLoadedTopology(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	raw, err := json.Marshal(&topology.Topology{
		Mongod: []topology.MongodNode{{Host: "localhost", Port: saveMetaPort, ReplicaSet: "rs0"}},
	})
	require.NoError(t, err)
	var topoMap map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &topoMap))

	op := &plan.PlannedOperation{
		Type:   plan.OpSaveMetadata,
		Target: plan.OperationTarget{Type: "cluster", Name: saveMetaCluster},
		Params: map[string]interface{}{
			"version":     saveMetaVersion,
			"deploy_mode": "local",
			"topology":    topoMap,
		},
	}

	_, err = (&SaveMetadataHandler{}).Execute(context.Background(), op, executor.NewLocalExecutor())
	require.NoError(t, err)

	metaMgr, err := meta.NewManager()
	require.NoError(t, err)
	saved, err := metaMgr.Load(saveMetaCluster)
	require.NoError(t, err)
	require.Len(t, saved.Nodes, 1)
	assert.Equal(t, "rs0", saved.Nodes[0].ReplicaSet)
	require.NotNil(t, saved.Topology)
	assert.Len(t, saved.Topology.Mongod, 1)
}
