package simulation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REQ-SIM-041: Load simulation scenarios from YAML files
func TestLoadScenarioFromFile(t *testing.T) {
	// Create a temporary scenario file
	tmpDir := t.TempDir()
	scenarioFile := filepath.Join(tmpDir, "test-scenario.yaml")

	scenarioYAML := `
simulation:
  responses:
    "mongod --version": "db version v7.0.5"
    "systemctl status mongod": "active (running)"

  failures:
    - operation: upload_file
      target: /etc/mongod.conf
      error: "permission denied"

  filesystem:
    existing_files:
      - /var/lib/mongodb/data
      - /etc/systemd/system/mongod.service
    existing_directories:
      - /data/mongodb
      - /var/log/mongodb

  processes:
    running:
      - command: mongod
        port: 27017
`

	err := os.WriteFile(scenarioFile, []byte(scenarioYAML), 0644)
	require.NoError(t, err)

	// REQ-SIM-041: Load scenario from file
	scenario, err := LoadScenarioFromFile(scenarioFile)
	require.NoError(t, err)
	require.NotNil(t, scenario)

	// Verify responses
	assert.Equal(t, "db version v7.0.5", scenario.Responses["mongod --version"])
	assert.Equal(t, "active (running)", scenario.Responses["systemctl status mongod"])

	// Verify failures
	require.Len(t, scenario.Failures, 1)
	assert.Equal(t, "upload_file", scenario.Failures[0].Operation)
	assert.Equal(t, "/etc/mongod.conf", scenario.Failures[0].Target)
	assert.Equal(t, "permission denied", scenario.Failures[0].Error)

	// Verify filesystem state
	require.Len(t, scenario.Filesystem.ExistingFiles, 2)
	assert.Contains(t, scenario.Filesystem.ExistingFiles, "/var/lib/mongodb/data")
	assert.Contains(t, scenario.Filesystem.ExistingFiles, "/etc/systemd/system/mongod.service")

	require.Len(t, scenario.Filesystem.ExistingDirectories, 2)
	assert.Contains(t, scenario.Filesystem.ExistingDirectories, "/data/mongodb")
	assert.Contains(t, scenario.Filesystem.ExistingDirectories, "/var/log/mongodb")

	// Verify running processes
	require.Len(t, scenario.Processes.Running, 1)
	assert.Equal(t, "mongod", scenario.Processes.Running[0].Command)
	assert.Equal(t, 27017, scenario.Processes.Running[0].Port)
}

// REQ-SIM-042: Apply scenario configuration to executor
func TestApplyScenarioToConfig(t *testing.T) {
	scenario := &Scenario{
		Responses: map[string]string{
			"mongod --version": "db version v8.0.0",
		},
		Failures: []FailureSpec{
			{
				Operation: "create_directory",
				Target:    "/restricted/path",
				Error:     "access denied",
			},
		},
		Filesystem: FilesystemSpec{
			ExistingFiles:       []string{"/etc/mongod.conf"},
			ExistingDirectories: []string{"/data/mongodb"},
		},
		Processes: ProcessSpec{
			Running: []ProcessInfo{
				{Command: "mongod", Port: 27017},
			},
		},
	}

	// REQ-SIM-042: Apply scenario to config
	config := NewConfig()
	ApplyScenarioToConfig(scenario, config)

	// Verify responses were applied
	assert.Equal(t, "db version v8.0.0", config.GetResponse("mongod --version"))

	// Verify failures were applied
	shouldFail, errMsg := config.ShouldFail("create_directory", "/restricted/path")
	assert.True(t, shouldFail)
	assert.Equal(t, "access denied", errMsg)

	// Verify filesystem state was applied
	assert.Contains(t, config.ExistingFiles, "/etc/mongod.conf")
	assert.Contains(t, config.ExistingDirectories, "/data/mongodb")

	// Verify processes were applied
	require.Len(t, config.RunningProcesses, 1)
	assert.Equal(t, "mongod --port 27017", config.RunningProcesses[0])
}

// REQ-SIM-041: Handle missing scenario file gracefully
func TestLoadScenarioFromFile_MissingFile(t *testing.T) {
	_, err := LoadScenarioFromFile("/nonexistent/scenario.yaml")
	assert.Error(t, err)
}

// REQ-SIM-041: Handle invalid YAML gracefully
func TestLoadScenarioFromFile_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	scenarioFile := filepath.Join(tmpDir, "invalid.yaml")

	invalidYAML := `
simulation:
  responses:
    "test": [this is not valid YAML syntax
`

	err := os.WriteFile(scenarioFile, []byte(invalidYAML), 0644)
	require.NoError(t, err)

	_, err = LoadScenarioFromFile(scenarioFile)
	assert.Error(t, err)
}

// REQ-SIM-025: Create executor with preconfigured scenario
func TestNewExecutorWithScenario(t *testing.T) {
	scenario := &Scenario{
		Responses: map[string]string{
			"test-command": "test-response",
		},
		Filesystem: FilesystemSpec{
			ExistingFiles: []string{"/test/file"},
		},
	}

	// Create config from scenario
	config := NewConfig()
	ApplyScenarioToConfig(scenario, config)

	executor := NewExecutor(config)

	// Verify scenario was applied
	output, err := executor.Execute("test-command")
	require.NoError(t, err)
	assert.Equal(t, "test-response", output)

	// Verify preconfigured file exists
	exists, err := executor.FileExists("/test/file")
	require.NoError(t, err)
	assert.True(t, exists)
}

// REQ-SIM-042: Test complex scenario with multiple failure conditions
func TestComplexScenario(t *testing.T) {
	scenario := &Scenario{
		Responses: map[string]string{
			"mongod --version":           "db version v7.0.5",
			"systemctl status mongod":    "inactive (dead)",
			"df -h /data":                "/dev/sda1  10G  9.5G  500M  95% /data",
		},
		Failures: []FailureSpec{
			{
				Operation: "upload_file",
				Target:    "/etc/mongod.conf",
				Error:     "permission denied",
			},
			{
				Operation: "create_directory",
				Target:    "/data/full",
				Error:     "no space left on device",
			},
		},
	}

	config := NewConfig()
	ApplyScenarioToConfig(scenario, config)
	executor := NewExecutor(config)

	// Test responses
	output, _ := executor.Execute("mongod --version")
	assert.Equal(t, "db version v7.0.5", output)

	output, _ = executor.Execute("df -h /data")
	assert.Contains(t, output, "95%")

	// Test failures
	err := executor.UploadFile("local.conf", "/etc/mongod.conf")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")

	err = executor.CreateDirectory("/data/full", 0755)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no space left")
}
