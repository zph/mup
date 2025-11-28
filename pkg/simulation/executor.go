package simulation

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zph/mup/pkg/executor"
)

// SimulationExecutor implements the Executor interface for simulation mode
// REQ-SIM-016: Implements Executor interface
// REQ-SIM-017: Records operations and returns simulated results
type SimulationExecutor struct {
	config *Config
	state  *SimulationState
	mu     sync.RWMutex
}

// Ensure SimulationExecutor implements Executor interface
var _ executor.Executor = (*SimulationExecutor)(nil)

// NewExecutor creates a new simulation executor
// REQ-SIM-001: Initialize simulation executor
func NewExecutor(config *Config) *SimulationExecutor {
	exec := &SimulationExecutor{
		config: config,
		state:  NewSimulationState(),
	}

	// REQ-SIM-025: Initialize preconfigured state
	exec.initializePreconfiguredState()

	return exec
}

// initializePreconfiguredState sets up the simulation with preconfigured files, dirs, and processes
// REQ-SIM-025: Support preconfigured state for testing scenarios
func (e *SimulationExecutor) initializePreconfiguredState() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Add preconfigured files
	for path, content := range e.config.ExistingFiles {
		e.state.Files[path] = &SimulatedFile{
			Path:      path,
			Content:   content,
			Mode:      0644,
			CreatedAt: e.state.StartTime,
		}
	}

	// Add preconfigured directories
	for _, path := range e.config.ExistingDirectories {
		e.state.Dirs[path] = &SimulatedDirectory{
			Path:      path,
			Mode:      0755,
			CreatedAt: e.state.StartTime,
		}
	}

	// Add preconfigured running processes
	for _, command := range e.config.RunningProcesses {
		pid := e.state.AllocatePID()
		e.state.Processes[pid] = &SimulatedProcess{
			PID:       pid,
			Command:   command,
			State:     ProcessStateRunning,
			StartTime: e.state.StartTime,
		}
	}
}

// GetOperations returns all recorded operations
// REQ-SIM-002: Track all operations for reporting
func (e *SimulationExecutor) GetOperations() []Operation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state.Operations
}

// GetState returns the current simulation state
// REQ-SIM-002: Access simulation state
func (e *SimulationExecutor) GetState() *SimulationState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// ========== File Operations ==========

// CreateDirectory simulates creating a directory
// REQ-SIM-004: Record filesystem operations without modifying actual filesystem
// Mimics `mkdir -p` behavior by creating parent directories
func (e *SimulationExecutor) CreateDirectory(path string, mode os.FileMode) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-018: Check for configured failures
	if shouldFail, errMsg := e.config.ShouldFail("create_directory", path); shouldFail {
		e.state.RecordFailure("create_directory", path, "", errMsg)
		return errors.New(errMsg)
	}

	// REQ-SIM-004: Add to simulated filesystem state (not actual filesystem)
	// Create parent directories too (like mkdir -p)
	currentPath := path
	for currentPath != "." && currentPath != "/" {
		if _, exists := e.state.Dirs[currentPath]; !exists {
			e.state.Dirs[currentPath] = &SimulatedDirectory{
				Path:      currentPath,
				Mode:      uint32(mode),
				CreatedAt: e.state.StartTime,
			}
		}
		currentPath = filepath.Dir(currentPath)
		if currentPath == currentPath+string(filepath.Separator) {
			break // Reached root
		}
	}

	// REQ-SIM-002: Record operation (only record the requested path, not parents)
	e.state.RecordOperation("create_directory", path, fmt.Sprintf("mode=%o", mode), nil)

	return nil
}

// UploadFile simulates uploading a file
// REQ-SIM-004: Record filesystem operations without modifying actual filesystem
func (e *SimulationExecutor) UploadFile(localPath, remotePath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-018: Check for configured failures
	if shouldFail, errMsg := e.config.ShouldFail("upload_file", remotePath); shouldFail {
		e.state.RecordFailure("upload_file", remotePath, localPath, errMsg)
		return errors.New(errMsg)
	}

	// REQ-SIM-007: If AllowRealFileReads, read actual content
	var content []byte
	if e.config.AllowRealFileReads {
		var err error
		content, err = os.ReadFile(localPath)
		if err != nil {
			// File doesn't exist locally, use placeholder
			content = []byte("simulated file content from " + localPath)
		}
	} else {
		content = []byte("simulated file content from " + localPath)
	}

	// REQ-SIM-004: Add to simulated filesystem
	e.state.Files[remotePath] = &SimulatedFile{
		Path:      remotePath,
		Content:   content,
		Mode:      0644,
		CreatedAt: e.state.StartTime,
	}

	// REQ-SIM-002: Record operation
	e.state.RecordOperation("upload_file", remotePath, localPath, map[string]interface{}{
		"size": len(content),
	})

	return nil
}

// UploadContent simulates uploading content to a file
// REQ-SIM-004: Record filesystem operations without modifying actual filesystem
func (e *SimulationExecutor) UploadContent(content []byte, remotePath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-018: Check for configured failures
	if shouldFail, errMsg := e.config.ShouldFail("upload_content", remotePath); shouldFail {
		e.state.RecordFailure("upload_content", remotePath, "", errMsg)
		return errors.New(errMsg)
	}

	// REQ-SIM-004: Add to simulated filesystem
	e.state.Files[remotePath] = &SimulatedFile{
		Path:      remotePath,
		Content:   content,
		Mode:      0644,
		CreatedAt: e.state.StartTime,
	}

	// REQ-SIM-002: Record operation
	e.state.RecordOperation("upload_content", remotePath, fmt.Sprintf("%d bytes", len(content)), map[string]interface{}{
		"size": len(content),
	})

	return nil
}

// DownloadFile simulates downloading a file
// REQ-SIM-004: Record filesystem operations without modifying actual filesystem
func (e *SimulationExecutor) DownloadFile(remotePath, localPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-018: Check for configured failures
	if shouldFail, errMsg := e.config.ShouldFail("download_file", remotePath); shouldFail {
		e.state.RecordFailure("download_file", remotePath, localPath, errMsg)
		return errors.New(errMsg)
	}

	// REQ-SIM-005: Check if file exists in simulated filesystem
	if _, exists := e.state.Files[remotePath]; !exists {
		return fmt.Errorf("file not found in simulated filesystem: %s", remotePath)
	}

	// REQ-SIM-002: Record operation (but don't actually write to local filesystem)
	e.state.RecordOperation("download_file", remotePath, localPath, nil)

	return nil
}

// FileExists checks if a file OR directory exists
// REQ-SIM-005: Check simulated filesystem state
// REQ-SIM-007: Optionally check actual filesystem
func (e *SimulationExecutor) FileExists(path string) (bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// REQ-SIM-007: Check actual filesystem if allowed
	if e.config.AllowRealFileReads {
		if _, err := os.Stat(path); err == nil {
			return true, nil
		}
	}

	// REQ-SIM-005: Check simulated filesystem (both files and directories)
	if _, exists := e.state.Files[path]; exists {
		return true, nil
	}
	if _, exists := e.state.Dirs[path]; exists {
		return true, nil
	}

	return false, nil
}

// RemoveFile simulates removing a file
// REQ-SIM-004: Record filesystem operations without modifying actual filesystem
func (e *SimulationExecutor) RemoveFile(path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-004: Remove from simulated filesystem
	delete(e.state.Files, path)

	// REQ-SIM-002: Record operation
	e.state.RecordOperation("remove_file", path, "", nil)

	return nil
}

// RemoveDirectory simulates removing a directory
// REQ-SIM-004: Record filesystem operations without modifying actual filesystem
func (e *SimulationExecutor) RemoveDirectory(path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-004: Remove from simulated filesystem
	delete(e.state.Dirs, path)

	// REQ-SIM-002: Record operation
	e.state.RecordOperation("remove_directory", path, "", nil)

	return nil
}

// ========== Command Execution ==========

// Execute simulates executing a command
// REQ-SIM-013: Return configured response
func (e *SimulationExecutor) Execute(command string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-018: Check for configured failures
	if shouldFail, errMsg := e.config.ShouldFail("execute", command); shouldFail {
		e.state.RecordFailure("execute", command, "", errMsg)
		return "", errors.New(errMsg)
	}

	// REQ-SIM-013: Get configured response
	response := e.config.GetResponse(command)

	// REQ-SIM-002: Record operation
	// Target is "shell" to avoid redundancy, Details contains the actual command
	e.state.RecordOperation("execute", "shell", command, map[string]interface{}{
		"output": response,
	})

	return response, nil
}

// ExecuteWithInput simulates executing a command with stdin
// REQ-SIM-013: Return configured response
func (e *SimulationExecutor) ExecuteWithInput(command string, stdin io.Reader) (string, error) {
	// For simulation, we ignore stdin and just use configured response
	return e.Execute(command)
}

// MongoExecute simulates MongoDB driver operations
// REQ-SIM-004: Record MongoDB operations separately from shell commands
func (e *SimulationExecutor) MongoExecute(host string, command string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-018: Check for configured failures
	target := fmt.Sprintf("%s: %s", host, command)
	if shouldFail, errMsg := e.config.ShouldFail("mongo_execute", target); shouldFail {
		e.state.RecordFailure("mongo_execute", host, command, errMsg)
		return "", errors.New(errMsg)
	}

	// REQ-SIM-013: Get configured response
	response := e.config.GetResponse(command)

	// REQ-SIM-002: Record MongoDB operation
	e.state.RecordOperation("mongo_execute", host, command, map[string]interface{}{
		"output": response,
	})

	return response, nil
}

// Background simulates starting a background process
// REQ-SIM-008: Record process operations without starting actual processes
// REQ-SIM-009: Track process as running in simulated state
func (e *SimulationExecutor) Background(command string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-018: Check for configured failures
	if shouldFail, errMsg := e.config.ShouldFail("background", command); shouldFail {
		e.state.RecordFailure("start_process", command, "", errMsg)
		return 0, errors.New(errMsg)
	}

	// REQ-SIM-009: Allocate simulated PID and track as running
	pid := e.state.AllocatePID()

	// Parse command into parts
	parts := strings.Fields(command)
	var cmd string
	var args []string
	if len(parts) > 0 {
		cmd = parts[0]
		args = parts[1:]
	}

	e.state.Processes[pid] = &SimulatedProcess{
		PID:       pid,
		Command:   cmd,
		Args:      args,
		State:     ProcessStateRunning,
		StartTime: e.state.StartTime,
	}

	// REQ-SIM-002: Record operation
	e.state.RecordOperation("start_process", fmt.Sprintf("pid:%d", pid), command, map[string]interface{}{
		"pid":     pid,
		"command": command,
	})

	return pid, nil
}

// ========== Process Management ==========

// IsProcessRunning checks if a process is running
// REQ-SIM-011: Query simulated process state
func (e *SimulationExecutor) IsProcessRunning(pid int) (bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// REQ-SIM-011: Check simulated process state
	if proc, exists := e.state.Processes[pid]; exists {
		return proc.State == ProcessStateRunning, nil
	}

	return false, nil
}

// KillProcess simulates killing a process
// REQ-SIM-008: Record process operations without affecting actual processes
func (e *SimulationExecutor) KillProcess(pid int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-011: Update simulated process state
	if proc, exists := e.state.Processes[pid]; exists {
		proc.State = ProcessStateStopped
		now := e.state.StartTime
		proc.StopTime = &now
	}

	// REQ-SIM-002: Record operation
	e.state.RecordOperation("kill_process", fmt.Sprintf("pid:%d", pid), "", map[string]interface{}{
		"pid": pid,
	})

	return nil
}

// StopProcess simulates gracefully stopping a process (SIGINT)
// REQ-SIM-008: Record process operations without affecting actual processes
func (e *SimulationExecutor) StopProcess(pid int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// REQ-SIM-011: Update simulated process state
	if proc, exists := e.state.Processes[pid]; exists {
		proc.State = ProcessStateStopped
		now := e.state.StartTime
		proc.StopTime = &now
	}

	// REQ-SIM-002: Record operation
	e.state.RecordOperation("stop_process", fmt.Sprintf("pid:%d", pid), "", map[string]interface{}{
		"pid": pid,
	})

	return nil
}

// ========== System Information ==========

// GetOSInfo returns simulated OS information
// REQ-SIM-013: Return configured system information
func (e *SimulationExecutor) GetOSInfo() (*executor.OSInfo, error) {
	// REQ-SIM-002: Record operation
	e.mu.Lock()
	e.state.RecordOperation("get_os_info", "system", "", nil)
	e.mu.Unlock()

	// REQ-SIM-043: Return sensible default
	return &executor.OSInfo{
		OS:      "linux",
		Arch:    "amd64",
		Version: "5.10.0",
	}, nil
}

// GetDiskSpace returns simulated disk space
// REQ-SIM-013: Return configured disk space
func (e *SimulationExecutor) GetDiskSpace(path string) (uint64, error) {
	// REQ-SIM-002: Record operation
	e.mu.Lock()
	e.state.RecordOperation("get_disk_space", path, "", nil)
	e.mu.Unlock()

	// REQ-SIM-043: Return sensible default (500GB available)
	return 500 * 1024 * 1024 * 1024, nil
}

// CheckPortAvailable returns simulated port availability
// REQ-SIM-032: Perform safety checks that don't require infrastructure
func (e *SimulationExecutor) CheckPortAvailable(port int) (bool, error) {
	// REQ-SIM-002: Record operation
	e.mu.Lock()
	e.state.RecordOperation("check_port_available", fmt.Sprintf("port:%d", port), "", map[string]interface{}{
		"port": port,
	})
	e.mu.Unlock()

	// REQ-SIM-043: Always available in simulation (unless specifically configured to fail)
	return true, nil
}

// UserExists checks if a user exists
// REQ-SIM-013: Return simulated user existence
func (e *SimulationExecutor) UserExists(username string) (bool, error) {
	// REQ-SIM-002: Record operation
	e.mu.Lock()
	e.state.RecordOperation("user_exists", username, "", nil)
	e.mu.Unlock()

	// REQ-SIM-043: Common users exist in simulation
	commonUsers := map[string]bool{
		"root":    true,
		"mongodb": true,
		"mongod":  true,
	}

	return commonUsers[username], nil
}

// ========== Connection Management ==========

// CheckConnectivity simulates connectivity check
// REQ-SIM-014: Connectivity checks always succeed in simulation
func (e *SimulationExecutor) CheckConnectivity() error {
	// REQ-SIM-002: Record operation
	e.mu.Lock()
	e.state.RecordOperation("check_connectivity", "network", "", nil)
	e.mu.Unlock()

	// REQ-SIM-014: Always succeeds in simulation
	return nil
}

// Close simulates closing the executor
func (e *SimulationExecutor) Close() error {
	// Nothing to close in simulation
	return nil
}
