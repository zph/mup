package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/go-version"
	"gopkg.in/yaml.v3"

	"github.com/zph/mup/pkg/topology"
)

// finalize implements Phase 5: Finalize
// - Save cluster metadata
// - Display cluster information
// - Provide connection instructions
func (d *Deployer) finalize(ctx context.Context) error {
	fmt.Println("Phase 5: Finalize")
	fmt.Println("=================")

	// Step 1: Save metadata
	if err := d.saveMetadata(ctx); err != nil {
		return fmt.Errorf("failed to save metadata: %w", err)
	}

	// Step 2: Display cluster info
	d.displayClusterInfo()

	fmt.Println("✓ Phase 5 complete: Deployment finalized")
	return nil
}

// ClusterMetadata represents the stored cluster state
type ClusterMetadata struct {
	Name              string               `yaml:"name"`
	Version           string               `yaml:"version"`
	BinPath           string               `yaml:"bin_path"`     // Path to MongoDB binaries
	CreatedAt         time.Time            `yaml:"created_at"`
	Status            string               `yaml:"status"`
	Topology          *topology.Topology   `yaml:"topology"`
	DeployMode        string               `yaml:"deploy_mode"` // "local" or "remote"
	Nodes             []NodeMetadata       `yaml:"nodes"`
	ConnectionCommand string               `yaml:"connection_command,omitempty"` // Command to connect to cluster
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

// saveMetadata saves the cluster metadata to disk
func (d *Deployer) saveMetadata(ctx context.Context) error {
	fmt.Println("Saving cluster metadata...")

	// Create metadata directory
	if err := os.MkdirAll(d.metaDir, 0755); err != nil {
		return fmt.Errorf("failed to create metadata directory: %w", err)
	}

	// Build metadata
	connectionString := d.getConnectionString()
	connectionCommand := d.getConnectionCommand(connectionString)

	metadata := ClusterMetadata{
		Name:              d.clusterName,
		Version:           d.version,
		BinPath:           d.binPath,
		CreatedAt:         time.Now(),
		Status:            "running",
		Topology:          d.topology,
		DeployMode: func() string {
			if d.isLocal {
				return "local"
			}
			return "remote"
		}(),
		Nodes:             d.collectNodeMetadata(),
		ConnectionCommand: connectionCommand,
	}

	// Serialize to YAML
	data, err := yaml.Marshal(&metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Write to file
	metaFile := filepath.Join(d.metaDir, "meta.yaml")
	if err := os.WriteFile(metaFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	fmt.Printf("  ✓ Metadata saved: %s\n", metaFile)
	return nil
}

// collectNodeMetadata collects metadata for all nodes
func (d *Deployer) collectNodeMetadata() []NodeMetadata {
	var nodes []NodeMetadata

	// Collect mongod nodes
	for _, node := range d.topology.Mongod {
		configDir := d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir)
		nodeKey := fmt.Sprintf("%s:%d", node.Host, node.Port)
		nodes = append(nodes, NodeMetadata{
			Type:       "mongod",
			Host:       node.Host,
			Port:       node.Port,
			ReplicaSet: node.ReplicaSet,
			DataDir:    d.getNodeDataDir(node.Host, node.Port, node.DataDir),
			LogDir:     d.getNodeLogDir(node.Host, node.Port, node.LogDir),
			ConfigDir:  configDir,
			ConfigFile: filepath.Join(configDir, "mongod.conf"),
			PID:        d.nodePIDs[nodeKey],
		})
	}

	// Collect mongos nodes
	for _, node := range d.topology.Mongos {
		configDir := d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir)
		nodeKey := fmt.Sprintf("%s:%d", node.Host, node.Port)
		nodes = append(nodes, NodeMetadata{
			Type:       "mongos",
			Host:       node.Host,
			Port:       node.Port,
			LogDir:     d.getNodeLogDir(node.Host, node.Port, node.LogDir),
			ConfigDir:  configDir,
			ConfigFile: filepath.Join(configDir, "mongos.conf"),
			PID:        d.nodePIDs[nodeKey],
		})
	}

	// Collect config server nodes
	for _, node := range d.topology.ConfigSvr {
		configDir := d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir)
		nodeKey := fmt.Sprintf("%s:%d", node.Host, node.Port)
		nodes = append(nodes, NodeMetadata{
			Type:       "config",
			Host:       node.Host,
			Port:       node.Port,
			ReplicaSet: node.ReplicaSet,
			DataDir:    d.getNodeDataDir(node.Host, node.Port, node.DataDir),
			LogDir:     d.getNodeLogDir(node.Host, node.Port, node.LogDir),
			ConfigDir:  configDir,
			ConfigFile: filepath.Join(configDir, "mongod.conf"),
			PID:        d.nodePIDs[nodeKey],
		})
	}

	return nodes
}

// displayClusterInfo displays information about the deployed cluster
func (d *Deployer) displayClusterInfo() {
	fmt.Println("\n" + repeatString("=", 60))
	fmt.Println("Cluster Deployed Successfully!")
	fmt.Println(repeatString("=", 60))
	fmt.Printf("Cluster name:    %s\n", d.clusterName)
	fmt.Printf("MongoDB version: %s\n", d.version)
	fmt.Printf("Topology type:   %s\n", d.topology.GetTopologyType())
	fmt.Printf("Deploy mode:     %s\n", func() string {
		if d.isLocal {
			return "local"
		}
		return "remote"
	}())

	fmt.Println("\nNodes:")
	fmt.Println(repeatString("-", 60))

	// Display mongod nodes
	if len(d.topology.Mongod) > 0 {
		fmt.Println("Mongod Servers:")
		for _, node := range d.topology.Mongod {
			rsInfo := ""
			if node.ReplicaSet != "" {
				rsInfo = fmt.Sprintf(" (replica set: %s)", node.ReplicaSet)
			}
			fmt.Printf("  - %s:%d%s\n", node.Host, node.Port, rsInfo)
		}
	}

	// Display mongos nodes
	if len(d.topology.Mongos) > 0 {
		fmt.Println("Mongos Routers:")
		for _, node := range d.topology.Mongos {
			fmt.Printf("  - %s:%d\n", node.Host, node.Port)
		}
	}

	// Display config servers
	if len(d.topology.ConfigSvr) > 0 {
		fmt.Println("Config Servers:")
		for _, node := range d.topology.ConfigSvr {
			fmt.Printf("  - %s:%d (replica set: %s)\n", node.Host, node.Port, node.ReplicaSet)
		}
	}

	fmt.Println("\nConnection:")
	fmt.Println(repeatString("-", 60))

	// Display connection instructions
	connectionString := d.getConnectionString()
	fmt.Printf("Connection URI: %s\n", connectionString)
	fmt.Printf("\nTo connect:\n")
	fmt.Printf("  mongosh \"%s\"\n", connectionString)

	fmt.Println("\nManagement:")
	fmt.Println(repeatString("-", 60))
	fmt.Printf("Start cluster:   mup cluster start %s\n", d.clusterName)
	fmt.Printf("Stop cluster:    mup cluster stop %s\n", d.clusterName)
	fmt.Printf("Cluster status:  mup cluster display %s\n", d.clusterName)
	fmt.Printf("Destroy cluster: mup cluster destroy %s\n", d.clusterName)

	fmt.Println("\n" + repeatString("=", 60))
}

// getConnectionString builds the MongoDB connection string
func (d *Deployer) getConnectionString() string {
	topoType := d.topology.GetTopologyType()

	switch topoType {
	case "sharded":
		// Connect via mongos
		if len(d.topology.Mongos) > 0 {
			mongos := d.topology.Mongos[0]
			return fmt.Sprintf("mongodb://%s:%d", mongos.Host, mongos.Port)
		}

	case "replica_set":
		// Build replica set connection string
		if len(d.topology.Mongod) > 0 {
			rsName := d.topology.Mongod[0].ReplicaSet
			var hosts []string
			for _, node := range d.topology.Mongod {
				if node.ReplicaSet == rsName {
					hosts = append(hosts, fmt.Sprintf("%s:%d", node.Host, node.Port))
				}
			}
			return fmt.Sprintf("mongodb://%s/?replicaSet=%s", joinStrings(hosts, ","), rsName)
		}

	case "standalone":
		// Connect to single mongod
		if len(d.topology.Mongod) > 0 {
			node := d.topology.Mongod[0]
			return fmt.Sprintf("mongodb://%s:%d", node.Host, node.Port)
		}
	}

	return "mongodb://localhost:27017"
}

// getConnectionCommand builds the command to connect to the cluster
func (d *Deployer) getConnectionCommand(connectionString string) string {
	// Use mongosh from BinPath for MongoDB >= 4.0, mongo for older versions
	// The command will be executed via shell, so we need to quote the connection string
	v, err := version.NewVersion(d.version)
	if err != nil {
		// If version parsing fails, default to mongosh from BinPath
		mongoshPath := filepath.Join(d.binPath, "mongosh")
		return fmt.Sprintf("%s \"%s\"", mongoshPath, connectionString)
	}

	// Check if version is >= 4.0
	constraint, err := version.NewConstraint(">= 4.0")
	if err != nil {
		// If constraint parsing fails, default to mongosh from BinPath
		mongoshPath := filepath.Join(d.binPath, "mongosh")
		return fmt.Sprintf("%s \"%s\"", mongoshPath, connectionString)
	}

	if constraint.Check(v) {
		// MongoDB >= 4.0: use mongosh from BinPath
		mongoshPath := filepath.Join(d.binPath, "mongosh")
		return fmt.Sprintf("%s \"%s\"", mongoshPath, connectionString)
	}

	// MongoDB < 4.0: use mongo from BinPath
	mongoPath := filepath.Join(d.binPath, "mongo")
	return fmt.Sprintf("%s \"%s\"", mongoPath, connectionString)
}

// repeatString creates a string by repeating a character n times
func repeatString(char string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += char
	}
	return result
}
