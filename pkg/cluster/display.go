package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zph/mup/pkg/cluster/health"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
)

// Display shows cluster information
func (m *Manager) Display(ctx context.Context, clusterName string, format string) error {
	// Load metadata
	metadata, err := m.metaMgr.Load(clusterName)
	if err != nil {
		return err
	}

	switch format {
	case "text":
		return m.displayText(metadata)
	case "yaml":
		return m.displayYAML(metadata)
	case "json":
		return m.displayJSON(metadata)
	default:
		return fmt.Errorf("unknown format: %s", format)
	}
}

// displayText displays cluster info in text format with comprehensive health checks
func (m *Manager) displayText(metadata *meta.ClusterMetadata) error {
	// Perform health checks
	exec := executor.NewLocalExecutor()
	defer exec.Close()

	checker, err := health.NewChecker(metadata, exec)
	if err != nil {
		return fmt.Errorf("failed to create health checker: %w", err)
	}

	ctx := context.Background()
	clusterHealth, err := checker.Check(ctx)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	// Display header
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("Cluster: %s\n", metadata.Name)
	fmt.Println(strings.Repeat("=", 70))

	fmt.Printf("MongoDB Version: %s\n", metadata.Version)
	fmt.Printf("Deploy Mode:     %s\n", metadata.DeployMode)
	fmt.Printf("Created:         %s\n", metadata.CreatedAt.Format("2006-01-02 15:04:05"))

	// Get topology type
	topoType := "unknown"
	if metadata.Topology != nil {
		topoType = metadata.Topology.GetTopologyType()
	}
	fmt.Printf("Topology Type:   %s\n", topoType)

	// Display nodes with health status
	fmt.Println("\n" + strings.Repeat("━", 70))
	fmt.Println("NODES")
	fmt.Println(strings.Repeat("━", 70))

	// Group nodes by type with health info
	mongodNodes := []health.NodeHealth{}
	mongosNodes := []health.NodeHealth{}
	configNodes := []health.NodeHealth{}

	for _, nodeHealth := range clusterHealth.Nodes {
		switch nodeHealth.Type {
		case "mongod":
			mongodNodes = append(mongodNodes, nodeHealth)
		case "mongos":
			mongosNodes = append(mongosNodes, nodeHealth)
		case "config":
			configNodes = append(configNodes, nodeHealth)
		}
	}

	if len(configNodes) > 0 {
		fmt.Println("\nConfig Servers:")
		for _, node := range configNodes {
			m.displayNodeHealth(node)
		}
	}

	if len(mongodNodes) > 0 {
		fmt.Println("\nMongod Servers:")
		for _, node := range mongodNodes {
			m.displayNodeHealth(node)
		}
	}

	if len(mongosNodes) > 0 {
		fmt.Println("\nMongos Routers:")
		for _, node := range mongosNodes {
			m.displayNodeHealth(node)
		}
	}

	// Display monitoring if enabled
	if clusterHealth.Monitoring.Enabled {
		m.displayMonitoringHealth(clusterHealth.Monitoring, metadata)
	}

	// Display port mapping
	m.displayPortMapping(clusterHealth.Ports)

	// Display connection info
	fmt.Println("\n" + strings.Repeat("━", 70))
	fmt.Println("CONNECTION")
	fmt.Println(strings.Repeat("━", 70))
	connStr := m.getConnectionString(metadata)
	fmt.Printf("%s\n", connStr)
	fmt.Printf("\nTo connect:\n  mongosh \"%s\"\n", connStr)
	if metadata.ConnectionCommand != "" {
		fmt.Printf("\nOr use:\n  %s\n", metadata.ConnectionCommand)
	}

	// Display management commands
	fmt.Println("\n" + strings.Repeat("━", 70))
	fmt.Println("MANAGEMENT")
	fmt.Println(strings.Repeat("━", 70))
	fmt.Printf("Start cluster:   mup cluster start %s\n", metadata.Name)
	fmt.Printf("Stop cluster:    mup cluster stop %s\n", metadata.Name)
	fmt.Printf("Cluster status:  mup cluster display %s\n", metadata.Name)
	fmt.Printf("Destroy cluster: mup cluster destroy %s\n", metadata.Name)

	if clusterHealth.Monitoring.Enabled {
		fmt.Printf("\nMonitoring:\n")
		fmt.Printf("View dashboards: mup monitoring dashboard %s\n", metadata.Name)
		fmt.Printf("View status:     mup monitoring status %s\n", metadata.Name)
	}

	fmt.Println(strings.Repeat("=", 70))

	return nil
}

// displayNodeHealth displays detailed health info for a node
func (m *Manager) displayNodeHealth(node health.NodeHealth) {
	// Status indicator
	statusIcon := m.getStatusIcon(node.Status)

	// Build node description
	desc := fmt.Sprintf("%s:%d", node.Host, node.Port)
	if node.ReplicaSet != "" {
		desc += fmt.Sprintf(" [%s]", node.ReplicaSet)
	}

	// Build status line
	statusLine := fmt.Sprintf("%s %s", statusIcon, node.Status)
	if node.Uptime > 0 {
		statusLine += fmt.Sprintf(" (uptime: %s)", formatDuration(node.Uptime))
	}
	if node.PID > 0 {
		statusLine += fmt.Sprintf(" PID: %d", node.PID)
	}

	fmt.Printf("  %-40s %s\n", desc, statusLine)
}

// displayMonitoringHealth displays monitoring infrastructure health
func (m *Manager) displayMonitoringHealth(mon health.MonitoringHealth, metadata *meta.ClusterMetadata) {
	fmt.Println("\n" + strings.Repeat("━", 70))
	fmt.Println("MONITORING")
	fmt.Println(strings.Repeat("━", 70))

	// Victoria Metrics
	vmIcon := m.getStatusIcon(mon.VictoriaMetrics.Status)
	fmt.Printf("Victoria Metrics: %s %s\n", vmIcon, mon.VictoriaMetrics.Status)
	if mon.VictoriaMetrics.URL != "" {
		fmt.Printf("  URL: %s\n", mon.VictoriaMetrics.URL)
	}

	// Grafana
	grafanaIcon := m.getStatusIcon(mon.Grafana.Status)
	fmt.Printf("\nGrafana:          %s %s\n", grafanaIcon, mon.Grafana.Status)
	if mon.Grafana.URL != "" {
		fmt.Printf("  URL: %s\n", mon.Grafana.URL)
		fmt.Printf("  User: admin\n")
	}

	// Exporters
	if len(mon.Exporters) > 0 {
		fmt.Println("\nExporters:")
		for _, exp := range mon.Exporters {
			icon := m.getStatusIcon(exp.Status)
			fmt.Printf("  %s (%s:%d): %s %s\n",
				exp.Type, exp.Host, exp.Port, icon, exp.Status)
		}
	}
}

// displayPortMapping displays all ports used by the cluster
func (m *Manager) displayPortMapping(ports health.PortMapping) {
	fmt.Println("\n" + strings.Repeat("━", 70))
	fmt.Println("PORTS")
	fmt.Println(strings.Repeat("━", 70))

	if len(ports.MongoDB) > 0 {
		fmt.Println("\nMongoDB:")
		for _, port := range ports.MongoDB {
			icon := m.getStatusIcon(port.Status)
			fmt.Printf("  %s:%d  %s %s - %s\n",
				port.Host, port.Port, icon, port.Status, port.Description)
		}
	}

	if len(ports.Monitoring) > 0 {
		fmt.Println("\nMonitoring:")
		for _, port := range ports.Monitoring {
			icon := m.getStatusIcon(port.Status)
			fmt.Printf("  %s:%d  %s %s - %s\n",
				port.Host, port.Port, icon, port.Status, port.Description)
		}
	}

	if len(ports.Supervisor) > 0 {
		fmt.Println("\nSupervisor APIs:")
		for _, port := range ports.Supervisor {
			fmt.Printf("  %s:%d  - %s\n",
				port.Host, port.Port, port.Description)
		}
	}
}

// getStatusIcon returns a visual indicator for status
func (m *Manager) getStatusIcon(status string) string {
	switch status {
	case "running":
		return "✓"
	case "stopped":
		return "✗"
	case "starting":
		return "⟳"
	case "failed":
		return "✗"
	default:
		return "?"
	}
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd %dh", days, hours)
}

// displayYAML displays cluster info in YAML format
func (m *Manager) displayYAML(metadata *meta.ClusterMetadata) error {
	// TODO: Implement YAML output
	return fmt.Errorf("YAML format not yet implemented")
}

// displayJSON displays cluster info in JSON format
func (m *Manager) displayJSON(metadata *meta.ClusterMetadata) error {
	// TODO: Implement JSON output
	return fmt.Errorf("JSON format not yet implemented")
}

// getNodeStatus checks if a node is running
func (m *Manager) getNodeStatus(node meta.NodeMetadata) string {
	// Create executor
	exec := executor.NewLocalExecutor()
	defer exec.Close()

	// Check if port is in use
	available, err := exec.CheckPortAvailable(node.Port)
	if err != nil {
		return "unknown"
	}

	if !available {
		return "running"
	}

	return "stopped"
}

// getConnectionString builds the connection string
func (m *Manager) getConnectionString(metadata *meta.ClusterMetadata) string {
	if metadata.Topology == nil {
		return ""
	}

	topoType := metadata.Topology.GetTopologyType()

	switch topoType {
	case "sharded":
		// Connect via mongos
		for _, node := range metadata.Nodes {
			if node.Type == "mongos" {
				return fmt.Sprintf("mongodb://%s:%d", node.Host, node.Port)
			}
		}

	case "replica_set":
		// Build replica set connection string
		rsName := ""
		var hosts []string

		for _, node := range metadata.Nodes {
			if node.Type == "mongod" && node.ReplicaSet != "" {
				if rsName == "" {
					rsName = node.ReplicaSet
				}
				if rsName == node.ReplicaSet {
					hosts = append(hosts, fmt.Sprintf("%s:%d", node.Host, node.Port))
				}
			}
		}

		if len(hosts) > 0 {
			return fmt.Sprintf("mongodb://%s/?replicaSet=%s", strings.Join(hosts, ","), rsName)
		}

	case "standalone":
		// Connect to single mongod
		for _, node := range metadata.Nodes {
			if node.Type == "mongod" {
				return fmt.Sprintf("mongodb://%s:%d", node.Host, node.Port)
			}
		}
	}

	return "mongodb://localhost:27017"
}
