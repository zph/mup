# SSH Executor Local Testing Plan

## Overview

This document outlines strategies for testing the SSHExecutor implementation locally before deploying to remote hosts. Multiple approaches are provided, each with different trade-offs.

## Approach 1: SSH to Localhost (RECOMMENDED)

### Description
Configure your local machine to accept SSH connections and test SSHExecutor by connecting to `127.0.0.1` or `localhost`.

### Pros
- Most realistic - tests actual SSH protocol
- No additional infrastructure needed
- Tests same environment as production (real filesystem, real processes)
- Can test key-based and password authentication
- Simple setup

### Cons
- Requires SSH server running locally
- Can interfere with actual local files (need careful path isolation)
- macOS requires enabling Remote Login in System Preferences

### Setup Steps

1. **Enable SSH Server**:
   - macOS: System Preferences → Sharing → Remote Login
   - Linux: `sudo systemctl start sshd`

2. **Set up SSH keys** (no password prompt):
   ```bash
   # Generate key if you don't have one
   ssh-keygen -t ed25519 -f ~/.ssh/id_ed25519_mup_test -N ""

   # Add to authorized_keys
   cat ~/.ssh/id_ed25519_mup_test.pub >> ~/.ssh/authorized_keys
   chmod 600 ~/.ssh/authorized_keys

   # Test connection
   ssh -i ~/.ssh/id_ed25519_mup_test localhost "echo test"
   ```

3. **Create test workspace** to avoid conflicts:
   ```bash
   mkdir -p ~/mup-ssh-test-workspace
   ```

### Implementation Pattern

```go
// pkg/executor/ssh_test.go
func TestSSHExecutor_LocalConnection(t *testing.T) {
    // Connect to localhost
    executor, err := NewSSHExecutor(SSHConfig{
        Host:     "127.0.0.1",
        Port:     22,
        User:     os.Getenv("USER"),
        KeyFile:  filepath.Join(os.Getenv("HOME"), ".ssh/id_ed25519_mup_test"),
        WorkDir:  filepath.Join(os.Getenv("HOME"), "mup-ssh-test-workspace"),
    })
    if err != nil {
        t.Fatalf("Failed to create SSH executor: %v", err)
    }
    defer executor.Close()

    // Test connectivity
    err = executor.CheckConnectivity()
    assert.NoError(t, err)

    // Test command execution
    output, err := executor.Execute("echo 'hello world'")
    assert.NoError(t, err)
    assert.Contains(t, output, "hello world")
}
```

## Approach 2: Docker Containers with SSH

### Description
Run Docker containers with SSH servers for isolated testing.

### Pros
- Complete isolation - no risk to local files
- Can test multiple OS flavors (Ubuntu, CentOS, etc.)
- Can simulate network conditions
- Easy cleanup (just remove containers)
- Repeatable test environment

### Cons
- Requires Docker installed and running
- More complex setup
- Additional resource overhead
- Not testing exact production environment

### Setup

Create a Dockerfile for the SSH test container:

```dockerfile
# test/docker/ssh-test/Dockerfile
FROM ubuntu:22.04

RUN apt-get update && \
    apt-get install -y openssh-server sudo && \
    mkdir /var/run/sshd && \
    useradd -m -s /bin/bash testuser && \
    echo 'testuser:testpass' | chpasswd && \
    echo 'testuser ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers

# Allow SSH login
RUN mkdir -p /home/testuser/.ssh && \
    chmod 700 /home/testuser/.ssh

EXPOSE 22

CMD ["/usr/sbin/sshd", "-D"]
```

Build and run the container:

```bash
# Build and run
cd test/docker/ssh-test
docker build -t mup-ssh-test .
docker run -d -p 2222:22 --name mup-ssh-test mup-ssh-test

# Copy SSH key
docker exec mup-ssh-test bash -c "mkdir -p /home/testuser/.ssh"
cat ~/.ssh/id_ed25519_mup_test.pub | docker exec -i mup-ssh-test \
    bash -c "cat > /home/testuser/.ssh/authorized_keys"
docker exec mup-ssh-test chown -R testuser:testuser /home/testuser/.ssh
docker exec mup-ssh-test chmod 600 /home/testuser/.ssh/authorized_keys

# Test connection
ssh -i ~/.ssh/id_ed25519_mup_test -p 2222 testuser@localhost "echo test"
```

### Test Implementation

```go
func TestSSHExecutor_Docker(t *testing.T) {
    executor, err := NewSSHExecutor(SSHConfig{
        Host:     "127.0.0.1",
        Port:     2222,  // Docker mapped port
        User:     "testuser",
        KeyFile:  filepath.Join(os.Getenv("HOME"), ".ssh/id_ed25519_mup_test"),
        WorkDir:  "/home/testuser/mup-test",
    })
    // ... same tests as above
}
```

## Approach 3: Integration Tests with Real Scenarios

### Description
Create comprehensive integration tests that mirror actual deployment workflows.

### Test Structure

```
pkg/executor/
├── executor.go           # Interface
├── local.go             # Local implementation
├── ssh.go               # SSH implementation
├── local_test.go        # Local executor tests
├── ssh_test.go          # SSH executor basic tests
└── integration_test.go  # Cross-executor integration tests
```

### Key Test Cases

```go
// pkg/executor/integration_test.go
package executor

import (
    "testing"
)

// Test cases that should work for BOTH Local and SSH executors
var executorTests = []struct {
    name string
    test func(t *testing.T, exec Executor)
}{
    {"CreateDirectory", testCreateDirectory},
    {"UploadContent", testUploadContent},
    {"UploadFile", testUploadFile},
    {"FileExists", testFileExists},
    {"Execute", testExecute},
    {"Background", testBackground},
    {"ProcessManagement", testProcessManagement},
    {"GetOSInfo", testGetOSInfo},
    {"GetDiskSpace", testGetDiskSpace},
    {"CheckPortAvailable", testCheckPortAvailable},
}

func TestLocalExecutor_AllOperations(t *testing.T) {
    exec := NewLocalExecutor()
    defer exec.Close()

    for _, tt := range executorTests {
        t.Run(tt.name, func(t *testing.T) {
            tt.test(t, exec)
        })
    }
}

func TestSSHExecutor_AllOperations(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping SSH integration test in short mode")
    }

    exec := newTestSSHExecutor(t)
    defer exec.Close()

    for _, tt := range executorTests {
        t.Run(tt.name, func(t *testing.T) {
            tt.test(t, exec)
        })
    }
}

func testCreateDirectory(t *testing.T, exec Executor) {
    testDir := "/tmp/mup-test-dir-" + randomString()
    defer exec.RemoveDirectory(testDir)

    err := exec.CreateDirectory(testDir, 0755)
    assert.NoError(t, err)

    exists, err := exec.FileExists(testDir)
    assert.NoError(t, err)
    assert.True(t, exists)
}

func testUploadContent(t *testing.T, exec Executor) {
    testFile := "/tmp/mup-test-" + randomString()
    defer exec.RemoveFile(testFile)

    content := []byte("test content\n")
    err := exec.UploadContent(content, testFile)
    assert.NoError(t, err)

    exists, err := exec.FileExists(testFile)
    assert.NoError(t, err)
    assert.True(t, exists)
}

func testBackground(t *testing.T, exec Executor) {
    // Start a long-running process
    pid, err := exec.Background("sleep 30")
    assert.NoError(t, err)
    assert.Greater(t, pid, 0)

    // Check it's running
    running, err := exec.IsProcessRunning(pid)
    assert.NoError(t, err)
    assert.True(t, running)

    // Kill it
    err = exec.KillProcess(pid)
    assert.NoError(t, err)

    // Wait a bit for process to die
    time.Sleep(100 * time.Millisecond)

    // Check it's not running
    running, err = exec.IsProcessRunning(pid)
    assert.NoError(t, err)
    assert.False(t, running)
}

func testCheckPortAvailable(t *testing.T, exec Executor) {
    // Port 99999 should be available
    available, err := exec.CheckPortAvailable(99999)
    assert.NoError(t, err)
    assert.True(t, available)

    // Port 22 (SSH) should NOT be available (already in use)
    available, err = exec.CheckPortAvailable(22)
    assert.NoError(t, err)
    assert.False(t, available)
}

func newTestSSHExecutor(t *testing.T) Executor {
    t.Helper()

    config := SSHConfig{
        Host:    "127.0.0.1",
        Port:    22,
        User:    os.Getenv("USER"),
        KeyFile: filepath.Join(os.Getenv("HOME"), ".ssh/id_ed25519_mup_test"),
        WorkDir: "/tmp/mup-ssh-test-" + randomString(),
    }

    exec, err := NewSSHExecutor(config)
    if err != nil {
        t.Fatalf("Failed to create SSH executor: %v", err)
    }

    // Create work directory
    if err := exec.CreateDirectory(config.WorkDir, 0755); err != nil {
        t.Fatalf("Failed to create work directory: %v", err)
    }

    t.Cleanup(func() {
        exec.RemoveDirectory(config.WorkDir)
        exec.Close()
    })

    return exec
}
```

## Approach 4: Unit Tests with Mock SSH

### Description
Use a mock SSH server library for fast unit tests.

### Library
`github.com/gliderlabs/ssh` (SSH server library in Go)

### Pros
- Very fast
- No external dependencies
- Complete control over responses
- Good for edge case testing (network failures, permission errors, etc.)

### Cons
- Not testing real SSH implementation
- More complex mock setup
- May miss real-world issues

### Example

```go
func TestSSHExecutor_MockServer(t *testing.T) {
    // Start mock SSH server
    server := &ssh.Server{
        Addr: "127.0.0.1:2200",
        Handler: mockSSHHandler,
    }
    go server.ListenAndServe()
    defer server.Close()

    time.Sleep(100 * time.Millisecond) // Let server start

    // Test against mock
    exec, err := NewSSHExecutor(SSHConfig{
        Host: "127.0.0.1",
        Port: 2200,
        // ... config
    })
    // ... tests
}
```

## Recommended Testing Strategy

### For Development (fastest iteration)
1. Use **SSH to localhost** for quick manual testing
2. Use **table-driven integration tests** that run against both Local and SSH executors
3. Run with `go test -short` to skip SSH tests during rapid development

### For CI/CD
1. Use **Docker containers** for isolated, repeatable tests
2. Test against multiple OS flavors (Ubuntu 20.04, 22.04, CentOS, etc.)
3. Include network failure scenarios

### Test Commands

```bash
# Quick tests (local only)
go test -short ./pkg/executor/

# Full tests (including SSH)
go test -v ./pkg/executor/

# With coverage
go test -v -coverprofile=coverage.out ./pkg/executor/
go tool cover -html=coverage.out

# Just SSH tests
go test -v -run TestSSH ./pkg/executor/
```

## Implementation Checklist

- [ ] Create `pkg/executor/ssh.go` with SSHExecutor implementation
- [ ] Add SSH client library dependency: `golang.org/x/crypto/ssh`
- [ ] Implement all Executor interface methods for SSH
- [ ] Create `pkg/executor/ssh_test.go` with basic SSH tests
- [ ] Create `pkg/executor/integration_test.go` with shared test suite
- [ ] Set up SSH keys for local testing (`.ssh/id_ed25519_mup_test`)
- [ ] Add Docker test environment (optional but recommended)
- [ ] Document SSH testing setup in `docs/DEVELOPMENT.md`
- [ ] Add test helpers in `pkg/executor/testing.go`
- [ ] Update Makefile with `test-ssh` target

## Environment Variables for Testing

```bash
# For flexible test configuration
export MUP_SSH_TEST_HOST="127.0.0.1"
export MUP_SSH_TEST_PORT="22"
export MUP_SSH_TEST_USER="$USER"
export MUP_SSH_TEST_KEY="$HOME/.ssh/id_ed25519_mup_test"
export MUP_SSH_TEST_WORKDIR="/tmp/mup-ssh-test"
```

## Special Considerations for SSHExecutor

### 1. Port Checking
Port availability checks need special handling over SSH. You can't bind directly, so use commands like:
```bash
# Linux
lsof -i :PORT || netstat -tuln | grep :PORT

# macOS
lsof -i :PORT || netstat -an | grep LISTEN | grep :PORT
```

The SSHExecutor should parse command output to determine if a port is in use.

### 2. Process Management
PID tracking is trickier over SSH. Consider:
- Storing PIDs in files (e.g., `/var/run/mongod-30000.pid`)
- Using process name matching with `pgrep`
- Tracking PIDs in mup's metadata

Example:
```go
func (e *SSHExecutor) Background(command string) (int, error) {
    // Run command in background and capture PID
    cmd := fmt.Sprintf("nohup %s > /dev/null 2>&1 & echo $!", command)
    output, err := e.Execute(cmd)
    if err != nil {
        return 0, err
    }

    pid, err := strconv.Atoi(strings.TrimSpace(output))
    return pid, err
}
```

### 3. Background Processes
Need to use `nohup` or `disown` to keep processes running after SSH session closes:

```bash
# Option 1: nohup
nohup mongod --config /path/to/config > /dev/null 2>&1 &

# Option 2: disown
mongod --config /path/to/config &
disown

# Option 3: screen/tmux
screen -dmS mongod-session mongod --config /path/to/config
```

### 4. File Permissions
Ensure uploaded files have correct ownership and permissions:

```go
func (e *SSHExecutor) UploadContent(content []byte, remotePath string) error {
    // Upload file
    // ...

    // Set permissions
    _, err := e.Execute(fmt.Sprintf("chmod 644 %s", remotePath))
    return err
}
```

### 5. Path Differences
- **SSH executor**: Paths are on remote system (e.g., `/home/user/mup/data`)
- **Local executor**: Paths are local (e.g., `~/.mup/playground/data`)

Ensure your code handles both scenarios correctly.

### 6. Connection Pooling
For efficiency, consider SSH connection pooling:
- Keep SSH connection alive between operations
- Use SSH multiplexing (`ControlMaster`)
- Implement connection retry logic
- Handle connection timeouts gracefully

### 7. Error Handling
SSH adds another layer of potential failures:
- Network issues (timeouts, dropped connections)
- Authentication failures
- Permission denied errors
- Remote command not found
- Disk space issues on remote host

Implement robust error handling and retry logic.

## Example SSHExecutor Skeleton

```go
// pkg/executor/ssh.go
package executor

import (
    "golang.org/x/crypto/ssh"
)

type SSHConfig struct {
    Host     string
    Port     int
    User     string
    Password string
    KeyFile  string
    WorkDir  string
}

type SSHExecutor struct {
    config SSHConfig
    client *ssh.Client
}

func NewSSHExecutor(config SSHConfig) (*SSHExecutor, error) {
    // Create SSH client
    // Connect to remote host
    // Return executor
}

func (e *SSHExecutor) Execute(command string) (string, error) {
    // Create session
    // Run command
    // Return output
}

func (e *SSHExecutor) Background(command string) (int, error) {
    // Run command with nohup
    // Capture PID
    // Return PID
}

// ... implement all other Executor interface methods
```

## Testing Best Practices

1. **Cleanup**: Always clean up test artifacts (files, directories, processes)
2. **Isolation**: Use unique paths/ports for each test to avoid conflicts
3. **Timeouts**: Set reasonable timeouts for SSH operations
4. **Parallel Tests**: Use `t.Parallel()` where appropriate
5. **Skip Logic**: Allow skipping SSH tests if SSH not available
6. **Table-Driven**: Use table-driven tests for similar test cases
7. **Assertions**: Use `testify/assert` for clear test assertions
8. **Coverage**: Aim for >80% code coverage on executor implementations

## Next Steps

1. Start with **Approach 1** (SSH to localhost) for quickest feedback
2. Implement basic SSHExecutor with core methods (Execute, CreateDirectory, UploadContent)
3. Write integration tests that work for both Local and SSH executors
4. Add Docker-based tests for CI/CD
5. Document any platform-specific quirks or workarounds
6. Update CLAUDE.md with SSH testing instructions
