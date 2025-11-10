package deploy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zph/mup/pkg/logger"
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

	fmt.Println("✓ Phase 3 complete: MongoDB processes deployed")
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
// NOTE: Does NOT generate mongos configs - those are generated later after config RS init
func (d *Deployer) generateConfigurations(ctx context.Context) error {
	fmt.Println("Generating configuration files...")

	// Generate configs for mongod nodes
	for _, node := range d.topology.Mongod {
		if err := d.generateMongodConfig(node); err != nil {
			return fmt.Errorf("failed to generate config for mongod %s:%d: %w",
				node.Host, node.Port, err)
		}
	}

	// NOTE: mongos configs are generated in Phase 4 after config servers are initialized
	// This ensures the configDB connection string points to an initialized replica set

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

// startProcesses starts all MongoDB processes (except mongos)
func (d *Deployer) startProcesses(ctx context.Context) error {
	fmt.Println("Starting MongoDB processes...")

	// Final port verification before starting any processes
	// This catches any last-minute port conflicts
	if err := d.verifyPortsBeforeStart(); err != nil {
		return fmt.Errorf("port verification failed: %w", err)
	}

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

	// NOTE: mongos nodes are NOT started here
	// For sharded clusters, mongos must be started AFTER config servers
	// are initialized as a replica set (in Phase 4: Initialize)

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

// startMongod starts a mongod process
func (d *Deployer) startMongod(node topology.MongodNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir),
		"mongod.conf",
	)

	logPath := filepath.Join(
		d.getNodeLogDir(node.Host, node.Port, node.LogDir),
		"mongod.log",
	)

	// Use full path to versioned binary
	mongodPath := filepath.Join(d.binPath, "mongod")
	command := fmt.Sprintf("%s --config %s", mongodPath, configPath)

	logger.Debug("Starting mongod %s:%d", node.Host, node.Port)
	logger.Debug("  Binary: %s", mongodPath)
	logger.Debug("  Config: %s", configPath)
	logger.Debug("  Command: %s", command)

	pid, err := exec.Background(command)
	if err != nil {
		logger.Error("Failed to start mongod %s:%d: %v", node.Host, node.Port, err)
		return fmt.Errorf("failed to start mongod: %w", err)
	}

	// Store PID for later use
	nodeKey := fmt.Sprintf("%s:%d", node.Host, node.Port)
	d.nodePIDs[nodeKey] = pid

	logger.Debug("Mongod %s:%d started with PID %d", node.Host, node.Port, pid)

	// Wait a moment and check if process is still running
	time.Sleep(2 * time.Second)
	running, err := exec.IsProcessRunning(pid)
	if err != nil {
		logger.Warn("Failed to check if mongod %s:%d is running: %v", node.Host, node.Port, err)
	} else if !running {
		// Process died - try to read log file for error details
		logger.Error("Mongod %s:%d (PID: %d) died shortly after starting", node.Host, node.Port, pid)

		// Try to read log file to show error
		tempLogPath := filepath.Join(os.TempDir(), fmt.Sprintf("mongod-%s-%d.log", node.Host, node.Port))
		if err := exec.DownloadFile(logPath, tempLogPath); err == nil {
			if logContent, err := os.ReadFile(tempLogPath); err == nil {
				// Show last 20 lines of log
				lines := strings.Split(string(logContent), "\n")
				start := len(lines) - 20
				if start < 0 {
					start = 0
				}
				fmt.Printf("  Error: mongod %s:%d died. Last log lines:\n", node.Host, node.Port)
				for i := start; i < len(lines); i++ {
					if strings.TrimSpace(lines[i]) != "" {
						fmt.Printf("    %s\n", lines[i])
					}
				}
			}
			os.Remove(tempLogPath)
		}

		return fmt.Errorf("mongod %s:%d (PID: %d) died shortly after starting - check logs at %s", node.Host, node.Port, pid, logPath)
	}

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

	logPath := filepath.Join(
		d.getNodeLogDir(node.Host, node.Port, node.LogDir),
		"mongos.log",
	)

	// Use full path to versioned binary
	mongosPath := filepath.Join(d.binPath, "mongos")
	command := fmt.Sprintf("%s --config %s", mongosPath, configPath)

	logger.Debug("Starting mongos %s:%d", node.Host, node.Port)
	logger.Debug("  Binary: %s", mongosPath)
	logger.Debug("  Config: %s", configPath)
	logger.Debug("  Command: %s", command)

	pid, err := exec.Background(command)
	if err != nil {
		logger.Error("Failed to start mongos %s:%d: %v", node.Host, node.Port, err)
		return fmt.Errorf("failed to start mongos: %w", err)
	}

	// Store PID for later use
	nodeKey := fmt.Sprintf("%s:%d", node.Host, node.Port)
	d.nodePIDs[nodeKey] = pid

	logger.Debug("Mongos %s:%d started with PID %d", node.Host, node.Port, pid)

	// Wait a moment and check if process is still running
	time.Sleep(2 * time.Second)
	running, err := exec.IsProcessRunning(pid)
	if err != nil {
		logger.Warn("Failed to check if mongos %s:%d is running: %v", node.Host, node.Port, err)
	} else if !running {
		// Process died - try to read log file for error details
		logger.Error("Mongos %s:%d (PID: %d) died shortly after starting", node.Host, node.Port, pid)

		// Try to read log file to show error
		tempLogPath := filepath.Join(os.TempDir(), fmt.Sprintf("mongos-%s-%d.log", node.Host, node.Port))
		if err := exec.DownloadFile(logPath, tempLogPath); err == nil {
			if logContent, err := os.ReadFile(tempLogPath); err == nil {
				// Show last 20 lines of log
				lines := strings.Split(string(logContent), "\n")
				start := len(lines) - 20
				if start < 0 {
					start = 0
				}
				fmt.Printf("  Error: mongos %s:%d died. Last log lines:\n", node.Host, node.Port)
				for i := start; i < len(lines); i++ {
					if strings.TrimSpace(lines[i]) != "" {
						fmt.Printf("    %s\n", lines[i])
					}
				}
			}
			os.Remove(tempLogPath)
		}

		return fmt.Errorf("mongos %s:%d (PID: %d) died shortly after starting - check logs at %s", node.Host, node.Port, pid, logPath)
	}

	fmt.Printf("  Started mongos %s:%d (PID: %d, log: %s)\n", node.Host, node.Port, pid, logPath)
	return nil
}

// startConfigServer starts a config server process
func (d *Deployer) startConfigServer(node topology.ConfigNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir),
		"mongod.conf",
	)

	logPath := filepath.Join(
		d.getNodeLogDir(node.Host, node.Port, node.LogDir),
		"mongod.log",
	)

	// Use full path to versioned binary
	mongodPath := filepath.Join(d.binPath, "mongod")
	command := fmt.Sprintf("%s --config %s", mongodPath, configPath)

	logger.Debug("Starting config server %s:%d", node.Host, node.Port)
	logger.Debug("  Binary: %s", mongodPath)
	logger.Debug("  Config: %s", configPath)
	logger.Debug("  Command: %s", command)

	pid, err := exec.Background(command)
	if err != nil {
		logger.Error("Failed to start config server %s:%d: %v", node.Host, node.Port, err)
		return fmt.Errorf("failed to start config server: %w", err)
	}

	// Store PID for later use
	nodeKey := fmt.Sprintf("%s:%d", node.Host, node.Port)
	d.nodePIDs[nodeKey] = pid

	logger.Debug("Config server %s:%d started with PID %d", node.Host, node.Port, pid)

	// Wait a moment and check if process is still running
	time.Sleep(2 * time.Second)
	running, err := exec.IsProcessRunning(pid)
	if err != nil {
		logger.Warn("Failed to check if config server %s:%d is running: %v", node.Host, node.Port, err)
	} else if !running {
		// Process died - try to read log file for error details
		logger.Error("Config server %s:%d (PID: %d) died shortly after starting", node.Host, node.Port, pid)

		// Try to read log file to show error
		tempLogPath := filepath.Join(os.TempDir(), fmt.Sprintf("configsvr-%s-%d.log", node.Host, node.Port))
		if err := exec.DownloadFile(logPath, tempLogPath); err == nil {
			if logContent, err := os.ReadFile(tempLogPath); err == nil {
				// Show last 20 lines of log
				lines := strings.Split(string(logContent), "\n")
				start := len(lines) - 20
				if start < 0 {
					start = 0
				}
				fmt.Printf("  Error: config server %s:%d died. Last log lines:\n", node.Host, node.Port)
				for i := start; i < len(lines); i++ {
					if strings.TrimSpace(lines[i]) != "" {
						fmt.Printf("    %s\n", lines[i])
					}
				}
			}
			os.Remove(tempLogPath)
		}

		return fmt.Errorf("config server %s:%d (PID: %d) died shortly after starting - check logs at %s", node.Host, node.Port, pid, logPath)
	}

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
