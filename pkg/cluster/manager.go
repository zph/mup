package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

	// LOCAL-021: config servers, then shard members, then mongos
	for _, group := range nodeGroupsForStart(nodesToStart) {
		if err := m.startNodeGroup(supMgr, group); err != nil {
			if saveErr := m.metaMgr.Save(metadata); saveErr != nil {
				return errors.Join(err, fmt.Errorf("failed to update metadata: %w", saveErr))
			}
			return err
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
func (m *Manager) startMonitoring(_ context.Context, clusterName string) error {
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

	// LOCAL-021: mongos first, then shard members, then config servers
	for _, group := range nodeGroupsForStop(nodesToStop) {
		names := programNamesOf(group)
		fmt.Printf("  Stopping %d %s node(s)...\n", len(names), group[0].Type)
		if err := supMgr.StopProcesses(names); err != nil {
			return fmt.Errorf("failed to stop processes: %w", err)
		}
		for _, node := range group {
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
func (m *Manager) stopMonitoring(_ context.Context, clusterName string) error {
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

// nodesNotRunning returns a description of each node whose supervisor process
// is not RUNNING, and records the PID of those that are.
func nodesNotRunning(nodes []*meta.NodeMetadata, getStatus func(name string) (*supervisor.ProcessStatus, error)) []string {
	var failed []string
	for _, node := range nodes {
		addr := fmt.Sprintf("%s %s:%d", node.Type, node.Host, node.Port)
		status, err := getStatus(node.SupervisorProgramName)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (status check failed: %v)", addr, err))
			continue
		}
		state := status.ProcessState()
		if !state.IsRunning() {
			failed = append(failed, fmt.Sprintf("%s (state: %s)", addr, state))
			continue
		}
		node.PID = status.PID
	}
	return failed
}

// Node types as recorded in cluster metadata.
const (
	nodeTypeConfig = "config"
	nodeTypeMongos = "mongos"
)

// nodeStartupTimeout bounds how long one group of nodes may take to reach RUNNING.
const (
	nodeStartupTimeout = 30 * time.Second
	nodeStartupPoll    = 200 * time.Millisecond
)

// nodeGroupsForStart splits nodes into the order a sharded cluster needs:
// config servers, then data-bearing members, then mongos. Empty groups are
// omitted, so a replica set or standalone is a single group.
// LOCAL-021
func nodeGroupsForStart(nodes []*meta.NodeMetadata) [][]*meta.NodeMetadata {
	var config, data, mongos []*meta.NodeMetadata
	for _, node := range nodes {
		switch node.Type {
		case nodeTypeConfig:
			config = append(config, node)
		case nodeTypeMongos:
			mongos = append(mongos, node)
		default:
			data = append(data, node)
		}
	}

	var groups [][]*meta.NodeMetadata
	for _, group := range [][]*meta.NodeMetadata{config, data, mongos} {
		if len(group) > 0 {
			groups = append(groups, group)
		}
	}
	return groups
}

// nodeGroupsForStop is the reverse of nodeGroupsForStart.
func nodeGroupsForStop(nodes []*meta.NodeMetadata) [][]*meta.NodeMetadata {
	groups := nodeGroupsForStart(nodes)
	for i, j := 0, len(groups)-1; i < j; i, j = i+1, j-1 {
		groups[i], groups[j] = groups[j], groups[i]
	}
	return groups
}

func programNamesOf(nodes []*meta.NodeMetadata) []string {
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		names = append(names, node.SupervisorProgramName)
	}
	return names
}

// startNodeGroup starts one group of nodes together and waits for all of them
// to reach RUNNING.
// LOCAL-012
func (m *Manager) startNodeGroup(supMgr *supervisor.Manager, nodes []*meta.NodeMetadata) error {
	names := programNamesOf(nodes)
	fmt.Printf("  Starting %d %s node(s)...\n", len(names), nodes[0].Type)
	if err := supMgr.StartProcesses(names); err != nil {
		return fmt.Errorf("failed to start processes: %w", err)
	}

	deadline := time.Now().Add(nodeStartupTimeout)
	started := make(map[string]bool)
	for time.Now().Before(deadline) && len(started) < len(nodes) {
		for _, node := range nodes {
			if started[node.SupervisorProgramName] {
				continue
			}
			status, err := supMgr.GetProcessStatus(node.SupervisorProgramName)
			if err == nil && status.ProcessState().IsRunning() {
				fmt.Printf("  ✓ %s %s:%d running (PID: %d)\n", node.Type, node.Host, node.Port, status.PID)
				started[node.SupervisorProgramName] = true
				node.PID = status.PID
			}
		}
		if len(started) < len(nodes) {
			time.Sleep(nodeStartupPoll)
		}
	}

	if failed := nodesNotRunning(nodes, supMgr.GetProcessStatus); len(failed) > 0 {
		for _, failure := range failed {
			fmt.Printf("  ✗ %s\n", failure)
		}
		return fmt.Errorf("%d of %d node(s) failed to start: %s", len(failed), len(nodes), strings.Join(failed, "; "))
	}
	return nil
}
