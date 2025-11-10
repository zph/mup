package playground

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zph/mongo-scaffold/pkg/cluster"
	"github.com/zph/mongo-scaffold/pkg/mongocluster"
)

const (
	defaultStateDir = ".mup/playground"
	stateFileName   = "state.json"
)

// State represents the current state of the playground cluster
type State struct {
	Name             string    `json:"name"`
	Status           string    `json:"status"` // running, stopped
	StartedAt        time.Time `json:"started_at"`
	MongoVersion     string    `json:"mongo_version"`
	ConnectionURI    string    `json:"connection_uri"`
	DataDir          string    `json:"data_dir"`
	ClusterInfoFile  string    `json:"cluster_info_file"`
	MongosHosts      []string  `json:"mongos_hosts,omitempty"`
}

// Manager handles playground cluster lifecycle
type Manager struct {
	stateDir string
	cluster  *cluster.Cluster
}

// NewManager creates a new playground manager
func NewManager() (*Manager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	stateDir := filepath.Join(homeDir, defaultStateDir)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	return &Manager{
		stateDir: stateDir,
	}, nil
}

// Start launches a new playground cluster
func (m *Manager) Start(ctx context.Context, mongoVersion string) error {
	// Check if already running
	state, err := m.LoadState()
	if err == nil && state.Status == "running" {
		return fmt.Errorf("playground cluster already running")
	}

	// Create cluster info file path
	clusterInfoFile := filepath.Join(m.stateDir, "cluster-info.json")

	// Create cluster configuration using defaults
	// The library works best with its default configuration
	cfg := mongocluster.StartConfig{
		MongoVersion:   mongoVersion,
		Shards:         mongocluster.DefaultShards,        // 2 shards
		ReplicaNodes:   mongocluster.DefaultReplicaNodes,  // 3 nodes per shard
		ConfigServers:  mongocluster.DefaultConfigServers, // 1 config server
		MongosCount:    mongocluster.DefaultMongosCount,   // 1 mongos
		Auth:           false,
		Background:     true, // Run in background
		OutputFile:     clusterInfoFile,
		FileOverwrite:  true,
		StartupTimeout: 2 * time.Minute,
	}

	// Start the cluster
	result, err := mongocluster.Start(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to start cluster: %w", err)
	}

	m.cluster = result.Cluster

	// Save state
	state = &State{
		Name:            "playground",
		Status:          "running",
		StartedAt:       time.Now(),
		MongoVersion:    mongoVersion,
		ConnectionURI:   result.Cluster.ConnectionString(),
		DataDir:         result.Cluster.DataDir(),
		ClusterInfoFile: clusterInfoFile,
		MongosHosts:     result.Cluster.MongosHosts(),
	}

	if err := m.SaveState(state); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	return nil
}

// Stop shuts down the playground cluster
func (m *Manager) Stop(ctx context.Context) error {
	state, err := m.LoadState()
	if err != nil {
		return fmt.Errorf("no running playground cluster found: %w", err)
	}

	if state.Status != "running" {
		return fmt.Errorf("playground cluster is not running")
	}

	// Stop using the cluster info file
	if state.ClusterInfoFile != "" {
		if err := mongocluster.Stop(state.ClusterInfoFile, nil); err != nil {
			// If the file doesn't exist, the cluster might have already been stopped
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to stop cluster: %w", err)
			}
		}
	} else if m.cluster != nil {
		// Fallback: use cluster reference if available
		if err := m.cluster.Teardown(); err != nil {
			return fmt.Errorf("failed to stop cluster: %w", err)
		}
	}

	// Update state
	state.Status = "stopped"
	if err := m.SaveState(state); err != nil {
		return fmt.Errorf("failed to update state: %w", err)
	}

	return nil
}

// Status returns the current playground cluster status
func (m *Manager) Status() (*State, error) {
	return m.LoadState()
}

// GetClusterInfo reads and returns the cluster info from the JSON file
func (m *Manager) GetClusterInfo() (*mongocluster.ClusterInfo, error) {
	state, err := m.LoadState()
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	if state.ClusterInfoFile == "" {
		return nil, fmt.Errorf("no cluster info file found in state")
	}

	return mongocluster.ReadClusterInfoFromFile(state.ClusterInfoFile)
}

// Destroy completely removes the playground cluster and all data
func (m *Manager) Destroy(ctx context.Context) error {
	// Try to stop if running
	state, err := m.LoadState()
	if err == nil && state.Status == "running" {
		if err := m.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to stop cluster: %v\n", err)
		}
	}

	// Remove data directory
	if state != nil && state.DataDir != "" {
		if err := os.RemoveAll(state.DataDir); err != nil {
			return fmt.Errorf("failed to remove data directory: %w", err)
		}
	}

	// Remove state file
	stateFile := filepath.Join(m.stateDir, stateFileName)
	if err := os.Remove(stateFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove state file: %w", err)
	}

	return nil
}

// SaveState persists the cluster state to disk
func (m *Manager) SaveState(state *State) error {
	stateFile := filepath.Join(m.stateDir, stateFileName)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// LoadState reads the cluster state from disk
func (m *Manager) LoadState() (*State, error) {
	stateFile := filepath.Join(m.stateDir, stateFileName)
	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no playground cluster state found")
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return &state, nil
}
