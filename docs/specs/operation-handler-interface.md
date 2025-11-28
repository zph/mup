# Operation Handler Interface Specification

## Overview

This document defines the four-phase operation handler interface used by the Plan Execution System. All operation handlers implement this interface to enable safe, resumable, and verifiable execution.

**Related Documents:**
- `docs/specs/plan-execution-system-requirements.md` - EARS requirements
- `docs/specs/plan-execution-critical-analysis.md` - Design rationale

---

## Interface Definition

```go
// pkg/operation/handler.go

package operation

import (
    "context"
    "github.com/zph/mup/pkg/executor"
    "github.com/zph/mup/pkg/plan"
)

// OperationHandler defines the interface all operation handlers must implement
// REQ-PES-036, REQ-PES-047, REQ-PES-048
type OperationHandler interface {
    // IsComplete checks if the operation was already completed.
    // Used during resume to skip already-completed operations.
    // Returns (true, nil) if operation is complete and can be skipped.
    // Returns (false, nil) if operation needs to be executed.
    IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error)

    // PreHook performs pre-execution tasks:
    // 1. Validate preconditions (config files exist, ports available, etc.)
    // 2. Invoke user-defined pre-operation hooks
    // 3. Prepare state for execution
    // Returns HookResult with Valid=false to abort operation.
    PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error)

    // Execute performs the actual operation.
    // MUST be idempotent - executing twice produces same result as executing once.
    // Returns OperationResult with Success=true on completion.
    Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*OperationResult, error)

    // PostHook performs post-execution tasks:
    // 1. Verify operation actually succeeded (health checks, status queries)
    // 2. Invoke user-defined post-operation hooks
    // 3. Update state or metadata
    // Returns HookResult with Valid=false if verification failed.
    PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error)
}
```

---

## Supporting Types

### HookResult

```go
// HookResult contains the outcome of PreHook or PostHook execution
type HookResult struct {
    // Valid indicates whether the hook passed all checks
    // PreHook: If false, operation will not execute
    // PostHook: If false, operation is marked as failed
    Valid bool

    // Warnings are non-blocking issues that don't prevent execution
    // Example: "port already in use (may be from previous run)"
    Warnings []string

    // Errors are blocking issues that prevent execution (when Valid=false)
    // Example: "config file not found: /path/to/config"
    Errors []string

    // StateChanges describes divergence between expected and actual state
    // Used to inform user when world state has changed since plan generation
    StateChanges []StateChange

    // Metadata contains arbitrary data from hook execution
    // Example: verification attempt count, discovered values, etc.
    Metadata map[string]interface{}
}

// StateChange represents a divergence between planned and actual state
type StateChange struct {
    Resource string      // Resource identifier (e.g., "port:19101", "file:/path")
    Expected interface{} // Expected value from plan
    Actual   interface{} // Actual value discovered
    Impact   string      // Description of impact (e.g., "operation may fail")
}
```

### OperationResult

```go
// OperationResult contains the outcome of Execute
type OperationResult struct {
    // Success indicates whether operation completed successfully
    Success bool

    // Message is human-readable description of result
    Message string

    // Changes describes what was modified
    Changes map[string]interface{}

    // Metadata contains additional information about execution
    Metadata map[string]interface{}
}
```

---

## Execution Flow

```go
// Applier.ExecuteOperation orchestrates the four-phase execution
func (a *Applier) ExecuteOperation(ctx context.Context, op *plan.PlannedOperation) error {
    handler := a.getHandler(op.Type)

    // ========== Phase 1: IsComplete ==========
    // Check if operation already completed (resume scenario)
    completed, err := handler.IsComplete(ctx, op, a.executor)
    if err != nil {
        return fmt.Errorf("completion check failed: %w", err)
    }
    if completed {
        log.Infof("[%s] Already completed, skipping", op.ID)
        a.recordSkipped(op)
        return nil
    }

    // ========== Phase 2: PreHook ==========
    // Validate preconditions and run user hooks
    preResult, err := handler.PreHook(ctx, op, a.executor)
    if err != nil {
        return fmt.Errorf("pre-hook failed: %w", err)
    }

    // Log warnings
    for _, warning := range preResult.Warnings {
        log.Warnf("[%s] Pre-hook warning: %s", op.ID, warning)
    }

    // Abort if validation failed
    if !preResult.Valid {
        log.Errorf("[%s] Pre-hook validation failed:", op.ID)
        for _, errMsg := range preResult.Errors {
            log.Errorf("  - %s", errMsg)
        }

        // Show state divergence if any
        if len(preResult.StateChanges) > 0 {
            log.Warnf("[%s] State diverged from plan:", op.ID)
            for _, change := range preResult.StateChanges {
                log.Warnf("  - %s: expected=%v, actual=%v (%s)",
                    change.Resource, change.Expected, change.Actual, change.Impact)
            }
        }

        return fmt.Errorf("pre-hook validation failed: %v", preResult.Errors)
    }

    // ========== Phase 3: Execute ==========
    // Perform the actual operation
    log.Infof("[%s] Executing...", op.ID)
    result, err := handler.Execute(ctx, op, a.executor)
    if err != nil {
        return fmt.Errorf("execution failed: %w", err)
    }
    if !result.Success {
        return fmt.Errorf("execution reported failure: %s", result.Message)
    }

    log.Infof("[%s] Execution completed: %s", op.ID, result.Message)

    // ========== Phase 4: PostHook ==========
    // Verify success and run user hooks
    postResult, err := handler.PostHook(ctx, op, a.executor)
    if err != nil {
        return fmt.Errorf("post-hook failed: %w", err)
    }

    // Log warnings
    for _, warning := range postResult.Warnings {
        log.Warnf("[%s] Post-hook warning: %s", op.ID, warning)
    }

    // Fail if verification failed
    if !postResult.Valid {
        log.Errorf("[%s] Post-hook verification failed:", op.ID)
        for _, errMsg := range postResult.Errors {
            log.Errorf("  - %s", errMsg)
        }
        return fmt.Errorf("post-hook verification failed: %v", postResult.Errors)
    }

    log.Infof("[%s] Verification completed", op.ID)
    a.recordCompleted(op, result)
    return nil
}
```

---

## Implementation Examples

### Example 1: StartSupervisorHandler

```go
type StartSupervisorHandler struct{}

// IsComplete: Check if supervisord is already running
func (h *StartSupervisorHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
    var params StartSupervisorParams
    if err := json.Unmarshal(op.Params, &params); err != nil {
        return false, fmt.Errorf("unmarshal params: %w", err)
    }

    httpPort := supervisor.GetSupervisorHTTPPortForDir(params.ClusterDir)

    // Ping supervisor HTTP endpoint
    client := &http.Client{Timeout: 2 * time.Second}
    resp, err := client.Get(fmt.Sprintf("http://localhost:%d/", httpPort))
    if err == nil && resp.StatusCode == 200 {
        return true, nil // Already running
    }

    return false, nil
}

// PreHook: Validate config exists, binary available, port not blocked
func (h *StartSupervisorHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
    var params StartSupervisorParams
    json.Unmarshal(op.Params, &params)

    result := &HookResult{Valid: true}

    // Check config file
    configPath := filepath.Join(params.ClusterDir, "supervisor.ini")
    exists, _ := exec.FileExists(configPath)
    if !exists {
        result.Valid = false
        result.Errors = append(result.Errors, fmt.Sprintf("config not found: %s", configPath))
    }

    // Check binary
    binaryPath, err := supervisor.GetSupervisordBinary(cacheDir)
    if err != nil {
        result.Valid = false
        result.Errors = append(result.Errors, "supervisord binary not found")
    }

    // Check port (warning only, not error)
    httpPort := supervisor.GetSupervisorHTTPPortForDir(params.ClusterDir)
    available, _ := exec.CheckPortAvailable(httpPort)
    if !available {
        result.Warnings = append(result.Warnings,
            fmt.Sprintf("port %d in use (may be running)", httpPort))
    }

    return result, nil
}

// Execute: Start supervisord daemon
func (h *StartSupervisorHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*OperationResult, error) {
    var params StartSupervisorParams
    json.Unmarshal(op.Params, &params)

    binaryPath, _ := supervisor.GetSupervisordBinary(cacheDir)
    configPath := filepath.Join(params.ClusterDir, "supervisor.ini")
    httpPort := supervisor.GetSupervisorHTTPPortForDir(params.ClusterDir)

    command := fmt.Sprintf("%s -c %s", binaryPath, configPath)
    output, err := exec.Execute(command)
    if err != nil {
        return nil, fmt.Errorf("start supervisord: %w", err)
    }

    return &OperationResult{
        Success: true,
        Message: fmt.Sprintf("Started supervisord on port %d", httpPort),
        Changes: map[string]interface{}{
            "port":   httpPort,
            "config": configPath,
        },
    }, nil
}

// PostHook: Verify supervisord is responding
func (h *StartSupervisorHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
    var params StartSupervisorParams
    json.Unmarshal(op.Params, &params)

    result := &HookResult{Valid: true}
    httpPort := supervisor.GetSupervisorHTTPPortForDir(params.ClusterDir)

    // Retry verification up to 5 times
    maxAttempts := 5
    for attempt := 1; attempt <= maxAttempts; attempt++ {
        client := &http.Client{Timeout: 2 * time.Second}
        resp, err := client.Get(fmt.Sprintf("http://localhost:%d/", httpPort))
        if err == nil && resp.StatusCode == 200 {
            result.Metadata = map[string]interface{}{
                "port":     httpPort,
                "attempts": attempt,
            }
            return result, nil
        }

        if attempt < maxAttempts {
            time.Sleep(time.Duration(attempt) * time.Second)
        }
    }

    result.Valid = false
    result.Errors = append(result.Errors,
        fmt.Sprintf("supervisord not responding after %d attempts", maxAttempts))

    return result, nil
}
```

### Example 2: StartProcessHandler (via supervisorctl)

```go
type StartProcessHandler struct{}

// IsComplete: Check if process is running via supervisor
func (h *StartProcessHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
    var params StartProcessParams
    json.Unmarshal(op.Params, &params)

    // Query supervisor for process status
    command := fmt.Sprintf("%s ctl -s http://localhost:%d status %s",
        supervisordPath, params.SupervisorPort, params.ProgramName)

    output, err := exec.Execute(command)
    if err == nil && strings.Contains(output, "RUNNING") {
        return true, nil
    }

    return false, nil
}

// PreHook: Validate supervisor is running, program is configured
func (h *StartProcessHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
    var params StartProcessParams
    json.Unmarshal(op.Params, &params)

    result := &HookResult{Valid: true}

    // Check supervisord is reachable
    client := &http.Client{Timeout: 2 * time.Second}
    resp, err := client.Get(fmt.Sprintf("http://localhost:%d/", params.SupervisorPort))
    if err != nil || resp.StatusCode != 200 {
        result.Valid = false
        result.Errors = append(result.Errors, "supervisord not running")
    }

    // Check program is configured
    command := fmt.Sprintf("%s ctl -s http://localhost:%d avail",
        supervisordPath, params.SupervisorPort)
    output, _ := exec.Execute(command)
    if !strings.Contains(output, params.ProgramName) {
        result.Valid = false
        result.Errors = append(result.Errors,
            fmt.Sprintf("program %s not configured", params.ProgramName))
    }

    return result, nil
}

// Execute: Start process via supervisorctl
func (h *StartProcessHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*OperationResult, error) {
    var params StartProcessParams
    json.Unmarshal(op.Params, &params)

    command := fmt.Sprintf("%s ctl -c %s -s http://localhost:%d start %s",
        supervisordPath, params.SupervisorConfig, params.SupervisorPort, params.ProgramName)

    output, err := exec.Execute(command)
    if err != nil {
        return nil, fmt.Errorf("start process: %w", err)
    }

    return &OperationResult{
        Success: true,
        Message: fmt.Sprintf("Started %s", params.ProgramName),
        Changes: map[string]interface{}{
            "program": params.ProgramName,
            "output":  output,
        },
    }, nil
}

// PostHook: Verify process is actually running
func (h *StartProcessHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
    var params StartProcessParams
    json.Unmarshal(op.Params, &params)

    result := &HookResult{Valid: true}

    // Verify running status
    command := fmt.Sprintf("%s ctl -s http://localhost:%d status %s",
        supervisordPath, params.SupervisorPort, params.ProgramName)

    output, err := exec.Execute(command)
    if err != nil || !strings.Contains(output, "RUNNING") {
        result.Valid = false
        result.Errors = append(result.Errors,
            fmt.Sprintf("process %s not running", params.ProgramName))
    }

    return result, nil
}
```

### Example 3: InitReplicaSetHandler (MongoDB operation)

```go
type InitReplicaSetHandler struct{}

// IsComplete: Check if replica set is already initialized
func (h *InitReplicaSetHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
    var params InitReplicaSetParams
    json.Unmarshal(op.Params, &params)

    // Create MongoDB client
    client, err := operation.NewMongoDBClient(params.Host, exec)
    if err != nil {
        return false, nil // Can't connect, not initialized
    }
    defer client.Close(ctx)

    // Check replSetGetStatus
    result, err := client.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}})
    if err != nil {
        return false, nil // Not initialized
    }

    // If we got a response, replica set is configured
    if ok, _ := result["ok"].(int); ok == 1 {
        return true, nil
    }

    return false, nil
}

// PreHook: Validate MongoDB is reachable, not already in a replica set
func (h *InitReplicaSetHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
    var params InitReplicaSetParams
    json.Unmarshal(op.Params, &params)

    result := &HookResult{Valid: true}

    // Check MongoDB is running
    client, err := operation.NewMongoDBClient(params.Host, exec)
    if err != nil {
        result.Valid = false
        result.Errors = append(result.Errors, fmt.Sprintf("cannot connect to %s", params.Host))
        return result, nil
    }
    defer client.Close(ctx)

    // Safety check: Ensure not already in different replica set
    rsResult, _ := client.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}})
    if ok, _ := rsResult["ok"].(int); ok == 1 {
        // Already in a replica set
        if setName, _ := rsResult["set"].(string); setName != params.ReplicaSetName {
            result.Valid = false
            result.Errors = append(result.Errors,
                fmt.Sprintf("already in different replica set: %s", setName))
        }
    }

    return result, nil
}

// Execute: Run replSetInitiate
func (h *InitReplicaSetHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*OperationResult, error) {
    var params InitReplicaSetParams
    json.Unmarshal(op.Params, &params)

    client, err := operation.NewMongoDBClient(params.Host, exec)
    if err != nil {
        return nil, fmt.Errorf("connect: %w", err)
    }
    defer client.Close(ctx)

    // Build replica set config
    config := bson.D{
        {Key: "_id", Value: params.ReplicaSetName},
        {Key: "members", Value: bson.A{
            bson.D{
                {Key: "_id", Value: 0},
                {Key: "host", Value: params.Host},
            },
        }},
    }

    // Execute replSetInitiate
    result, err := client.RunCommand(ctx, bson.D{
        {Key: "replSetInitiate", Value: config},
    })
    if err != nil {
        return nil, fmt.Errorf("replSetInitiate: %w", err)
    }

    return &OperationResult{
        Success: true,
        Message: fmt.Sprintf("Initialized replica set %s", params.ReplicaSetName),
        Changes: map[string]interface{}{
            "replica_set": params.ReplicaSetName,
            "response":    result,
        },
    }, nil
}

// PostHook: Verify PRIMARY is elected
func (h *InitReplicaSetHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
    var params InitReplicaSetParams
    json.Unmarshal(op.Params, &params)

    result := &HookResult{Valid: true}

    client, err := operation.NewMongoDBClient(params.Host, exec)
    if err != nil {
        result.Valid = false
        result.Errors = append(result.Errors, "cannot connect after initialization")
        return result, nil
    }
    defer client.Close(ctx)

    // Wait for PRIMARY election (up to 30 seconds)
    maxAttempts := 30
    for attempt := 1; attempt <= maxAttempts; attempt++ {
        rsResult, err := client.RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}})
        if err == nil {
            members, _ := rsResult["members"].(bson.A)
            for _, m := range members {
                member := m.(bson.D).Map()
                if stateStr, _ := member["stateStr"].(string); stateStr == "PRIMARY" {
                    result.Metadata = map[string]interface{}{
                        "primary_elected": true,
                        "attempts":        attempt,
                    }
                    return result, nil
                }
            }
        }

        if attempt < maxAttempts {
            time.Sleep(1 * time.Second)
        }
    }

    result.Valid = false
    result.Errors = append(result.Errors,
        fmt.Sprintf("PRIMARY not elected after %d seconds", maxAttempts))

    return result, nil
}
```

---

## Handler Implementation Checklist

When implementing a new operation handler:

- [ ] Define typed parameter struct (e.g., `StartProcessParams`)
- [ ] Add validation tags to parameter struct
- [ ] Implement `IsComplete()` to detect already-completed operations
- [ ] Implement `PreHook()` to validate preconditions
- [ ] Implement `Execute()` idempotently (safe to run twice)
- [ ] Implement `PostHook()` to verify success
- [ ] Add unit tests for all four phases
- [ ] Add integration tests in simulation mode
- [ ] Document handler in operation catalog
- [ ] Register handler in `pkg/operation/executor.go`

---

## Testing Pattern

```go
func TestStartSupervisorHandler(t *testing.T) {
    // Setup simulation executor
    simConfig := &simulation.Config{
        AllowRealFileReads: false,
        ExistingFiles: map[string][]byte{
            "/cluster/supervisor.ini": []byte("[supervisord]..."),
        },
        ExistingDirectories: []string{"/cluster"},
    }
    exec := simulation.NewExecutor(simConfig)

    handler := &StartSupervisorHandler{}
    op := &plan.PlannedOperation{
        Type: plan.OpStartSupervisor,
        Params: marshalParams(StartSupervisorParams{
            ClusterDir:  "/cluster",
            ClusterName: "test",
        }),
    }

    ctx := context.Background()

    // Test IsComplete (should be false initially)
    completed, err := handler.IsComplete(ctx, op, exec)
    assert.NoError(t, err)
    assert.False(t, completed)

    // Test PreHook (should pass validation)
    preResult, err := handler.PreHook(ctx, op, exec)
    assert.NoError(t, err)
    assert.True(t, preResult.Valid)

    // Test Execute
    result, err := handler.Execute(ctx, op, exec)
    assert.NoError(t, err)
    assert.True(t, result.Success)

    // Test PostHook (verification)
    postResult, err := handler.PostHook(ctx, op, exec)
    assert.NoError(t, err)
    assert.True(t, postResult.Valid)

    // Test IsComplete (should be true now)
    completed, err = handler.IsComplete(ctx, op, exec)
    assert.NoError(t, err)
    assert.True(t, completed)
}
```

---

## Benefits of Four-Phase Design

1. **Resumability**: `IsComplete()` enables safe resume from failures
2. **Safety**: `PreHook()` validates before execution, preventing errors
3. **Verification**: `PostHook()` confirms success, catching silent failures
4. **Idempotency**: Clear contract for handlers to be safe to retry
5. **User Hooks**: Natural integration points for custom logic
6. **Observability**: Each phase can be logged/monitored separately
7. **Testing**: Each phase can be tested independently
8. **State Divergence**: `PreHook()` detects when plan assumptions changed

---

## Migration Path

### Current State (pkg/operation/handlers.go)

Handlers only have `Execute()` and sometimes `Validate()`:

```go
type SomeHandler struct{}

func (h *SomeHandler) Execute(ctx, op, exec) (*Result, error) {
    // Does everything: validation, execution, verification
}
```

### Migration Steps

1. **Add interface methods** to existing handlers with default implementations:
   ```go
   func (h *SomeHandler) IsComplete(...) (bool, error) { return false, nil }
   func (h *SomeHandler) PreHook(...) (*HookResult, error) { return &HookResult{Valid: true}, nil }
   func (h *SomeHandler) PostHook(...) (*HookResult, error) { return &HookResult{Valid: true}, nil }
   ```

2. **Move validation** from `Execute()` to `PreHook()`

3. **Add completion checks** to `IsComplete()`

4. **Add verification** to `PostHook()`

5. **Update tests** to test all four phases

6. **Remove old Validate()** method (now part of PreHook)
