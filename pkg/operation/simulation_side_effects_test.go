package operation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation"
	"github.com/zph/mup/pkg/topology"
)

const simTestCluster = "sim-side-effects"

func simTestTopology() *topology.Topology {
	return &topology.Topology{
		Mongod: []topology.MongodNode{{Host: "localhost", Port: saveMetaPort}},
	}
}

// LOCAL-008: simulated operations leave the real filesystem untouched.
func TestSimulation_SaveMetadataWritesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	op := &plan.PlannedOperation{
		Type:   plan.OpSaveMetadata,
		Target: plan.OperationTarget{Type: "cluster", Name: simTestCluster},
		Params: map[string]interface{}{
			"version":     saveMetaVersion,
			"deploy_mode": "local",
			"topology":    simTestTopology(),
		},
	}

	result, err := (&SaveMetadataHandler{}).Execute(context.Background(), op, simulation.NewExecutor(simulation.NewConfig()))
	require.NoError(t, err)
	assert.True(t, result.Success)

	metaMgr, err := meta.NewManager()
	require.NoError(t, err)
	assert.NoFileExists(t, metaMgr.GetMetaFile(simTestCluster))
	assert.NoDirExists(t, metaMgr.GetClusterDir(simTestCluster))
}

// LOCAL-008
func TestSimulation_CreateSymlinkWritesNothing(t *testing.T) {
	dir := t.TempDir()
	linkPath := filepath.Join(dir, "current")
	op := &plan.PlannedOperation{
		Type: plan.OpCreateSymlink,
		Params: map[string]interface{}{
			"link_path":   linkPath,
			"target_path": "v7.0",
		},
	}

	result, err := (&CreateSymlinkHandler{}).Execute(context.Background(), op, simulation.NewExecutor(simulation.NewConfig()))
	require.NoError(t, err)
	assert.True(t, result.Success)

	_, statErr := os.Lstat(linkPath)
	assert.True(t, os.IsNotExist(statErr), "simulated symlink must not exist on disk")
}

// LOCAL-008
func TestSimulation_GenerateSupervisorConfigWritesNothing(t *testing.T) {
	clusterDir := t.TempDir()
	op := &plan.PlannedOperation{
		Type: plan.OpGenerateSupervisorCfg,
		Params: map[string]interface{}{
			"cluster_dir":  clusterDir,
			"cluster_name": simTestCluster,
			"version":      saveMetaVersion,
			"bin_path":     filepath.Join(clusterDir, "bin"),
			"topology":     simTestTopology(),
		},
	}

	result, err := (&GenerateSupervisorConfigHandler{}).Execute(context.Background(), op, simulation.NewExecutor(simulation.NewConfig()))
	require.NoError(t, err)
	assert.True(t, result.Success)

	entries, err := os.ReadDir(clusterDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}
