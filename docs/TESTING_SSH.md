# SSH Testing with Testcontainers

This document describes how to test the SSHExecutor implementation using Docker containers via the testcontainers-go library.

## Overview

We use **testcontainers-go** to launch real SSH-enabled Docker containers for testing. This approach provides:

- **Realistic testing**: Tests run against actual SSH servers, not mocks
- **Isolation**: Each test gets fresh containers with no state pollution
- **Automated cleanup**: Containers are automatically removed after tests
- **Multi-host simulation**: Can test complex multi-host deployments locally
- **CI/CD ready**: Tests run anywhere Docker is available

## Architecture

### Components

1. **SSH Node Docker Image** (`test/docker/ssh-node/`)
   - Ubuntu 22.04 base
   - OpenSSH server pre-configured
   - Test user with sudo access
   - MongoDB-compatible directory structure

2. **Testcontainer Helpers** (`pkg/executor/testcontainer_helpers.go`)
   - `TestEnvironment`: Manages container lifecycle
   - `ContainerHost`: Represents a single SSH-enabled container
   - `LaunchContainersForTopology()`: Auto-creates containers for topology files

3. **SSHExecutor** (`pkg/executor/ssh.go`)
   - Implements the Executor interface over SSH
   - Connects to containers and executes commands remotely

4. **Integration Tests** (`pkg/executor/ssh_integration_test.go`)
   - Tests all Executor interface methods
   - Tests single-host and multi-host scenarios
   - Validates full deployment workflow

## Prerequisites

### Required

- **Docker**: Must be installed and running
  - macOS: Docker Desktop
  - Linux: Docker Engine
  - Windows: Docker Desktop with WSL2

- **Go 1.21+**: For running tests

### Optional

- **golangci-lint**: For linting (not required for tests)

## Building the SSH Node Image

Before running SSH tests, build the Docker image:

```bash
# Using make (recommended)
make docker-ssh-image

# Or manually
cd test/docker/ssh-node
./build.sh

# Or with docker directly
docker build -t mup-ssh-node:latest test/docker/ssh-node/
```

Verify the image was built:

```bash
docker images | grep mup-ssh-node
```

## Running Tests

### Quick Tests (Local Executor Only)

Skip SSH integration tests during rapid development:

```bash
# Using make
make test-ssh-short

# Or directly
go test -short -v ./pkg/executor/
```

### SSH Integration Tests

Run full SSH integration tests with containers:

```bash
# Using make (builds image automatically)
make test-ssh

# Or directly (requires image to be built first)
go test -v -run TestSSH ./pkg/executor/
```

### All Tests

Run all tests including SSH integration:

```bash
# Using make
make test-all

# Or directly
go test -v ./...
```

### Individual Test Cases

Run specific test functions:

```bash
# Run only testcontainer tests
go test -v -run TestSSHExecutor_Testcontainers ./pkg/executor/

# Run only multi-host tests
go test -v -run TestSSHExecutor_MultiHost ./pkg/executor/

# Run specific sub-test
go test -v -run TestSSHExecutor_Testcontainers/FileOperations ./pkg/executor/
```

## Test Structure

### Single Host Test

Tests deployment to a single container (simulates local deployment):

```go
func TestSSHExecutor_Testcontainers(t *testing.T) {
    // 1. Create test environment
    env := NewTestEnvironment(ctx, config)
    defer env.Cleanup()

    // 2. Create topology (all nodes on "localhost")
    topo := &topology.Topology{...}

    // 3. Launch containers (creates 1 container for localhost)
    env.LaunchContainersForTopology(topo)

    // 4. Create SSH executor
    executor := container.CreateExecutor()

    // 5. Run tests
    testSSHFileOperations(t, executor)
    testSSHCommandExecution(t, executor)
    // etc...
}
```

### Multi-Host Test

Tests deployment to multiple containers:

```go
func TestSSHExecutor_MultiHost(t *testing.T) {
    // 1. Create topology with distinct hosts
    topo := &topology.Topology{
        Mongod: []topology.MongodNode{
            {Host: "node1", ...},
            {Host: "node2", ...},
            {Host: "node3", ...},
        },
    }

    // 2. Launch containers (creates 3 containers)
    env.LaunchContainersForTopology(topo)

    // 3. Create executors for all hosts
    executors := env.CreateExecutorsForTopology(topo)

    // 4. Test operations on each host
    for host, exec := range executors {
        testOperations(t, exec, host)
    }
}
```

## Writing New Tests

### Basic Test Template

```go
func TestSSH_MyFeature(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test in short mode")
    }

    ctx := context.Background()

    // Build image if needed
    if err := buildSSHNodeImageIfNeeded(ctx, t); err != nil {
        t.Fatalf("Failed to build image: %v", err)
    }

    // Create environment
    env := NewTestEnvironment(ctx, SSHContainerConfig{
        ImageName:      "mup-ssh-node:latest",
        Username:       "testuser",
        Password:       "testpass",
        StartupTimeout: 60 * time.Second,
    })
    defer env.Cleanup()

    // Create topology
    topo := createTestTopology()

    // Launch containers
    if err := env.LaunchContainersForTopology(topo); err != nil {
        t.Fatalf("Failed to launch containers: %v", err)
    }

    // Get executor
    container := env.Containers["localhost"]
    executor, err := container.CreateExecutor()
    require.NoError(t, err)
    defer executor.Close()

    // Your test logic here
    // ...
}
```

### Testing Multiple Hosts

```go
// Create multi-host topology
topo := &topology.Topology{
    Global: topology.GlobalConfig{
        User: "testuser",
        // ...
    },
    Mongod: []topology.MongodNode{
        {Host: "db1", Port: 27017},
        {Host: "db2", Port: 27017},
        {Host: "db3", Port: 27017},
    },
}

// Launch containers for each unique host
env.LaunchContainersForTopology(topo)

// Create executors
executors, err := env.CreateExecutorsForTopology(topo)

// Test each host
for hostname, exec := range executors {
    t.Run(hostname, func(t *testing.T) {
        // Test operations on this host
    })
}
```

## Manual Testing

You can manually start a container for interactive testing:

```bash
# Start container
docker run -d \
  -p 2222:22 \
  --name mup-manual-test \
  mup-ssh-node:latest

# Wait for it to start
sleep 2

# SSH into it
ssh -p 2222 testuser@localhost
# Password: testpass

# Or with key (if you set one up)
ssh -i ~/.ssh/id_ed25519_mup_test -p 2222 testuser@localhost

# Clean up when done
docker stop mup-manual-test
docker rm mup-manual-test
```

### Useful Commands Inside Container

```bash
# Check SSH is running
ps aux | grep sshd

# Check port availability
lsof -i :27017

# Check disk space
df -h

# Test MongoDB directories
ls -la /data/mongodb /var/log/mongodb /etc/mongodb

# Check system info
uname -a
cat /etc/os-release
```

## CI/CD Integration

### GitHub Actions Example

```yaml
name: SSH Tests

on: [push, pull_request]

jobs:
  test-ssh:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Build SSH test image
        run: make docker-ssh-image

      - name: Run SSH tests
        run: make test-ssh

      - name: Upload coverage
        uses: codecov/codecov-action@v3
        with:
          files: ./coverage.out
```

## Troubleshooting

### Image Build Fails

**Problem**: `docker build` fails

**Solutions**:
- Ensure Docker is running: `docker ps`
- Check Dockerfile syntax
- Try building manually: `cd test/docker/ssh-node && docker build -t mup-ssh-node:latest .`

### Container Won't Start

**Problem**: `failed to start container`

**Solutions**:
- Check Docker daemon: `docker ps`
- Check port conflicts: `lsof -i :2222` (or whatever port)
- Check Docker logs: `docker logs <container-id>`
- Increase startup timeout in test config

### SSH Connection Fails

**Problem**: `failed to connect to SSH server`

**Solutions**:
- Verify container is running: `docker ps | grep mup-test`
- Check SSH is listening: `docker exec <container> ps aux | grep sshd`
- Test manual connection: `ssh -p <port> testuser@localhost`
- Check firewall/network settings

### Tests Hang or Timeout

**Problem**: Tests hang indefinitely

**Solutions**:
- Check container logs: `docker logs <container-id>`
- Reduce timeout in test to fail faster
- Run with verbose logging: `go test -v -run TestSSH ./pkg/executor/`
- Check for zombie containers: `docker ps -a | grep mup-test`

### Cleanup Issues

**Problem**: Containers not removed after tests

**Solutions**:
- Manual cleanup: `docker ps -a | grep mup-test | awk '{print $1}' | xargs docker rm -f`
- Use `docker system prune` (careful - removes all stopped containers)
- Check test cleanup code runs: use `defer env.Cleanup()`

### Port Already In Use

**Problem**: `port is already allocated`

**Solutions**:
- Find process using port: `lsof -i :<port>`
- Kill process or use different port
- Let testcontainers auto-assign ports (default behavior)

## Performance Tips

### Speed Up Tests

1. **Build image once**: Build the Docker image before running multiple test iterations
   ```bash
   make docker-ssh-image
   # Then run tests multiple times
   ```

2. **Use test parallelization**: Run independent tests in parallel
   ```go
   t.Run("Test1", func(t *testing.T) {
       t.Parallel()
       // ...
   })
   ```

3. **Reuse containers**: For rapid iteration, keep containers running between test runs
   - Not recommended for CI/CD (state pollution risk)
   - Useful for local development

4. **Skip SSH tests during development**:
   ```bash
   go test -short ./...  # Skips SSH tests
   ```

## Best Practices

### 1. Always Use `defer cleanup()`

```go
env := NewTestEnvironment(ctx, config)
defer env.Cleanup()  // IMPORTANT: Always clean up containers
```

### 2. Use Unique Paths

```go
// Good - unique path per test
testDir := fmt.Sprintf("/tmp/mup-test-%s", randomString())

// Bad - shared path (can conflict)
testDir := "/tmp/mup-test"
```

### 3. Check for Skipped Tests

```go
if testing.Short() {
    t.Skip("Skipping integration test in short mode")
}
```

### 4. Use Testify Assertions

```go
require.NoError(t, err, "Should not error")
assert.Equal(t, expected, actual, "Values should match")
```

### 5. Log Useful Information

```go
t.Logf("Container: %s:%d (internal IP: %s)",
    container.SSHHost, container.SSHPort, container.InternalIP)
```

## Future Enhancements

- [ ] Support for SSH key-based authentication in tests
- [ ] Multi-platform Docker image support (arm64, amd64)
- [ ] Network simulation (latency, packet loss)
- [ ] Resource limits testing (CPU, memory, disk)
- [ ] Systemd service testing
- [ ] MongoDB binary pre-installation in image
- [ ] Parallel container startup for faster tests
- [ ] Container image caching between test runs
- [ ] Test fixtures for common topologies

## References

- [Testcontainers Go Documentation](https://golang.testcontainers.org/)
- [Docker SDK for Go](https://docs.docker.com/engine/api/sdk/)
- [SSH Protocol RFC](https://datatracker.ietf.org/doc/html/rfc4251)
- [Go SSH Package](https://pkg.go.dev/golang.org/x/crypto/ssh)
