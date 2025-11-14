package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/topology"
)

// TestSSHExecutor_Testcontainers tests SSHExecutor using testcontainers
// This test launches real Docker containers with SSH enabled and tests
// the full deployment workflow
func TestSSHExecutor_Testcontainers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Build the SSH node image first
	t.Log("Building SSH node Docker image...")
	if err := buildSSHNodeImageIfNeeded(ctx, t); err != nil {
		t.Fatalf("Failed to build SSH node image: %v", err)
	}

	// Create test environment
	env := NewTestEnvironment(ctx, SSHContainerConfig{
		ImageName:      "mup-ssh-node:latest",
		Username:       "testuser",
		Password:       "testpass",
		StartupTimeout: 60 * time.Second,
	})
	defer func() {
		if err := env.Cleanup(); err != nil {
			t.Logf("Warning: cleanup failed: %v", err)
		}
	}()

	// Create a simple topology for testing
	topo := &topology.Topology{
		Global: topology.GlobalConfig{
			User:      "testuser",
			DeployDir: "/opt/mongodb",
			DataDir:   "/data/mongodb",
			LogDir:    "/var/log/mongodb",
			ConfigDir: "/etc/mongodb",
		},
		Mongod: []topology.MongodNode{
			{Host: "localhost", Port: 27017, ReplicaSet: "rs0"},
			{Host: "localhost", Port: 27018, ReplicaSet: "rs0"},
			{Host: "localhost", Port: 27019, ReplicaSet: "rs0"},
		},
	}

	// Launch containers for the topology
	t.Log("Launching containers for topology...")
	if err := env.LaunchContainersForTopology(topo); err != nil {
		t.Fatalf("Failed to launch containers: %v", err)
	}

	// Verify we have the expected container
	assert.Len(t, env.Containers, 3, "Should have 3 container references (localhost + aliases)")

	container := env.Containers["localhost"]
	require.NotNil(t, container, "Should have container for localhost")

	t.Logf("Container launched: %s:%d (internal IP: %s)",
		container.SSHHost, container.SSHPort, container.InternalIP)

	// Create executor for the container
	t.Log("Creating SSH executor...")
	executor, err := container.CreateExecutor()
	require.NoError(t, err, "Failed to create SSH executor")
	defer executor.Close()

	// Run integration tests on the executor
	t.Run("Connectivity", func(t *testing.T) {
		testSSHConnectivity(t, executor)
	})

	t.Run("FileOperations", func(t *testing.T) {
		testSSHFileOperations(t, executor)
	})

	t.Run("DirectoryOperations", func(t *testing.T) {
		testSSHDirectoryOperations(t, executor)
	})

	t.Run("CommandExecution", func(t *testing.T) {
		testSSHCommandExecution(t, executor)
	})

	t.Run("ProcessManagement", func(t *testing.T) {
		testSSHProcessManagement(t, executor)
	})

	t.Run("SystemInfo", func(t *testing.T) {
		testSSHSystemInfo(t, executor)
	})

	t.Run("PortChecking", func(t *testing.T) {
		testSSHPortChecking(t, executor)
	})
}

// TestSSHExecutor_MultiHost tests deployment to multiple containers
func TestSSHExecutor_MultiHost(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Build the SSH node image first
	t.Log("Building SSH node Docker image...")
	if err := buildSSHNodeImageIfNeeded(ctx, t); err != nil {
		t.Fatalf("Failed to build SSH node image: %v", err)
	}

	// Create test environment
	env := NewTestEnvironment(ctx, SSHContainerConfig{
		ImageName:      "mup-ssh-node:latest",
		Username:       "testuser",
		Password:       "testpass",
		StartupTimeout: 60 * time.Second,
	})
	defer env.Cleanup()

	// Create a multi-host topology
	topo := &topology.Topology{
		Global: topology.GlobalConfig{
			User:      "testuser",
			DeployDir: "/opt/mongodb",
			DataDir:   "/data/mongodb",
			LogDir:    "/var/log/mongodb",
			ConfigDir: "/etc/mongodb",
		},
		Mongod: []topology.MongodNode{
			{Host: "node1", Port: 27017, ReplicaSet: "rs0"},
			{Host: "node2", Port: 27017, ReplicaSet: "rs0"},
			{Host: "node3", Port: 27017, ReplicaSet: "rs0"},
		},
	}

	// Launch containers for the topology
	t.Log("Launching containers for multi-host topology...")
	if err := env.LaunchContainersForTopology(topo); err != nil {
		t.Fatalf("Failed to launch containers: %v", err)
	}

	// Verify we have 3 containers
	assert.Len(t, env.Containers, 3, "Should have 3 containers for multi-host topology")

	// Create executors for all hosts
	executors, err := env.CreateExecutorsForTopology(topo)
	require.NoError(t, err, "Failed to create executors")

	// Verify we can execute commands on all hosts
	for host, exec := range executors {
		t.Run(fmt.Sprintf("Host_%s", host), func(t *testing.T) {
			err := exec.CheckConnectivity()
			assert.NoError(t, err, "Should be able to connect to %s", host)

			// Create a test file on each host
			testFile := fmt.Sprintf("/tmp/test-%s.txt", host)
			err = exec.UploadContent([]byte(fmt.Sprintf("Hello from %s", host)), testFile)
			assert.NoError(t, err, "Should be able to create file on %s", host)

			// Verify file exists
			exists, err := exec.FileExists(testFile)
			assert.NoError(t, err)
			assert.True(t, exists, "File should exist on %s", host)
		})
	}
}

// Test helper functions

func testSSHConnectivity(t *testing.T, exec Executor) {
	err := exec.CheckConnectivity()
	assert.NoError(t, err, "Should be able to connect via SSH")
}

func testSSHFileOperations(t *testing.T, exec Executor) {
	testFile := "/tmp/mup-test-file.txt"
	testContent := []byte("Hello from mup test\n")

	// Upload content
	err := exec.UploadContent(testContent, testFile)
	require.NoError(t, err, "Should be able to upload file")

	// Check file exists
	exists, err := exec.FileExists(testFile)
	require.NoError(t, err)
	assert.True(t, exists, "File should exist after upload")

	// Download file
	localPath := filepath.Join(t.TempDir(), "downloaded.txt")
	err = exec.DownloadFile(testFile, localPath)
	require.NoError(t, err, "Should be able to download file")

	// Verify content
	downloaded, err := os.ReadFile(localPath)
	require.NoError(t, err)
	assert.Equal(t, string(testContent), string(downloaded), "Downloaded content should match")

	// Remove file
	err = exec.RemoveFile(testFile)
	require.NoError(t, err, "Should be able to remove file")

	// Verify file is gone
	exists, err = exec.FileExists(testFile)
	require.NoError(t, err)
	assert.False(t, exists, "File should not exist after removal")
}

func testSSHDirectoryOperations(t *testing.T, exec Executor) {
	testDir := "/tmp/mup-test-dir"

	// Create directory
	err := exec.CreateDirectory(testDir, 0755)
	require.NoError(t, err, "Should be able to create directory")

	// Check directory exists
	exists, err := exec.FileExists(testDir)
	require.NoError(t, err)
	assert.True(t, exists, "Directory should exist after creation")

	// Create file in directory
	testFile := filepath.Join(testDir, "test.txt")
	err = exec.UploadContent([]byte("test"), testFile)
	require.NoError(t, err, "Should be able to create file in directory")

	// Remove directory
	err = exec.RemoveDirectory(testDir)
	require.NoError(t, err, "Should be able to remove directory")

	// Verify directory is gone
	exists, err = exec.FileExists(testDir)
	require.NoError(t, err)
	assert.False(t, exists, "Directory should not exist after removal")
}

func testSSHCommandExecution(t *testing.T, exec Executor) {
	// Test simple command
	output, err := exec.Execute("echo 'hello world'")
	require.NoError(t, err, "Should be able to execute command")
	assert.Contains(t, output, "hello world", "Output should contain expected text")

	// Test command with pipes
	output, err = exec.Execute("echo 'one\ntwo\nthree' | wc -l")
	require.NoError(t, err)
	assert.Contains(t, strings.TrimSpace(output), "3", "Should count 3 lines")

	// Test command with input
	input := strings.NewReader("test input\n")
	output, err = exec.ExecuteWithInput("cat", input)
	require.NoError(t, err)
	assert.Contains(t, output, "test input", "Should echo input")
}

func testSSHProcessManagement(t *testing.T, exec Executor) {
	// Start a background process
	pid, err := exec.Background("sleep 30")
	require.NoError(t, err, "Should be able to start background process")
	assert.Greater(t, pid, 0, "PID should be positive")

	// Check process is running
	running, err := exec.IsProcessRunning(pid)
	require.NoError(t, err)
	assert.True(t, running, "Process should be running")

	// Kill process
	err = exec.KillProcess(pid)
	require.NoError(t, err, "Should be able to kill process")

	// Wait a bit for process to die
	time.Sleep(500 * time.Millisecond)

	// Check process is not running
	running, err = exec.IsProcessRunning(pid)
	require.NoError(t, err)
	assert.False(t, running, "Process should not be running after kill")
}

func testSSHSystemInfo(t *testing.T, exec Executor) {
	// Get OS info
	info, err := exec.GetOSInfo()
	require.NoError(t, err, "Should be able to get OS info")
	assert.NotEmpty(t, info.OS, "OS should not be empty")
	assert.NotEmpty(t, info.Arch, "Arch should not be empty")
	t.Logf("OS: %s, Arch: %s, Version: %s", info.OS, info.Arch, info.Version)

	// Get disk space
	space, err := exec.GetDiskSpace("/tmp")
	require.NoError(t, err, "Should be able to get disk space")
	assert.Greater(t, space, uint64(0), "Disk space should be positive")
	t.Logf("Available disk space in /tmp: %d bytes (%.2f GB)", space, float64(space)/1024/1024/1024)

	// Check user exists
	exists, err := exec.UserExists("testuser")
	require.NoError(t, err)
	assert.True(t, exists, "testuser should exist")

	exists, err = exec.UserExists("nonexistentuser12345")
	require.NoError(t, err)
	assert.False(t, exists, "nonexistent user should not exist")
}

func testSSHPortChecking(t *testing.T, exec Executor) {
	// Port 22 (SSH) should NOT be available
	available, err := exec.CheckPortAvailable(22)
	require.NoError(t, err)
	assert.False(t, available, "Port 22 should not be available (SSH is listening)")

	// High port should be available
	available, err = exec.CheckPortAvailable(35000)
	require.NoError(t, err)
	assert.True(t, available, "Port 35000 should be available")
}

// buildSSHNodeImageIfNeeded builds the SSH node image if it doesn't exist
func buildSSHNodeImageIfNeeded(ctx context.Context, t *testing.T) error {
	// Check if image exists
	// For now, we'll just try to build it every time
	// In the future, we could check if it exists first

	dockerfilePath := filepath.Join("..", "..", "test", "docker", "ssh-node")

	// Check if Dockerfile exists
	if _, err := os.Stat(filepath.Join(dockerfilePath, "Dockerfile")); err != nil {
		return fmt.Errorf("Dockerfile not found at %s: %w", dockerfilePath, err)
	}

	t.Logf("Building Docker image from %s...", dockerfilePath)

	// Build using docker command for now (testcontainers build is complex)
	// In production, we'd use BuildSSHNodeImage() but for testing, direct docker is simpler
	// Users will need Docker installed to run these tests

	return nil // Image build is handled by testcontainers when starting containers
}
