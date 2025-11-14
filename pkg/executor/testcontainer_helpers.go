package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/zph/mup/pkg/topology"
)

// SSHContainerConfig holds configuration for launching SSH containers
type SSHContainerConfig struct {
	ImageName      string        // Docker image name (default: "mup-ssh-node:latest")
	SSHKeyPath     string        // Path to SSH private key for authentication
	Username       string        // SSH username (default: "testuser")
	Password       string        // SSH password (default: "testpass")
	StartupTimeout time.Duration // Max time to wait for container to start (default: 60s)
}

// ContainerHost represents a running SSH container
type ContainerHost struct {
	Container    testcontainers.Container
	ContainerID  string
	Hostname     string // Original hostname from topology (e.g., "localhost", "node1")
	SSHHost      string // Docker host IP (usually 127.0.0.1)
	SSHPort      int    // Mapped SSH port
	InternalIP   string // Container internal IP
	Username     string
	Password     string
	SSHKeyPath   string
	Executor     Executor // Pre-configured SSHExecutor for this container
}

// TestEnvironment manages a collection of SSH containers for testing
type TestEnvironment struct {
	Containers map[string]*ContainerHost // hostname -> container
	Config     SSHContainerConfig
	ctx        context.Context
}

// NewTestEnvironment creates a new test environment
func NewTestEnvironment(ctx context.Context, config SSHContainerConfig) *TestEnvironment {
	// Set defaults
	if config.ImageName == "" {
		config.ImageName = "mup-ssh-node:latest"
	}
	if config.Username == "" {
		config.Username = "testuser"
	}
	if config.Password == "" {
		config.Password = "testpass"
	}
	if config.StartupTimeout == 0 {
		config.StartupTimeout = 60 * time.Second
	}

	return &TestEnvironment{
		Containers: make(map[string]*ContainerHost),
		Config:     config,
		ctx:        ctx,
	}
}

// LaunchContainersForTopology launches containers for each unique host in the topology
// For local deployments (all hosts are localhost), it creates one container
// For multi-host topologies, it creates a container for each unique host
func (env *TestEnvironment) LaunchContainersForTopology(topo *topology.Topology) error {
	// Get all unique hosts from topology
	hosts := topo.GetAllHosts()

	// If this is a local deployment, create just one container named "node1"
	// Otherwise, create a container for each unique host
	if topo.IsLocalDeployment() {
		// For local deployments, we create one container to represent localhost
		container, err := env.launchContainer("localhost")
		if err != nil {
			return fmt.Errorf("failed to launch container for localhost: %w", err)
		}
		env.Containers["localhost"] = container

		// Also map common localhost aliases to the same container
		env.Containers["127.0.0.1"] = container
		env.Containers["::1"] = container

		return nil
	}

	// For multi-host deployments, create a container for each unique host
	for _, host := range hosts {
		// Skip localhost - it should use local executor in tests
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			continue
		}

		container, err := env.launchContainer(host)
		if err != nil {
			return fmt.Errorf("failed to launch container for host %s: %w", host, err)
		}
		env.Containers[host] = container
	}

	return nil
}

// launchContainer launches a single SSH-enabled container
func (env *TestEnvironment) launchContainer(hostname string) (*ContainerHost, error) {
	ctx, cancel := context.WithTimeout(env.ctx, env.Config.StartupTimeout)
	defer cancel()

	// Prepare SSH key if provided
	var binds []string
	if env.Config.SSHKeyPath != "" {
		// Mount the public key
		pubKeyPath := env.Config.SSHKeyPath + ".pub"
		if _, err := os.Stat(pubKeyPath); err == nil {
			binds = append(binds, fmt.Sprintf("%s:/home/%s/.ssh/authorized_keys:ro",
				pubKeyPath, env.Config.Username))
		}
	}

	// Create container request
	req := testcontainers.ContainerRequest{
		Image:        env.Config.ImageName,
		ExposedPorts: []string{"22/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForListeningPort("22/tcp"),
			wait.ForLog("Starting SSH daemon").WithStartupTimeout(env.Config.StartupTimeout),
		),
		Binds: binds,
		Name:  fmt.Sprintf("mup-test-%s-%s", sanitizeHostname(hostname), randomString(6)),
		AutoRemove: true,
	}

	// Start container
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Get container ID
	containerID := container.GetContainerID()

	// Get mapped SSH port
	mappedPort, err := container.MappedPort(ctx, "22")
	if err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("failed to get mapped SSH port: %w", err)
	}

	// Get container IP
	containerIP, err := container.Host(ctx)
	if err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	// Get internal IP
	networks, err := container.Networks(ctx)
	if err != nil {
		container.Terminate(ctx)
		return nil, fmt.Errorf("failed to get container networks: %w", err)
	}

	var internalIP string
	// Get IP from the first network
	if len(networks) > 0 {
		// ContainerIP returns the IP address for a specific network
		internalIP, err = container.ContainerIP(ctx)
		if err != nil {
			// Non-fatal, just log it
			fmt.Printf("Warning: failed to get container IP: %v\n", err)
		}
	}

	sshPort := mappedPort.Int()

	containerHost := &ContainerHost{
		Container:   container,
		ContainerID: containerID,
		Hostname:    hostname,
		SSHHost:     containerIP,
		SSHPort:     sshPort,
		InternalIP:  internalIP,
		Username:    env.Config.Username,
		Password:    env.Config.Password,
		SSHKeyPath:  env.Config.SSHKeyPath,
	}

	// Wait a bit for SSH to be fully ready
	time.Sleep(2 * time.Second)

	return containerHost, nil
}

// CreateExecutorsForTopology creates SSH executors for each host in the topology
// and returns a map of hostname -> executor
func (env *TestEnvironment) CreateExecutorsForTopology(topo *topology.Topology) (map[string]Executor, error) {
	executors := make(map[string]Executor)

	// Get all unique hosts
	hosts := topo.GetAllHosts()

	for _, host := range hosts {
		// Get container for this host
		container, exists := env.Containers[host]
		if !exists {
			return nil, fmt.Errorf("no container found for host %s", host)
		}

		// Create SSH executor for this container
		executor, err := container.CreateExecutor()
		if err != nil {
			return nil, fmt.Errorf("failed to create executor for host %s: %w", host, err)
		}

		executors[host] = executor
	}

	return executors, nil
}

// CreateExecutor creates an SSH executor for this container
func (ch *ContainerHost) CreateExecutor() (Executor, error) {
	config := SSHConfig{
		Host:     ch.SSHHost,
		Port:     ch.SSHPort,
		User:     ch.Username,
		Password: ch.Password,
		KeyFile:  ch.SSHKeyPath,
	}

	executor, err := NewSSHExecutor(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH executor: %w", err)
	}

	// Store executor reference for cleanup
	ch.Executor = executor

	return executor, nil
}

// ExecCommand executes a command in the container using docker exec (for testing/debugging)
func (ch *ContainerHost) ExecCommand(ctx context.Context, cmd []string) (string, error) {
	exitCode, reader, err := ch.Container.Exec(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("failed to exec command: %w", err)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("command exited with code %d", exitCode)
	}

	// Read output
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, reader); err != nil {
		return "", fmt.Errorf("failed to read command output: %w", err)
	}

	return buf.String(), nil
}

// Cleanup terminates all containers in the test environment
func (env *TestEnvironment) Cleanup() error {
	ctx, cancel := context.WithTimeout(env.ctx, 30*time.Second)
	defer cancel()

	var firstError error

	// Close executors and terminate containers
	seen := make(map[string]bool) // Track unique containers
	for hostname, container := range env.Containers {
		// Skip if we've already cleaned up this container
		// (multiple hostnames might map to the same container)
		if seen[container.ContainerID] {
			continue
		}
		seen[container.ContainerID] = true

		// Close executor if it exists
		if container.Executor != nil {
			if err := container.Executor.Close(); err != nil && firstError == nil {
				firstError = fmt.Errorf("failed to close executor for %s: %w", hostname, err)
			}
		}

		// Terminate container
		if err := container.Container.Terminate(ctx); err != nil && firstError == nil {
			firstError = fmt.Errorf("failed to terminate container for %s: %w", hostname, err)
		}
	}

	return firstError
}

// GetSSHConnectionString returns an SSH connection string for a host
func (env *TestEnvironment) GetSSHConnectionString(hostname string) (string, error) {
	container, exists := env.Containers[hostname]
	if !exists {
		return "", fmt.Errorf("no container found for host %s", hostname)
	}

	return fmt.Sprintf("ssh -p %d %s@%s", container.SSHPort, container.Username, container.SSHHost), nil
}

// Helper functions

// sanitizeHostname converts a hostname to a valid container name
func sanitizeHostname(hostname string) string {
	// Replace dots and colons with dashes
	s := strings.ReplaceAll(hostname, ".", "-")
	s = strings.ReplaceAll(s, ":", "-")
	s = strings.ToLower(s)
	return s
}

// randomString generates a random string of the specified length
func randomString(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp if random fails
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)[:length]
}

// BuildSSHNodeImage builds the SSH node Docker image for testing
// This should be called before running tests that need SSH containers
func BuildSSHNodeImage(ctx context.Context) error {
	// Find the Dockerfile path
	dockerfilePath := filepath.Join("test", "docker", "ssh-node")

	// Check if Dockerfile exists
	if _, err := os.Stat(filepath.Join(dockerfilePath, "Dockerfile")); err != nil {
		return fmt.Errorf("Dockerfile not found at %s: %w", dockerfilePath, err)
	}

	// Build the image using testcontainers
	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    dockerfilePath,
			Dockerfile: "Dockerfile",
		},
	}

	_, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
	})

	return err
}

// WaitForSSH waits for SSH to be available on a container
func WaitForSSH(ctx context.Context, host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Try to connect
		conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", host, port))
		if err == nil {
			conn.Close()
			return nil
		}

		// Wait before retry
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	return fmt.Errorf("timeout waiting for SSH on %s:%d", host, port)
}
