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
	Name       string               `yaml:"name"`
	Version    string               `yaml:"version"`
	CreatedAt  time.Time            `yaml:"created_at"`
	Status     string               `yaml:"status"`
	Topology   *topology.Topology   `yaml:"topology"`
	DeployMode string               `yaml:"deploy_mode"` // "local" or "remote"
	Nodes      []NodeMetadata       `yaml:"nodes"`
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
	PID        int    `yaml:"pid,omitempty"`
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
