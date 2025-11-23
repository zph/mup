package deploy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zph/mup/pkg/supervisor"
	"github.com/zph/mup/pkg/template"
	"github.com/zph/mup/pkg/topology"
)

// deploy implements Phase 3: Deploy
// - Create directory structures
// - Generate configuration files (MongoDB + Supervisord)
// - Start supervisord and MongoDB processes
func (d *Deployer) deploy(ctx context.Context) error {
	fmt.Println("Phase 3: Deploy")
	fmt.Println("===============")

	// Step 1: Create directories
	if err := d.createDirectories(ctx); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Step 2: Generate and upload MongoDB configurations
	if err := d.generateConfigurations(ctx); err != nil {
		return fmt.Errorf("failed to generate configurations: %w", err)
	}

	// Step 3: Generate supervisord configurations
	if err := d.generateSupervisorConfigs(ctx); err != nil {
		return fmt.Errorf("failed to generate supervisor configs: %w", err)
	}

	// Step 4: Start supervisord and MongoDB processes
	if err := d.startProcesses(ctx); err != nil {
		return fmt.Errorf("failed to start processes: %w", err)
	}

	fmt.Println("✓ Phase 3 complete: MongoDB processes deployed via supervisord")
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
				dirs[d.getNodeLogDirWithType(node.Host, node.Port, node.LogDir, "mongos")] = true
				dirs[d.getNodeConfigDirWithType(node.Host, node.Port, node.ConfigDir, "mongos")] = true
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
// NOTE: Does NOT generate mongos configs - those are generated later after config RS init
func (d *Deployer) generateConfigurations(ctx context.Context) error {
	fmt.Println("Generating configuration files...")

	// Generate configs for mongod nodes
	for _, node := range d.topology.Mongod {
		if err := d.GenerateMongodConfig(node); err != nil {
			return fmt.Errorf("failed to generate config for mongod %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	// NOTE: mongos configs are generated in Phase 4 after config servers are initialized
	// This ensures the configDB connection string points to an initialized replica set

	// Generate configs for config server nodes
	for _, node := range d.topology.ConfigSvr {
		if err := d.GenerateConfigServerConfig(node); err != nil {
			return fmt.Errorf("failed to generate config for config server %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	return nil
}

// GenerateMongodConfig generates configuration for a mongod node using templates
// Exported for use by upgrade package to regenerate configs for new versions
func (d *Deployer) GenerateMongodConfig(node topology.MongodNode) error {
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

	// Add sharding config if this is a sharded cluster
	// Mongod nodes in sharded clusters need to be configured as shard servers
	if d.topology.GetTopologyType() == "sharded" {
		data.Sharding = &template.ShardingConfig{
			ClusterRole: "shardsvr",
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

// GenerateMongosConfig generates configuration for a mongos node using templates
// Exported for use by upgrade package to regenerate configs for new versions
func (d *Deployer) GenerateMongosConfig(node topology.MongosNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDirWithType(node.Host, node.Port, node.ConfigDir, "mongos"),
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
			Path:        filepath.Join(d.getNodeLogDirWithType(node.Host, node.Port, node.LogDir, "mongos"), "mongos.log"),
			LogAppend:   true,
		},
		ProcessManagement: template.ProcessManagementConfig{
			Fork:        false,
			PIDFilePath: filepath.Join(d.getNodeLogDirWithType(node.Host, node.Port, node.LogDir, "mongos"), "mongos.pid"),
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

// GenerateConfigServerConfig generates configuration for a config server node using templates
// Exported for use by upgrade package to regenerate configs for new versions
func (d *Deployer) GenerateConfigServerConfig(node topology.ConfigNode) error {
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

// generateSupervisorConfigs generates supervisord configuration files
func (d *Deployer) generateSupervisorConfigs(ctx context.Context) error {
	fmt.Println("Generating supervisord configuration files...")

	// Use version-specific directory for supervisor config
	versionDir := filepath.Join(d.metaDir, fmt.Sprintf("v%s", d.version))

	// Copy binaries to version directory
	if err := d.setupVersionBinaries(versionDir); err != nil {
		return fmt.Errorf("failed to setup version binaries: %w", err)
	}

	// Create a ConfigGenerator for supervisor using version directory
	binPath := filepath.Join(versionDir, "bin")
	gen := supervisor.NewConfigGenerator(versionDir, d.clusterName, d.topology, d.version, binPath)

	// Generate all supervisor configs (main + per-node)
	if err := gen.GenerateAll(); err != nil {
		return fmt.Errorf("failed to generate supervisor configs: %w", err)
	}

	// Create current/previous symlinks
	if err := d.createVersionSymlinks(versionDir); err != nil {
		return fmt.Errorf("failed to create version symlinks: %w", err)
	}

	fmt.Println("  Generated supervisord configuration")
	return nil
}

// startProcesses starts supervisord and all MongoDB processes
func (d *Deployer) startProcesses(ctx context.Context) error {
	fmt.Println("Starting supervisord and MongoDB processes...")

	// Final port verification before starting any processes
	// This catches any last-minute port conflicts
	if err := d.verifyPortsBeforeStart(); err != nil {
		return fmt.Errorf("port verification failed: %w", err)
	}

	// Load the supervisor manager with the generated config from version directory
	versionDir := filepath.Join(d.metaDir, fmt.Sprintf("v%s", d.version))
	mgr, err := supervisor.LoadManager(versionDir, d.clusterName)
	if err != nil {
		return fmt.Errorf("failed to load supervisor manager: %w", err)
	}

	// Start the supervisor daemon
	fmt.Println("  Starting supervisord daemon...")
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start supervisord: %w", err)
	}

	// Reload supervisor configuration to pick up any newly generated program definitions
	// This is needed if supervisord was already running from a previous deployment
	fmt.Println("  Reloading supervisord configuration...")
	if err := mgr.Reload(); err != nil {
		return fmt.Errorf("failed to reload supervisor config: %w", err)
	}

	// Start config servers first (if sharded cluster) - in parallel
	if len(d.topology.ConfigSvr) > 0 {
		var configPrograms []string
		for _, node := range d.topology.ConfigSvr {
			programName := fmt.Sprintf("mongod-%d", node.Port)
			configPrograms = append(configPrograms, programName)
			fmt.Printf("  Config server %s:%d (program: %s)\n",
				node.Host, node.Port, programName)
		}
		fmt.Printf("  Starting %d config server(s) in parallel...\n", len(configPrograms))
		if err := mgr.StartProcesses(configPrograms); err != nil {
			return fmt.Errorf("failed to start config servers: %w", err)
		}
	}

	// Start mongod nodes - in parallel
	if len(d.topology.Mongod) > 0 {
		var mongodPrograms []string
		for _, node := range d.topology.Mongod {
			programName := fmt.Sprintf("mongod-%d", node.Port)
			mongodPrograms = append(mongodPrograms, programName)
			fmt.Printf("  Mongod %s:%d (program: %s)\n",
				node.Host, node.Port, programName)
		}
		fmt.Printf("  Starting %d mongod node(s) in parallel...\n", len(mongodPrograms))
		if err := mgr.StartProcesses(mongodPrograms); err != nil {
			return fmt.Errorf("failed to start mongod nodes: %w", err)
		}
	}

	// NOTE: mongos nodes are NOT started here
	// For sharded clusters, mongos must be started AFTER config servers
	// are initialized as a replica set (in Phase 4: Initialize)

	// Store supervisor manager for later use
	d.supervisorMgr = mgr

	return nil
}

// verifyPortsBeforeStart performs a final port check before starting processes
// This is a safety net to catch any ports that became unavailable between
// pre-flight checks and process startup
func (d *Deployer) verifyPortsBeforeStart() error {
	type portCheck struct {
		host string
		port int
	}

	var portsToCheck []portCheck

	// Collect all ports that will be used
	for _, node := range d.topology.ConfigSvr {
		portsToCheck = append(portsToCheck, portCheck{node.Host, node.Port})
	}
	for _, node := range d.topology.Mongod {
		portsToCheck = append(portsToCheck, portCheck{node.Host, node.Port})
	}

	// Check each port
	for _, pc := range portsToCheck {
		exec := d.executors[pc.host]
		available, err := exec.CheckPortAvailable(pc.port)
		if err != nil {
			return fmt.Errorf("failed to verify port %s:%d: %w", pc.host, pc.port, err)
		}
		if !available {
			return fmt.Errorf("port %s:%d is no longer available (was free during pre-flight checks)", pc.host, pc.port)
		}
	}

	return nil
}

// setupVersionBinaries copies MongoDB binaries to version-specific directory
func (d *Deployer) setupVersionBinaries(versionDir string) error {
	if !d.isLocal {
		return nil // Only needed for local deployments
	}

	binDir := filepath.Join(versionDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("failed to create bin directory: %w", err)
	}

	// Copy binaries from binary manager cache to version directory
	binaries := []string{"mongod", "mongos", "mongosh", "mongo"}
	for _, binary := range binaries {
		srcPath := filepath.Join(d.binPath, binary)
		dstPath := filepath.Join(binDir, binary)

		// Skip if source doesn't exist
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			continue
		}

		// Skip if already copied
		if _, err := os.Stat(dstPath); err == nil {
			continue
		}

		// Copy file
		if err := copyFile(srcPath, dstPath); err != nil {
			return fmt.Errorf("failed to copy %s: %w", binary, err)
		}

		// Make executable
		if err := os.Chmod(dstPath, 0755); err != nil {
			return fmt.Errorf("failed to make %s executable: %w", binary, err)
		}
	}

	fmt.Printf("  Copied binaries to %s\n", binDir)
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if _, err := destFile.ReadFrom(sourceFile); err != nil {
		return err
	}

	return destFile.Sync()
}

// createVersionSymlinks creates current and previous symlinks
func (d *Deployer) createVersionSymlinks(versionDir string) error {
	if !d.isLocal {
		return nil // Only needed for local deployments
	}

	versionName := fmt.Sprintf("v%s", d.version)
	currentLink := filepath.Join(d.metaDir, "current")
	previousLink := filepath.Join(d.metaDir, "previous")

	// Get old version from current symlink if it exists
	oldVersion := ""
	if target, err := os.Readlink(currentLink); err == nil {
		oldVersion = target
	}

	// Remove old symlinks
	os.Remove(currentLink)
	if oldVersion != "" {
		os.Remove(previousLink)
	}

	// Create new symlinks
	if err := os.Symlink(versionName, currentLink); err != nil {
		return fmt.Errorf("failed to create current symlink: %w", err)
	}

	if oldVersion != "" && oldVersion != versionName {
		if err := os.Symlink(oldVersion, previousLink); err != nil {
			// Log warning but don't fail
			fmt.Printf("  Warning: failed to create previous symlink: %v\n", err)
		}
	}

	fmt.Printf("  Created symlinks: current -> %s\n", versionName)
	return nil
}

// Helper functions for directory paths
// Uses per-version directory structure for conf/ and logs/, but keeps data/ version-independent

func (d *Deployer) getNodeDataDir(host string, port int, defaultDir string) string {
	if d.isLocal {
		// Data directory is version-independent (MongoDB handles compatibility)
		return filepath.Join(d.metaDir, "data", fmt.Sprintf("%s-%d", host, port))
	}
	return filepath.Join(defaultDir, fmt.Sprintf("mongod-%d", port))
}

func (d *Deployer) getNodeLogDir(host string, port int, defaultDir string) string {
	return d.getNodeLogDirWithType(host, port, defaultDir, "mongod")
}

func (d *Deployer) getNodeConfigDir(host string, port int, defaultDir string) string {
	return d.getNodeConfigDirWithType(host, port, defaultDir, "mongod")
}

// getNodeLogDirWithType gets log directory for a node with explicit type (mongod/mongos)
func (d *Deployer) getNodeLogDirWithType(host string, port int, defaultDir string, nodeType string) string {
	if d.isLocal {
		// Logs are version-specific, per-process directory structure
		return filepath.Join(d.metaDir, fmt.Sprintf("v%s", d.version), fmt.Sprintf("%s-%d", nodeType, port), "log")
	}
	return filepath.Join(defaultDir, fmt.Sprintf("%s-%d", nodeType, port))
}

// getNodeConfigDirWithType gets config directory for a node with explicit type (mongod/mongos)
func (d *Deployer) getNodeConfigDirWithType(host string, port int, defaultDir string, nodeType string) string {
	if d.isLocal {
		// Config is version-specific, per-process directory structure
		return filepath.Join(d.metaDir, fmt.Sprintf("v%s", d.version), fmt.Sprintf("%s-%d", nodeType, port), "config")
	}
	return filepath.Join(defaultDir, fmt.Sprintf("%s-%d", nodeType, port))
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
