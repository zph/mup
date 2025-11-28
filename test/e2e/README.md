# End-to-End (E2E) Testing Framework

This directory contains end-to-end tests for the `mup` CLI tool. These tests build and execute the actual `mup` binary to verify real-world behavior.

## Overview

The E2E testing framework tests the complete CLI workflow by:
1. Building the actual `mup` binary
2. Executing it with real arguments
3. Capturing and validating stdout/stderr
4. Verifying exit codes and behavior

This approach catches issues that unit tests might miss, such as:
- Binary build problems
- Command-line argument parsing
- Flag handling and defaults
- Error message formatting
- Help text accuracy
- Integration between components

## Directory Structure

```
test/e2e/
├── README.md                 # This file
├── testutil/                 # Test utilities and helpers
│   ├── binary.go            # Binary building and execution
│   └── fixtures.go          # Fixture management
├── fixtures/                 # Test data and configuration files
│   ├── simple-replica-set.yaml
│   └── standalone.yaml
├── help_test.go             # Help command tests
├── deploy_test.go           # Deploy command tests
└── ...more test files...
```

## Running E2E Tests

### Quick Start

```bash
# Run all E2E tests
make test-e2e

# Run E2E tests with verbose output
make test-e2e-verbose

# Run specific E2E test
go test -v -tags=e2e ./test/e2e/ -run TestHelpCommand

# Run all tests (unit + integration + E2E + SSH)
make test-complete
```

### Requirements

- Go 1.21 or later
- Access to build the `mup` binary
- Sufficient disk space for test directories

### Environment Variables

E2E tests support the following environment variables:

- `MUP_STORAGE_DIR` - Custom storage directory for tests (default: temp dir)
- `PATH` - Tests prepend `test/bin/` to PATH for binary access

## Writing E2E Tests

### Basic Test Structure

```go
// +build e2e

package e2e

import (
    "testing"
    "github.com/zph/mup/test/e2e/testutil"
)

func TestYourFeature(t *testing.T) {
    // Run the mup binary
    result := testutil.RunCommand(t, "cluster", "list")

    // Assert success
    testutil.AssertSuccess(t, result)

    // Verify output
    testutil.AssertContains(t, result, "expected text")
}
```

### Test Helper Functions

#### Binary Execution

```go
// Run command and capture output
result := testutil.RunCommand(t, "cluster", "deploy", "--help")

// Run with custom environment
env := map[string]string{"MUP_STORAGE_DIR": "/tmp/test"}
result := testutil.RunCommandWithEnv(t, env, "cluster", "list")

// Run with stdin input
result := testutil.RunCommandWithInput(t, "yes\n", "cluster", "deploy", "...")
```

#### Assertions

```go
// Exit code assertions
testutil.AssertSuccess(t, result)           // Exit code 0
testutil.AssertFailure(t, result)           // Exit code != 0
testutil.AssertExitCode(t, result, 2)       // Specific exit code

// Output assertions
testutil.AssertContains(t, result, "text")      // Stdout contains
testutil.AssertNotContains(t, result, "text")   // Stdout doesn't contain
testutil.AssertStderrContains(t, result, "err") // Stderr contains

// Access output
lines := result.Lines()           // []string of stdout lines
stderrLines := result.StderrLines() // []string of stderr lines
success := result.Success()       // true if exit code 0
```

#### File Operations

```go
// Create temp directory (auto-cleanup)
tmpDir := testutil.TempDir(t)

// Write file to temp directory
path := testutil.WriteFile(t, tmpDir, "topology.yaml", content)

// Read file
content := testutil.ReadFile(t, path)

// Check file exists
exists := testutil.FileExists(t, path)

// Get fixture path
fixturePath := testutil.GetFixturePath(t, "simple-replica-set.yaml")

// Copy fixture to temp directory
path := testutil.CopyFixture(t, "simple-replica-set.yaml", tmpDir)
```

### Example: Testing Deploy Command

```go
func TestDeployWithTopology(t *testing.T) {
    // Create temp directory
    tmpDir := testutil.TempDir(t)

    // Create topology file
    topology := `global:
  user: testuser
  deploy_dir: ` + tmpDir + `/deploy

mongod_servers:
  - host: localhost
    port: 27017
`
    topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", topology)

    // Run deploy in plan-only mode
    result := testutil.RunCommand(t,
        "cluster", "deploy",
        "test-cluster",
        topologyFile,
        "--version", "7.0.0",
        "--plan-only",
    )

    // Verify success
    testutil.AssertSuccess(t, result)
    testutil.AssertContains(t, result, "test-cluster")
    testutil.AssertContains(t, result, "[PLAN-ONLY]")
}
```

### Example: Table-Driven Tests

```go
func TestCommandValidation(t *testing.T) {
    tests := []struct {
        name     string
        args     []string
        wantFail bool
        contains string
    }{
        {
            name:     "missing required flag",
            args:     []string{"cluster", "deploy", "test"},
            wantFail: true,
            contains: "required flag",
        },
        {
            name:     "invalid command",
            args:     []string{"invalid"},
            wantFail: true,
            contains: "unknown command",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := testutil.RunCommand(t, tt.args...)

            if tt.wantFail {
                testutil.AssertFailure(t, result)
            } else {
                testutil.AssertSuccess(t, result)
            }

            if tt.contains != "" {
                combined := result.Stdout + result.Stderr
                if !strings.Contains(combined, tt.contains) {
                    t.Errorf("Output missing %q", tt.contains)
                }
            }
        })
    }
}
```

## Best Practices

### 1. Use Plan-Only Mode

For most tests, use `--plan-only` to avoid actual deployments:

```go
result := testutil.RunCommand(t,
    "cluster", "deploy", "test", "topology.yaml",
    "--version", "7.0.0",
    "--plan-only",  // ← No actual deployment
)
```

### 2. Isolate Test Data

Always use temp directories and custom storage directories:

```go
tmpDir := testutil.TempDir(t)  // Auto-cleanup
env := map[string]string{
    "MUP_STORAGE_DIR": tmpDir + "/storage",
}
result := testutil.RunCommandWithEnv(t, env, ...)
```

### 3. Test Both Success and Failure Cases

```go
// Success case
result := testutil.RunCommand(t, "cluster", "list")
testutil.AssertSuccess(t, result)

// Failure case
result = testutil.RunCommand(t, "cluster", "deploy")  // Missing args
testutil.AssertFailure(t, result)
testutil.AssertStderrContains(t, result, "usage")
```

### 4. Use Descriptive Test Names

```go
func TestDeployCommandValidation(t *testing.T) { ... }          // Good
func TestDeploy(t *testing.T) { ... }                           // Too vague
func TestDeployFailsWhenTopologyFileMissing(t *testing.T) { ... } // Better
```

### 5. Add Build Tags

Always include the `e2e` build tag:

```go
// +build e2e

package e2e
```

This allows selective test execution:

```bash
go test -tags=e2e ./test/e2e/...        # Run E2E tests only
go test ./...                           # Skip E2E tests
```

## Debugging Failed Tests

### Verbose Output

Run tests with verbose mode to see all command output:

```bash
make test-e2e-verbose
```

### Inspect Test Artifacts

Temp directories are cleaned up after tests pass. To keep them for debugging:

```go
// Comment out automatic cleanup
tmpDir := testutil.TempDir(t)
// t.Cleanup(func() { os.RemoveAll(tmpDir) })  // Commented
```

### Print Command Results

```go
result := testutil.RunCommand(t, ...)
t.Logf("Exit code: %d", result.ExitCode)
t.Logf("Stdout:\n%s", result.Stdout)
t.Logf("Stderr:\n%s", result.Stderr)
t.Logf("Duration: %v", result.Duration)
```

### Run Single Test

```bash
go test -v -tags=e2e ./test/e2e/ -run TestSpecificTest
```

## Performance Considerations

### Binary Build Caching

The binary is built once and cached for all tests in the package:

```go
// First test triggers build
result1 := testutil.RunCommand(t, ...)  // Builds binary

// Subsequent tests reuse binary
result2 := testutil.RunCommand(t, ...)  // Uses cached binary
result3 := testutil.RunCommand(t, ...)  // Uses cached binary
```

### Parallel Execution

Tests can run in parallel if they use isolated storage:

```go
func TestSomething(t *testing.T) {
    t.Parallel()  // Enable parallel execution

    tmpDir := testutil.TempDir(t)  // Isolated storage
    env := map[string]string{
        "MUP_STORAGE_DIR": tmpDir,
    }
    // ... test logic ...
}
```

## CI/CD Integration

### GitHub Actions Example

```yaml
name: E2E Tests
on: [push, pull_request]

jobs:
  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      - run: make test-e2e
```

### Makefile Targets

```bash
make test-e2e          # Run E2E tests
make test-complete     # Run all tests (unit + integration + E2E)
make clean             # Clean build artifacts including test binary
```

## Troubleshooting

### "Binary build failed"

Check Go compiler errors:

```bash
go build -o test/bin/mup ./cmd/mup
```

### "Command timed out"

Increase timeout in `RunCommand` (default: 5 minutes):

```go
// Modify test/e2e/testutil/binary.go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
```

### "Test binary not found"

Ensure `make test-e2e` builds the binary to `test/bin/mup`:

```bash
ls -la test/bin/
```

## Future Enhancements

- [ ] Snapshot testing for CLI output stability
- [ ] Performance benchmarking for E2E operations
- [ ] Chaos testing with failure injection
- [ ] Visual diff reporting for output changes
- [ ] Docker-based E2E tests for SSH executors
- [ ] Automated fixture generation from running clusters

## Related Documentation

- [Testing Guide](../../docs/TESTING.md) - Overall testing strategy
- [TDD Approach](../../CLAUDE.md) - Test-driven development guidelines
- [Plan-Only Mode](../../docs/IMPLEMENTATION.md#plan-only-testing-framework) - Simulation testing

---

**Last Updated:** 2025-11-27
**Framework Version:** 1.0.0
**Maintainer:** See CLAUDE.md for development guidelines
