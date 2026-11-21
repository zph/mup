package upgrade

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/topology"
)

// UPGSAFE-012: each node's log directory matches the name it is supervised
// under, so config servers get config-<port>, not mongod-<port>.
func TestCreateProcessLogDirs(t *testing.T) {
	versionDir := t.TempDir()
	topo := &topology.Topology{
		ConfigSvr: []topology.ConfigNode{{Host: "localhost", Port: 38400, ReplicaSet: "cfg"}},
		Mongod:    []topology.MongodNode{{Host: "localhost", Port: 38410, ReplicaSet: "shard1"}},
		Mongos:    []topology.MongosNode{{Host: "localhost", Port: 38420}},
	}

	require.NoError(t, createProcessLogDirs(versionDir, topo))

	for _, dir := range []string{"config-38400", "mongod-38410", "mongos-38420"} {
		assert.DirExists(t, filepath.Join(versionDir, dir, "log"))
	}
	assert.NoDirExists(t, filepath.Join(versionDir, "mongod-38400"))
}
