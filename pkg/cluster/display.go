package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// displayText displays cluster info in text format
func (m *Manager) displayText(metadata *meta.ClusterMetadata) error {
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Cluster: %s\n", metadata.Name)
	fmt.Println(strings.Repeat("=", 60))

	fmt.Printf("Status:         %s\n", metadata.Status)
	fmt.Printf("MongoDB:        %s\n", metadata.Version)
	fmt.Printf("Deploy mode:    %s\n", metadata.DeployMode)
	fmt.Printf("Created:        %s\n", metadata.CreatedAt.Format(time.RFC3339))

	// Get topology type
	topoType := "unknown"
	if metadata.Topology != nil {
		topoType = metadata.Topology.GetTopologyType()
	}
	fmt.Printf("Topology:       %s\n", topoType)

	// Group nodes by type
	mongodNodes := []meta.NodeMetadata{}
	mongosNodes := []meta.NodeMetadata{}
	configNodes := []meta.NodeMetadata{}

	for _, node := range metadata.Nodes {
		switch node.Type {
		case "mongod":
			mongodNodes = append(mongodNodes, node)
		case "mongos":
			mongosNodes = append(mongosNodes, node)
		case "config":
			configNodes = append(configNodes, node)
		}
	}

	fmt.Println("\nNodes:")
	fmt.Println(strings.Repeat("-", 60))

	if len(configNodes) > 0 {
		fmt.Println("Config Servers:")
		for _, node := range configNodes {
			status := m.getNodeStatus(node)
			fmt.Printf("  - %s:%d (%s) [%s]\n", node.Host, node.Port, node.ReplicaSet, status)
		}
	}

	if len(mongodNodes) > 0 {
		fmt.Println("Mongod Servers:")
		for _, node := range mongodNodes {
			status := m.getNodeStatus(node)
			rsInfo := ""
			if node.ReplicaSet != "" {
				rsInfo = fmt.Sprintf(" (%s)", node.ReplicaSet)
			}
			fmt.Printf("  - %s:%d%s [%s]\n", node.Host, node.Port, rsInfo, status)
		}
	}

	if len(mongosNodes) > 0 {
		fmt.Println("Mongos Routers:")
		for _, node := range mongosNodes {
			status := m.getNodeStatus(node)
			fmt.Printf("  - %s:%d [%s]\n", node.Host, node.Port, status)
		}
	}

	fmt.Println("\nConnection:")
	fmt.Println(strings.Repeat("-", 60))
	connStr := m.getConnectionString(metadata)
	fmt.Printf("URI: %s\n", connStr)
	fmt.Printf("\nTo connect:\n  mongosh \"%s\"\n", connStr)

	fmt.Println("\nManagement:")
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("Start:   mup cluster start %s\n", metadata.Name)
	fmt.Printf("Stop:    mup cluster stop %s\n", metadata.Name)
	fmt.Printf("Destroy: mup cluster destroy %s\n", metadata.Name)

	fmt.Println(strings.Repeat("=", 60))

	return nil
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
