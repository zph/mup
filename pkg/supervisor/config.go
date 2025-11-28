package supervisor

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"text/template"

	"github.com/zph/mup/pkg/naming"
	"github.com/zph/mup/pkg/topology"
)

// ConfigGenerator generates supervisord configuration files for a cluster
type ConfigGenerator struct {
	clusterDir    string // Version-specific directory (e.g., ~/.mup/storage/clusters/test/v7.0)
	clusterRoot   string // Cluster root directory (e.g., ~/.mup/storage/clusters/test)
	clusterName   string
	topology      *topology.Topology
	version       string
	binPath       string
}

// NewConfigGenerator creates a new config generator
func NewConfigGenerator(clusterDir, clusterName string, topo *topology.Topology, version, binPath string) *ConfigGenerator {
	// clusterDir is the version-specific directory (e.g., ~/.mup/storage/clusters/test/v7.0)
	// clusterRoot is the parent directory (e.g., ~/.mup/storage/clusters/test)
	clusterRoot := filepath.Dir(clusterDir)

	return &ConfigGenerator{
		clusterDir:  clusterDir,
		clusterRoot: clusterRoot,
		clusterName: clusterName,
		topology:    topo,
		version:     version,
		binPath:     binPath,
	}
}

// GenerateAll generates all supervisord configuration files
// REQ-PM-010: Creates per-node supervisor.conf in version-specific directories,
// then a main supervisor.ini that includes them via [include] directive
func (g *ConfigGenerator) GenerateAll() error {
	// Step 1: Generate per-node supervisor.conf files
	if err := g.GeneratePerNodeConfigs(); err != nil {
		return fmt.Errorf("failed to generate per-node configs: %w", err)
	}

	// Step 2: Generate main supervisor.ini with includes
	if err := g.GenerateMainConfigWithIncludes(); err != nil {
		return fmt.Errorf("failed to generate main config: %w", err)
	}

	// Step 3: Generate convenience wrapper scripts
	if err := g.GenerateWrapperScripts(); err != nil {
		return fmt.Errorf("failed to generate wrapper scripts: %w", err)
	}

	return nil
}

// GenerateUnifiedConfig generates a single supervisor.ini with all programs
func (g *ConfigGenerator) GenerateUnifiedConfig() error {
	configPath := filepath.Join(g.clusterDir, "supervisor.ini")
	file, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer file.Close()

	// Write main supervisord section
	fmt.Fprintf(file, "[supervisord]\n")
	fmt.Fprintf(file, "logfile = %s\n", filepath.Join(g.clusterDir, "supervisor.log"))
	fmt.Fprintf(file, "loglevel = info\n")
	fmt.Fprintf(file, "pidfile = %s\n", filepath.Join(g.clusterDir, "supervisor.pid"))
	fmt.Fprintf(file, "nodaemon = false\n")
	fmt.Fprintf(file, "identifier = %s\n\n", g.clusterName)

	// Write HTTP server section with unique port per cluster
	// Use hash of cluster name to generate a port in range 19000-19999
	// This avoids conflicts when running multiple clusters
	httpPort := g.getSupervisorHTTPPort()
	fmt.Fprintf(file, "[inet_http_server]\n")
	fmt.Fprintf(file, "port = 127.0.0.1:%d\n\n", httpPort)

	// Write all config server programs
	for _, node := range g.topology.ConfigSvr {
		if err := g.writeMongodProgram(file, node.Host, node.Port, node.ReplicaSet, true); err != nil {
			return err
		}
	}

	// Write all mongod programs
	for _, node := range g.topology.Mongod {
		if err := g.writeMongodProgram(file, node.Host, node.Port, node.ReplicaSet, false); err != nil {
			return err
		}
	}

	// Write all mongos programs
	for _, node := range g.topology.Mongos {
		if err := g.writeMongosProgram(file, node.Host, node.Port); err != nil {
			return err
		}
	}

	// Add include section for monitoring configuration
	// This allows monitoring to be added later without regenerating the main config
	fmt.Fprintf(file, "\n[include]\n")
	fmt.Fprintf(file, "files = monitoring-supervisor.ini\n")

	return nil
}

// writeMongodProgram writes a mongod program section to the config file
func (g *ConfigGenerator) writeMongodProgram(file *os.File, host string, port int, replicaSet string, isConfigSvr bool) error {
	programName := fmt.Sprintf("mongod-%d", port)
	// Per-process directory structure: mongod-{port}/{config,log,bin}
	processDir := filepath.Join(g.clusterDir, programName)
	configPath := filepath.Join(processDir, "config", "mongod.conf")
	logFile := filepath.Join(processDir, "log", fmt.Sprintf("supervisor-mongod-%d.log", port))
	// Data is version-independent (in clusterRoot/data/, NOT clusterDir/data/)
	dataDir := filepath.Join(g.clusterRoot, "data", fmt.Sprintf("%s-%d", host, port))
	mongodPath := filepath.Join(g.binPath, "mongod")

	fmt.Fprintf(file, "[program:%s]\n", programName)
	fmt.Fprintf(file, "command = %s --config %s\n", mongodPath, configPath)
	fmt.Fprintf(file, "directory = %s\n", dataDir)
	fmt.Fprintf(file, "autostart = false\n")
	fmt.Fprintf(file, "autorestart = unexpected\n")
	fmt.Fprintf(file, "startsecs = 5\n")
	fmt.Fprintf(file, "startretries = 3\n")
	fmt.Fprintf(file, "stdout_logfile = %s\n", logFile)
	fmt.Fprintf(file, "stderr_logfile = %s\n", logFile)
	fmt.Fprintf(file, "stdout_logfile_maxbytes = 50MB\n")
	fmt.Fprintf(file, "stdout_logfile_backups = 10\n")
	fmt.Fprintf(file, "stopwaitsecs = 30\n")
	fmt.Fprintf(file, "stopsignal = INT\n")
	fmt.Fprintf(file, "environment = HOME=\"%s\",USER=\"%s\"\n", os.Getenv("HOME"), os.Getenv("USER"))
	if replicaSet != "" {
		fmt.Fprintf(file, "; Replica Set: %s\n", replicaSet)
	}
	fmt.Fprintf(file, "\n")

	return nil
}

// writeMongosProgram writes a mongos program section to the config file
func (g *ConfigGenerator) writeMongosProgram(file *os.File, host string, port int) error {
	programName := fmt.Sprintf("mongos-%d", port)
	// Per-process directory structure: mongos-{port}/{config,log,bin}
	processDir := filepath.Join(g.clusterDir, programName)
	configPath := filepath.Join(processDir, "config", "mongos.conf")
	logFile := filepath.Join(processDir, "log", fmt.Sprintf("supervisor-mongos-%d.log", port))
	mongosPath := filepath.Join(g.binPath, "mongos")

	fmt.Fprintf(file, "[program:%s]\n", programName)
	fmt.Fprintf(file, "command = %s --config %s\n", mongosPath, configPath)
	fmt.Fprintf(file, "autostart = false\n")
	fmt.Fprintf(file, "autorestart = unexpected\n")
	fmt.Fprintf(file, "startsecs = 5\n")
	fmt.Fprintf(file, "startretries = 3\n")
	fmt.Fprintf(file, "stdout_logfile = %s\n", logFile)
	fmt.Fprintf(file, "stderr_logfile = %s\n", logFile)
	fmt.Fprintf(file, "stdout_logfile_maxbytes = 50MB\n")
	fmt.Fprintf(file, "stdout_logfile_backups = 10\n")
	fmt.Fprintf(file, "stopwaitsecs = 30\n")
	fmt.Fprintf(file, "stopsignal = INT\n")
	fmt.Fprintf(file, "environment = HOME=\"%s\",USER=\"%s\"\n", os.Getenv("HOME"), os.Getenv("USER"))
	fmt.Fprintf(file, "\n")

	return nil
}

// getSupervisorHTTPPort generates a unique HTTP port for this cluster's supervisor
// Uses hash of cluster directory (includes version) to get a port in range 19000-19999
// This ensures old and new supervisors can run side-by-side during upgrades
func (g *ConfigGenerator) getSupervisorHTTPPort() int {
	h := fnv.New32a()
	// Hash the full cluster directory path (includes version like "v7.0")
	// This gives different ports for different versions of the same cluster
	h.Write([]byte(g.clusterDir))
	hash := h.Sum32()

	// Map to port range 19000-19999 (1000 ports available)
	port := 19000 + int(hash%1000)
	return port
}

// GeneratePerNodeConfigs creates individual supervisor.conf for each node
// REQ-PM-010: Each node gets its own supervisor.conf in its process directory
func (g *ConfigGenerator) GeneratePerNodeConfigs() error {
	// Generate configs for config servers
	for _, node := range g.topology.ConfigSvr {
		if err := g.generateNodeSupervisorConf("config", node.Host, node.Port, node.ReplicaSet, true); err != nil {
			return err
		}
	}

	// Generate configs for mongod nodes
	for _, node := range g.topology.Mongod {
		if err := g.generateNodeSupervisorConf("mongod", node.Host, node.Port, node.ReplicaSet, false); err != nil {
			return err
		}
	}

	// Generate configs for mongos routers
	for _, node := range g.topology.Mongos {
		if err := g.generateMongosNodeSupervisorConf(node.Host, node.Port); err != nil {
			return err
		}
	}

	return nil
}

// generateNodeSupervisorConf creates a supervisor.conf for a mongod/config server node
func (g *ConfigGenerator) generateNodeSupervisorConf(nodeType, host string, port int, replicaSet string, isConfigSvr bool) error {
	programName := naming.GetProgramName(nodeType, port)
	processDir := filepath.Join(g.clusterDir, naming.GetProcessDir(nodeType, port))
	confPath := filepath.Join(processDir, "supervisor.conf")

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(confPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", confPath, err)
	}

	file, err := os.Create(confPath)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", confPath, err)
	}
	defer file.Close()

	// Write program section
	// Use naming package to get the correct config filename for the node type
	confFileName := naming.GetConfigFileName(nodeType)
	configPath := filepath.Join(processDir, "config", confFileName)
	logFile := filepath.Join(processDir, "log", fmt.Sprintf("supervisor-mongod-%d.log", port))
	dataDir := filepath.Join(g.clusterRoot, "data", fmt.Sprintf("%s-%d", host, port))
	mongodPath := filepath.Join(g.binPath, "mongod")

	fmt.Fprintf(file, "[program:%s]\n", programName)
	fmt.Fprintf(file, "command = %s --config %s\n", mongodPath, configPath)
	fmt.Fprintf(file, "directory = %s\n", dataDir)
	fmt.Fprintf(file, "autostart = false\n")
	fmt.Fprintf(file, "autorestart = unexpected\n")
	fmt.Fprintf(file, "startsecs = 5\n")
	fmt.Fprintf(file, "startretries = 3\n")
	fmt.Fprintf(file, "stdout_logfile = %s\n", logFile)
	fmt.Fprintf(file, "stderr_logfile = %s\n", logFile)
	fmt.Fprintf(file, "stdout_logfile_maxbytes = 50MB\n")
	fmt.Fprintf(file, "stdout_logfile_backups = 10\n")
	fmt.Fprintf(file, "stopwaitsecs = 30\n")
	fmt.Fprintf(file, "stopsignal = INT\n")
	fmt.Fprintf(file, "environment = HOME=\"%s\",USER=\"%s\"\n", os.Getenv("HOME"), os.Getenv("USER"))
	if replicaSet != "" {
		fmt.Fprintf(file, "; Replica Set: %s\n", replicaSet)
	}

	return nil
}

// generateMongosNodeSupervisorConf creates a supervisor.conf for a mongos router
func (g *ConfigGenerator) generateMongosNodeSupervisorConf(host string, port int) error {
	programName := naming.GetProgramName("mongos", port)
	processDir := filepath.Join(g.clusterDir, naming.GetProcessDir("mongos", port))
	confPath := filepath.Join(processDir, "supervisor.conf")

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(confPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", confPath, err)
	}

	file, err := os.Create(confPath)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", confPath, err)
	}
	defer file.Close()

	// Write program section
	configPath := filepath.Join(processDir, "config", naming.GetConfigFileName("mongos"))
	logFile := filepath.Join(processDir, "log", fmt.Sprintf("supervisor-mongos-%d.log", port))
	mongosPath := filepath.Join(g.binPath, "mongos")

	fmt.Fprintf(file, "[program:%s]\n", programName)
	fmt.Fprintf(file, "command = %s --config %s\n", mongosPath, configPath)
	fmt.Fprintf(file, "autostart = false\n")
	fmt.Fprintf(file, "autorestart = unexpected\n")
	fmt.Fprintf(file, "startsecs = 5\n")
	fmt.Fprintf(file, "startretries = 3\n")
	fmt.Fprintf(file, "stdout_logfile = %s\n", logFile)
	fmt.Fprintf(file, "stderr_logfile = %s\n", logFile)
	fmt.Fprintf(file, "stdout_logfile_maxbytes = 50MB\n")
	fmt.Fprintf(file, "stdout_logfile_backups = 10\n")
	fmt.Fprintf(file, "stopwaitsecs = 30\n")
	fmt.Fprintf(file, "stopsignal = INT\n")
	fmt.Fprintf(file, "environment = HOME=\"%s\",USER=\"%s\"\n", os.Getenv("HOME"), os.Getenv("USER"))

	return nil
}

// GenerateMainConfigWithIncludes creates supervisor.ini with [include] directives
// REQ-PM-010: Main config includes all per-node configs via relative paths
func (g *ConfigGenerator) GenerateMainConfigWithIncludes() error {
	configPath := filepath.Join(g.clusterDir, "supervisor.ini")
	file, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer file.Close()

	// Write main supervisord section
	fmt.Fprintf(file, "[supervisord]\n")
	fmt.Fprintf(file, "logfile = %s\n", filepath.Join(g.clusterDir, "supervisor.log"))
	fmt.Fprintf(file, "loglevel = info\n")
	fmt.Fprintf(file, "pidfile = %s\n", filepath.Join(g.clusterDir, "supervisor.pid"))
	fmt.Fprintf(file, "nodaemon = false\n")
	fmt.Fprintf(file, "identifier = %s\n\n", g.clusterName)

	// Write HTTP server section
	httpPort := g.getSupervisorHTTPPort()
	fmt.Fprintf(file, "[inet_http_server]\n")
	fmt.Fprintf(file, "port = 127.0.0.1:%d\n\n", httpPort)

	// Write include section with all per-node configs
	fmt.Fprintf(file, "[include]\n")
	fmt.Fprintf(file, "files = ")

	// Add all node configs to include list
	var includes []string

	for _, node := range g.topology.ConfigSvr {
		includes = append(includes, fmt.Sprintf("config-%d/supervisor.conf", node.Port))
	}

	for _, node := range g.topology.Mongod {
		includes = append(includes, fmt.Sprintf("mongod-%d/supervisor.conf", node.Port))
	}

	for _, node := range g.topology.Mongos {
		includes = append(includes, fmt.Sprintf("mongos-%d/supervisor.conf", node.Port))
	}

	// Add monitoring config
	includes = append(includes, "monitoring-supervisor.ini")

	// Write all includes (space-separated for supervisord)
	for i, inc := range includes {
		if i > 0 {
			fmt.Fprintf(file, " ")
		}
		fmt.Fprintf(file, "%s", inc)
	}
	fmt.Fprintf(file, "\n")

	return nil
}

// GenerateMainConfig generates the main supervisor.ini file
func (g *ConfigGenerator) GenerateMainConfig() error {
	tmpl := template.Must(template.New("supervisor").Parse(mainConfigTemplate))

	data := struct {
		ClusterDir  string
		ClusterName string
		LogFile     string
		PidFile     string
		HTTPPort    int
	}{
		ClusterDir:  g.clusterDir,
		ClusterName: g.clusterName,
		LogFile:     filepath.Join(g.clusterDir, "supervisor.log"),
		PidFile:     filepath.Join(g.clusterDir, "supervisor.pid"),
		HTTPPort:    9001, // TODO: make configurable or auto-allocate
	}

	configPath := filepath.Join(g.clusterDir, "supervisor.ini")
	file, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// GenerateMongodConfig generates supervisord config for a mongod node
func (g *ConfigGenerator) GenerateMongodConfig(node topology.MongodNode) error {
	programName := fmt.Sprintf("mongod-%d", node.Port)
	configPath := filepath.Join(g.clusterDir, "conf", fmt.Sprintf("%s-%d", node.Host, node.Port), "supervisor-mongod.ini")
	mongodConfigPath := filepath.Join(g.clusterDir, "conf", fmt.Sprintf("%s-%d", node.Host, node.Port), "mongod.conf")
	dataDir := filepath.Join(g.clusterDir, "data", fmt.Sprintf("%s-%d", node.Host, node.Port))
	logFile := filepath.Join(g.clusterDir, "logs", fmt.Sprintf("supervisor-mongod-%d.log", node.Port))

	tmpl := template.Must(template.New("mongod").Parse(mongodProgramTemplate))

	data := struct {
		Name          string
		BinPath       string
		ConfigPath    string
		DataDir       string
		LogFile       string
		ReplicaSet    string
		HomeDir       string
		User          string
	}{
		Name:          programName,
		BinPath:       filepath.Join(g.binPath, "mongod"),
		ConfigPath:    mongodConfigPath,
		DataDir:       dataDir,
		LogFile:       logFile,
		ReplicaSet:    node.ReplicaSet,
		HomeDir:       os.Getenv("HOME"),
		User:          os.Getenv("USER"),
	}

	// Ensure config directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	file, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create program config file: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to write program config: %w", err)
	}

	return nil
}

// GenerateMongosConfig generates supervisord config for a mongos router
func (g *ConfigGenerator) GenerateMongosConfig(node topology.MongosNode) error {
	programName := fmt.Sprintf("mongos-%d", node.Port)
	configPath := filepath.Join(g.clusterDir, "conf", fmt.Sprintf("%s-%d", node.Host, node.Port), "supervisor-mongos.ini")
	mongosConfigPath := filepath.Join(g.clusterDir, "conf", fmt.Sprintf("%s-%d", node.Host, node.Port), "mongos.conf")
	logFile := filepath.Join(g.clusterDir, "logs", fmt.Sprintf("supervisor-mongos-%d.log", node.Port))

	tmpl := template.Must(template.New("mongos").Parse(mongosProgramTemplate))

	data := struct {
		Name        string
		BinPath     string
		ConfigPath  string
		LogFile     string
		HomeDir     string
		User        string
	}{
		Name:        programName,
		BinPath:     filepath.Join(g.binPath, "mongos"),
		ConfigPath:  mongosConfigPath,
		LogFile:     logFile,
		HomeDir:     os.Getenv("HOME"),
		User:        os.Getenv("USER"),
	}

	// Ensure config directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	file, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to create program config file: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to write program config: %w", err)
	}

	return nil
}

// GenerateConfigServerConfig generates supervisord config for a config server
func (g *ConfigGenerator) GenerateConfigServerConfig(node topology.ConfigNode) error {
	// Config servers are just mongod with configsvr=true
	// Convert to MongodNode
	mongodNode := topology.MongodNode{
		Host:       node.Host,
		Port:       node.Port,
		ReplicaSet: node.ReplicaSet,
	}

	return g.GenerateMongodConfig(mongodNode)
}

// Configuration templates

const mainConfigTemplate = `[supervisord]
logfile = {{.LogFile}}
loglevel = info
pidfile = {{.PidFile}}
nodaemon = false
identifier = {{.ClusterName}}

[inet_http_server]
port = 127.0.0.1:{{.HTTPPort}}

[include]
files = {{.ClusterDir}}/conf/*/supervisor-*.ini
`

const mongodProgramTemplate = `[program:{{.Name}}]
command = {{.BinPath}} --config {{.ConfigPath}}
directory = {{.DataDir}}
autostart = false
autorestart = unexpected
startsecs = 5
startretries = 3
stdout_logfile = {{.LogFile}}
stderr_logfile = {{.LogFile}}
stdout_logfile_maxbytes = 50MB
stdout_logfile_backups = 10
stopwaitsecs = 30
stopsignal = INT
environment = HOME="{{.HomeDir}}",USER="{{.User}}"
{{if .ReplicaSet}}; Replica Set: {{.ReplicaSet}}
{{end}}`

const mongosProgramTemplate = `[program:{{.Name}}]
command = {{.BinPath}} --config {{.ConfigPath}}
autostart = false
autorestart = unexpected
startsecs = 5
startretries = 3
stdout_logfile = {{.LogFile}}
stderr_logfile = {{.LogFile}}
stdout_logfile_maxbytes = 50MB
stdout_logfile_backups = 10
stopwaitsecs = 30
stopsignal = INT
environment = HOME="{{.HomeDir}}",USER="{{.User}}"
`

// GenerateWrapperScripts generates convenience start/stop scripts in per-process directories
// and a supervisorctl wrapper in the cluster's main bin directory
func (g *ConfigGenerator) GenerateWrapperScripts() error {
	// Get supervisor binary path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}
	supervisorBin := filepath.Join(homeDir, ".mup", "storage", "bin", "supervisor", "v1.0.0",
		fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH), "supervisord")

	configPath := filepath.Join(g.clusterDir, "supervisor.ini")
	httpPort := g.getSupervisorHTTPPort()
	serverURL := fmt.Sprintf("http://localhost:%d", httpPort)

	// Generate supervisorctl wrapper in cluster's main bin directory
	clusterBinDir := filepath.Join(g.clusterDir, "bin")
	if err := os.MkdirAll(clusterBinDir, 0755); err != nil {
		return fmt.Errorf("failed to create cluster bin directory: %w", err)
	}

	if err := g.generateSupervisorctlWrapper(clusterBinDir, supervisorBin, configPath, serverURL); err != nil {
		return fmt.Errorf("failed to generate supervisorctl wrapper: %w", err)
	}

	// Generate per-node scripts for mongod
	for _, node := range g.topology.Mongod {
		programName := fmt.Sprintf("mongod-%d", node.Port)
		processDir := filepath.Join(g.clusterDir, programName)
		binDir := filepath.Join(processDir, "bin")

		if err := os.MkdirAll(binDir, 0755); err != nil {
			return fmt.Errorf("failed to create bin dir for %s: %w", programName, err)
		}

		if err := g.generateProcessScripts(binDir, programName, supervisorBin, configPath, serverURL); err != nil {
			return fmt.Errorf("failed to generate scripts for %s: %w", programName, err)
		}
	}

	// Generate per-node scripts for config servers
	for _, node := range g.topology.ConfigSvr {
		programName := fmt.Sprintf("mongod-%d", node.Port)
		processDir := filepath.Join(g.clusterDir, programName)
		binDir := filepath.Join(processDir, "bin")

		if err := os.MkdirAll(binDir, 0755); err != nil {
			return fmt.Errorf("failed to create bin dir for %s: %w", programName, err)
		}

		if err := g.generateProcessScripts(binDir, programName, supervisorBin, configPath, serverURL); err != nil {
			return fmt.Errorf("failed to generate scripts for %s: %w", programName, err)
		}
	}

	// Generate per-node scripts for mongos
	for _, node := range g.topology.Mongos {
		programName := fmt.Sprintf("mongos-%d", node.Port)
		processDir := filepath.Join(g.clusterDir, programName)
		binDir := filepath.Join(processDir, "bin")

		if err := os.MkdirAll(binDir, 0755); err != nil {
			return fmt.Errorf("failed to create bin dir for %s: %w", programName, err)
		}

		if err := g.generateProcessScripts(binDir, programName, supervisorBin, configPath, serverURL); err != nil {
			return fmt.Errorf("failed to generate scripts for %s: %w", programName, err)
		}
	}

	return nil
}

// generateProcessScripts generates start/stop/status scripts for a single process
func (g *ConfigGenerator) generateProcessScripts(binDir, programName, supervisorBin, configPath, serverURL string) error {
	pidFile := filepath.Join(g.clusterDir, "supervisor.pid")

	// Generate start script
	startScript := fmt.Sprintf(`#!/bin/bash
# Auto-generated wrapper script for starting %s
set -e

SUPERVISOR_BIN="%s"
SUPERVISOR_CONFIG="%s"
SUPERVISOR_PID="%s"
SERVER_URL="%s"

# Check if supervisor is running
if [ -f "$SUPERVISOR_PID" ] && kill -0 $(cat "$SUPERVISOR_PID" 2>/dev/null) 2>/dev/null; then
    echo "Supervisor already running"
else
    echo "Starting supervisor daemon..."
    "$SUPERVISOR_BIN" -c "$SUPERVISOR_CONFIG"
    sleep 1
fi

echo "Starting %s..."
"$SUPERVISOR_BIN" ctl -c "$SUPERVISOR_CONFIG" -s "$SERVER_URL" start %s
"$SUPERVISOR_BIN" ctl -c "$SUPERVISOR_CONFIG" -s "$SERVER_URL" status %s
`, programName, supervisorBin, configPath, pidFile, serverURL, programName, programName, programName)

	startPath := filepath.Join(binDir, "start")
	if err := os.WriteFile(startPath, []byte(startScript), 0755); err != nil {
		return fmt.Errorf("failed to write start script: %w", err)
	}

	// Generate stop script
	stopScript := fmt.Sprintf(`#!/bin/bash
# Auto-generated wrapper script for stopping %s
set -e

SUPERVISOR_BIN="%s"
SUPERVISOR_CONFIG="%s"
SERVER_URL="%s"

echo "Stopping %s..."
"$SUPERVISOR_BIN" ctl -c "$SUPERVISOR_CONFIG" -s "$SERVER_URL" stop %s
"$SUPERVISOR_BIN" ctl -c "$SUPERVISOR_CONFIG" -s "$SERVER_URL" status %s
`, programName, supervisorBin, configPath, serverURL, programName, programName, programName)

	stopPath := filepath.Join(binDir, "stop")
	if err := os.WriteFile(stopPath, []byte(stopScript), 0755); err != nil {
		return fmt.Errorf("failed to write stop script: %w", err)
	}

	// Generate status script
	statusScript := fmt.Sprintf(`#!/bin/bash
# Auto-generated wrapper script for checking %s status
SUPERVISOR_BIN="%s"
SUPERVISOR_CONFIG="%s"
SERVER_URL="%s"

"$SUPERVISOR_BIN" ctl -c "$SUPERVISOR_CONFIG" -s "$SERVER_URL" status %s
`, programName, supervisorBin, configPath, serverURL, programName)

	statusPath := filepath.Join(binDir, "status")
	if err := os.WriteFile(statusPath, []byte(statusScript), 0755); err != nil {
		return fmt.Errorf("failed to write status script: %w", err)
	}

	return nil
}

// generateSupervisorctlWrapper creates a supervisorctl wrapper in the cluster's bin directory
// that automatically uses the correct config and server URL for this cluster
func (g *ConfigGenerator) generateSupervisorctlWrapper(binDir, supervisorBin, configPath, serverURL string) error {
	// The supervisorctl command is: supervisord ctl -c <config> -s <server_url> <args...>
	supervisorctlScript := fmt.Sprintf(`#!/bin/bash
# Auto-generated supervisorctl wrapper for cluster %s
# This script automatically connects to the correct supervisor instance

SUPERVISOR_BIN="%s"
SUPERVISOR_CONFIG="%s"
SERVER_URL="%s"

# Pass all arguments to supervisorctl
exec "$SUPERVISOR_BIN" ctl -c "$SUPERVISOR_CONFIG" -s "$SERVER_URL" "$@"
`, g.clusterName, supervisorBin, configPath, serverURL)

	supervisorctlPath := filepath.Join(binDir, "supervisorctl")
	if err := os.WriteFile(supervisorctlPath, []byte(supervisorctlScript), 0755); err != nil {
		return fmt.Errorf("failed to write supervisorctl wrapper: %w", err)
	}

	return nil
}
