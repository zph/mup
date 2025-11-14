package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/zph/mup/pkg/topology"
)

// ConfigGenerator generates supervisord configuration files for a cluster
type ConfigGenerator struct {
	clusterDir  string
	clusterName string
	topology    *topology.Topology
	version     string
	binPath     string
}

// NewConfigGenerator creates a new config generator
func NewConfigGenerator(clusterDir, clusterName string, topo *topology.Topology, version, binPath string) *ConfigGenerator {
	return &ConfigGenerator{
		clusterDir:  clusterDir,
		clusterName: clusterName,
		topology:    topo,
		version:     version,
		binPath:     binPath,
	}
}

// GenerateAll generates all supervisord configuration files
// NOTE: We generate a single config file with all programs because the supervisord
// library doesn't properly expand [include] glob patterns during Load()
func (g *ConfigGenerator) GenerateAll() error {
	// Generate single supervisor.ini with all programs included directly
	if err := g.GenerateUnifiedConfig(); err != nil {
		return fmt.Errorf("failed to generate unified config: %w", err)
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

	// Write HTTP server section
	fmt.Fprintf(file, "[inet_http_server]\n")
	fmt.Fprintf(file, "port = 127.0.0.1:9001\n\n")

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
	configPath := filepath.Join(g.clusterDir, "conf", fmt.Sprintf("%s-%d", host, port), "mongod.conf")
	dataDir := filepath.Join(g.clusterDir, "data", fmt.Sprintf("%s-%d", host, port))
	logFile := filepath.Join(g.clusterDir, "logs", fmt.Sprintf("supervisor-mongod-%d.log", port))
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
	configPath := filepath.Join(g.clusterDir, "conf", fmt.Sprintf("%s-%d", host, port), "mongos.conf")
	logFile := filepath.Join(g.clusterDir, "logs", fmt.Sprintf("supervisor-mongos-%d.log", port))
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
