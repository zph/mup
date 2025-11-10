package cluster

import (
	"context"
	"fmt"

	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
)

// Manager manages cluster lifecycle operations
type Manager struct {
	metaMgr *meta.Manager
}

// NewManager creates a new cluster manager
func NewManager() (*Manager, error) {
	metaMgr, err := meta.NewManager()
	if err != nil {
		return nil, err
	}

	return &Manager{
		metaMgr: metaMgr,
	}, nil
}

// Start starts a cluster
func (m *Manager) Start(ctx context.Context, clusterName string, nodeFilter string) error {
	// Load metadata
	metadata, err := m.metaMgr.Load(clusterName)
	if err != nil {
		return err
	}

	// Create executors
	executors, err := m.createExecutors(metadata)
	if err != nil {
		return fmt.Errorf("failed to create executors: %w", err)
	}
	defer m.closeExecutors(executors)

	fmt.Printf("Starting cluster '%s'...\n", clusterName)

	// Start nodes
	started := 0
	for i := range metadata.Nodes {
		node := &metadata.Nodes[i] // Use pointer to modify in place
		if nodeFilter != "" && fmt.Sprintf("%s:%d", node.Host, node.Port) != nodeFilter {
			continue
		}

		exec := executors[node.Host]
		if err := m.startNode(exec, node, metadata.BinPath); err != nil {
			return fmt.Errorf("failed to start %s %s:%d: %w", node.Type, node.Host, node.Port, err)
		}

		fmt.Printf("  ✓ Started %s %s:%d (PID: %d)\n", node.Type, node.Host, node.Port, node.PID)
		started++
	}

	if started == 0 {
		return fmt.Errorf("no nodes matched filter '%s'", nodeFilter)
	}

	// Update status and save metadata (including PIDs)
	if nodeFilter == "" {
		metadata.Status = "running"
	}
	if err := m.metaMgr.Save(metadata); err != nil {
		return fmt.Errorf("failed to update metadata: %w", err)
	}

	fmt.Printf("\n✓ Started %d node(s)\n", started)
	return nil
}

// Stop stops a cluster
func (m *Manager) Stop(ctx context.Context, clusterName string, nodeFilter string) error {
	// Load metadata
	metadata, err := m.metaMgr.Load(clusterName)
	if err != nil {
		return err
	}

	// Create executors
	executors, err := m.createExecutors(metadata)
	if err != nil {
		return fmt.Errorf("failed to create executors: %w", err)
	}
	defer m.closeExecutors(executors)

	fmt.Printf("Stopping cluster '%s'...\n", clusterName)

	// Stop nodes
	stopped := 0
	for i := range metadata.Nodes {
		node := &metadata.Nodes[i] // Use pointer to modify in place
		if nodeFilter != "" && fmt.Sprintf("%s:%d", node.Host, node.Port) != nodeFilter {
			continue
		}

		exec := executors[node.Host]
		if err := m.stopNode(exec, node); err != nil {
			// Log error but continue
			fmt.Printf("  ! Failed to stop %s %s:%d: %v\n", node.Type, node.Host, node.Port, err)
			continue
		}

		fmt.Printf("  ✓ Stopped %s %s:%d\n", node.Type, node.Host, node.Port)
		stopped++
	}

	if stopped == 0 && nodeFilter != "" {
		return fmt.Errorf("no nodes matched filter '%s'", nodeFilter)
	}

	// Update status and save metadata (including cleared PIDs)
	if nodeFilter == "" {
		metadata.Status = "stopped"
	}
	if err := m.metaMgr.Save(metadata); err != nil {
		return fmt.Errorf("failed to update metadata: %w", err)
	}

	fmt.Printf("\n✓ Stopped %d node(s)\n", stopped)
	return nil
}

// startNode starts a single MongoDB node and updates PID in metadata
func (m *Manager) startNode(exec executor.Executor, node *meta.NodeMetadata, binPath string) error {
	var command string
	var binaryName string

	switch node.Type {
	case "mongod", "config":
		binaryName = "mongod"
	case "mongos":
		binaryName = "mongos"
	default:
		return fmt.Errorf("unknown node type: %s", node.Type)
	}

	// Use full path to versioned binary if binPath is provided
	if binPath != "" {
		command = fmt.Sprintf("%s/%s --config %s", binPath, binaryName, node.ConfigFile)
	} else {
		// Fallback to binary in PATH (for backward compatibility)
		command = fmt.Sprintf("%s --config %s", binaryName, node.ConfigFile)
	}

	pid, err := exec.Background(command)
	if err != nil {
		return err
	}

	// Update PID in the node metadata (passed by pointer)
	node.PID = pid
	return nil
}

// stopNode stops a single MongoDB node using PID from metadata
func (m *Manager) stopNode(exec executor.Executor, node *meta.NodeMetadata) error {
	// Check if we have a PID in metadata
	if node.PID == 0 {
		// No PID stored - process might not be running or wasn't tracked
		return nil
	}

	// Use PID from metadata - send SIGINT for graceful shutdown
	if err := exec.StopProcess(node.PID); err != nil {
		return fmt.Errorf("failed to stop process %d: %w", node.PID, err)
	}

	// Clear PID from metadata
	node.PID = 0
	return nil
}

// createExecutors creates executors for the cluster
func (m *Manager) createExecutors(metadata *meta.ClusterMetadata) (map[string]executor.Executor, error) {
	executors := make(map[string]executor.Executor)

	// Get unique hosts
	hosts := make(map[string]bool)
	for _, node := range metadata.Nodes {
		hosts[node.Host] = true
	}

	// Create executor for each host
	for host := range hosts {
		if metadata.DeployMode == "local" {
			executors[host] = executor.NewLocalExecutor()
		} else {
			// TODO: Create SSH executor
			return nil, fmt.Errorf("remote mode not yet implemented")
		}
	}

	return executors, nil
}

// closeExecutors closes all executors
func (m *Manager) closeExecutors(executors map[string]executor.Executor) {
	for _, exec := range executors {
		exec.Close()
	}
}
