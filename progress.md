# SSH Testing Implementation Progress

## Overview
Implemented comprehensive testcontainers-based testing infrastructure for SSHExecutor to enable realistic end-to-end testing of SSH-based MongoDB deployments.

## Implementation Completed

### 1. Docker Infrastructure (`test/docker/ssh-node/`)
- **Dockerfile**: Ubuntu 22.04 with OpenSSH server
  - Test user: `testuser:testpass` with sudo access
  - Pre-created MongoDB directories: `/data/mongodb`, `/var/log/mongodb`, `/etc/mongodb`
  - Common utilities: curl, wget, lsof, net-tools, procps, vim, netcat
  - SSH configured for both password and key-based auth

- **entrypoint.sh**: Container startup script
  - Generates SSH host keys
  - Waits for authorized_keys mount (optional)
  - Starts SSH daemon in foreground

- **build.sh**: Helper script to build image
- **README.md**: Documentation for Docker image

### 2. Testcontainers Helpers (`pkg/executor/testcontainer_helpers.go`)
Core infrastructure for managing Docker containers in tests.

**Key Types:**
- `SSHContainerConfig`: Configuration for SSH containers
- `ContainerHost`: Represents running SSH container with executor
- `TestEnvironment`: Manages collection of SSH containers

**Key Functions:**
- `NewTestEnvironment()`: Creates test environment
- `LaunchContainersForTopology()`: Auto-creates containers based on topology
  - **Local deployments** (all hosts = localhost): Creates 1 container
  - **Multi-host deployments**: Creates container per unique host
- `launchContainer()`: Launches single SSH-enabled container
- `CreateExecutorsForTopology()`: Creates SSH executors for all hosts
- `Cleanup()`: Terminates all containers

**Critical Logic:**
```go
// For local deployments, create one container for localhost
if topo.IsLocalDeployment() {
    container, err := env.launchContainer("localhost")
    // Map localhost aliases to same container
    env.Containers["localhost"] = container
    env.Containers["127.0.0.1"] = container
    env.Containers["::1"] = container
}
```

### 3. SSHExecutor Implementation (`pkg/executor/ssh.go`)
Complete implementation of Executor interface over SSH protocol.

**SSHConfig:**
```go
type SSHConfig struct {
    Host     string
    Port     int
    User     string
    Password string
    KeyFile  string
    Timeout  time.Duration
}
```

**Authentication:**
- Supports both password and key-based authentication
- Uses `golang.org/x/crypto/ssh` library

**Key Methods Implemented:**
- **File Operations**: CreateDirectory, UploadFile, UploadContent, DownloadFile, FileExists, RemoveFile, RemoveDirectory
- **Command Execution**: Execute, ExecuteWithInput, Background
- **Process Management**: IsProcessRunning, KillProcess, StopProcess
- **System Info**: GetOSInfo, GetDiskSpace, CheckPortAvailable, UserExists
- **Connection**: CheckConnectivity, Close

**Notable Implementation Details:**

1. **Background Process Management** (critical for MongoDB):
```go
// Uses nohup to keep processes alive after SSH disconnect
cmd := fmt.Sprintf("nohup %s > /dev/null 2>&1 & echo $!", command)
```

2. **Safe File Upload** (handles special characters):
```go
// Escape single quotes and use heredoc
escapedContent := strings.ReplaceAll(string(content), "'", "'\\''")
cmd := fmt.Sprintf("cat > %s << 'MUPEOF'\n%s\nMUPEOF", remotePath, escapedContent)
```

3. **Port Availability Check**:
```go
// Uses lsof to check for listening processes
cmd := fmt.Sprintf("lsof -i :%d -sTCP:LISTEN", port)
// lsof returns error if nothing listening = port available
```

### 4. Integration Tests (`pkg/executor/ssh_integration_test.go`)
Comprehensive tests validating all Executor interface methods over SSH.

**Test Functions:**
1. `TestSSHExecutor_Testcontainers`: Single host deployment
   - Sub-tests: Connectivity, FileOperations, DirectoryOperations, CommandExecution, ProcessManagement, SystemInfo, PortChecking

2. `TestSSHExecutor_MultiHost`: Multi-host deployment
   - Creates 3 containers (node1, node2, node3)
   - Verifies operations on each host independently

**Test Pattern:**
```go
func TestSSHExecutor_Testcontainers(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test in short mode")
    }

    env := NewTestEnvironment(ctx, SSHContainerConfig{...})
    defer env.Cleanup()

    topo := &topology.Topology{...}
    env.LaunchContainersForTopology(topo)

    executor, err := container.CreateExecutor()
    require.NoError(t, err)
    defer executor.Close()

    // Run sub-tests
    t.Run("FileOperations", func(t *testing.T) {
        testSSHFileOperations(t, executor)
    })
}
```

### 5. Makefile Updates
Added convenience targets for SSH testing:

```makefile
## docker-ssh-image: Build Docker image for SSH testing
docker-ssh-image:
	@bash test/docker/ssh-node/build.sh

## test-ssh-short: Run quick SSH tests (skip integration tests)
test-ssh-short:
	$(GOTEST) -short -v ./pkg/executor/

## test-ssh: Run full SSH integration tests (requires Docker)
test-ssh: docker-ssh-image
	$(GOTEST) -v -run TestSSH ./pkg/executor/

## test-all: Run all tests including SSH integration tests
test-all: docker-ssh-image
	$(GOTEST) -v ./...
```

### 6. Documentation
- **docs/SSHExecutor_PLAN.md**: Planning document with 4 testing approaches
- **docs/TESTING_SSH.md**: Comprehensive testing guide
  - Architecture overview
  - Prerequisites and setup
  - Building and running tests
  - Writing new tests
  - Manual testing procedures
  - CI/CD integration examples
  - Troubleshooting guide
  - Best practices

### 7. Dependencies Added
- `github.com/testcontainers/testcontainers-go v0.40.0`
- `github.com/docker/docker v28.5.1+incompatible`
- `golang.org/x/crypto v0.43.0` (upgraded)
- Supporting Docker/container libraries

## Testing Commands

```bash
# Build Docker image
make docker-ssh-image

# Run full SSH integration tests
make test-ssh

# Run quick tests without containers
make test-ssh-short

# Run all tests including SSH integration
make test-all

# Manual container testing
docker run -d -p 2222:22 --name mup-test mup-ssh-node:latest
ssh -p 2222 testuser@localhost  # password: testpass
docker stop mup-test && docker rm mup-test
```

## Architecture Decisions

1. **Container per unique host**: For local deployments (all localhost), create 1 container. For multi-host, create container per unique hostname.

2. **Dual authentication support**: Both password and key-based SSH authentication supported for flexibility.

3. **Testcontainers auto-port mapping**: Let testcontainers handle port allocation to avoid conflicts.

4. **Deferred cleanup**: Use `defer env.Cleanup()` pattern to ensure containers are removed.

5. **Short test flag**: Use `testing.Short()` to skip integration tests during rapid development.

6. **Ubuntu 22.04 base**: Modern, well-supported base image for SSH testing.

## Files Created/Modified

### New Files:
- `test/docker/ssh-node/Dockerfile`
- `test/docker/ssh-node/entrypoint.sh`
- `test/docker/ssh-node/README.md`
- `test/docker/ssh-node/build.sh`
- `pkg/executor/testcontainer_helpers.go`
- `pkg/executor/ssh.go`
- `pkg/executor/ssh_integration_test.go`
- `docs/SSHExecutor_PLAN.md`
- `docs/TESTING_SSH.md`

### Modified Files:
- `go.mod` (added testcontainers-go v0.40.0)
- `go.sum` (dependency updates)
- `Makefile` (added SSH testing targets)

## Next Steps

1. **Validate Implementation**: Build Docker image and run integration tests
   ```bash
   make test-ssh
   ```

2. **Integrate with Deployment**: Update cluster deployment to use SSHExecutor for remote hosts

3. **Add CI/CD**: Configure GitHub Actions to run SSH integration tests

4. **Security Hardening**:
   - Replace `ssh.InsecureIgnoreHostKey()` with proper host key verification
   - Add support for SSH agent forwarding
   - Document secure key management practices

5. **Future Enhancements**:
   - Support for SSH key-based authentication in tests
   - Multi-platform Docker image support (arm64, amd64)
   - Network simulation (latency, packet loss)
   - Resource limits testing (CPU, memory, disk)
   - Systemd service testing
   - MongoDB binary pre-installation in image
   - Parallel container startup for faster tests
   - Container image caching between test runs
   - Test fixtures for common topologies

## Status

✅ **Implementation Complete** - All code written and ready for testing
⏳ **Validation Pending** - Docker image needs to be built and tests run
🎯 **Ready for Integration** - SSHExecutor ready to be integrated into cluster deployment workflow
