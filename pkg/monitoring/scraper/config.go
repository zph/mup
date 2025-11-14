package scraper

import (
	"fmt"
	"os"

	"github.com/zph/mup/pkg/topology"
	"gopkg.in/yaml.v3"
)

// ScrapeConfig represents Prometheus scrape configuration
type ScrapeConfig struct {
	Global        GlobalConfig `yaml:"global"`
	ScrapeConfigs []JobConfig  `yaml:"scrape_configs"`
}

// GlobalConfig contains global scrape settings
type GlobalConfig struct {
	ScrapeInterval     string            `yaml:"scrape_interval"`
	EvaluationInterval string            `yaml:"evaluation_interval"`
	ExternalLabels     map[string]string `yaml:"external_labels"`
}

// JobConfig represents a scrape job
type JobConfig struct {
	JobName       string         `yaml:"job_name"`
	StaticConfigs []StaticConfig `yaml:"static_configs"`
}

// StaticConfig represents static targets
type StaticConfig struct {
	Targets []string          `yaml:"targets"`
	Labels  map[string]string `yaml:"labels"`
}

// ExporterRegistry tracks all exporters for a cluster
type ExporterRegistry struct {
	NodeExporters    []NodeExporterInfo
	MongoDBExporters []MongoDBExporterInfo
}

// NodeExporterInfo contains info about a node_exporter instance
type NodeExporterInfo struct {
	Host string
	Port int
}

// MongoDBExporterInfo contains info about a mongodb_exporter instance
type MongoDBExporterInfo struct {
	Host         string
	ExporterPort int
	MongoDBPort  int
}

// GenerateScrapeConfig generates a Prometheus scrape config for a cluster
func GenerateScrapeConfig(clusterName string, topo *topology.Topology, registry *ExporterRegistry, scrapeInterval string) (*ScrapeConfig, error) {
	config := &ScrapeConfig{
		Global: GlobalConfig{
			ScrapeInterval:     scrapeInterval,
			EvaluationInterval: scrapeInterval,
			ExternalLabels: map[string]string{
				"cluster": clusterName,
			},
		},
		ScrapeConfigs: []JobConfig{},
	}

	// Add node_exporter targets (one per unique host)
	if len(registry.NodeExporters) > 0 {
		nodeExporterJob := JobConfig{
			JobName:       "node-exporter",
			StaticConfigs: []StaticConfig{},
		}

		for _, ne := range registry.NodeExporters {
			nodeExporterJob.StaticConfigs = append(nodeExporterJob.StaticConfigs, StaticConfig{
				Targets: []string{fmt.Sprintf("%s:%d", ne.Host, ne.Port)},
				Labels: map[string]string{
					"host": ne.Host,
					"role": "mongodb-host",
				},
			})
		}

		config.ScrapeConfigs = append(config.ScrapeConfigs, nodeExporterJob)
	}

	// Add mongodb_exporter targets (one per MongoDB process)
	if len(registry.MongoDBExporters) > 0 {
		mongoExporterJob := JobConfig{
			JobName:       "mongodb-exporter",
			StaticConfigs: []StaticConfig{},
		}

		for _, me := range registry.MongoDBExporters {
			labels := map[string]string{
				"host": me.Host,
				"port": fmt.Sprintf("%d", me.MongoDBPort),
			}

			// Find node in topology to get additional labels
			for _, node := range topo.Mongod {
				if node.Host == me.Host && node.Port == me.MongoDBPort {
					if node.ReplicaSet != "" {
						labels["replica_set"] = node.ReplicaSet
					}
					labels["role"] = "mongod"
					break
				}
			}

			// Check mongos
			for _, node := range topo.Mongos {
				if node.Host == me.Host && node.Port == me.MongoDBPort {
					labels["role"] = "mongos"
					break
				}
			}

			// Check config servers
			for _, node := range topo.ConfigSvr {
				if node.Host == me.Host && node.Port == me.MongoDBPort {
					if node.ReplicaSet != "" {
						labels["replica_set"] = node.ReplicaSet
					}
					labels["role"] = "config-server"
					break
				}
			}

			mongoExporterJob.StaticConfigs = append(mongoExporterJob.StaticConfigs, StaticConfig{
				Targets: []string{fmt.Sprintf("%s:%d", me.Host, me.ExporterPort)},
				Labels:  labels,
			})
		}

		config.ScrapeConfigs = append(config.ScrapeConfigs, mongoExporterJob)
	}

	return config, nil
}

// WriteScrapeConfig writes scrape config to a file
func WriteScrapeConfig(config *ScrapeConfig, path string) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal scrape config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write scrape config: %w", err)
	}

	return nil
}

// BuildExporterRegistry builds an exporter registry from topology
// This will be populated during exporter deployment
func BuildExporterRegistry(topo *topology.Topology, nodeExporterPort, mongoExporterPortBase int) *ExporterRegistry {
	registry := &ExporterRegistry{
		NodeExporters:    []NodeExporterInfo{},
		MongoDBExporters: []MongoDBExporterInfo{},
	}

	// Track unique hosts for node_exporter
	seenHosts := make(map[string]bool)

	// Process mongod nodes
	exporterPort := mongoExporterPortBase
	for _, node := range topo.Mongod {
		// Add node_exporter for unique hosts
		if !seenHosts[node.Host] {
			registry.NodeExporters = append(registry.NodeExporters, NodeExporterInfo{
				Host: node.Host,
				Port: nodeExporterPort,
			})
			seenHosts[node.Host] = true
		}

		// Add mongodb_exporter for each mongod
		registry.MongoDBExporters = append(registry.MongoDBExporters, MongoDBExporterInfo{
			Host:         node.Host,
			ExporterPort: exporterPort,
			MongoDBPort:  node.Port,
		})
		exporterPort++
	}

	// Process mongos nodes
	for _, node := range topo.Mongos {
		if !seenHosts[node.Host] {
			registry.NodeExporters = append(registry.NodeExporters, NodeExporterInfo{
				Host: node.Host,
				Port: nodeExporterPort,
			})
			seenHosts[node.Host] = true
		}

		registry.MongoDBExporters = append(registry.MongoDBExporters, MongoDBExporterInfo{
			Host:         node.Host,
			ExporterPort: exporterPort,
			MongoDBPort:  node.Port,
		})
		exporterPort++
	}

	// Process config server nodes
	for _, node := range topo.ConfigSvr {
		if !seenHosts[node.Host] {
			registry.NodeExporters = append(registry.NodeExporters, NodeExporterInfo{
				Host: node.Host,
				Port: nodeExporterPort,
			})
			seenHosts[node.Host] = true
		}

		registry.MongoDBExporters = append(registry.MongoDBExporters, MongoDBExporterInfo{
			Host:         node.Host,
			ExporterPort: exporterPort,
			MongoDBPort:  node.Port,
		})
		exporterPort++
	}

	return registry
}
