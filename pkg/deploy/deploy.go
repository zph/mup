package deploy

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/zph/mup/pkg/template"
	"github.com/zph/mup/pkg/topology"
)

// deploy implements Phase 3: Deploy
// - Create directory structures
// - Generate configuration files
// - Start MongoDB processes
func (d *Deployer) deploy(ctx context.Context) error {
	fmt.Println("Phase 3: Deploy")
	fmt.Println("===============")

	// Step 1: Create directories
	if err := d.createDirectories(ctx); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Step 2: Generate and upload configurations
	if err := d.generateConfigurations(ctx); err != nil {
		return fmt.Errorf("failed to generate configurations: %w", err)
	}

	// Step 3: Start MongoDB processes
	if err := d.startProcesses(ctx); err != nil {
		return fmt.Errorf("failed to start processes: %w", err)
	}

	fmt.Println("✓ Phase 3 complete: MongoDB processes deployed\n")
	return nil
}

// createDirectories creates all required directories on each host
func (d *Deployer) createDirectories(ctx context.Context) error {
	fmt.Println("Creating directory structures...")

	for host, exec := range d.executors {
		fmt.Printf("  Host: %s\n", host)

		// Collect unique directories for this host
		dirs := make(map[string]bool)

		// Add directories for mongod nodes
		for _, node := range d.topology.Mongod {
			if node.Host == host {
				dirs[d.getNodeDataDir(node.Host, node.Port, node.DataDir)] = true
				dirs[d.getNodeLogDir(node.Host, node.Port, node.LogDir)] = true
				dirs[d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir)] = true
			}
		}

		// Add directories for mongos nodes
		for _, node := range d.topology.Mongos {
			if node.Host == host {
				dirs[d.getNodeLogDir(node.Host, node.Port, node.LogDir)] = true
				dirs[d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir)] = true
			}
		}

		// Add directories for config server nodes
		for _, node := range d.topology.ConfigSvr {
			if node.Host == host {
				dirs[d.getNodeDataDir(node.Host, node.Port, node.DataDir)] = true
				dirs[d.getNodeLogDir(node.Host, node.Port, node.LogDir)] = true
				dirs[d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir)] = true
			}
		}

		// Create each directory
		for dir := range dirs {
			if err := exec.CreateDirectory(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
			fmt.Printf("    Created: %s\n", dir)
		}
	}

	return nil
}

// generateConfigurations generates MongoDB configuration files
func (d *Deployer) generateConfigurations(ctx context.Context) error {
	fmt.Println("Generating configuration files...")

	// Generate configs for mongod nodes
	for _, node := range d.topology.Mongod {
		if err := d.generateMongodConfig(node); err != nil {
			return fmt.Errorf("failed to generate config for mongod %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	// Generate configs for mongos nodes
	for _, node := range d.topology.Mongos {
		if err := d.generateMongosConfig(node); err != nil {
			return fmt.Errorf("failed to generate config for mongos %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	// Generate configs for config server nodes
	for _, node := range d.topology.ConfigSvr {
		if err := d.generateConfigServerConfig(node); err != nil {
			return fmt.Errorf("failed to generate config for config server %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	return nil
}

// generateMongodConfig generates configuration for a mongod node using templates
func (d *Deployer) generateMongodConfig(node topology.MongodNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir),
		"mongod.conf",
	)

	// Build template data
	data := template.MongodConfig{
		Net: template.NetConfig{
			Port:   node.Port,
			BindIP: "127.0.0.1",
		},
		Storage: template.StorageConfig{
			DBPath: d.getNodeDataDir(node.Host, node.Port, node.DataDir),
			Journal: template.JournalConfig{
				Enabled: true,
			},
			Engine: "wiredTiger",
			WiredTiger: &template.WiredTigerConfig{
				EngineConfig: template.WiredTigerEngineConfig{
					CacheSizeGB: 1.0,
				},
			},
		},
		SystemLog: template.SystemLogConfig{
			Destination: "file",
			Path:        filepath.Join(d.getNodeLogDir(node.Host, node.Port, node.LogDir), "mongod.log"),
			LogAppend:   true,
		},
		ProcessManagement: template.ProcessManagementConfig{
			Fork:        false,
			PIDFilePath: filepath.Join(d.getNodeDataDir(node.Host, node.Port, node.DataDir), "mongod.pid"),
		},
	}

	// Add replication config if this is part of a replica set
	if node.ReplicaSet != "" {
		data.Replication = &template.ReplicationConfig{
			ReplSetName:               node.ReplicaSet,
			EnableMajorityReadConcern: true,
		}
	}

	// Get appropriate template for MongoDB version
	tmpl, err := d.templateMgr.GetTemplate("mongod", d.version)
	if err != nil {
		return fmt.Errorf("failed to get template: %w", err)
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Upload configuration
	if err := exec.UploadContent(buf.Bytes(), configPath); err != nil {
		return fmt.Errorf("failed to upload config: %w", err)
	}

	fmt.Printf("  Generated config: %s\n", configPath)
	return nil
}

// generateMongosConfig generates configuration for a mongos node using templates
func (d *Deployer) generateMongosConfig(node topology.MongosNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir),
		"mongos.conf",
	)

	// Build template data
	data := template.MongosConfig{
		Net: template.NetConfig{
			Port:   node.Port,
			BindIP: "127.0.0.1",
		},
		SystemLog: template.SystemLogConfig{
			Destination: "file",
			Path:        filepath.Join(d.getNodeLogDir(node.Host, node.Port, node.LogDir), "mongos.log"),
			LogAppend:   true,
		},
		ProcessManagement: template.ProcessManagementConfig{
			Fork:        false,
			PIDFilePath: filepath.Join(d.getNodeLogDir(node.Host, node.Port, node.LogDir), "mongos.pid"),
		},
		Sharding: template.MongosShardingConfig{
			ConfigDB: d.getConfigServerConnectionString(),
		},
	}

	// Get appropriate template for MongoDB version
	tmpl, err := d.templateMgr.GetTemplate("mongos", d.version)
	if err != nil {
		return fmt.Errorf("failed to get template: %w", err)
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Upload configuration
	if err := exec.UploadContent(buf.Bytes(), configPath); err != nil {
		return fmt.Errorf("failed to upload config: %w", err)
	}

	fmt.Printf("  Generated config: %s\n", configPath)
	return nil
}

// generateConfigServerConfig generates configuration for a config server node using templates
func (d *Deployer) generateConfigServerConfig(node topology.ConfigNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir),
		"mongod.conf",
	)

	// Build template data (config servers are mongod with clusterRole: configsvr)
	data := template.MongodConfig{
		Net: template.NetConfig{
			Port:   node.Port,
			BindIP: "127.0.0.1",
		},
		Storage: template.StorageConfig{
			DBPath: d.getNodeDataDir(node.Host, node.Port, node.DataDir),
			Journal: template.JournalConfig{
				Enabled: true,
			},
			Engine: "wiredTiger",
			WiredTiger: &template.WiredTigerConfig{
				EngineConfig: template.WiredTigerEngineConfig{
					CacheSizeGB: 1.0,
				},
			},
		},
		SystemLog: template.SystemLogConfig{
			Destination: "file",
			Path:        filepath.Join(d.getNodeLogDir(node.Host, node.Port, node.LogDir), "mongod.log"),
			LogAppend:   true,
		},
		ProcessManagement: template.ProcessManagementConfig{
			Fork:        false,
			PIDFilePath: filepath.Join(d.getNodeDataDir(node.Host, node.Port, node.DataDir), "mongod.pid"),
		},
		Replication: &template.ReplicationConfig{
			ReplSetName:               node.ReplicaSet,
			EnableMajorityReadConcern: true,
		},
		Sharding: &template.ShardingConfig{
			ClusterRole: "configsvr",
		},
	}

	// Get appropriate template for MongoDB version (config servers use config templates)
	tmpl, err := d.templateMgr.GetTemplate("config", d.version)
	if err != nil {
		return fmt.Errorf("failed to get template: %w", err)
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Upload configuration
	if err := exec.UploadContent(buf.Bytes(), configPath); err != nil {
		return fmt.Errorf("failed to upload config: %w", err)
	}

	fmt.Printf("  Generated config: %s\n", configPath)
	return nil
}

// startProcesses starts all MongoDB processes
func (d *Deployer) startProcesses(ctx context.Context) error {
	fmt.Println("Starting MongoDB processes...")

	// Start config servers first (if sharded cluster)
	for _, node := range d.topology.ConfigSvr {
		if err := d.startConfigServer(node); err != nil {
			return fmt.Errorf("failed to start config server %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	// Start mongod nodes
	for _, node := range d.topology.Mongod {
		if err := d.startMongod(node); err != nil {
			return fmt.Errorf("failed to start mongod %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	// Start mongos nodes last
	for _, node := range d.topology.Mongos {
		if err := d.startMongos(node); err != nil {
			return fmt.Errorf("failed to start mongos %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	return nil
}

// startMongod starts a mongod process
func (d *Deployer) startMongod(node topology.MongodNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir),
		"mongod.conf",
	)

	// Start mongod with config file
	command := fmt.Sprintf("mongod --config %s", configPath)

	pid, err := exec.Background(command)
	if err != nil {
		return fmt.Errorf("failed to start mongod: %w", err)
	}

	// Store PID for later use
	nodeKey := fmt.Sprintf("%s:%d", node.Host, node.Port)
	d.nodePIDs[nodeKey] = pid

	fmt.Printf("  Started mongod %s:%d (PID: %d)\n", node.Host, node.Port, pid)
	return nil
}

// startMongos starts a mongos process
func (d *Deployer) startMongos(node topology.MongosNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir),
		"mongos.conf",
	)

	command := fmt.Sprintf("mongos --config %s", configPath)

	pid, err := exec.Background(command)
	if err != nil {
		return fmt.Errorf("failed to start mongos: %w", err)
	}

	// Store PID for later use
	nodeKey := fmt.Sprintf("%s:%d", node.Host, node.Port)
	d.nodePIDs[nodeKey] = pid

	fmt.Printf("  Started mongos %s:%d (PID: %d)\n", node.Host, node.Port, pid)
	return nil
}

// startConfigServer starts a config server process
func (d *Deployer) startConfigServer(node topology.ConfigNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir),
		"mongod.conf",
	)

	command := fmt.Sprintf("mongod --config %s", configPath)

	pid, err := exec.Background(command)
	if err != nil {
		return fmt.Errorf("failed to start config server: %w", err)
	}

	// Store PID for later use
	nodeKey := fmt.Sprintf("%s:%d", node.Host, node.Port)
	d.nodePIDs[nodeKey] = pid

	fmt.Printf("  Started config server %s:%d (PID: %d)\n", node.Host, node.Port, pid)
	return nil
}

// Helper functions for directory paths

func (d *Deployer) getNodeDataDir(host string, port int, defaultDir string) string {
	if d.isLocal {
		return filepath.Join(d.metaDir, "data", fmt.Sprintf("%s-%d", host, port))
	}
	return filepath.Join(defaultDir, fmt.Sprintf("mongod-%d", port))
}

func (d *Deployer) getNodeLogDir(host string, port int, defaultDir string) string {
	if d.isLocal {
		return filepath.Join(d.metaDir, "logs", fmt.Sprintf("%s-%d", host, port))
	}
	return filepath.Join(defaultDir, fmt.Sprintf("mongod-%d", port))
}

func (d *Deployer) getNodeConfigDir(host string, port int, defaultDir string) string {
	if d.isLocal {
		return filepath.Join(d.metaDir, "conf", fmt.Sprintf("%s-%d", host, port))
	}
	return filepath.Join(defaultDir, fmt.Sprintf("mongod-%d", port))
}

func (d *Deployer) getConfigServerConnectionString() string {
	if len(d.topology.ConfigSvr) == 0 {
		return ""
	}

	// Get replica set name from first config server
	rsName := d.topology.ConfigSvr[0].ReplicaSet

	// Build connection string
	var members []string
	for _, node := range d.topology.ConfigSvr {
		members = append(members, fmt.Sprintf("%s:%d", node.Host, node.Port))
	}

	return fmt.Sprintf("%s/%s", rsName, joinStrings(members, ","))
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
