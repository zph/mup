package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/topology"
	"gopkg.in/yaml.v3"
)

// IMP-033: Generate topology.yaml from discovered cluster state
func TestGenerateTopology(t *testing.T) {
	t.Run("standalone_topology", func(t *testing.T) {
		generator := NewTopologyGenerator()

		discovery := &DiscoveryResult{
			Instances: []MongoInstance{
				{
					Host:         "localhost",
					Port:         27017,
					Version:      "7.0.5",
					Variant:      "mongo",
					TopologyType: "standalone",
					ConfigFile:   "/etc/mongod.conf",
					DataDir:      "/var/lib/mongodb",
				},
			},
		}

		clusterDir := "/home/test/.mup/storage/clusters/my-cluster"

		topo, err := generator.Generate(discovery, clusterDir)
		require.NoError(t, err)
		require.NotNil(t, topo)

		// Verify global config
		assert.Equal(t, clusterDir, topo.Global.DeployDir)

		// Verify mongod nodes
		assert.Len(t, topo.Mongod, 1)
		assert.Equal(t, "localhost", topo.Mongod[0].Host)
		assert.Equal(t, 27017, topo.Mongod[0].Port)
		assert.Empty(t, topo.Mongod[0].ReplicaSet)

		// Should not have mongos or config servers
		assert.Len(t, topo.Mongos, 0)
		assert.Len(t, topo.ConfigSvr, 0)
	})

	// IMP-034: Handle replica set topology
	t.Run("replica_set_topology", func(t *testing.T) {
		generator := NewTopologyGenerator()

		discovery := &DiscoveryResult{
			Instances: []MongoInstance{
				{
					Host:         "localhost",
					Port:         27017,
					Version:      "7.0.5",
					Variant:      "mongo",
					TopologyType: "replica set",
					ReplicaSet:   "rs0",
					ConfigFile:   "/etc/mongod-27017.conf",
					DataDir:      "/var/lib/mongodb-27017",
				},
				{
					Host:         "localhost",
					Port:         27018,
					Version:      "7.0.5",
					Variant:      "mongo",
					TopologyType: "replica set",
					ReplicaSet:   "rs0",
					ConfigFile:   "/etc/mongod-27018.conf",
					DataDir:      "/var/lib/mongodb-27018",
				},
				{
					Host:         "localhost",
					Port:         27019,
					Version:      "7.0.5",
					Variant:      "mongo",
					TopologyType: "replica set",
					ReplicaSet:   "rs0",
					ConfigFile:   "/etc/mongod-27019.conf",
					DataDir:      "/var/lib/mongodb-27019",
				},
			},
		}

		clusterDir := "/home/test/.mup/storage/clusters/rs-cluster"

		topo, err := generator.Generate(discovery, clusterDir)
		require.NoError(t, err)
		require.NotNil(t, topo)

		// Verify mongod nodes with replica set
		assert.Len(t, topo.Mongod, 3)
		for _, node := range topo.Mongod {
			assert.Equal(t, "rs0", node.ReplicaSet)
			assert.Equal(t, "localhost", node.Host)
			assert.Contains(t, []int{27017, 27018, 27019}, node.Port)
		}
	})

	// IMP-034: Handle sharded cluster topology
	t.Run("sharded_cluster_topology", func(t *testing.T) {
		generator := NewTopologyGenerator()

		discovery := &DiscoveryResult{
			Instances: []MongoInstance{
				// Config servers
				{
					Host:         "localhost",
					Port:         27019,
					Version:      "7.0.5",
					Variant:      "mongo",
					TopologyType: "sharded cluster",
					ProcessType:  "configsvr",
					ReplicaSet:   "configRS",
					ConfigFile:   "/etc/mongod-config.conf",
					DataDir:      "/var/lib/mongodb-config",
				},
				// Shard
				{
					Host:         "localhost",
					Port:         27018,
					Version:      "7.0.5",
					Variant:      "mongo",
					TopologyType: "sharded cluster",
					ProcessType:  "shardsvr",
					ReplicaSet:   "shard0",
					ConfigFile:   "/etc/mongod-shard.conf",
					DataDir:      "/var/lib/mongodb-shard",
				},
				// Mongos
				{
					Host:         "localhost",
					Port:         27017,
					Version:      "7.0.5",
					Variant:      "mongo",
					TopologyType: "sharded cluster",
					ProcessType:  "mongos",
					ConfigFile:   "/etc/mongos.conf",
				},
			},
		}

		clusterDir := "/home/test/.mup/storage/clusters/sharded-cluster"

		topo, err := generator.Generate(discovery, clusterDir)
		require.NoError(t, err)
		require.NotNil(t, topo)

		// Verify config servers
		assert.Len(t, topo.ConfigSvr, 1)
		assert.Equal(t, "localhost", topo.ConfigSvr[0].Host)
		assert.Equal(t, 27019, topo.ConfigSvr[0].Port)
		assert.Equal(t, "configRS", topo.ConfigSvr[0].ReplicaSet)

		// Verify shards (should be in mongod)
		assert.Len(t, topo.Mongod, 1)
		assert.Equal(t, "localhost", topo.Mongod[0].Host)
		assert.Equal(t, 27018, topo.Mongod[0].Port)
		assert.Equal(t, "shard0", topo.Mongod[0].ReplicaSet)

		// Verify mongos
		assert.Len(t, topo.Mongos, 1)
		assert.Equal(t, "localhost", topo.Mongos[0].Host)
		assert.Equal(t, 27017, topo.Mongos[0].Port)
	})
}

// IMP-035: Save topology.yaml to cluster directory
func TestSaveTopology(t *testing.T) {
	t.Run("save_topology_to_file", func(t *testing.T) {
		// Create temp directory
		tmpDir := t.TempDir()
		clusterDir := filepath.Join(tmpDir, "test-cluster")
		err := os.MkdirAll(clusterDir, 0755)
		require.NoError(t, err)

		generator := NewTopologyGenerator()

		discovery := &DiscoveryResult{
			Instances: []MongoInstance{
				{
					Host:         "localhost",
					Port:         27017,
					Version:      "7.0.5",
					Variant:      "mongo",
					TopologyType: "standalone",
					ConfigFile:   "/etc/mongod.conf",
					DataDir:      "/var/lib/mongodb",
				},
			},
		}

		// Generate and save topology
		topo, err := generator.Generate(discovery, clusterDir)
		require.NoError(t, err)

		topologyPath := filepath.Join(clusterDir, "topology.yaml")
		err = generator.Save(topo, topologyPath)
		require.NoError(t, err)

		// Verify file exists
		_, err = os.Stat(topologyPath)
		require.NoError(t, err)

		// Verify file content is valid YAML
		data, err := os.ReadFile(topologyPath)
		require.NoError(t, err)

		var loadedTopo topology.Topology
		err = yaml.Unmarshal(data, &loadedTopo)
		require.NoError(t, err)

		// Verify loaded topology matches
		assert.Equal(t, clusterDir, loadedTopo.Global.DeployDir)
		assert.Len(t, loadedTopo.Mongod, 1)
		assert.Equal(t, "localhost", loadedTopo.Mongod[0].Host)
		assert.Equal(t, 27017, loadedTopo.Mongod[0].Port)
	})

	t.Run("save_creates_parent_directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		clusterDir := filepath.Join(tmpDir, "nested", "test-cluster")

		generator := NewTopologyGenerator()

		discovery := &DiscoveryResult{
			Instances: []MongoInstance{
				{
					Host:         "localhost",
					Port:         27017,
					Version:      "7.0.5",
					Variant:      "mongo",
					TopologyType: "standalone",
					ConfigFile:   "/etc/mongod.conf",
					DataDir:      "/var/lib/mongodb",
				},
			},
		}

		topo, err := generator.Generate(discovery, clusterDir)
		require.NoError(t, err)

		topologyPath := filepath.Join(clusterDir, "topology.yaml")
		err = generator.Save(topo, topologyPath)
		require.NoError(t, err)

		// Verify file exists
		_, err = os.Stat(topologyPath)
		require.NoError(t, err)
	})
}

// Test helper to verify topology type detection
func TestTopologyTypeMapping(t *testing.T) {
	t.Run("standalone", func(t *testing.T) {
		generator := NewTopologyGenerator()

		discovery := &DiscoveryResult{
			Instances: []MongoInstance{
				{
					Host:         "localhost",
					Port:         27017,
					TopologyType: "standalone",
				},
			},
		}

		topo, err := generator.Generate(discovery, "/test")
		require.NoError(t, err)

		actualType := topo.GetTopologyType()
		assert.Equal(t, "standalone", actualType)
	})

	t.Run("replica_set", func(t *testing.T) {
		generator := NewTopologyGenerator()

		discovery := &DiscoveryResult{
			Instances: []MongoInstance{
				{
					Host:         "localhost",
					Port:         27017,
					TopologyType: "replica set",
					ReplicaSet:   "rs0",
				},
			},
		}

		topo, err := generator.Generate(discovery, "/test")
		require.NoError(t, err)

		actualType := topo.GetTopologyType()
		assert.Equal(t, "replica_set", actualType)
	})

	t.Run("sharded_cluster", func(t *testing.T) {
		generator := NewTopologyGenerator()

		// For sharded cluster, need config servers and/or mongos
		discovery := &DiscoveryResult{
			Instances: []MongoInstance{
				{
					Host:         "localhost",
					Port:         27019,
					TopologyType: "sharded cluster",
					ProcessType:  "configsvr",
					ReplicaSet:   "configRS",
				},
				{
					Host:         "localhost",
					Port:         27017,
					TopologyType: "sharded cluster",
					ProcessType:  "mongos",
				},
			},
		}

		topo, err := generator.Generate(discovery, "/test")
		require.NoError(t, err)

		actualType := topo.GetTopologyType()
		assert.Equal(t, "sharded", actualType)
	})
}
