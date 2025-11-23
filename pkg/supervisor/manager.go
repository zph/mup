package supervisor

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Manager wraps supervisord binary functionality for managing MongoDB processes
type Manager struct {
	clusterDir  string
	clusterName string
	configPath  string
	binaryPath  string // Path to supervisord binary
	httpPort    int    // HTTP server port for this cluster
}

// ProcessStatus represents the state of a supervised process
type ProcessStatus struct {
	Name        string
	State       string // RUNNING, STOPPED, STARTING, FATAL, etc.
	PID         int
	Uptime      int64
	Description string
}

// NewManager creates a new supervisord manager for a cluster
func NewManager(clusterDir, clusterName string) (*Manager, error) {
	configPath := filepath.Join(clusterDir, "supervisor.ini")

	// Get supervisord binary - cache in ~/.mup/storage/bin
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	cacheDir := filepath.Join(homeDir, ".mup", "storage", "bin")

	binaryPath, err := GetSupervisordBinary(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get supervisord binary: %w", err)
	}

	// Calculate HTTP port for this cluster (same logic as ConfigGenerator)
	// Use clusterDir (which includes version) to allow multiple supervisors during upgrade
	httpPort := getSupervisorHTTPPort(clusterDir)

	return &Manager{
		clusterDir:  clusterDir,
		clusterName: clusterName,
		configPath:  configPath,
		binaryPath:  binaryPath,
		httpPort:    httpPort,
	}, nil
}

// LoadManager loads an existing supervisord manager
// clusterDir can be either:
// - A version-specific directory (e.g., ~/.mup/storage/clusters/test/v7.0)
// - A cluster root directory (e.g., ~/.mup/storage/clusters/test) - will use "current" symlink
func LoadManager(clusterDir, clusterName string) (*Manager, error) {
	// Check if clusterDir has a supervisor.ini - if not, try "current" symlink
	configPath := filepath.Join(clusterDir, "supervisor.ini")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Try using "current" symlink
		currentDir := filepath.Join(clusterDir, "current")
		if _, err := os.Stat(filepath.Join(currentDir, "supervisor.ini")); err == nil {
			clusterDir = currentDir
			configPath = filepath.Join(currentDir, "supervisor.ini")
		} else {
			return nil, fmt.Errorf("supervisor config not found at %s or %s/current/supervisor.ini",
				configPath, clusterDir)
		}
	}

	// Read the port from the existing config file (for backwards compatibility)
	// This handles cases where the config was generated with old port calculation
	httpPort, err := readPortFromConfig(configPath)
	if err != nil {
		// If we can't read the port, fall back to calculating it
		httpPort = getSupervisorHTTPPort(clusterDir)
	}

	// Get supervisord binary path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	cacheDir := filepath.Join(homeDir, ".mup", "storage", "bin")
	binaryPath, err := GetSupervisordBinary(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get supervisord binary: %w", err)
	}

	mgr := &Manager{
		clusterDir:  clusterDir,
		clusterName: clusterName,
		configPath:  configPath,
		binaryPath:  binaryPath,
		httpPort:    httpPort,
	}

	return mgr, nil
}

// IsRunning checks if supervisord daemon is running for this cluster
func (m *Manager) IsRunning() bool {
	pidFile := filepath.Join(m.clusterDir, "supervisor.pid")

	// Check if PID file exists
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}

	// Parse PID
	var pid int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &pid); err != nil {
		return false
	}

	// Check if process is running (Unix: kill -0 checks without actually killing)
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix, Signal(0) checks if process exists without sending a real signal
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// Start starts the supervisord daemon
func (m *Manager) Start(ctx context.Context) error {
	if m.IsRunning() {
		fmt.Printf("Supervisor already running for cluster %s\n", m.clusterName)
		return nil
	}

	fmt.Printf("Starting supervisor daemon for cluster %s...\n", m.clusterName)

	// Start supervisord binary as daemon
	cmd := exec.Command(m.binaryPath, "-c", m.configPath)

	// Run in background
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start supervisord: %w", err)
	}

	// Wait a moment for daemon to start
	time.Sleep(500 * time.Millisecond)

	// Verify it's running
	if !m.IsRunning() {
		return fmt.Errorf("supervisord failed to start - check %s/supervisor.log", m.clusterDir)
	}

	fmt.Printf("  ✓ Supervisor daemon started\n")
	return nil
}

// ctl runs a supervisord ctl command
func (m *Manager) ctl(args ...string) *exec.Cmd {
	// Use: supervisord ctl -c config.ini -s http://localhost:PORT <command>
	// The -s flag is required to specify the HTTP server address when using custom ports
	serverURL := fmt.Sprintf("http://localhost:%d", m.httpPort)
	ctlArgs := append([]string{"ctl", "-c", m.configPath, "-s", serverURL}, args...)
	return exec.Command(m.binaryPath, ctlArgs...)
}

// Stop stops the supervisord daemon
func (m *Manager) Stop(ctx context.Context) error {
	if !m.IsRunning() {
		return nil
	}

	fmt.Printf("Stopping supervisor daemon for cluster %s...\n", m.clusterName)

	// Use supervisord ctl to shutdown
	cmd := m.ctl("shutdown")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop supervisord: %w", err)
	}

	// Wait for shutdown
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !m.IsRunning() {
			fmt.Printf("  ✓ Supervisor daemon stopped\n")
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// If graceful shutdown failed, try force kill
	fmt.Printf("  ⚠️  Graceful shutdown timed out, attempting force kill...\n")
	if err := m.ForceKill(); err != nil {
		return fmt.Errorf("supervisord did not stop within 30 seconds and force kill failed: %w", err)
	}

	fmt.Printf("  ✓ Supervisor daemon stopped (force killed)\n")
	return nil
}

// ForceKill forcefully kills the supervisord process using SIGKILL
func (m *Manager) ForceKill() error {
	pidFile := filepath.Join(m.clusterDir, "supervisor.pid")

	// Read PID
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	var pid int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &pid); err != nil {
		return fmt.Errorf("failed to parse PID: %w", err)
	}

	// Find process
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	// Send SIGKILL
	if err := process.Kill(); err != nil {
		return fmt.Errorf("failed to kill process: %w", err)
	}

	// Wait a bit for process to die
	time.Sleep(1 * time.Second)

	// Verify it's dead
	if m.IsRunning() {
		return fmt.Errorf("process still running after kill")
	}

	return nil
}

// StartProcess starts a specific process
func (m *Manager) StartProcess(name string) error {
	cmd := m.ctl("start", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start process %s: %w (output: %s)", name, err, string(output))
	}

	return nil
}

// StartProcesses starts multiple processes in parallel
// This is much faster than calling StartProcess multiple times
func (m *Manager) StartProcesses(names []string) error {
	if len(names) == 0 {
		return nil
	}

	// Pass all program names to a single start command
	// supervisord will start them in parallel
	args := append([]string{"start"}, names...)
	cmd := m.ctl(args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start processes: %w (output: %s)", err, string(output))
	}

	return nil
}

// StopProcess stops a specific process
func (m *Manager) StopProcess(name string) error {
	cmd := m.ctl("stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop process %s: %w (output: %s)", name, err, string(output))
	}

	return nil
}

// StopProcesses stops multiple processes in parallel
// This is much faster than calling StopProcess multiple times
func (m *Manager) StopProcesses(names []string) error {
	if len(names) == 0 {
		return nil
	}

	// Pass all program names to a single stop command
	// supervisord will stop them in parallel
	args := append([]string{"stop"}, names...)
	cmd := m.ctl(args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop processes: %w (output: %s)", err, string(output))
	}

	return nil
}

// RestartProcess restarts a specific process
func (m *Manager) RestartProcess(name string) error {
	cmd := m.ctl("restart", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to restart process %s: %w (output: %s)", name, err, string(output))
	}

	return nil
}

// GetProcessStatus returns the status of a process
func (m *Manager) GetProcessStatus(name string) (*ProcessStatus, error) {
	cmd := m.ctl("status", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get status for %s: %w", name, err)
	}

	// Parse supervisord ctl status output
	// Format: "progname  STATE  pid 12345, uptime 0:01:23"
	// Note: May contain ANSI color codes
	line := strings.TrimSpace(string(output))

	// Remove ANSI color codes
	line = strings.ReplaceAll(line, "\x1b[0;32m", "")
	line = strings.ReplaceAll(line, "\x1b[0m", "")
	line = strings.ReplaceAll(line, "[0;32m", "")
	line = strings.ReplaceAll(line, "[0m", "")

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, fmt.Errorf("unexpected status output format: %s", line)
	}

	status := &ProcessStatus{
		Name:  fields[0],
		State: fields[1],
	}

	// Extract PID if running - format: "pid 12345,"
	for i, field := range fields {
		if field == "pid" && i+1 < len(fields) {
			pidStr := strings.TrimSuffix(fields[i+1], ",")
			if pid, err := strconv.Atoi(pidStr); err == nil {
				status.PID = pid
			}
			break
		}
	}

	// Get description (rest of line after state)
	if len(fields) > 2 {
		status.Description = strings.Join(fields[2:], " ")
	}

	return status, nil
}

// GetAllProcesses returns status for all processes
func (m *Manager) GetAllProcesses() ([]*ProcessStatus, error) {
	cmd := m.ctl("status")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get all process status: %w", err)
	}

	var statuses []*ProcessStatus
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Remove ANSI color codes
		line = strings.ReplaceAll(line, "\x1b[0;32m", "")
		line = strings.ReplaceAll(line, "\x1b[0m", "")
		line = strings.ReplaceAll(line, "[0;32m", "")
		line = strings.ReplaceAll(line, "[0m", "")

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		status := &ProcessStatus{
			Name:  fields[0],
			State: fields[1],
		}

		// Extract PID - format: "pid 12345,"
		for i, field := range fields {
			if field == "pid" && i+1 < len(fields) {
				pidStr := strings.TrimSuffix(fields[i+1], ",")
				if pid, err := strconv.Atoi(pidStr); err == nil {
					status.PID = pid
				}
				break
			}
		}

		// Get description
		if len(fields) > 2 {
			status.Description = strings.Join(fields[2:], " ")
		}

		statuses = append(statuses, status)
	}

	return statuses, nil
}

// StartGroup starts all processes in a group
func (m *Manager) StartGroup(groupName string) error {
	cmd := m.ctl("start", groupName+":*")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start group %s: %w (output: %s)", groupName, err, string(output))
	}

	return nil
}

// StopGroup stops all processes in a group
func (m *Manager) StopGroup(groupName string) error {
	cmd := m.ctl("stop", groupName+":*")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to stop group %s: %w (output: %s)", groupName, err, string(output))
	}

	return nil
}

// Reload reloads the supervisor configuration
func (m *Manager) Reload() error {
	cmd := m.ctl("reload")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to reload config: %w (output: %s)", err, string(output))
	}

	return nil
}

// readPortFromConfig reads the HTTP port from an existing supervisor.ini file
func readPortFromConfig(configPath string) (int, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read config: %w", err)
	}

	// Look for line like: port = 127.0.0.1:19639
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "port = 127.0.0.1:") {
			portStr := strings.TrimPrefix(line, "port = 127.0.0.1:")
			port, err := strconv.Atoi(portStr)
			if err != nil {
				return 0, fmt.Errorf("failed to parse port from config: %w", err)
			}
			return port, nil
		}
	}

	return 0, fmt.Errorf("port not found in config")
}

// getSupervisorHTTPPort generates a unique HTTP port for this cluster's supervisor
// Uses hash of cluster directory (includes version) to get a port in range 19000-19999
// This must match the logic in ConfigGenerator.getSupervisorHTTPPort()
// Using clusterDir instead of clusterName allows multiple supervisors (different versions)
// to run side-by-side during upgrades
func getSupervisorHTTPPort(clusterDir string) int {
	h := fnv.New32a()
	h.Write([]byte(clusterDir))
	hash := h.Sum32()

	// Map to port range 19000-19999 (1000 ports available)
	port := 19000 + int(hash%1000)
	return port
}
