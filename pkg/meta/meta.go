package meta

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/zph/mup/pkg/topology"
)

// ClusterMetadata represents the stored cluster state
type ClusterMetadata struct {
	Name              string               `yaml:"name"`
	Version           string               `yaml:"version"`
	BinPath           string               `yaml:"bin_path"`     // Path to MongoDB binaries
	CreatedAt         time.Time            `yaml:"created_at"`
	Status            string               `yaml:"status"`
	Topology          *topology.Topology    `yaml:"topology"`
	DeployMode        string               `yaml:"deploy_mode"` // "local" or "remote"
	Nodes             []NodeMetadata       `yaml:"nodes"`
	ConnectionCommand string               `yaml:"connection_command,omitempty"` // Command to connect to cluster

	// Supervisord fields
	SupervisorConfigPath string `yaml:"supervisor_config_path,omitempty"` // Path to supervisor.ini
	SupervisorPIDFile    string `yaml:"supervisor_pid_file,omitempty"`    // Path to supervisor.pid
	SupervisorRunning    bool   `yaml:"supervisor_running,omitempty"`     // Whether supervisord is running

	// Monitoring fields
	Monitoring *MonitoringMetadata `yaml:"monitoring,omitempty"`
}

// MonitoringMetadata tracks monitoring infrastructure state
type MonitoringMetadata struct {
	Enabled             bool                      `yaml:"enabled"`
	VictoriaMetricsURL  string                    `yaml:"victoria_metrics_url,omitempty"`
	GrafanaURL          string                    `yaml:"grafana_url,omitempty"`
	NodeExporters       []NodeExporterMetadata    `yaml:"node_exporters,omitempty"`
	MongoDBExporters    []MongoDBExporterMetadata `yaml:"mongodb_exporters,omitempty"`
	SupervisorConfigPath string                   `yaml:"supervisor_config_path,omitempty"` // Monitoring supervisor.ini
	SupervisorPIDFile    string                   `yaml:"supervisor_pid_file,omitempty"`
}

// NodeExporterMetadata tracks a node_exporter instance
type NodeExporterMetadata struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	PID  int    `yaml:"pid,omitempty"`
}

// MongoDBExporterMetadata tracks a mongodb_exporter instance
type MongoDBExporterMetadata struct {
	Host         string `yaml:"host"`
	ExporterPort int    `yaml:"exporter_port"`
	MongoDBPort  int    `yaml:"mongodb_port"`
	PID          int    `yaml:"pid,omitempty"`
}

// NodeMetadata represents metadata for a single node
type NodeMetadata struct {
	Type       string `yaml:"type"`        // "mongod", "mongos", "config"
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	ReplicaSet string `yaml:"replica_set,omitempty"`
	DataDir    string `yaml:"data_dir,omitempty"`
	LogDir     string `yaml:"log_dir"`
	ConfigDir  string `yaml:"config_dir"`
	ConfigFile string `yaml:"config_file"`
	PID        int    `yaml:"pid,omitempty"` // Deprecated: supervisord manages PIDs now

	// Supervisord fields
	SupervisorProgramName string `yaml:"supervisor_program_name,omitempty"` // Name in supervisor config (e.g., "mongod-27017")
	SupervisorConfigFile  string `yaml:"supervisor_config_file,omitempty"`  // Path to node's supervisor config
}

// Manager manages cluster metadata
type Manager struct {
	baseDir string
}

// NewManager creates a new metadata manager
func NewManager() (*Manager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	baseDir := filepath.Join(homeDir, ".mup", "storage", "clusters")
	return &Manager{baseDir: baseDir}, nil
}

// GetClusterDir returns the directory for a cluster
func (m *Manager) GetClusterDir(clusterName string) string {
	return filepath.Join(m.baseDir, clusterName)
}

// GetMetaFile returns the path to the metadata file
func (m *Manager) GetMetaFile(clusterName string) string {
	return filepath.Join(m.GetClusterDir(clusterName), "meta.yaml")
}

// Load loads cluster metadata
func (m *Manager) Load(clusterName string) (*ClusterMetadata, error) {
	metaFile := m.GetMetaFile(clusterName)

	data, err := os.ReadFile(metaFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("cluster '%s' not found", clusterName)
		}
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}

	var metadata ClusterMetadata
	if err := yaml.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	return &metadata, nil
}

// Save saves cluster metadata
func (m *Manager) Save(metadata *ClusterMetadata) error {
	clusterDir := m.GetClusterDir(metadata.Name)
	if err := os.MkdirAll(clusterDir, 0755); err != nil {
		return fmt.Errorf("failed to create cluster directory: %w", err)
	}

	data, err := yaml.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	metaFile := m.GetMetaFile(metadata.Name)
	if err := os.WriteFile(metaFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	return nil
}

// Delete deletes cluster metadata
func (m *Manager) Delete(clusterName string) error {
	clusterDir := m.GetClusterDir(clusterName)
	if err := os.RemoveAll(clusterDir); err != nil {
		return fmt.Errorf("failed to delete cluster directory: %w", err)
	}
	return nil
}

// List lists all clusters
func (m *Manager) List() ([]string, error) {
	if _, err := os.Stat(m.baseDir); os.IsNotExist(err) {
		return []string{}, nil
	}

	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read clusters directory: %w", err)
	}

	var clusters []string
	for _, entry := range entries {
		if entry.IsDir() {
			// Check if meta.yaml exists
			metaFile := filepath.Join(m.baseDir, entry.Name(), "meta.yaml")
			if _, err := os.Stat(metaFile); err == nil {
				clusters = append(clusters, entry.Name())
			}
		}
	}

	return clusters, nil
}

// UpdateStatus updates the cluster status
func (m *Manager) UpdateStatus(clusterName, status string) error {
	metadata, err := m.Load(clusterName)
	if err != nil {
		return err
	}

	metadata.Status = status
	return m.Save(metadata)
}
