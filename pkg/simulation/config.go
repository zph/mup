package simulation

import "sync"

// REQ-SIM-041 to REQ-SIM-043: Simulation configuration
type Config struct {
	// REQ-SIM-043: Default responses for common commands
	Responses map[string]string

	// REQ-SIM-018, REQ-SIM-042: Configured failures for testing
	Failures []ConfiguredFailure

	// REQ-SIM-007: Allow reading actual files from disk
	AllowRealFileReads bool

	// REQ-SIM-025: Preconfigured filesystem state
	ExistingFiles       map[string][]byte
	ExistingDirectories []string

	// REQ-SIM-025: Preconfigured process state
	RunningProcesses []string

	mu sync.RWMutex
}

// NewConfig creates a new simulation configuration with sensible defaults
func NewConfig() *Config {
	config := &Config{
		Responses:           make(map[string]string),
		Failures:            make([]ConfiguredFailure, 0),
		AllowRealFileReads:  false,
		ExistingFiles:       make(map[string][]byte),
		ExistingDirectories: make([]string, 0),
		RunningProcesses:    make([]string, 0),
	}

	// REQ-SIM-043: Set sensible default responses for common operations
	config.setDefaultResponses()

	return config
}

// setDefaultResponses configures default responses for common commands
// REQ-SIM-043: Sensible defaults enable zero-config simulation
func (c *Config) setDefaultResponses() {
	// MongoDB version checks
	c.Responses["mongod --version"] = "db version v7.0.5\nBuild Info: {\"version\":\"7.0.5\"}"
	c.Responses["mongos --version"] = "mongos version v7.0.5"

	// System checks
	c.Responses["whoami"] = "mongodb"
	c.Responses["hostname"] = "simulated-host"
	c.Responses["uname -s"] = "Linux"
	c.Responses["uname -m"] = "x86_64"

	// Systemd checks
	c.Responses["systemctl is-active mongod"] = "active"
	c.Responses["systemctl is-enabled mongod"] = "enabled"
	c.Responses["systemctl status mongod"] = "● mongod.service - MongoDB Database Server\n   Loaded: loaded\n   Active: active (running)"

	// MongoDB replica set commands
	c.Responses["mongosh --eval rs.initiate()"] = `{"ok": 1}`
	c.Responses["mongosh --eval rs.status()"] = `{"set": "rs0", "members": [...], "ok": 1}`
	c.Responses["mongosh --eval rs.add()"] = `{"ok": 1}`
	c.Responses["mongosh --eval rs.conf()"] = `{"_id": "rs0", "members": [...], "ok": 1}`

	// MongoDB sharding commands
	c.Responses["mongosh --eval sh.addShard()"] = `{"shardAdded": "shard0", "ok": 1}`
	c.Responses["mongosh --eval sh.status()"] = `{"shards": [...], "databases": [...], "ok": 1}`
	c.Responses["mongosh --eval sh.enableSharding()"] = `{"ok": 1}`

	// Process checks
	c.Responses["pgrep mongod"] = "1234\n5678"
	c.Responses["ps aux | grep mongod"] = "mongodb  1234  0.5  2.0  mongod --config /etc/mongod.conf"

	// File system checks
	c.Responses["df -h /data"] = "/dev/sda1  500G  50G  450G  10% /data"
	c.Responses["id mongodb"] = "uid=999(mongodb) gid=999(mongodb) groups=999(mongodb)"
}

// SetResponse configures a custom response for a command
// REQ-SIM-013, REQ-SIM-042: Allow custom responses for testing
func (c *Config) SetResponse(command, response string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Responses[command] = response
}

// GetResponse retrieves the configured response for a command
// REQ-SIM-013: Return configured response or default
func (c *Config) GetResponse(command string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if response, ok := c.Responses[command]; ok {
		return response
	}

	// Default empty response for unconfigured commands
	return ""
}

// SetFailure configures an operation to fail
// REQ-SIM-018, REQ-SIM-042: Support configurable failures for testing
func (c *Config) SetFailure(operation, target, errorMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Failures = append(c.Failures, ConfiguredFailure{
		Operation: operation,
		Target:    target,
		Error:     errorMsg,
	})
}

// ShouldFail checks if an operation should fail based on configuration
// REQ-SIM-018: Check configured failures
func (c *Config) ShouldFail(operation, target string) (bool, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, failure := range c.Failures {
		if failure.Operation == operation && failure.Target == target {
			return true, failure.Error
		}
	}

	return false, ""
}

// AddExistingFile preconfigures a file that exists in simulation
// REQ-SIM-025: Preconfigure simulation state
func (c *Config) AddExistingFile(path string, content []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ExistingFiles[path] = content
}

// AddExistingDirectory preconfigures a directory that exists in simulation
// REQ-SIM-025: Preconfigure simulation state
func (c *Config) AddExistingDirectory(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ExistingDirectories = append(c.ExistingDirectories, path)
}

// AddRunningProcess preconfigures a process that is running in simulation
// REQ-SIM-025: Preconfigure simulation state
func (c *Config) AddRunningProcess(command string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.RunningProcesses = append(c.RunningProcesses, command)
}
