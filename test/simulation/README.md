# Simulation Test Scenarios

This directory (`test/simulation/`) contains YAML scenario files for testing various edge cases and failure conditions in simulation mode.

## Available Scenarios

### port-conflict.yaml
Tests handling when MongoDB ports are already in use by other processes.

**Usage:**
```bash
mup cluster deploy test topology.yaml --version 7.0 \
  --simulate --simulate-scenario test/simulation/port-conflict.yaml
```

**Expected behavior:** Deploy should detect port conflict and report error.

---

### permission-denied.yaml
Tests handling of insufficient filesystem permissions.

**Usage:**
```bash
mup cluster deploy test topology.yaml --version 7.0 \
  --simulate --simulate-scenario test/simulation/permission-denied.yaml
```

**Expected behavior:** Operations requiring elevated permissions should fail gracefully.

---

### disk-full.yaml
Tests handling of insufficient disk space.

**Usage:**
```bash
mup cluster deploy test topology.yaml --version 7.0 \
  --simulate --simulate-scenario test/simulation/disk-full.yaml
```

**Expected behavior:** File operations should fail with "no space left" errors.

---

### network-failure.yaml
Tests handling of network connectivity issues.

**Usage:**
```bash
mup cluster deploy test topology.yaml --version 7.0 \
  --simulate --simulate-scenario test/simulation/network-failure.yaml
```

**Expected behavior:** SSH operations should fail with connection errors.

---

### existing-cluster.yaml
Simulates an already-running MongoDB cluster for import testing.

**Usage:**
```bash
mup cluster import prod-cluster --auto-detect \
  --simulate --simulate-scenario test/simulation/existing-cluster.yaml
```

**Expected behavior:** Import should detect running processes and existing files.

---

## Creating Custom Scenarios

### Scenario File Format

```yaml
simulation:
  # Custom command responses
  responses:
    "command": "output"

  # Operations that should fail
  failures:
    - operation: operation_type
      target: path_or_command
      error: error_message

  # Pre-existing filesystem state
  filesystem:
    existing_files:
      - /path/to/file
    existing_directories:
      - /path/to/dir
    file_contents:
      /path/to/file: "file content"

  # Pre-existing running processes
  processes:
    running:
      - command: mongod
        port: 27017
        pid: 1234
```

### Operation Types

- `create_directory` - Directory creation
- `upload_file` - File upload
- `upload_content` - Content upload
- `download_file` - File download
- `execute` - Command execution
- `background` - Background process start

### Wildcard Targets

Use `"*"` as target to match all operations of that type:

```yaml
failures:
  - operation: upload_file
    target: "*"
    error: "disk full"
```

## Using Scenarios in Tests

```go
// Load scenario in Go tests
executor, err := simulation.NewExecutorWithScenario("test/simulation/port-conflict.yaml")
require.NoError(t, err)

// Use in test
err = someOperation(executor)
assert.Error(t, err)
assert.Contains(t, err.Error(), "address already in use")
```

## Generating Scenario Templates

```go
// Generate common scenario templates programmatically
scenario := simulation.GenerateScenarioTemplate("port-conflict")
simulation.SaveScenarioToFile(scenario, "my-scenario.yaml")
```

Available templates:
- `port-conflict`
- `permission-denied`
- `disk-full`
- `network-failure`
- `existing-cluster`
