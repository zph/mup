package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/supervisor"
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

// Start starts a cluster using supervisor
func (m *Manager) Start(ctx context.Context, clusterName string, nodeFilter string) error {
	// Load metadata
	metadata, err := m.metaMgr.Load(clusterName)
	if err != nil {
		return err
	}

	// Load supervisor manager
	clusterDir := m.metaMgr.GetClusterDir(clusterName)
	supMgr, err := supervisor.LoadManager(clusterDir, clusterName)
	if err != nil {
		return fmt.Errorf("failed to load supervisor: %w", err)
	}

	fmt.Printf("Starting cluster '%s' via supervisor...\n", clusterName)

	// Start supervisor daemon if not already running
	if !supMgr.IsRunning() {
		fmt.Println("  Starting supervisor daemon...")
		if err := supMgr.Start(ctx); err != nil {
			return fmt.Errorf("failed to start supervisor: %w", err)
		}
	}

	// Collect program names to start in parallel
	var programNames []string
	var nodesToStart []*meta.NodeMetadata

	for i := range metadata.Nodes {
		node := &metadata.Nodes[i]
		if nodeFilter != "" && fmt.Sprintf("%s:%d", node.Host, node.Port) != nodeFilter {
			continue
		}

		// Use supervisor program name to start the process
		if node.SupervisorProgramName == "" {
			return fmt.Errorf("node %s:%d missing supervisor program name - cluster may need redeployment", node.Host, node.Port)
		}

		programNames = append(programNames, node.SupervisorProgramName)
		nodesToStart = append(nodesToStart, node)
	}

	// Start all processes in parallel
	if len(programNames) > 0 {
		fmt.Printf("  Starting %d node(s) in parallel...\n", len(programNames))
		if err := supMgr.StartProcesses(programNames); err != nil {
			return fmt.Errorf("failed to start processes: %w", err)
		}

		// Show live progress while processes are starting
		deadline := time.Now().Add(30 * time.Second)
		started := make(map[string]bool)

		for time.Now().Before(deadline) && len(started) < len(nodesToStart) {
			for _, node := range nodesToStart {
				if started[node.SupervisorProgramName] {
					continue
				}

				status, err := supMgr.GetProcessStatus(node.SupervisorProgramName)
				if err == nil && status.State == "Running" {
					fmt.Printf("  ✓ %s %s:%d running (PID: %d)\n", node.Type, node.Host, node.Port, status.PID)
					started[node.SupervisorProgramName] = true
					node.PID = status.PID
				}
			}
			if len(started) < len(nodesToStart) {
				time.Sleep(200 * time.Millisecond)
			}
		}

		// Get final status for each node and save PIDs
		for _, node := range nodesToStart {
			status, err := supMgr.GetProcessStatus(node.SupervisorProgramName)
			if err != nil {
				fmt.Printf("  ! Started %s %s:%d (status check failed: %v)\n", node.Type, node.Host, node.Port, err)
			} else {
				fmt.Printf("  ✓ Started %s %s:%d (PID: %d, state: %s)\n", node.Type, node.Host, node.Port, status.PID, status.State)
				// Save PID to metadata
				node.PID = status.PID
			}
		}
	}

	started := len(programNames)

	if started == 0 {
		return fmt.Errorf("no nodes matched filter '%s'", nodeFilter)
	}

	// Update status and save metadata
	if nodeFilter == "" {
		metadata.Status = "running"
		metadata.SupervisorRunning = true
	}
	if err := m.metaMgr.Save(metadata); err != nil {
		return fmt.Errorf("failed to update metadata: %w", err)
	}

	// Start monitoring if enabled (only when starting all nodes)
	if nodeFilter == "" && metadata.Monitoring != nil && metadata.Monitoring.Enabled {
		fmt.Println("\nStarting monitoring infrastructure...")
		if err := m.startMonitoring(ctx, clusterName); err != nil {
			fmt.Printf("  Warning: Failed to start monitoring: %v\n", err)
		} else {
			fmt.Println("  ✓ Monitoring started")
		}
	}

	fmt.Printf("\n✓ Started %d node(s) via supervisor\n", started)
	return nil
}

// startMonitoring starts the monitoring infrastructure
func (m *Manager) startMonitoring(ctx context.Context, clusterName string) error {
	// Monitoring is now managed by cluster supervisor via the "monitoring" group
	// Load cluster supervisor and start monitoring group
	clusterDir := m.metaMgr.GetClusterDir(clusterName)
	supMgr, err := supervisor.LoadManager(clusterDir, clusterName)
	if err != nil {
		return fmt.Errorf("failed to load supervisor: %w", err)
	}

	// Start the monitoring group (Victoria Metrics + Grafana)
	return supMgr.StartGroup("monitoring")
}

// Stop stops a cluster using supervisor
func (m *Manager) Stop(ctx context.Context, clusterName string, nodeFilter string) error {
	// Load metadata
	metadata, err := m.metaMgr.Load(clusterName)
	if err != nil {
		return err
	}

	// Load supervisor manager
	clusterDir := m.metaMgr.GetClusterDir(clusterName)
	supMgr, err := supervisor.LoadManager(clusterDir, clusterName)
	if err != nil {
		return fmt.Errorf("failed to load supervisor: %w", err)
	}

	fmt.Printf("Stopping cluster '%s'...\n", clusterName)

	// Check if supervisor is running
	if !supMgr.IsRunning() {
		fmt.Println("  ! Supervisor daemon not running - processes may already be stopped")
		metadata.Status = "stopped"
		metadata.SupervisorRunning = false
		if err := m.metaMgr.Save(metadata); err != nil {
			return fmt.Errorf("failed to update metadata: %w", err)
		}
		return nil
	}

	// Stop monitoring first if enabled and stopping all nodes
	// This must be done BEFORE stopping supervisor daemon
	if nodeFilter == "" && metadata.Monitoring != nil && metadata.Monitoring.Enabled {
		fmt.Println("Stopping monitoring infrastructure...")
		if err := m.stopMonitoring(ctx, clusterName); err != nil {
			fmt.Printf("  Warning: Failed to stop monitoring: %v\n", err)
		} else {
			fmt.Println("  ✓ Monitoring stopped")
		}
		fmt.Println()
	}

	// Collect program names to stop in parallel
	var programNames []string
	var nodesToStop []*meta.NodeMetadata

	for i := range metadata.Nodes {
		node := &metadata.Nodes[i]
		if nodeFilter != "" && fmt.Sprintf("%s:%d", node.Host, node.Port) != nodeFilter {
			continue
		}

		// Use supervisor program name to stop the process
		if node.SupervisorProgramName == "" {
			fmt.Printf("  ! Skipping %s %s:%d (no supervisor program name)\n", node.Type, node.Host, node.Port)
			continue
		}

		programNames = append(programNames, node.SupervisorProgramName)
		nodesToStop = append(nodesToStop, node)
	}

	// Stop all MongoDB processes in parallel
	if len(programNames) > 0 {
		fmt.Printf("  Stopping %d node(s) in parallel...\n", len(programNames))
		if err := supMgr.StopProcesses(programNames); err != nil {
			return fmt.Errorf("failed to stop processes: %w", err)
		}

		// Show stopped nodes
		for _, node := range nodesToStop {
			fmt.Printf("  ✓ Stopped %s %s:%d\n", node.Type, node.Host, node.Port)
		}
	}

	stopped := len(programNames)

	if stopped == 0 && nodeFilter != "" {
		return fmt.Errorf("no nodes matched filter '%s'", nodeFilter)
	}

	// If stopping all nodes, stop supervisor daemon last
	if nodeFilter == "" {
		fmt.Println("  Stopping supervisor daemon...")
		if err := supMgr.Stop(ctx); err != nil {
			fmt.Printf("  ! Warning: failed to stop supervisor daemon: %v\n", err)
		}
		metadata.Status = "stopped"
		metadata.SupervisorRunning = false
	}

	// Update status and save metadata
	if err := m.metaMgr.Save(metadata); err != nil {
		return fmt.Errorf("failed to update metadata: %w", err)
	}

	fmt.Printf("\n✓ Stopped %d node(s)\n", stopped)
	return nil
}

// stopMonitoring stops the monitoring infrastructure
func (m *Manager) stopMonitoring(ctx context.Context, clusterName string) error {
	// Monitoring is now managed by cluster supervisor via the "monitoring" group
	// Load cluster supervisor and stop monitoring group
	clusterDir := m.metaMgr.GetClusterDir(clusterName)
	supMgr, err := supervisor.LoadManager(clusterDir, clusterName)
	if err != nil {
		return fmt.Errorf("failed to load supervisor: %w", err)
	}

	// Stop the monitoring group (Victoria Metrics + Grafana)
	return supMgr.StopGroup("monitoring")
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
