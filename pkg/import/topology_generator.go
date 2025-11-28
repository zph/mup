package importer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zph/mup/pkg/topology"
	"gopkg.in/yaml.v3"
)

// TopologyGenerator generates topology.yaml from discovered cluster state
type TopologyGenerator struct{}

// NewTopologyGenerator creates a new topology generator
func NewTopologyGenerator() *TopologyGenerator {
	return &TopologyGenerator{}
}

// Generate creates a Topology from discovered cluster information
// IMP-033: Generate topology.yaml from discovered cluster state
// IMP-034: Handle different topology types (standalone, replica set, sharded)
func (tg *TopologyGenerator) Generate(discovery *DiscoveryResult, clusterDir string) (*topology.Topology, error) {
	if discovery == nil || len(discovery.Instances) == 0 {
		return nil, fmt.Errorf("no instances to generate topology from")
	}

	topo := &topology.Topology{
		Global: topology.GlobalConfig{
			User:      "mongodb",
			DeployDir: clusterDir,
			DataDir:   filepath.Join(clusterDir, "data"),
			LogDir:    filepath.Join(clusterDir, "current", "logs"),
			ConfigDir: filepath.Join(clusterDir, "current", "conf"),
		},
	}

	// Group instances by process type
	for _, instance := range discovery.Instances {
		switch instance.ProcessType {
		case "mongos":
			// Mongos router
			topo.Mongos = append(topo.Mongos, topology.MongosNode{
				Host: instance.Host,
				Port: instance.Port,
			})

		case "configsvr":
			// Config server
			topo.ConfigSvr = append(topo.ConfigSvr, topology.ConfigNode{
				Host:       instance.Host,
				Port:       instance.Port,
				ReplicaSet: instance.ReplicaSet,
			})

		case "shardsvr", "": // Empty means mongod (default)
			// Regular mongod or shard server
			topo.Mongod = append(topo.Mongod, topology.MongodNode{
				Host:       instance.Host,
				Port:       instance.Port,
				ReplicaSet: instance.ReplicaSet,
			})

		default:
			// Unknown process type, treat as mongod
			topo.Mongod = append(topo.Mongod, topology.MongodNode{
				Host:       instance.Host,
				Port:       instance.Port,
				ReplicaSet: instance.ReplicaSet,
			})
		}
	}

	return topo, nil
}

// Save writes the topology to a YAML file
// IMP-035: Save topology.yaml to cluster directory
func (tg *TopologyGenerator) Save(topo *topology.Topology, path string) error {
	// Create parent directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Marshal topology to YAML
	data, err := yaml.Marshal(topo)
	if err != nil {
		return fmt.Errorf("failed to marshal topology to YAML: %w", err)
	}

	// Write to file
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write topology file: %w", err)
	}

	return nil
}

// GenerateAndSave is a convenience function that generates and saves in one call
func (tg *TopologyGenerator) GenerateAndSave(discovery *DiscoveryResult, clusterDir string) error {
	topo, err := tg.Generate(discovery, clusterDir)
	if err != nil {
		return err
	}

	topologyPath := filepath.Join(clusterDir, "topology.yaml")
	return tg.Save(topo, topologyPath)
}
