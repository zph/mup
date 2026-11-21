package topology

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPort = 30000

// LOCAL-007: local topologies need no remote-only global settings.
func TestValidate_LocalTopologyNeedsNoGlobalUser(t *testing.T) {
	topo := &Topology{Mongod: []MongodNode{{Host: "localhost", Port: testPort}}}

	require.NoError(t, topo.Validate())
}

// LOCAL-005
func TestNonLocalHosts(t *testing.T) {
	topo := &Topology{
		Mongod:    []MongodNode{{Host: "localhost"}, {Host: "10.0.0.2"}},
		Mongos:    []MongosNode{{Host: "127.0.0.1"}, {Host: "db.example.com"}},
		ConfigSvr: []ConfigNode{{Host: "::1"}, {Host: "10.0.0.2"}},
	}

	assert.Equal(t, []string{"10.0.0.2", "db.example.com"}, topo.NonLocalHosts())
	assert.False(t, topo.IsLocalDeployment())
	assert.Empty(t, (&Topology{Mongod: []MongodNode{{Host: "localhost"}}}).NonLocalHosts())
}
