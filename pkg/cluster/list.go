package cluster

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/zph/mup/pkg/meta"
)

// ClusterSummary represents a summary of a cluster for listing
type ClusterSummary struct {
	Name       string `json:"name" yaml:"name"`
	Status     string `json:"status" yaml:"status"`
	Version    string `json:"version" yaml:"version"`
	Topology   string `json:"topology" yaml:"topology"`
	DeployMode string `json:"deploy_mode" yaml:"deploy_mode"`
	Nodes      int    `json:"nodes" yaml:"nodes"`
	CreatedAt  string `json:"created_at" yaml:"created_at"`
}

// List lists all managed clusters
func (m *Manager) List(format string) error {
	// Get list of cluster names
	clusterNames, err := m.metaMgr.List()
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	if len(clusterNames) == 0 {
		fmt.Println("No clusters found")
		fmt.Println("\nTo create a cluster:")
		fmt.Println("  mup cluster deploy <cluster-name> <topology-file>")
		fmt.Println("  mup playground start")
		return nil
	}

	// Load metadata for each cluster
	var summaries []ClusterSummary
	for _, name := range clusterNames {
		metadata, err := m.metaMgr.Load(name)
		if err != nil {
			// Skip clusters that can't be loaded
			continue
		}

		summary := ClusterSummary{
			Name:       metadata.Name,
			Status:     metadata.Status,
			Version:    metadata.Version,
			Topology:   getTopologyType(metadata),
			DeployMode: metadata.DeployMode,
			Nodes:      len(metadata.Nodes),
			CreatedAt:  metadata.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		summaries = append(summaries, summary)
	}

	// Display based on format
	switch format {
	case "json":
		return m.listJSON(summaries)
	case "yaml":
		return m.listYAML(summaries)
	default:
		return m.listText(summaries)
	}
}

// listText displays clusters in text format
func (m *Manager) listText(summaries []ClusterSummary) error {
	fmt.Println("============================================================")
	fmt.Printf("Clusters (%d)\n", len(summaries))
	fmt.Println("============================================================")
	fmt.Println()

	// Calculate column widths
	nameWidth := 15
	statusWidth := 10
	versionWidth := 8
	topologyWidth := 12
	modeWidth := 8
	nodesWidth := 6

	// Print header
	fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s\n",
		nameWidth, "NAME",
		statusWidth, "STATUS",
		versionWidth, "VERSION",
		topologyWidth, "TOPOLOGY",
		modeWidth, "MODE",
		nodesWidth, "NODES",
		"CREATED")
	fmt.Println("------------------------------------------------------------")

	// Print each cluster
	for _, s := range summaries {
		fmt.Printf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*d  %s\n",
			nameWidth, s.Name,
			statusWidth, s.Status,
			versionWidth, s.Version,
			topologyWidth, s.Topology,
			modeWidth, s.DeployMode,
			nodesWidth, s.Nodes,
			s.CreatedAt)
	}

	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("------------------------------------------------------------")
	fmt.Println("View details:    mup cluster display <cluster-name>")
	fmt.Println("Start cluster:   mup cluster start <cluster-name>")
	fmt.Println("Stop cluster:    mup cluster stop <cluster-name>")
	fmt.Println("Destroy cluster: mup cluster destroy <cluster-name>")
	fmt.Println("============================================================")

	return nil
}

// listYAML displays clusters in YAML format
func (m *Manager) listYAML(summaries []ClusterSummary) error {
	data, err := yaml.Marshal(map[string]interface{}{
		"clusters": summaries,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal to YAML: %w", err)
	}

	fmt.Println(string(data))
	return nil
}

// listJSON displays clusters in JSON format
func (m *Manager) listJSON(summaries []ClusterSummary) error {
	data, err := json.MarshalIndent(map[string]interface{}{
		"clusters": summaries,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal to JSON: %w", err)
	}

	fmt.Println(string(data))
	return nil
}

// getTopologyType returns the topology type from metadata
func getTopologyType(metadata *meta.ClusterMetadata) string {
	if metadata.Topology != nil {
		return metadata.Topology.GetTopologyType()
	}
	// Fallback: try to determine from nodes
	hasMongos := false
	hasConfig := false
	for _, node := range metadata.Nodes {
		if node.Type == "mongos" {
			hasMongos = true
		}
		if node.Type == "config" {
			hasConfig = true
		}
	}
	if hasMongos && hasConfig {
		return "sharded"
	}
	if len(metadata.Nodes) > 0 && metadata.Nodes[0].ReplicaSet != "" {
		return "replica_set"
	}
	return "standalone"
}
