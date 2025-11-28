package simulation

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Scenario represents a complete simulation scenario from a YAML file
// REQ-SIM-041: Load simulation scenarios from YAML configuration files
type Scenario struct {
	Responses  map[string]string `yaml:"responses"`
	Failures   []FailureSpec     `yaml:"failures"`
	Filesystem FilesystemSpec    `yaml:"filesystem"`
	Processes  ProcessSpec       `yaml:"processes"`
}

// FailureSpec defines when an operation should fail
// REQ-SIM-042: Custom responses for operations
type FailureSpec struct {
	Operation string `yaml:"operation"`
	Target    string `yaml:"target"`
	Error     string `yaml:"error"`
}

// FilesystemSpec defines preconfigured filesystem state
// REQ-SIM-025: Preconfigure simulation state
type FilesystemSpec struct {
	ExistingFiles       []string          `yaml:"existing_files"`
	ExistingDirectories []string          `yaml:"existing_directories"`
	FileContents        map[string]string `yaml:"file_contents"` // Optional: specific file contents
}

// ProcessSpec defines preconfigured process state
// REQ-SIM-025: Preconfigure simulation state
type ProcessSpec struct {
	Running []ProcessInfo `yaml:"running"`
}

// ProcessInfo describes a running process in a scenario
type ProcessInfo struct {
	Command string `yaml:"command"`
	Port    int    `yaml:"port"`
	PID     int    `yaml:"pid,omitempty"`
}

// ScenarioFile is the root structure of a scenario YAML file
type ScenarioFile struct {
	Simulation Scenario `yaml:"simulation"`
}

// LoadScenarioFromFile loads a simulation scenario from a YAML file
// REQ-SIM-041: Support loading simulation scenarios from YAML configuration files
func LoadScenarioFromFile(path string) (*Scenario, error) {
	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read scenario file: %w", err)
	}

	// Parse YAML
	var scenarioFile ScenarioFile
	if err := yaml.Unmarshal(data, &scenarioFile); err != nil {
		return nil, fmt.Errorf("failed to parse scenario YAML: %w", err)
	}

	return &scenarioFile.Simulation, nil
}

// ApplyScenarioToConfig applies a scenario to a simulation config
// REQ-SIM-042: When a simulation scenario specifies custom responses, use those responses
func ApplyScenarioToConfig(scenario *Scenario, config *Config) {
	if scenario == nil {
		return
	}

	config.mu.Lock()
	defer config.mu.Unlock()

	// Apply custom responses
	for command, response := range scenario.Responses {
		config.Responses[command] = response
	}

	// Apply failures
	for _, failure := range scenario.Failures {
		config.Failures = append(config.Failures, ConfiguredFailure{
			Operation: failure.Operation,
			Target:    failure.Target,
			Error:     failure.Error,
		})
	}

	// Apply filesystem state
	for _, file := range scenario.Filesystem.ExistingFiles {
		// If specific content is provided, use it; otherwise use empty content
		content := []byte{}
		if fileContent, ok := scenario.Filesystem.FileContents[file]; ok {
			content = []byte(fileContent)
		}
		config.ExistingFiles[file] = content
	}

	for _, dir := range scenario.Filesystem.ExistingDirectories {
		config.ExistingDirectories = append(config.ExistingDirectories, dir)
	}

	// Apply running processes
	for _, proc := range scenario.Processes.Running {
		command := proc.Command
		if proc.Port > 0 {
			command = fmt.Sprintf("%s --port %d", proc.Command, proc.Port)
		}
		config.RunningProcesses = append(config.RunningProcesses, command)
	}
}

// LoadConfigWithScenario creates a new config with a scenario applied
// REQ-SIM-041, REQ-SIM-042: Convenience function for loading config with scenario
func LoadConfigWithScenario(scenarioPath string) (*Config, error) {
	scenario, err := LoadScenarioFromFile(scenarioPath)
	if err != nil {
		return nil, err
	}

	config := NewConfig()
	ApplyScenarioToConfig(scenario, config)

	return config, nil
}

// NewExecutorWithScenario creates a new executor with a scenario file
// REQ-SIM-041, REQ-SIM-025: Create executor with preconfigured scenario
func NewExecutorWithScenario(scenarioPath string) (*SimulationExecutor, error) {
	config, err := LoadConfigWithScenario(scenarioPath)
	if err != nil {
		return nil, err
	}

	return NewExecutor(config), nil
}

// SaveScenarioToFile saves a scenario to a YAML file
// Useful for generating scenario templates
func SaveScenarioToFile(scenario *Scenario, path string) error {
	scenarioFile := ScenarioFile{
		Simulation: *scenario,
	}

	data, err := yaml.Marshal(scenarioFile)
	if err != nil {
		return fmt.Errorf("failed to marshal scenario: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write scenario file: %w", err)
	}

	return nil
}

// GenerateScenarioTemplate creates a template scenario with common patterns
// REQ-SIM-043: Provide templates for common scenarios
func GenerateScenarioTemplate(templateType string) *Scenario {
	switch templateType {
	case "port-conflict":
		return &Scenario{
			Responses: map[string]string{
				"mongod --version": "db version v7.0.5",
			},
			Failures: []FailureSpec{
				{
					Operation: "background",
					Target:    "mongod --port 27017",
					Error:     "port 27017 already in use",
				},
			},
		}

	case "permission-denied":
		return &Scenario{
			Responses: map[string]string{
				"whoami": "mongodb",
			},
			Failures: []FailureSpec{
				{
					Operation: "create_directory",
					Target:    "/etc/mongodb",
					Error:     "permission denied",
				},
				{
					Operation: "upload_file",
					Target:    "/etc/mongod.conf",
					Error:     "permission denied",
				},
			},
		}

	case "disk-full":
		return &Scenario{
			Responses: map[string]string{
				"df -h /data": "/dev/sda1  100G  99.5G  500M  99% /data",
			},
			Failures: []FailureSpec{
				{
					Operation: "upload_file",
					Target:    "*", // Wildcard: all file uploads fail
					Error:     "no space left on device",
				},
			},
		}

	case "network-failure":
		return &Scenario{
			Failures: []FailureSpec{
				{
					Operation: "execute",
					Target:    "*", // All command executions fail
					Error:     "connection refused",
				},
			},
		}

	case "existing-cluster":
		return &Scenario{
			Responses: map[string]string{
				"systemctl status mongod": "active (running)",
				"pgrep mongod":            "1234\n5678\n9012",
			},
			Filesystem: FilesystemSpec{
				ExistingFiles: []string{
					"/etc/mongod.conf",
					"/etc/systemd/system/mongod.service",
				},
				ExistingDirectories: []string{
					"/data/mongodb",
					"/var/log/mongodb",
				},
			},
			Processes: ProcessSpec{
				Running: []ProcessInfo{
					{Command: "mongod", Port: 27017, PID: 1234},
					{Command: "mongod", Port: 27018, PID: 5678},
					{Command: "mongod", Port: 27019, PID: 9012},
				},
			},
		}

	default:
		// Empty scenario - use defaults
		return &Scenario{
			Responses:  make(map[string]string),
			Failures:   []FailureSpec{},
			Filesystem: FilesystemSpec{},
			Processes:  ProcessSpec{},
		}
	}
}
