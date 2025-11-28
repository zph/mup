package executor

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// SSHConfig holds SSH connection configuration
type SSHConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	KeyFile  string
	Timeout  time.Duration
}

// SSHExecutor implements Executor for remote operations via SSH
type SSHExecutor struct {
	config    SSHConfig
	client    *ssh.Client
	agentConn net.Conn // Keep agent connection alive for the lifetime of the executor
}

// NewSSHExecutor creates a new SSH executor and establishes connection
func NewSSHExecutor(config SSHConfig) (*SSHExecutor, error) {
	// Set defaults
	if config.Port == 0 {
		config.Port = 22
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	// Build SSH client config
	sshConfig := &ssh.ClientConfig{
		User:            config.User,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // For testing only
		Timeout:         config.Timeout,
	}

	// Add authentication methods in order of preference:
	// 1. Physical key file (if provided)
	// 2. SSH agent (if available)
	// 3. Password (if provided)

	if config.KeyFile != "" {
		// Key-based authentication from file
		key, err := os.ReadFile(config.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read SSH key file: %w", err)
		}

		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("failed to parse SSH private key: %w", err)
		}

		sshConfig.Auth = append(sshConfig.Auth, ssh.PublicKeys(signer))
	}

	// Try SSH agent if available
	var agentConn net.Conn
	if conn, err := getSSHAgentConnection(); err == nil {
		agentConn = conn
		// SSH agent is available, create agent client and add its keys
		sshAgent := agent.NewClient(agentConn)
		signers, err := sshAgent.Signers()
		if err == nil && len(signers) > 0 {
			sshConfig.Auth = append(sshConfig.Auth, ssh.PublicKeys(signers...))
			// Keep the connection alive for the lifetime of the executor
		} else {
			// No signers available, close the connection
			agentConn.Close()
			agentConn = nil
		}
	}

	if config.Password != "" {
		// Password authentication
		sshConfig.Auth = append(sshConfig.Auth, ssh.Password(config.Password))
	}

	if len(sshConfig.Auth) == 0 {
		return nil, fmt.Errorf("no authentication method provided (need key file, SSH agent, or password)")
	}

	// Connect to SSH server
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH server at %s: %w", addr, err)
	}

	executor := &SSHExecutor{
		config:    config,
		client:    client,
		agentConn: agentConn,
	}

	return executor, nil
}

// CreateDirectory creates a directory with the specified permissions
func (e *SSHExecutor) CreateDirectory(path string, mode os.FileMode) error {
	// First check if directory already exists
	exists, err := e.FileExists(path)
	if err != nil {
		return fmt.Errorf("failed to check if directory exists: %w", err)
	}

	if exists {
		// Directory already exists, don't try to chmod it (might fail for system dirs like /tmp)
		return nil
	}

	// Create directory and set permissions only if it didn't exist
	cmd := fmt.Sprintf("mkdir -p %s && chmod %o %s", path, mode, path)
	_, err = e.Execute(cmd)
	return err
}

// UploadFile copies a file from localPath to remotePath
func (e *SSHExecutor) UploadFile(localPath, remotePath string) error {
	// Read local file
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read local file: %w", err)
	}

	return e.UploadContent(data, remotePath)
}

// UploadContent writes content to a file at remotePath
func (e *SSHExecutor) UploadContent(content []byte, remotePath string) error {
	// Ensure parent directory exists
	dir := filepath.Dir(remotePath)
	if err := e.CreateDirectory(dir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Use cat with heredoc to write content
	// Escape single quotes in content
	escapedContent := string(content)
	escapedContent = strings.ReplaceAll(escapedContent, "'", "'\\''")

	// Use heredoc without adding trailing newline
	// The -n flag in the EOF marker prevents adding a newline after content
	cmd := fmt.Sprintf("cat > %s << 'MUPEOF'\n%sMUPEOF", remotePath, escapedContent)
	_, err := e.Execute(cmd)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// DownloadFile copies a file from remotePath to localPath
func (e *SSHExecutor) DownloadFile(remotePath, localPath string) error {
	// Read remote file
	output, err := e.Execute(fmt.Sprintf("cat %s", remotePath))
	if err != nil {
		return fmt.Errorf("failed to read remote file: %w", err)
	}

	// Ensure local parent directory exists
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create local parent directory: %w", err)
	}

	// Write to local file
	if err := os.WriteFile(localPath, []byte(output), 0644); err != nil {
		return fmt.Errorf("failed to write local file: %w", err)
	}

	return nil
}

// FileExists checks if a file exists
func (e *SSHExecutor) FileExists(path string) (bool, error) {
	_, err := e.Execute(fmt.Sprintf("test -e %s", path))
	if err != nil {
		// Command failed - file doesn't exist
		return false, nil
	}
	return true, nil
}

// RemoveFile removes a file
func (e *SSHExecutor) RemoveFile(path string) error {
	_, err := e.Execute(fmt.Sprintf("rm -f %s", path))
	return err
}

// RemoveDirectory removes a directory and all its contents
func (e *SSHExecutor) RemoveDirectory(path string) error {
	_, err := e.Execute(fmt.Sprintf("rm -rf %s", path))
	return err
}

// Execute runs a command and returns its output
func (e *SSHExecutor) Execute(command string) (string, error) {
	session, err := e.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(command)
	if err != nil {
		return "", fmt.Errorf("command failed: %w\nstderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// ExecuteWithInput runs a command with stdin and returns output
func (e *SSHExecutor) ExecuteWithInput(command string, stdin io.Reader) (string, error) {
	session, err := e.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdin = stdin
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(command)
	if err != nil {
		return "", fmt.Errorf("command failed: %w\nstderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}

// MongoExecute runs a MongoDB driver command
// For SSHExecutor, this is just informational (the actual driver work happens in Go)
func (e *SSHExecutor) MongoExecute(host string, command string) (string, error) {
	// MongoDB driver commands don't actually execute as shell commands
	// This method exists for interface compliance and logging
	return "", nil
}

// Background starts a command in the background and returns its PID
func (e *SSHExecutor) Background(command string) (int, error) {
	// Use nohup and capture PID
	// The command format ensures the process stays alive after SSH session closes
	cmd := fmt.Sprintf("nohup %s > /dev/null 2>&1 & echo $!", command)

	output, err := e.Execute(cmd)
	if err != nil {
		return 0, fmt.Errorf("failed to start background process: %w", err)
	}

	// Parse PID from output
	pidStr := strings.TrimSpace(output)
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse PID from output '%s': %w", pidStr, err)
	}

	return pid, nil
}

// IsProcessRunning checks if a process with the given PID is running
func (e *SSHExecutor) IsProcessRunning(pid int) (bool, error) {
	// Use kill -0 to check if process exists
	_, err := e.Execute(fmt.Sprintf("kill -0 %d", pid))
	if err != nil {
		// Command failed - process doesn't exist
		return false, nil
	}
	return true, nil
}

// KillProcess kills a process with the given PID
func (e *SSHExecutor) KillProcess(pid int) error {
	_, err := e.Execute(fmt.Sprintf("kill -9 %d", pid))
	return err
}

// StopProcess sends SIGINT to a process for graceful shutdown
func (e *SSHExecutor) StopProcess(pid int) error {
	_, err := e.Execute(fmt.Sprintf("kill -INT %d", pid))
	return err
}

// GetOSInfo returns information about the operating system
func (e *SSHExecutor) GetOSInfo() (*OSInfo, error) {
	// Get OS type
	osType, err := e.Execute("uname -s")
	if err != nil {
		return nil, fmt.Errorf("failed to get OS type: %w", err)
	}
	osType = strings.ToLower(strings.TrimSpace(osType))

	// Get architecture
	arch, err := e.Execute("uname -m")
	if err != nil {
		return nil, fmt.Errorf("failed to get architecture: %w", err)
	}
	arch = strings.TrimSpace(arch)

	// Convert to Go arch names
	switch arch {
	case "x86_64", "amd64":
		arch = "amd64"
	case "aarch64", "arm64":
		arch = "arm64"
	}

	// Get OS version
	version, err := e.Execute("uname -r")
	if err != nil {
		version = "" // Non-fatal
	} else {
		version = strings.TrimSpace(version)
	}

	return &OSInfo{
		OS:      osType,
		Arch:    arch,
		Version: version,
	}, nil
}

// GetDiskSpace returns available disk space in bytes for the given path
func (e *SSHExecutor) GetDiskSpace(path string) (uint64, error) {
	// Use df to get available space
	output, err := e.Execute(fmt.Sprintf("df -B1 %s | tail -n1 | awk '{print $4}'", path))
	if err != nil {
		return 0, fmt.Errorf("failed to get disk space: %w", err)
	}

	// Parse available bytes
	availStr := strings.TrimSpace(output)
	avail, err := strconv.ParseUint(availStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse disk space '%s': %w", availStr, err)
	}

	return avail, nil
}

// CheckPortAvailable checks if a port is available
// For SSH executor, we use multiple methods to check if port is in use
func (e *SSHExecutor) CheckPortAvailable(port int) (bool, error) {
	// Try using sudo lsof first (can see all processes)
	cmd := fmt.Sprintf("sudo lsof -i :%d -sTCP:LISTEN 2>/dev/null", port)
	output, err := e.Execute(cmd)

	if err == nil && len(strings.TrimSpace(output)) > 0 {
		// Got output, something is listening on the port
		return false, nil
	}

	// Try without sudo as backup (might work for processes owned by current user)
	cmd = fmt.Sprintf("lsof -i :%d -sTCP:LISTEN 2>/dev/null || netstat -tuln 2>/dev/null | grep ':%d ' | grep LISTEN", port, port)
	output, err = e.Execute(cmd)

	if err == nil && len(strings.TrimSpace(output)) > 0 {
		// Got output, something is listening on the port
		return false, nil
	}

	// If both commands failed or returned empty output, port is available
	return true, nil
}

// UserExists checks if a user exists on the system
func (e *SSHExecutor) UserExists(username string) (bool, error) {
	_, err := e.Execute(fmt.Sprintf("id %s", username))
	if err != nil {
		// Command failed - user doesn't exist
		return false, nil
	}
	return true, nil
}

// CheckConnectivity checks if the executor can perform operations
func (e *SSHExecutor) CheckConnectivity() error {
	_, err := e.Execute("echo test")
	return err
}

// Close closes the SSH connection and agent connection
func (e *SSHExecutor) Close() error {
	var errs []error
	if e.client != nil {
		if err := e.client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if e.agentConn != nil {
		if err := e.agentConn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing connections: %v", errs)
	}
	return nil
}

// getSSHAgentConnection connects to the SSH agent socket and returns the connection
// Returns error if SSH agent is not available
func getSSHAgentConnection() (net.Conn, error) {
	// Check if SSH_AUTH_SOCK environment variable is set
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}

	// Connect to the SSH agent socket
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH agent: %w", err)
	}

	return conn, nil
}
