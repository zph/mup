package scraper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zph/mup/pkg/topology"
)

func TestBuildExporterRegistry(t *testing.T) {
	topo := &topology.Topology{
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: 27017, ReplicaSet: "rs0"},
			{Host: "localhost", Port: 27018, ReplicaSet: "rs0"},
			{Host: "localhost", Port: 27019, ReplicaSet: "rs0"},
		},
	}

	registry := BuildExporterRegistry(topo, 9100, 9216)

	// Should have 1 node_exporter (unique host)
	if len(registry.NodeExporters) != 1 {
		t.Errorf("expected 1 node exporter, got %d", len(registry.NodeExporters))
	}

	if registry.NodeExporters[0].Host != "localhost" {
		t.Errorf("expected host localhost, got %s", registry.NodeExporters[0].Host)
	}

	if registry.NodeExporters[0].Port != 9100 {
		t.Errorf("expected port 9100, got %d", registry.NodeExporters[0].Port)
	}

	// Should have 3 mongodb_exporters (one per mongod)
	if len(registry.MongoDBExporters) != 3 {
		t.Errorf("expected 3 mongodb exporters, got %d", len(registry.MongoDBExporters))
	}

	// Verify ports are auto-incremented
	expectedPorts := []int{9216, 9217, 9218}
	for i, me := range registry.MongoDBExporters {
		if me.ExporterPort != expectedPorts[i] {
			t.Errorf("exporter %d: expected port %d, got %d", i, expectedPorts[i], me.ExporterPort)
		}
	}
}

func TestBuildExporterRegistryMultipleHosts(t *testing.T) {
	topo := &topology.Topology{
		Mongod: []topology.MongodNode{
			{Host: "host1", Port: 27017, ReplicaSet: "rs0"},
			{Host: "host2", Port: 27017, ReplicaSet: "rs0"},
			{Host: "host3", Port: 27017, ReplicaSet: "rs0"},
		},
	}

	registry := BuildExporterRegistry(topo, 9100, 9216)

	// Should have 3 node_exporters (one per host)
	if len(registry.NodeExporters) != 3 {
		t.Errorf("expected 3 node exporters, got %d", len(registry.NodeExporters))
	}

	// Should have 3 mongodb_exporters
	if len(registry.MongoDBExporters) != 3 {
		t.Errorf("expected 3 mongodb exporters, got %d", len(registry.MongoDBExporters))
	}

	// Verify unique hosts
	hosts := make(map[string]bool)
	for _, ne := range registry.NodeExporters {
		if hosts[ne.Host] {
			t.Errorf("duplicate host in node exporters: %s", ne.Host)
		}
		hosts[ne.Host] = true
	}
}

func TestGenerateScrapeConfig(t *testing.T) {
	topo := &topology.Topology{
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: 27017, ReplicaSet: "rs0"},
			{Host: "localhost", Port: 27018, ReplicaSet: "rs0"},
		},
	}

	registry := BuildExporterRegistry(topo, 9100, 9216)
	config, err := GenerateScrapeConfig("test-cluster", topo, registry, "15s")
	if err != nil {
		t.Fatalf("failed to generate scrape config: %v", err)
	}

	// Check global config
	if config.Global.ScrapeInterval != "15s" {
		t.Errorf("expected scrape interval 15s, got %s", config.Global.ScrapeInterval)
	}

	if config.Global.ExternalLabels["cluster"] != "test-cluster" {
		t.Errorf("expected cluster label test-cluster, got %s", config.Global.ExternalLabels["cluster"])
	}

	// Should have 2 jobs: node-exporter and mongodb-exporter
	if len(config.ScrapeConfigs) != 2 {
		t.Errorf("expected 2 scrape jobs, got %d", len(config.ScrapeConfigs))
	}

	// Find node-exporter job
	var nodeJob *JobConfig
	for i := range config.ScrapeConfigs {
		if config.ScrapeConfigs[i].JobName == "node-exporter" {
			nodeJob = &config.ScrapeConfigs[i]
			break
		}
	}

	if nodeJob == nil {
		t.Fatal("node-exporter job not found")
	}

	if len(nodeJob.StaticConfigs) != 1 {
		t.Errorf("expected 1 node-exporter target, got %d", len(nodeJob.StaticConfigs))
	}

	// Find mongodb-exporter job
	var mongoJob *JobConfig
	for i := range config.ScrapeConfigs {
		if config.ScrapeConfigs[i].JobName == "mongodb-exporter" {
			mongoJob = &config.ScrapeConfigs[i]
			break
		}
	}

	if mongoJob == nil {
		t.Fatal("mongodb-exporter job not found")
	}

	if len(mongoJob.StaticConfigs) != 2 {
		t.Errorf("expected 2 mongodb-exporter targets, got %d", len(mongoJob.StaticConfigs))
	}

	// Check labels
	for _, sc := range mongoJob.StaticConfigs {
		if sc.Labels["replica_set"] != "rs0" {
			t.Errorf("expected replica_set label rs0, got %s", sc.Labels["replica_set"])
		}
		if sc.Labels["role"] != "mongod" {
			t.Errorf("expected role label mongod, got %s", sc.Labels["role"])
		}
	}
}

func TestWriteScrapeConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "promscrape.yaml")

	topo := &topology.Topology{
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: 27017},
		},
	}

	registry := BuildExporterRegistry(topo, 9100, 9216)
	config, err := GenerateScrapeConfig("test", topo, registry, "15s")
	if err != nil {
		t.Fatalf("failed to generate config: %v", err)
	}

	if err := WriteScrapeConfig(config, configPath); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("config file not created")
	}

	// Read and verify content
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	configStr := string(data)
	if !contains(configStr, "scrape_interval: 15s") {
		t.Error("config missing scrape_interval")
	}

	if !contains(configStr, "cluster: test") {
		t.Error("config missing cluster label")
	}
}

func TestGenerateScrapeConfigWithSharding(t *testing.T) {
	topo := &topology.Topology{
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: 27017, ReplicaSet: "shard1"},
		},
		Mongos: []topology.MongosNode{
			{Host: "localhost", Port: 27016},
		},
		ConfigSvr: []topology.ConfigNode{
			{Host: "localhost", Port: 27019, ReplicaSet: "configrs"},
		},
	}

	registry := BuildExporterRegistry(topo, 9100, 9216)

	// Should have 3 mongodb exporters (shard + mongos + config)
	if len(registry.MongoDBExporters) != 3 {
		t.Errorf("expected 3 mongodb exporters, got %d", len(registry.MongoDBExporters))
	}

	config, err := GenerateScrapeConfig("sharded-cluster", topo, registry, "15s")
	if err != nil {
		t.Fatalf("failed to generate config: %v", err)
	}

	// Find mongodb-exporter job
	var mongoJob *JobConfig
	for i := range config.ScrapeConfigs {
		if config.ScrapeConfigs[i].JobName == "mongodb-exporter" {
			mongoJob = &config.ScrapeConfigs[i]
			break
		}
	}

	if mongoJob == nil {
		t.Fatal("mongodb-exporter job not found")
	}

	// Verify roles are correctly labeled
	roleCount := make(map[string]int)
	for _, sc := range mongoJob.StaticConfigs {
		role := sc.Labels["role"]
		roleCount[role]++
	}

	if roleCount["mongod"] != 1 {
		t.Errorf("expected 1 mongod, got %d", roleCount["mongod"])
	}

	if roleCount["mongos"] != 1 {
		t.Errorf("expected 1 mongos, got %d", roleCount["mongos"])
	}

	if roleCount["config-server"] != 1 {
		t.Errorf("expected 1 config-server, got %d", roleCount["config-server"])
	}
}

// Helper function
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
