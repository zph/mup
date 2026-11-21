package supervisor

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/paths"
	"github.com/zph/mup/pkg/topology"
)

// LOCAL-029: wrapper scripts live under the directory of the program they
// control; config servers are config-<port>.
func TestGenerateWrapperScripts_UsesConfigServerProgramName(t *testing.T) {
	t.Setenv(paths.EnvStorageDir, t.TempDir())
	clusterRoot := t.TempDir()
	versionDir := filepath.Join(clusterRoot, "v7.0.0")
	topo := &topology.Topology{
		ConfigSvr: []topology.ConfigNode{{Host: "localhost", Port: 38400, ReplicaSet: "cfg"}},
		Mongod:    []topology.MongodNode{{Host: "localhost", Port: 38410, ReplicaSet: "shard1"}},
	}

	gen := NewConfigGenerator(versionDir, "wrapper-test", topo, "7.0.0", filepath.Join(versionDir, "bin"))
	require.NoError(t, gen.GenerateWrapperScripts())

	assert.DirExists(t, filepath.Join(versionDir, "config-38400", "bin"))
	assert.DirExists(t, filepath.Join(versionDir, "mongod-38410", "bin"))
	assert.NoDirExists(t, filepath.Join(versionDir, "mongod-38400"))
}
