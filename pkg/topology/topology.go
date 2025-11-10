package topology

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Topology represents the complete cluster topology
type Topology struct {
	Global      GlobalConfig     `yaml:"global"`
	Mongod      []MongodNode     `yaml:"mongod_servers,omitempty"`
	Mongos      []MongosNode     `yaml:"mongos_servers,omitempty"`
	ConfigSvr   []ConfigNode     `yaml:"config_servers,omitempty"`
	ReplicaSets []ReplicaSetSpec `yaml:"replica_sets,omitempty"`
}

// GlobalConfig contains global configuration for all nodes
type GlobalConfig struct {
	User            string            `yaml:"user"`
	SSHPort         int               `yaml:"ssh_port"`
	DeployDir       string            `yaml:"deploy_dir"`
	DataDir         string            `yaml:"data_dir"`
	LogDir          string            `yaml:"log_dir"`
	ConfigDir       string            `yaml:"config_dir"`
	RuntimeConfig   map[string]any    `yaml:"runtime_config,omitempty"`
	SystemdConfig   map[string]string `yaml:"systemd_config,omitempty"`
	OSConfig        map[string]string `yaml:"os_config,omitempty"`
	ResourceControl *ResourceControl  `yaml:"resource_control,omitempty"`
}

// ResourceControl defines resource limits
type ResourceControl struct {
	MemoryLimit string `yaml:"memory_limit,omitempty"`
	CPUQuota    string `yaml:"cpu_quota,omitempty"`
	IOWeight    uint64 `yaml:"io_weight,omitempty"`
}

// MongodNode represents a mongod server configuration
type MongodNode struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	ReplicaSet string `yaml:"replica_set,omitempty"`
	//Role          string            `yaml:"role,omitempty"` // "primary", "secondary", "arbiter"
	Priority      *float64       `yaml:"priority,omitempty"`
	Hidden        *bool          `yaml:"hidden,omitempty"`
	Votes         *int           `yaml:"votes,omitempty"`
	DeployDir     string         `yaml:"deploy_dir,omitempty"`
	DataDir       string         `yaml:"data_dir,omitempty"`
	LogDir        string         `yaml:"log_dir,omitempty"`
	ConfigDir     string         `yaml:"config_dir,omitempty"`
	RuntimeConfig map[string]any `yaml:"runtime_config,omitempty"`
}

// MongosNode represents a mongos router configuration
type MongosNode struct {
	Host          string         `yaml:"host"`
	Port          int            `yaml:"port"`
	DeployDir     string         `yaml:"deploy_dir,omitempty"`
	LogDir        string         `yaml:"log_dir,omitempty"`
	ConfigDir     string         `yaml:"config_dir,omitempty"`
	RuntimeConfig map[string]any `yaml:"runtime_config,omitempty"`
}

// ConfigNode represents a config server configuration
type ConfigNode struct {
	Host          string         `yaml:"host"`
	Port          int            `yaml:"port"`
	ReplicaSet    string         `yaml:"replica_set"`
	DeployDir     string         `yaml:"deploy_dir,omitempty"`
	DataDir       string         `yaml:"data_dir,omitempty"`
	LogDir        string         `yaml:"log_dir,omitempty"`
	ConfigDir     string         `yaml:"config_dir,omitempty"`
	RuntimeConfig map[string]any `yaml:"runtime_config,omitempty"`
}

// ReplicaSetSpec defines a replica set configuration
type ReplicaSetSpec struct {
	Name    string   `yaml:"name"`
	Members []string `yaml:"members"` // host:port format
}

// ParseTopologyFile parses a topology YAML file
func ParseTopologyFile(path string) (*Topology, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read topology file: %w", err)
	}

	var topology Topology
	if err := yaml.Unmarshal(data, &topology); err != nil {
		return nil, fmt.Errorf("failed to parse topology YAML: %w", err)
	}

	// Apply global defaults to nodes
	if err := topology.applyDefaults(); err != nil {
		return nil, fmt.Errorf("failed to apply defaults: %w", err)
	}

	return &topology, nil
}

// applyDefaults applies global configuration to individual nodes
func (t *Topology) applyDefaults() error {
	// Apply defaults to mongod nodes
	for i := range t.Mongod {
		node := &t.Mongod[i]
		if node.DeployDir == "" {
			node.DeployDir = t.Global.DeployDir
		}
		if node.DataDir == "" {
			node.DataDir = t.Global.DataDir
		}
		if node.LogDir == "" {
			node.LogDir = t.Global.LogDir
		}
		if node.ConfigDir == "" {
			node.ConfigDir = t.Global.ConfigDir
		}
		// Merge runtime config
		if node.RuntimeConfig == nil && t.Global.RuntimeConfig != nil {
			node.RuntimeConfig = make(map[string]any)
		}
		for k, v := range t.Global.RuntimeConfig {
			if _, exists := node.RuntimeConfig[k]; !exists {
				node.RuntimeConfig[k] = v
			}
		}
	}

	// Apply defaults to mongos nodes
	for i := range t.Mongos {
		node := &t.Mongos[i]
		if node.DeployDir == "" {
			node.DeployDir = t.Global.DeployDir
		}
		if node.LogDir == "" {
			node.LogDir = t.Global.LogDir
		}
		if node.ConfigDir == "" {
			node.ConfigDir = t.Global.ConfigDir
		}
		// Merge runtime config
		if node.RuntimeConfig == nil && t.Global.RuntimeConfig != nil {
			node.RuntimeConfig = make(map[string]any)
		}
		for k, v := range t.Global.RuntimeConfig {
			if _, exists := node.RuntimeConfig[k]; !exists {
				node.RuntimeConfig[k] = v
			}
		}
	}

	// Apply defaults to config server nodes
	for i := range t.ConfigSvr {
		node := &t.ConfigSvr[i]
		if node.DeployDir == "" {
			node.DeployDir = t.Global.DeployDir
		}
		if node.DataDir == "" {
			node.DataDir = t.Global.DataDir
		}
		if node.LogDir == "" {
			node.LogDir = t.Global.LogDir
		}
		if node.ConfigDir == "" {
			node.ConfigDir = t.Global.ConfigDir
		}
		// Merge runtime config
		if node.RuntimeConfig == nil && t.Global.RuntimeConfig != nil {
			node.RuntimeConfig = make(map[string]any)
		}
		for k, v := range t.Global.RuntimeConfig {
			if _, exists := node.RuntimeConfig[k]; !exists {
				node.RuntimeConfig[k] = v
			}
		}
	}

	return nil
}

// Validate validates the topology configuration
func (t *Topology) Validate() error {
	// Check that we have at least some nodes
	if len(t.Mongod) == 0 && len(t.Mongos) == 0 && len(t.ConfigSvr) == 0 {
		return fmt.Errorf("topology must contain at least one node")
	}

	// Validate global config
	if t.Global.User == "" {
		return fmt.Errorf("global.user is required")
	}
	if t.Global.DeployDir == "" {
		return fmt.Errorf("global.deploy_dir is required")
	}
	if t.Global.DataDir == "" {
		t.Global.DataDir = filepath.Join(t.Global.DeployDir, "data")
	}
	if t.Global.LogDir == "" {
		t.Global.LogDir = filepath.Join(t.Global.DeployDir, "logs")
	}
	if t.Global.ConfigDir == "" {
		t.Global.ConfigDir = filepath.Join(t.Global.DeployDir, "conf")
	}

	// Check if this is a local deployment (allows port 0 for auto-allocation)
	isLocal := t.IsLocalDeployment()

	// Validate mongod nodes
	seenPorts := make(map[string]bool)
	for _, node := range t.Mongod {
		if node.Host == "" {
			return fmt.Errorf("mongod node missing host")
		}
		// Port 0 is allowed for local deployments (means auto-allocate)
		if node.Port == 0 && !isLocal {
			return fmt.Errorf("mongod node %s missing port", node.Host)
		}

		// Check for port conflicts on the same host (skip if port is 0)
		if node.Port > 0 {
			key := fmt.Sprintf("%s:%d", node.Host, node.Port)
			if seenPorts[key] {
				return fmt.Errorf("duplicate port %d on host %s", node.Port, node.Host)
			}
			seenPorts[key] = true
		}
	}

	// Validate mongos nodes
	for _, node := range t.Mongos {
		if node.Host == "" {
			return fmt.Errorf("mongos node missing host")
		}
		if node.Port == 0 && !isLocal {
			return fmt.Errorf("mongos node %s missing port", node.Host)
		}

		if node.Port > 0 {
			key := fmt.Sprintf("%s:%d", node.Host, node.Port)
			if seenPorts[key] {
				return fmt.Errorf("duplicate port %d on host %s", node.Port, node.Host)
			}
			seenPorts[key] = true
		}
	}

	// Validate config server nodes
	for _, node := range t.ConfigSvr {
		if node.Host == "" {
			return fmt.Errorf("config server node missing host")
		}
		if node.Port == 0 && !isLocal {
			return fmt.Errorf("config server node %s missing port", node.Host)
		}
		if node.ReplicaSet == "" {
			return fmt.Errorf("config server node %s missing replica_set", node.Host)
		}

		if node.Port > 0 {
			key := fmt.Sprintf("%s:%d", node.Host, node.Port)
			if seenPorts[key] {
				return fmt.Errorf("duplicate port %d on host %s", node.Port, node.Host)
			}
			seenPorts[key] = true
		}
	}

	return nil
}

// GetTopologyType returns the type of topology (standalone, replica_set, sharded)
func (t *Topology) GetTopologyType() string {
	if len(t.Mongos) > 0 || len(t.ConfigSvr) > 0 {
		return "sharded"
	}

	// Check if mongod nodes have replica set configured
	for _, node := range t.Mongod {
		if node.ReplicaSet != "" {
			return "replica_set"
		}
	}

	return "standalone"
}

// IsLocalDeployment returns true if all hosts are localhost
func (t *Topology) IsLocalDeployment() bool {
	isLocal := func(host string) bool {
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}

	for _, node := range t.Mongod {
		if !isLocal(node.Host) {
			return false
		}
	}

	for _, node := range t.Mongos {
		if !isLocal(node.Host) {
			return false
		}
	}

	for _, node := range t.ConfigSvr {
		if !isLocal(node.Host) {
			return false
		}
	}

	return true
}

// GetAllHosts returns a unique list of all hosts in the topology
func (t *Topology) GetAllHosts() []string {
	hostMap := make(map[string]bool)

	for _, node := range t.Mongod {
		hostMap[node.Host] = true
	}
	for _, node := range t.Mongos {
		hostMap[node.Host] = true
	}
	for _, node := range t.ConfigSvr {
		hostMap[node.Host] = true
	}

	hosts := make([]string, 0, len(hostMap))
	for host := range hostMap {
		hosts = append(hosts, host)
	}

	return hosts
}

// GetNodeID returns a unique identifier for a node
func GetNodeID(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}

// ParseNodeID parses a node ID into host and port
func ParseNodeID(nodeID string) (string, int, error) {
	parts := strings.Split(nodeID, ":")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid node ID format: %s", nodeID)
	}

	port := 0
	_, err := fmt.Sscanf(parts[1], "%d", &port)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port in node ID: %s", nodeID)
	}

	return parts[0], port, nil
}
