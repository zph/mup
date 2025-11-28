package executor

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// LocalExecutor implements Executor for local operations
type LocalExecutor struct {
	workDir string
}

// NewLocalExecutor creates a new LocalExecutor
func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{}
}

// CreateDirectory creates a directory with the specified permissions
func (e *LocalExecutor) CreateDirectory(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}

// UploadFile copies a file from localPath to remotePath (both local in this case)
func (e *LocalExecutor) UploadFile(localPath, remotePath string) error {
	// Ensure parent directory exists
	dir := filepath.Dir(remotePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Read source file
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}

	// Write to destination
	if err := os.WriteFile(remotePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write destination file: %w", err)
	}

	return nil
}

// UploadContent writes content to a file at remotePath
func (e *LocalExecutor) UploadContent(content []byte, remotePath string) error {
	// Ensure parent directory exists
	dir := filepath.Dir(remotePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	if err := os.WriteFile(remotePath, content, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// DownloadFile copies a file from remotePath to localPath
func (e *LocalExecutor) DownloadFile(remotePath, localPath string) error {
	return e.UploadFile(remotePath, localPath)
}

// FileExists checks if a file exists
func (e *LocalExecutor) FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// RemoveFile removes a file
func (e *LocalExecutor) RemoveFile(path string) error {
	return os.Remove(path)
}

// RemoveDirectory removes a directory and all its contents
func (e *LocalExecutor) RemoveDirectory(path string) error {
	return os.RemoveAll(path)
}

// Execute runs a command and returns its output
func (e *LocalExecutor) Execute(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("command failed: %w\nstderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// ExecuteWithInput runs a command with stdin and returns output
func (e *LocalExecutor) ExecuteWithInput(command string, stdin io.Reader) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("command failed: %w\nstderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// MongoExecute runs a MongoDB driver command
// For LocalExecutor, this is just informational (the actual driver work happens in Go)
func (e *LocalExecutor) MongoExecute(host string, command string) (string, error) {
	// MongoDB driver commands don't actually execute as shell commands
	// This method exists for interface compliance and logging
	return "", nil
}

// Background starts a command in the background and returns its PID
func (e *LocalExecutor) Background(command string) (int, error) {
	cmd := exec.Command("sh", "-c", command)

	// Start the process in a new process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed to start background process: %w", err)
	}

	return cmd.Process.Pid, nil
}

// IsProcessRunning checks if a process with the given PID is running
func (e *LocalExecutor) IsProcessRunning(pid int) (bool, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, nil
	}

	// Send signal 0 to check if process exists
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}

	if err == syscall.ESRCH {
		return false, nil
	}

	return false, err
}

// KillProcess kills a process with the given PID
func (e *LocalExecutor) KillProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	return process.Kill()
}

// StopProcess sends SIGINT to a process for graceful shutdown
func (e *LocalExecutor) StopProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	return process.Signal(syscall.SIGINT)
}

// GetOSInfo returns information about the operating system
func (e *LocalExecutor) GetOSInfo() (*OSInfo, error) {
	var version string

	// Get OS version
	if runtime.GOOS == "linux" {
		output, err := e.Execute("uname -r")
		if err == nil {
			version = strings.TrimSpace(output)
		}
	} else if runtime.GOOS == "darwin" {
		output, err := e.Execute("sw_vers -productVersion")
		if err == nil {
			version = strings.TrimSpace(output)
		}
	}

	return &OSInfo{
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Version: version,
	}, nil
}

// GetDiskSpace returns available disk space in bytes for the given path
func (e *LocalExecutor) GetDiskSpace(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("failed to get disk space: %w", err)
	}

	// Available space = available blocks * block size
	return stat.Bavail * uint64(stat.Bsize), nil
}

// CheckPortAvailable checks if a port is available using a two-phase approach:
// 1. Try to bind to the port (tests actual usability)
// 2. Try to connect to the port (catches listening processes)
func (e *LocalExecutor) CheckPortAvailable(port int) (bool, error) {
	// Phase 1: Try to bind to the port
	// This is the most reliable test - if we can't bind, we can't use it
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// Cannot bind - port is not available
		// This catches ports in use, TIME_WAIT state, or reserved
		return false, nil
	}
	// Successfully bound - close immediately
	listener.Close()

	// Phase 2: Double-check with dial to catch any listening processes
	// Small delay to allow the socket to fully close
	time.Sleep(10 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err != nil {
		// Connection failed - port is available
		return true, nil
	}

	// Something is listening on this port - not available
	conn.Close()
	return false, nil
}

// UserExists checks if a user exists on the system
func (e *LocalExecutor) UserExists(username string) (bool, error) {
	_, err := user.Lookup(username)
	if err != nil {
		if _, ok := err.(user.UnknownUserError); ok {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CheckConnectivity checks if the executor can perform operations
func (e *LocalExecutor) CheckConnectivity() error {
	// For local executor, just check if we can execute a simple command
	_, err := e.Execute("echo test")
	return err
}

// Close closes any resources held by the executor
func (e *LocalExecutor) Close() error {
	// No resources to close for local executor
	return nil
}
