# Plan Execution System: Critical Analysis and Design Decisions

## Overview

This document analyzes critical design challenges for the Plan/Apply system and proposes solutions. It addresses concerns not fully covered in the requirements specification.

**Author:** System Architect
**Date:** 2025-11-26
**Status:** Design Discussion
**Related:** `docs/specs/plan-execution-system-requirements.md`

---

## 1. Parameter Type Safety Problem

### The Problem

**Current State:** PlannedOperation stores `Params map[string]interface{}`

**Issue:** When unmarshaling JSON, Go's default behavior:
```go
// JSON: {"supervisor_port": 19101}
// Unmarshals to: map[string]interface{}{"supervisor_port": float64(19101)}
```

This breaks type assertions in handlers:
```go
port, ok := params["supervisor_port"].(int)  // FAILS! It's float64
```

### Solution Options

#### Option A: Custom JSON Unmarshaling with Type Hints

Add `ParamTypes` field to PlannedOperation that specifies expected types:

```go
type PlannedOperation struct {
    // ... existing fields ...
    Params map[string]interface{} `json:"params"`
    ParamTypes map[string]string `json:"param_types"` // NEW
}

// During unmarshal:
// ParamTypes: {"supervisor_port": "int", "program_name": "string"}
```

**Pros:** Minimal changes, backwards compatible
**Cons:** Duplicate type information, manual type conversion still needed

#### Option B: Typed Parameter Structs per Operation

Define operation-specific parameter types:

```go
type StartProcessParams struct {
    ProgramName     string `json:"program_name"`
    SupervisorConfig string `json:"supervisor_config"`
    SupervisorPort   int    `json:"supervisor_port"`
}

type PlannedOperation struct {
    // ... existing fields ...
    Params json.RawMessage `json:"params"` // Store raw JSON
}

// Handlers unmarshal to their specific type:
var params StartProcessParams
json.Unmarshal(op.Params, &params)
```

**Pros:** Type-safe, compile-time checking, self-documenting
**Cons:** More code, requires defining struct for each operation type

#### Option C: Custom JSON Decoder with Type Inference

Use `json.Number` for numeric values and convert based on context:

```go
decoder := json.NewDecoder(reader)
decoder.UseNumber()

// Then in handler:
portNum, ok := params["supervisor_port"].(json.Number)
port, err := portNum.Int64()
```

**Pros:** Standard library, no extra fields
**Cons:** Still requires runtime type checking, error-prone

### Recommendation: Option B (Typed Parameter Structs)

**Rationale:**
1. **Type Safety:** Compile-time verification of parameter types
2. **Self-Documenting:** Each operation's parameters are explicitly defined
3. **Validation:** Can add validation tags (`validate:"required,min=1"`)
4. **Future-Proof:** Supports parameter evolution with versioning

**Implementation:**
```go
// pkg/operation/params.go
type OperationParams interface {
    Validate() error
}

type StartSupervisorParams struct {
    ClusterDir  string `json:"cluster_dir" validate:"required"`
    ClusterName string `json:"cluster_name" validate:"required"`
}

type StartProcessParams struct {
    ProgramName      string `json:"program_name" validate:"required"`
    SupervisorConfig string `json:"supervisor_config" validate:"required,file"`
    SupervisorPort   int    `json:"supervisor_port" validate:"required,min=1,max=65535"`
}

// In handlers:
func (h *StartProcessHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
    var params StartProcessParams
    if err := json.Unmarshal(op.Params, &params); err != nil {
        return nil, fmt.Errorf("invalid parameters: %w", err)
    }
    if err := params.Validate(); err != nil {
        return nil, fmt.Errorf("parameter validation failed: %w", err)
    }

    // Use params.SupervisorPort (guaranteed to be int)
}
```

**Migration Path:**
1. Add parameter structs for new operations
2. Gradually convert existing operations
3. Keep `map[string]interface{}` as fallback for unknown types

---

## 2. Simulation Accuracy vs Reality

### The Problem

Simulation cannot predict all real-world failures:
- Network timeouts
- Disk full
- Port conflicts (if another process starts between plan and apply)
- Race conditions
- Permission changes

**Question:** How do we handle divergence between simulated plan and real execution?

### Solution: Validation as Refresh

**Approach:** Treat validation as a "refresh" step (like Terraform)

1. **Plan Generation:** Simulate with current state assumptions
2. **Apply:** Before each operation, validate preconditions
3. **Divergence Detection:** If validation fails, report what changed
4. **User Decision:** Abort or continue with warnings

**Implementation:**

```go
// REQ-PES-012 enhancement
type ValidationResult struct {
    Valid        bool
    Warnings     []string  // Non-blocking issues
    Errors       []string  // Blocking issues
    StateChanges []StateChange // What diverged
}

type StateChange struct {
    Resource string
    Expected interface{}
    Actual   interface{}
    Impact   string // "operation will be skipped" or "operation may fail"
}

// Handler validation:
func (h *StartProcessHandler) Validate(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*ValidationResult, error) {
    result := &ValidationResult{Valid: true}

    // Check if supervisord is already running
    isRunning, _ := exec.IsProcessRunning(/* supervisord PID */)
    if isRunning {
        result.Warnings = append(result.Warnings, "supervisord already running (will use existing)")
        // Still valid, just note the divergence
    }

    // Check if port is available
    available, _ := exec.CheckPortAvailable(supervisorPort)
    if !available {
        result.Valid = false
        result.Errors = append(result.Errors, fmt.Sprintf("port %d already in use", supervisorPort))
        result.StateChanges = append(result.StateChanges, StateChange{
            Resource: fmt.Sprintf("port:%d", supervisorPort),
            Expected: "available",
            Actual:   "in use",
            Impact:   "start_supervisor will fail",
        })
    }

    return result, nil
}
```

**User Experience:**
```
Applying plan 4c243baf...

[001/050] create_directory /Users/zph/.mup/storage/clusters/test-rs/v7.0
  ✓ Validated
  ✓ Executed

[027/050] start_supervisor (port: 19101)
  ⚠ Warning: port 19101 already in use (expected: available)
  ✗ Validation failed

Plan diverged from reality. State changes detected:
  - port:19101: available → in use (start_supervisor will fail)

Abort apply? [Y/n]
```

### Recommendation: Implement Rich Validation Results

**Requirements Update:**
- REQ-PES-012: Validation returns structured result with warnings/errors
- Add REQ-PES-034: When validation warnings occur, prompt user to continue or abort
- Add REQ-PES-035: When validation errors occur, abort with state divergence report

---

## 3. Idempotency and Resume Semantics

### The Problem

**Scenario:** Operation 27 (start_supervisor) partially completes:
1. Supervisord process starts
2. Network timeout before we receive confirmation
3. Handler returns error
4. Checkpoint records operation as failed

**On Resume:** Should we:
- A) Re-run Execute() and hope it's idempotent?
- B) Re-run Validate() first, skip if already done?
- C) Ask user what to do?

### Solution: Four-Phase Operation Execution

```go
type OperationHandler interface {
    // Check if operation was already completed (for resume)
    IsComplete(ctx context.Context, op *PlannedOperation, exec Executor) (bool, error)

    // Pre-execution hook: validate preconditions, run user hooks, prepare state
    PreHook(ctx context.Context, op *PlannedOperation, exec Executor) (*HookResult, error)

    // Execute the operation (must be idempotent)
    Execute(ctx context.Context, op *PlannedOperation, exec Executor) (*OperationResult, error)

    // Post-execution hook: verify success, run user hooks, update state
    PostHook(ctx context.Context, op *PlannedOperation, exec Executor) (*HookResult, error)
}

type HookResult struct {
    Valid        bool
    Warnings     []string
    Errors       []string
    StateChanges []StateChange
    Metadata     map[string]interface{}
}
```

**Operation Execution Logic:**
```go
func (a *Applier) ExecuteOperation(ctx context.Context, op *PlannedOperation) error {
    handler := a.getHandler(op.Type)

    // Phase 1: Check if already completed (for resume scenarios)
    completed, err := handler.IsComplete(ctx, op, a.executor)
    if err != nil {
        return fmt.Errorf("completion check failed: %w", err)
    }
    if completed {
        log.Info("Operation already completed, skipping")
        return nil
    }

    // Phase 2: Pre-execution hook (validation + user hooks + preparation)
    preResult, err := handler.PreHook(ctx, op, a.executor)
    if err != nil {
        return fmt.Errorf("pre-hook failed: %w", err)
    }
    if !preResult.Valid {
        return fmt.Errorf("pre-hook validation failed: %v", preResult.Errors)
    }
    if len(preResult.Warnings) > 0 {
        for _, warning := range preResult.Warnings {
            log.Warnf("Pre-hook warning: %s", warning)
        }
    }

    // Phase 3: Execute the operation
    result, err := handler.Execute(ctx, op, a.executor)
    if err != nil {
        return fmt.Errorf("execution failed: %w", err)
    }

    // Phase 4: Post-execution hook (verification + user hooks + cleanup)
    postResult, err := handler.PostHook(ctx, op, a.executor)
    if err != nil {
        return fmt.Errorf("post-hook failed: %w", err)
    }
    if !postResult.Valid {
        return fmt.Errorf("post-hook verification failed: %v", postResult.Errors)
    }
    if len(postResult.Warnings) > 0 {
        for _, warning := range postResult.Warnings {
            log.Warnf("Post-hook warning: %s", warning)
        }
    }

    return nil
}
```

**Handler Implementation Example:**
```go
type StartSupervisorHandler struct{}

// REQ-PES-036: Check if operation already completed
func (h *StartSupervisorHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
    var params StartSupervisorParams
    if err := json.Unmarshal(op.Params, &params); err != nil {
        return false, fmt.Errorf("unmarshal params: %w", err)
    }

    // Check if supervisord is already running on expected port
    httpPort := supervisor.GetSupervisorHTTPPortForDir(params.ClusterDir)

    // Try HTTP ping to supervisor's status endpoint
    client := &http.Client{Timeout: 2 * time.Second}
    resp, err := client.Get(fmt.Sprintf("http://localhost:%d/", httpPort))
    if err == nil && resp.StatusCode == 200 {
        log.Debugf("Supervisord already running on port %d", httpPort)
        return true, nil // Already running
    }

    return false, nil // Not running
}

// REQ-PES-034: Pre-execution validation and user hook invocation
func (h *StartSupervisorHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
    var params StartSupervisorParams
    if err := json.Unmarshal(op.Params, &params); err != nil {
        return nil, fmt.Errorf("unmarshal params: %w", err)
    }

    result := &HookResult{Valid: true}

    // 1. Validate preconditions
    configPath := filepath.Join(params.ClusterDir, "supervisor.ini")
    exists, _ := exec.FileExists(configPath)
    if !exists {
        result.Valid = false
        result.Errors = append(result.Errors,
            fmt.Sprintf("supervisor config not found: %s", configPath))
    }

    // Check if supervisord binary exists
    homeDir, _ := os.UserHomeDir()
    cacheDir := filepath.Join(homeDir, ".mup", "storage", "bin")
    binaryPath, err := supervisor.GetSupervisordBinary(cacheDir)
    if err != nil {
        result.Valid = false
        result.Errors = append(result.Errors,
            fmt.Sprintf("supervisord binary not found: %v", err))
    }

    // Check port availability
    httpPort := supervisor.GetSupervisorHTTPPortForDir(params.ClusterDir)
    available, _ := exec.CheckPortAvailable(httpPort)
    if !available {
        result.Warnings = append(result.Warnings,
            fmt.Sprintf("port %d already in use (supervisord may be running)", httpPort))
        // Not an error - might be from previous run
    }

    // 2. Call user-defined pre-operation hooks
    // (hooks system integration would go here)

    return result, nil
}

// REQ-PES-013: Execute the operation idempotently
func (h *StartSupervisorHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*OperationResult, error) {
    var params StartSupervisorParams
    if err := json.Unmarshal(op.Params, &params); err != nil {
        return nil, fmt.Errorf("unmarshal params: %w", err)
    }

    homeDir, _ := os.UserHomeDir()
    cacheDir := filepath.Join(homeDir, ".mup", "storage", "bin")
    binaryPath, err := supervisor.GetSupervisordBinary(cacheDir)
    if err != nil {
        return nil, fmt.Errorf("get supervisord binary: %w", err)
    }

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
            "port":    httpPort,
            "config":  configPath,
            "output":  output,
        },
    }, nil
}

// REQ-PES-035: Post-execution verification and user hook invocation
func (h *StartSupervisorHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
    var params StartSupervisorParams
    if err := json.Unmarshal(op.Params, &params); err != nil {
        return nil, fmt.Errorf("unmarshal params: %w", err)
    }

    result := &HookResult{Valid: true}
    httpPort := supervisor.GetSupervisorHTTPPortForDir(params.ClusterDir)

    // 1. Verify supervisord is actually running
    maxAttempts := 5
    for attempt := 1; attempt <= maxAttempts; attempt++ {
        client := &http.Client{Timeout: 2 * time.Second}
        resp, err := client.Get(fmt.Sprintf("http://localhost:%d/", httpPort))
        if err == nil && resp.StatusCode == 200 {
            log.Debugf("Supervisord verified running on port %d", httpPort)
            result.Metadata = map[string]interface{}{
                "port":             httpPort,
                "verification_attempts": attempt,
            }
            break
        }

        if attempt == maxAttempts {
            result.Valid = false
            result.Errors = append(result.Errors,
                fmt.Sprintf("supervisord not responding after %d attempts", maxAttempts))
        } else {
            time.Sleep(time.Duration(attempt) * time.Second)
        }
    }

    // 2. Call user-defined post-operation hooks
    // (hooks system integration would go here)

    return result, nil
}
```

### Recommendation: Implement Four-Phase Handler Interface

**Requirements Update:**
- Add REQ-PES-036: Handlers MUST implement IsComplete() to detect completed operations
- Add REQ-PES-047: Handlers MUST implement PreHook() for validation and pre-execution tasks
- Add REQ-PES-048: Handlers MUST implement PostHook() for verification and post-execution tasks
- Update REQ-PES-016: Resume logic calls IsComplete() before re-executing

**Interface Definition:**
```go
// pkg/operation/handler.go
type OperationHandler interface {
    // REQ-PES-036: Check if operation was already completed (for resume)
    IsComplete(ctx context.Context, op *PlannedOperation, exec Executor) (bool, error)

    // REQ-PES-047: Pre-execution: validate preconditions, run user hooks, prepare state
    PreHook(ctx context.Context, op *PlannedOperation, exec Executor) (*HookResult, error)

    // REQ-PES-013: Execute the operation (must be idempotent)
    Execute(ctx context.Context, op *PlannedOperation, exec Executor) (*OperationResult, error)

    // REQ-PES-048: Post-execution: verify success, run user hooks, update state
    PostHook(ctx context.Context, op *PlannedOperation, exec Executor) (*HookResult, error)
}
```

**Benefits:**
1. **IsComplete**: Enables safe resume by detecting partial completions
2. **PreHook**: Unifies validation, user hooks, and preparation in single phase
3. **PostHook**: Ensures operations actually succeeded (not just exited without error)
4. **Clear Separation**: Each phase has single responsibility

---

## 4. Dependency Resolution

### Current State

PlannedOperation has `Dependencies []string` (operation IDs), but dependency resolution algorithm is not specified.

### Questions

1. **Explicit vs Implicit:** Should dependencies be explicit in plan, or inferred from operation order?
2. **DAG Validation:** How do we detect circular dependencies?
3. **Parallel Execution:** Can independent operations run in parallel?
4. **Dynamic Dependencies:** What if an operation creates a dependency at runtime?

### Solution: Hybrid Approach

**Plan Format:**
```json
{
  "id": "deploy-027",
  "type": "start_supervisor",
  "dependencies": ["deploy-026"],  // Explicit: Must complete before this
  "phase": "deploy"
}
```

**Resolution Algorithm:**

1. **Sequential Within Phase:** Operations in same phase execute in order unless independent
2. **Explicit Dependencies:** Must complete before dependent operation starts
3. **Parallel When Possible:** Independent operations can run concurrently
4. **Phase Boundaries:** All operations in phase N must complete before phase N+1 starts

**Implementation:**
```go
type DependencyGraph struct {
    nodes map[string]*PlannedOperation
    edges map[string][]string // operation ID -> dependencies
}

func (g *DependencyGraph) TopologicalSort() ([]string, error) {
    // Kahn's algorithm for topological sort
    // Returns operation IDs in execution order
    // Returns error if circular dependency detected
}

func (g *DependencyGraph) GetExecutableBatch() []string {
    // Returns all operations with no pending dependencies
    // Enables parallel execution
}
```

**Example Execution:**
```
Phase: prepare
  Batch 1 (parallel):
    - create_directory /Users/zph/.mup/storage/clusters/test-rs
    - create_directory /Users/zph/.mup/storage/packages
  Batch 2 (parallel):
    - create_directory /Users/zph/.mup/storage/clusters/test-rs/v7.0
    - download_binary mongod:7.0.0
  Batch 3:
    - create_directory /Users/zph/.mup/storage/clusters/test-rs/v7.0/mongod-30000

Phase: deploy
  Batch 1:
    - start_supervisor (depends on all prepare operations)
  Batch 2 (parallel):
    - start_process mongod-30000
    - start_process mongod-30001
    - start_process mongod-30002
```

### Recommendation: Sequential for MVP, Parallel for v2

**MVP (v0.1):**
- Sequential execution within phase
- Simple dependency checking (ensure dependencies completed)
- Tags: REQ-PES-011

**Future (v0.2):**
- Parallel execution of independent operations
- DAG validation
- Batch execution
- Add REQ-PES-037: Support parallel execution of independent operations

---

## 5. Plan File Versioning and Compatibility

### The Problem

Plan files may become incompatible across mup versions:
- New operation types added
- Operation parameter schema changes
- Handler behavior changes

**Example:**
```
# v0.1.0: start_process uses direct binary paths
{
  "type": "start_process",
  "params": {
    "binary": "/path/to/mongod",
    "config": "/path/to/mongod.conf"
  }
}

# v0.2.0: start_process uses supervisorctl
{
  "type": "start_process",
  "params": {
    "program_name": "mongod-30000",
    "supervisor_config": "/path/to/supervisor.ini",
    "supervisor_port": 19101
  }
}
```

Plans created with v0.1.0 cannot be applied with v0.2.0.

### Solution: Plan Format Versioning

**Add `plan_version` to metadata:**

```json
{
  "metadata": {
    "plan_version": "1.0",  // NEW: Plan format version
    "mup_version": "0.2.0", // Binary version
    // ...
  }
}
```

**Version Compatibility Matrix:**

| mup Version | plan_version | Backwards Compatible |
|-------------|--------------|---------------------|
| 0.1.0       | 1.0          | -                   |
| 0.2.0       | 1.0, 1.1     | Can apply 1.0 plans |
| 0.3.0       | 1.1, 2.0     | Cannot apply 1.0    |

**Implementation:**
```go
const CurrentPlanVersion = "1.0"

var SupportedPlanVersions = []string{"1.0"}

func (a *Applier) LoadPlan(path string) (*Plan, error) {
    var plan Plan
    // ... load JSON ...

    if !isSupportedVersion(plan.Metadata.PlanVersion) {
        return nil, fmt.Errorf(
            "plan version %s not supported by mup %s (supported: %v)",
            plan.Metadata.PlanVersion,
            version.Current,
            SupportedPlanVersions,
        )
    }

    // Apply migrations if needed
    if plan.Metadata.PlanVersion != CurrentPlanVersion {
        plan = migratePlan(plan, CurrentPlanVersion)
    }

    return &plan, nil
}
```

### Recommendation: Implement Plan Versioning from Start

**Requirements Update:**
- Update REQ-PES-004: Include `plan_version` in metadata
- Update REQ-PES-009: Check plan_version compatibility, not mup_version
- Add REQ-PES-038: Support reading older plan versions with migration

---

## 6. Concurrency Control and Locking

### The Problem

**Scenario:**
1. User A generates plan to deploy test-rs
2. User B generates plan to upgrade test-rs
3. User A applies deploy plan
4. User B applies upgrade plan concurrently
5. **CONFLICT:** Both modify same cluster simultaneously

### Solution: Cluster Lock Files

**Lock Mechanism:**
```go
type ClusterLock struct {
    ClusterName string    `json:"cluster_name"`
    PlanID      string    `json:"plan_id"`
    Operation   string    `json:"operation"`   // deploy/upgrade/import
    LockedBy    string    `json:"locked_by"`   // user@hostname:pid
    LockedAt    time.Time `json:"locked_at"`
    ExpiresAt   time.Time `json:"expires_at"`  // Lock timeout (default: 24h)
    LockTimeout string    `json:"lock_timeout"` // Human-readable: "24h"
    LockFile    string    `json:"-"`           // ~/.mup/storage/clusters/<name>/.lock
}

const DefaultLockTimeout = 24 * time.Hour

func (a *Applier) AcquireLock(clusterName, planID, operation string) (*ClusterLock, error) {
    return a.AcquireLockWithTimeout(clusterName, planID, operation, DefaultLockTimeout)
}

func (a *Applier) AcquireLockWithTimeout(clusterName, planID, operation string, timeout time.Duration) (*ClusterLock, error) {
    lockPath := filepath.Join(GetClusterDir(clusterName), ".lock")

    // Try to create lock file exclusively
    f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
    if err != nil {
        if os.IsExist(err) {
            // Lock exists, check if stale or expired
            existingLock, err := loadLock(lockPath)
            if err != nil {
                return nil, fmt.Errorf("lock file corrupted: %w", err)
            }

            // Check if lock has expired (timeout-based staleness)
            if time.Now().After(existingLock.ExpiresAt) {
                log.Warnf("Lock expired (created at %s, expired at %s), removing",
                    existingLock.LockedAt.Format(time.RFC3339),
                    existingLock.ExpiresAt.Format(time.RFC3339))
                os.Remove(lockPath)
                return a.AcquireLockWithTimeout(clusterName, planID, operation, timeout)
            }

            // Check if process still exists (process-based staleness)
            if !isProcessAlive(existingLock.LockedBy) {
                log.Warnf("Lock process %s no longer running, removing", existingLock.LockedBy)
                os.Remove(lockPath)
                return a.AcquireLockWithTimeout(clusterName, planID, operation, timeout)
            }

            // Lock is valid and held by active process
            timeUntilExpiry := time.Until(existingLock.ExpiresAt)
            return nil, fmt.Errorf(
                "cluster %s locked by %s (plan: %s, operation: %s, expires in %s)",
                clusterName,
                existingLock.LockedBy,
                existingLock.PlanID,
                existingLock.Operation,
                timeUntilExpiry.Round(time.Minute),
            )
        }
        return nil, err
    }
    defer f.Close()

    now := time.Now()
    lock := &ClusterLock{
        ClusterName: clusterName,
        PlanID:      planID,
        Operation:   operation,
        LockedBy:    fmt.Sprintf("%s@%s:%d", currentUser(), hostname(), os.Getpid()),
        LockedAt:    now,
        ExpiresAt:   now.Add(timeout),
        LockTimeout: timeout.String(),
        LockFile:    lockPath,
    }

    if err := json.NewEncoder(f).Encode(lock); err != nil {
        return nil, fmt.Errorf("write lock file: %w", err)
    }

    log.Infof("Acquired lock for cluster %s (expires in %s)", clusterName, timeout)

    return lock, nil
}

func (l *ClusterLock) Release() error {
    return os.Remove(l.LockFile)
}

func isProcessAlive(lockedBy string) bool {
    // Parse "user@hostname:pid"
    parts := strings.Split(lockedBy, ":")
    if len(parts) != 2 {
        return false
    }

    pid, err := strconv.Atoi(parts[1])
    if err != nil {
        return false
    }

    // Check if process exists (Unix-specific)
    process, err := os.FindProcess(pid)
    if err != nil {
        return false
    }

    // Send signal 0 to check if process is alive
    err = process.Signal(syscall.Signal(0))
    return err == nil
}

func currentUser() string {
    if u, err := user.Current(); err == nil {
        return u.Username
    }
    return "unknown"
}

func hostname() string {
    if h, err := os.Hostname(); err == nil {
        return h
    }
    return "unknown"
}
```

**CLI Commands for Lock Management:**

```go
// cmd/mup/cluster_lock.go

// Show lock status
func clusterLockStatus(clusterName string) error {
    lockPath := filepath.Join(GetClusterDir(clusterName), ".lock")

    if _, err := os.Stat(lockPath); os.IsNotExist(err) {
        fmt.Printf("Cluster %s is not locked\n", clusterName)
        return nil
    }

    lock, err := loadLock(lockPath)
    if err != nil {
        return fmt.Errorf("load lock: %w", err)
    }

    // Check if expired
    if time.Now().After(lock.ExpiresAt) {
        fmt.Printf("Cluster %s has EXPIRED lock:\n", clusterName)
    } else {
        fmt.Printf("Cluster %s is locked:\n", clusterName)
    }

    fmt.Printf("  Plan ID:      %s\n", lock.PlanID)
    fmt.Printf("  Operation:    %s\n", lock.Operation)
    fmt.Printf("  Locked By:    %s\n", lock.LockedBy)
    fmt.Printf("  Locked At:    %s\n", lock.LockedAt.Format(time.RFC3339))
    fmt.Printf("  Expires At:   %s\n", lock.ExpiresAt.Format(time.RFC3339))

    timeUntilExpiry := time.Until(lock.ExpiresAt)
    if timeUntilExpiry > 0 {
        fmt.Printf("  Expires In:   %s\n", timeUntilExpiry.Round(time.Minute))
    } else {
        fmt.Printf("  Expired:      %s ago\n", (-timeUntilExpiry).Round(time.Minute))
    }

    // Check if process is alive
    if isProcessAlive(lock.LockedBy) {
        fmt.Printf("  Status:       ACTIVE (process running)\n")
    } else {
        fmt.Printf("  Status:       STALE (process not running)\n")
    }

    return nil
}

// Force remove lock
func clusterLockClear(clusterName string, force bool) error {
    lockPath := filepath.Join(GetClusterDir(clusterName), ".lock")

    if _, err := os.Stat(lockPath); os.IsNotExist(err) {
        fmt.Printf("Cluster %s is not locked\n", clusterName)
        return nil
    }

    lock, err := loadLock(lockPath)
    if err != nil {
        return fmt.Errorf("load lock: %w", err)
    }

    // Check if lock is expired or stale
    isExpired := time.Now().After(lock.ExpiresAt)
    isStale := !isProcessAlive(lock.LockedBy)

    if !force && !isExpired && !isStale {
        return fmt.Errorf(
            "lock is active (held by %s, expires in %s). Use --force to clear anyway",
            lock.LockedBy,
            time.Until(lock.ExpiresAt).Round(time.Minute),
        )
    }

    if err := os.Remove(lockPath); err != nil {
        return fmt.Errorf("remove lock: %w", err)
    }

    if force && !isExpired && !isStale {
        fmt.Printf("WARNING: Forcefully cleared ACTIVE lock held by %s\n", lock.LockedBy)
    } else if isExpired {
        fmt.Printf("Cleared expired lock (was held by %s)\n", lock.LockedBy)
    } else if isStale {
        fmt.Printf("Cleared stale lock (process %s not running)\n", lock.LockedBy)
    }

    return nil
}
```

**CLI Usage:**

```bash
# Check lock status
mup cluster lock status test-rs
# Output:
# Cluster test-rs is locked:
#   Plan ID:      4c243baf-fde1-4440-b9d6-4cd6093978be
#   Operation:    deploy
#   Locked By:    alice@workstation:12345
#   Locked At:    2025-11-26T10:30:00Z
#   Expires At:   2025-11-27T10:30:00Z
#   Expires In:   23h 45m
#   Status:       ACTIVE (process running)

# Clear expired or stale lock
mup cluster lock clear test-rs
# Output: Cleared expired lock (was held by alice@workstation:12345)

# Force clear active lock (dangerous!)
mup cluster lock clear test-rs --force
# Output: WARNING: Forcefully cleared ACTIVE lock held by alice@workstation:12345

# List all locked clusters
mup cluster lock list
# Output:
# CLUSTER    LOCKED BY              OPERATION  EXPIRES IN
# test-rs    alice@workstation      deploy     23h 45m
# prod-rs    bob@server             upgrade    2h 15m (STALE)
```

**Lock Renewal (Distributed Lease):**

```go
// RenewLock extends the lock expiration for the same plan
// This allows long-running operations to keep the lock active
func (a *Applier) RenewLock(lock *ClusterLock, extension time.Duration) error {
    lockPath := lock.LockFile

    // Read current lock
    currentLock, err := loadLock(lockPath)
    if err != nil {
        return fmt.Errorf("load lock for renewal: %w", err)
    }

    // Verify it's the same plan (only same plan can renew)
    if currentLock.PlanID != lock.PlanID {
        return fmt.Errorf(
            "cannot renew lock: plan ID mismatch (current: %s, attempting: %s)",
            currentLock.PlanID, lock.PlanID,
        )
    }

    // Extend expiration
    now := time.Now()
    currentLock.ExpiresAt = now.Add(extension)

    // Write updated lock atomically
    tmpPath := lockPath + ".tmp"
    f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
    if err != nil {
        return fmt.Errorf("create temp lock: %w", err)
    }

    if err := json.NewEncoder(f).Encode(currentLock); err != nil {
        f.Close()
        os.Remove(tmpPath)
        return fmt.Errorf("write renewed lock: %w", err)
    }
    f.Close()

    // Atomic rename
    if err := os.Rename(tmpPath, lockPath); err != nil {
        os.Remove(tmpPath)
        return fmt.Errorf("atomic rename lock: %w", err)
    }

    // Update in-memory lock
    lock.ExpiresAt = currentLock.ExpiresAt

    log.Debugf("Renewed lock for %s (new expiration: %s)",
        lock.ClusterName, lock.ExpiresAt.Format(time.RFC3339))

    return nil
}

// StartLockRenewal starts a background goroutine that periodically renews the lock
// Renewal happens every renewInterval (default: 1 hour for 24h locks)
func (a *Applier) StartLockRenewal(ctx context.Context, lock *ClusterLock, renewInterval, extension time.Duration) {
    ticker := time.NewTicker(renewInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            log.Debug("Lock renewal stopped (context cancelled)")
            return
        case <-ticker.C:
            if err := a.RenewLock(lock, extension); err != nil {
                log.Errorf("Failed to renew lock: %v", err)
                // Continue trying - don't stop renewal on error
            } else {
                log.Infof("Lock renewed for cluster %s (expires: %s)",
                    lock.ClusterName, lock.ExpiresAt.Format(time.RFC3339))
            }
        }
    }
}
```

**Apply with Locking and Renewal:**

```go
func (a *Applier) Apply(ctx context.Context, plan *Plan) error {
    // Acquire lock (24h timeout)
    lock, err := a.AcquireLock(plan.Metadata.ClusterName, plan.Metadata.PlanID, plan.Metadata.OperationType)
    if err != nil {
        return fmt.Errorf("failed to acquire cluster lock: %w", err)
    }
    defer lock.Release()

    // Start background lock renewal (renew every hour, extend by 24h)
    renewCtx, cancelRenewal := context.WithCancel(ctx)
    defer cancelRenewal()

    go a.StartLockRenewal(renewCtx, lock, 1*time.Hour, DefaultLockTimeout)

    // Apply operations...
    for _, op := range plan.Operations {
        if err := a.ExecuteOperation(ctx, &op); err != nil {
            return fmt.Errorf("operation %s failed: %w", op.ID, err)
        }
    }

    return nil
}
```

**Resume with Lock Renewal:**

```go
func (a *Applier) Resume(ctx context.Context, plan *Plan, checkpointPath string) error {
    checkpoint, err := loadCheckpoint(checkpointPath)
    if err != nil {
        return fmt.Errorf("load checkpoint: %w", err)
    }

    // Verify plan ID matches
    if checkpoint.PlanID != plan.Metadata.PlanID {
        return fmt.Errorf("checkpoint plan ID mismatch")
    }

    // Try to acquire or renew lock
    lock, err := a.AcquireLock(plan.Metadata.ClusterName, plan.Metadata.PlanID, plan.Metadata.OperationType)
    if err != nil {
        // Lock might still exist from previous run - try to renew if same plan
        lockPath := filepath.Join(GetClusterDir(plan.Metadata.ClusterName), ".lock")
        existingLock, loadErr := loadLock(lockPath)
        if loadErr == nil && existingLock.PlanID == plan.Metadata.PlanID {
            // Same plan, renew the lock
            lock = existingLock
            if err := a.RenewLock(lock, DefaultLockTimeout); err != nil {
                return fmt.Errorf("failed to renew lock: %w", err)
            }
            log.Info("Renewed existing lock for resume")
        } else {
            return fmt.Errorf("failed to acquire lock: %w", err)
        }
    }
    defer lock.Release()

    // Start background lock renewal
    renewCtx, cancelRenewal := context.WithCancel(ctx)
    defer cancelRenewal()

    go a.StartLockRenewal(renewCtx, lock, 1*time.Hour, DefaultLockTimeout)

    // Resume from checkpoint
    completedOps := make(map[string]bool)
    for _, opID := range checkpoint.CompletedOperations {
        completedOps[opID] = true
    }

    for _, op := range plan.Operations {
        if completedOps[op.ID] {
            log.Infof("[%s] Already completed, skipping", op.ID)
            continue
        }

        if err := a.ExecuteOperation(ctx, &op); err != nil {
            return fmt.Errorf("operation %s failed: %w", op.ID, err)
        }
    }

    return nil
}
```

**Lock Lifecycle Example:**

```
Timeline for a 6-hour deploy operation:

T+0h:00m  - Acquire lock (expires at T+24h)
T+0h:05m  - Start deployment
T+1h:00m  - Lock renewed (expires at T+25h)
T+2h:00m  - Lock renewed (expires at T+26h)
T+3h:00m  - Operation fails, checkpoint saved
T+3h:05m  - Lock released

--- User reviews logs, decides to resume ---

T+4h:00m  - Resume attempt
T+4h:00m  - Check existing lock: expired? No. Same plan? Yes.
T+4h:00m  - Renew lock (expires at T+28h)
T+4h:00m  - Start lock renewal goroutine
T+5h:00m  - Lock renewed (expires at T+29h)
T+6h:00m  - Deployment completes successfully
T+6h:00m  - Lock released
```

**Configuration:**

```go
// pkg/apply/lock.go

const (
    DefaultLockTimeout    = 24 * time.Hour  // Initial lock duration
    DefaultRenewInterval  = 1 * time.Hour   // How often to renew
    DefaultRenewExtension = 24 * time.Hour  // How much to extend on renewal
)

// Can be configured per operation
type LockConfig struct {
    Timeout         time.Duration // Initial timeout
    RenewInterval   time.Duration // How often to renew
    RenewExtension  time.Duration // How much to extend each renewal
}

var LockConfigs = map[string]LockConfig{
    "deploy": {
        Timeout:        24 * time.Hour,
        RenewInterval:  1 * time.Hour,
        RenewExtension: 24 * time.Hour,
    },
    "upgrade": {
        Timeout:        48 * time.Hour,  // Upgrades can take longer
        RenewInterval:  2 * time.Hour,
        RenewExtension: 48 * time.Hour,
    },
    "import": {
        Timeout:        12 * time.Hour,
        RenewInterval:  30 * time.Minute,
        RenewExtension: 12 * time.Hour,
    },
}
```

### Recommendation: Implement Locking with Timeout and Renewal

**Requirements:**
- Add REQ-PES-039: Acquire exclusive cluster lock before applying plan
- Add REQ-PES-040: Release lock on apply completion or error
- Add REQ-PES-041: Detect and remove stale locks (process dead or timeout expired)
- Add REQ-PES-049: Lock files include timeout/expiration timestamp
- Add REQ-PES-050: Locks expire after configurable timeout (default: 24h)
- Add REQ-PES-051: Same plan can renew its own lock during apply
- Add REQ-PES-052: Background goroutine renews lock periodically
- Add REQ-PES-053: CLI commands to view and clear locks
- Add REQ-PES-054: Resume operation can renew existing lock if same plan

**Benefits:**
1. **Timeout-based cleanup**: Locks auto-expire after 24h even if process can't be checked
2. **Long-running operations**: Renewal prevents timeout during lengthy applies
3. **Resume support**: Same plan can pick up existing lock and continue
4. **Observability**: CLI shows lock status including time until expiry
5. **Safe cleanup**: Manual clearing requires expired/stale lock or --force flag

---

## 7. Simulation Output Format: Human vs Machine

### Current State

Simulation outputs human-readable format:
```
[SIMULATION] [027] [332ms] execute
[SIMULATION]       Target: shell
[SIMULATION]       Details: /path/to/supervisord ctl ...
```

### Requirements

1. **Human Format:** Easy to read and review
2. **Machine Format:** Parseable by CI/CD, testing tools
3. **Unified Source:** Both formats from same data

### Solution: Structured Logging with Multiple Renderers

**Core Data Structure:**
```go
type SimulationEvent struct {
    Index       int                    `json:"index"`
    ElapsedMs   int64                  `json:"elapsed_ms"`
    OperationID string                 `json:"operation_id"`
    Type        string                 `json:"type"`
    Target      string                 `json:"target"`
    Details     string                 `json:"details"`
    Changes     map[string]interface{} `json:"changes,omitempty"`
    Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type SimulationReport struct {
    Metadata struct {
        ClusterName   string    `json:"cluster_name"`
        OperationType string    `json:"operation_type"`
        StartTime     time.Time `json:"start_time"`
        EndTime       time.Time `json:"end_time"`
        DurationMs    int64     `json:"duration_ms"`
    } `json:"metadata"`
    Events []SimulationEvent `json:"events"`
    Summary struct {
        TotalOperations   int            `json:"total_operations"`
        OperationsByType  map[string]int `json:"operations_by_type"`
        OperationsByPhase map[string]int `json:"operations_by_phase"`
    } `json:"summary"`
}
```

**Renderers:**

```go
type ReportRenderer interface {
    Render(report *SimulationReport) (string, error)
}

type HumanRenderer struct{}

func (r *HumanRenderer) Render(report *SimulationReport) (string, error) {
    var buf strings.Builder

    buf.WriteString(fmt.Sprintf("Simulation: %s (%s)\n",
        report.Metadata.ClusterName,
        report.Metadata.OperationType))
    buf.WriteString(fmt.Sprintf("Duration: %dms\n\n", report.Metadata.DurationMs))

    for _, event := range report.Events {
        buf.WriteString(fmt.Sprintf("[%03d] [%dms] %s\n",
            event.Index, event.ElapsedMs, event.Type))
        buf.WriteString(fmt.Sprintf("      Target: %s\n", event.Target))
        buf.WriteString(fmt.Sprintf("      Details: %s\n\n", event.Details))
    }

    // Summary
    buf.WriteString(fmt.Sprintf("\nTotal operations: %d\n", report.Summary.TotalOperations))
    buf.WriteString("Operations by type:\n")
    for typ, count := range report.Summary.OperationsByType {
        buf.WriteString(fmt.Sprintf("  - %s: %d\n", typ, count))
    }

    return buf.String(), nil
}

type JSONRenderer struct{}

func (r *JSONRenderer) Render(report *SimulationReport) (string, error) {
    data, err := json.MarshalIndent(report, "", "  ")
    if err != nil {
        return "", err
    }
    return string(data), nil
}

type MarkdownRenderer struct{}

func (r *MarkdownRenderer) Render(report *SimulationReport) (string, error) {
    var buf strings.Builder

    buf.WriteString(fmt.Sprintf("# Simulation Report: %s\n\n", report.Metadata.ClusterName))
    buf.WriteString(fmt.Sprintf("**Operation:** %s  \n", report.Metadata.OperationType))
    buf.WriteString(fmt.Sprintf("**Duration:** %dms  \n\n", report.Metadata.DurationMs))

    buf.WriteString("## Operations\n\n")
    buf.WriteString("| # | Type | Target | Details |\n")
    buf.WriteString("|---|------|--------|----------|\n")

    for _, event := range report.Events {
        buf.WriteString(fmt.Sprintf("| %d | %s | %s | %s |\n",
            event.Index, event.Type, event.Target, event.Details))
    }

    buf.WriteString("\n## Summary\n\n")
    buf.WriteString(fmt.Sprintf("**Total operations:** %d\n\n", report.Summary.TotalOperations))

    return buf.String(), nil
}
```

**Usage:**
```bash
# Human-readable (default)
mup cluster deploy test-rs --dry-run

# JSON for CI/CD
mup cluster deploy test-rs --dry-run --format=json | jq '.events | length'

# Markdown for documentation
mup cluster deploy test-rs --dry-run --format=markdown > docs/deploy-plan.md
```

### Recommendation: Implement Multiple Renderers

**Requirements Update:**
- Update REQ-PES-027: Support `--format=json|markdown|human`
- Add structured SimulationReport in `pkg/simulation/report.go`
- Add renderers in `pkg/simulation/renderer.go`

---

## 8. Error Recovery Strategies

### Classification of Errors

**1. Precondition Failures (Validation)**
- Binary not found
- Directory doesn't exist
- Port already in use
- **Strategy:** Abort, user must fix state

**2. Transient Failures (Network, Timeouts)**
- Connection timeout
- Temporary network partition
- Resource temporarily unavailable
- **Strategy:** Retry with exponential backoff

**3. Partial Failures (Operation Started but Unconfirmed)**
- Process started but timeout before confirmation
- Command executed but output lost
- **Strategy:** Check() method to detect completion

**4. Invariant Violations (Logic Errors)**
- Unexpected response from MongoDB
- File exists when it shouldn't
- Process in wrong state
- **Strategy:** Abort, log detailed error, user intervention

### Retry Policy

```go
type RetryPolicy struct {
    MaxAttempts int
    InitialDelay time.Duration
    MaxDelay     time.Duration
    Multiplier   float64
}

var DefaultRetryPolicy = RetryPolicy{
    MaxAttempts:  3,
    InitialDelay: 1 * time.Second,
    MaxDelay:     10 * time.Second,
    Multiplier:   2.0,
}

func (a *Applier) ExecuteWithRetry(ctx context.Context, op *PlannedOperation, policy RetryPolicy) (*OperationResult, error) {
    var lastErr error
    delay := policy.InitialDelay

    for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
        result, err := a.executeOperation(ctx, op)
        if err == nil {
            return result, nil
        }

        // Check if error is retryable
        if !isRetryable(err) {
            return nil, err // Don't retry
        }

        lastErr = err
        log.Warnf("Operation %s failed (attempt %d/%d): %v",
            op.ID, attempt, policy.MaxAttempts, err)

        if attempt < policy.MaxAttempts {
            log.Infof("Retrying in %v...", delay)
            time.Sleep(delay)
            delay = time.Duration(float64(delay) * policy.Multiplier)
            if delay > policy.MaxDelay {
                delay = policy.MaxDelay
            }
        }
    }

    return nil, fmt.Errorf("operation failed after %d attempts: %w",
        policy.MaxAttempts, lastErr)
}

func isRetryable(err error) bool {
    // Network errors
    if errors.Is(err, context.DeadlineExceeded) {
        return true
    }
    if errors.Is(err, syscall.ECONNREFUSED) {
        return true
    }

    // Timeout errors
    var netErr net.Error
    if errors.As(err, &netErr) && netErr.Timeout() {
        return true
    }

    // MongoDB driver errors
    if mongo.IsNetworkError(err) || mongo.IsTimeout(err) {
        return true
    }

    return false
}
```

### Recommendation: Implement Retry with Classification

**Requirements:**
- Add REQ-PES-042: Retry transient failures with exponential backoff
- Add REQ-PES-043: Do not retry validation or invariant violations
- Add error classification in `pkg/apply/errors.go`

---

## 9. Audit Trail and Observability

### Requirements

1. **Who did what when:** Track which user/host applied which plan
2. **Operation timeline:** Detailed timing for each operation
3. **State snapshots:** Capture cluster state before/after apply
4. **Failure forensics:** Enough data to diagnose any failure

### Solution: Structured Audit Log

```go
type ApplyAuditLog struct {
    PlanID      string    `json:"plan_id"`
    ClusterName string    `json:"cluster_name"`
    Operation   string    `json:"operation"`
    AppliedBy   string    `json:"applied_by"` // user@hostname
    AppliedAt   time.Time `json:"applied_at"`
    CompletedAt time.Time `json:"completed_at,omitempty"`
    Status      string    `json:"status"` // in_progress/completed/failed

    Operations []OperationAuditEntry `json:"operations"`

    StateSnapshot struct {
        Before map[string]interface{} `json:"before"`
        After  map[string]interface{} `json:"after"`
    } `json:"state_snapshot"`

    Error string `json:"error,omitempty"`
}

type OperationAuditEntry struct {
    ID          string                 `json:"id"`
    Type        string                 `json:"type"`
    StartedAt   time.Time              `json:"started_at"`
    CompletedAt time.Time              `json:"completed_at,omitempty"`
    DurationMs  int64                  `json:"duration_ms"`
    Status      string                 `json:"status"` // validating/executing/completed/failed
    Validation  *ValidationResult      `json:"validation,omitempty"`
    Result      *OperationResult       `json:"result,omitempty"`
    Error       string                 `json:"error,omitempty"`
}
```

**Storage:**
```
~/.mup/storage/clusters/<name>/audit/
  20251126-103000-4c243baf.json  # Apply audit log
  20251126-140000-8a9b1c2d.json
```

**Querying:**
```bash
# List all applies for cluster
mup cluster audit test-rs

# Show specific apply
mup cluster audit test-rs --apply-id=4c243baf

# Show failed applies
mup cluster audit test-rs --status=failed
```

### Recommendation: Implement Audit Logging

**Requirements:**
- Add REQ-PES-044: Record audit log for every apply operation
- Add REQ-PES-045: Include user, timestamp, operation timeline in audit
- Add REQ-PES-046: Retain audit logs indefinitely (or configurable retention)

---

## Summary of Recommendations

### Priority 1 (MVP - v0.1)

1. ✅ **Typed Parameter Structs** (Section 1)
   - Define operation-specific parameter types with validation
   - Example: `StartProcessParams`, `StartSupervisorParams`
   - Tags: REQ-PES-002, REQ-PES-003

2. ✅ **Four-Phase Handler Interface** (Section 3)
   - Add IsComplete(), PreHook(), Execute(), PostHook() methods
   - Implement for all operation handlers
   - Tags: REQ-PES-036, REQ-PES-047, REQ-PES-048

3. ✅ **HookResult for Pre/Post Execution** (Section 2, 3)
   - HookResult with Valid, Warnings, Errors, StateChanges
   - User prompts on validation failures or divergence
   - Tags: REQ-PES-034, REQ-PES-035

4. ✅ **Cluster Locking** (Section 6)
   - Lock files to prevent concurrent applies
   - Stale lock detection (check process alive)
   - Tags: REQ-PES-039, REQ-PES-040, REQ-PES-041

5. ✅ **Plan Versioning** (Section 5)
   - Add plan_version separate from mup_version
   - Version compatibility checking and migration
   - Tags: REQ-PES-038

### Priority 2 (Post-MVP - v0.2)

6. ✅ **Multiple Output Formats** (Section 7)
   - JSON, Markdown, Human renderers
   - Structured SimulationReport
   - Tags: REQ-PES-027

7. ✅ **Retry Logic** (Section 8)
   - Classify errors as retryable/non-retryable
   - Exponential backoff
   - Tags: REQ-PES-042, REQ-PES-043

8. ✅ **Audit Logging** (Section 9)
   - Complete operation timeline
   - User tracking
   - Tags: REQ-PES-044, REQ-PES-045, REQ-PES-046

### Priority 3 (Future - v0.3+)

9. ✅ **Parallel Execution** (Section 4)
   - DAG-based dependency resolution
   - Concurrent operation execution
   - Tags: REQ-PES-037

---

## Open Questions for Discussion

1. **Plan Approval Workflow:** Should we support multi-user review/approval (e.g., plan generated by dev, approved by ops)?

2. **Remote State:** Should plans/checkpoints be stored remotely for team collaboration (like Terraform Cloud)?

3. **Plan Diffs:** Should we support comparing two plans or plan vs actual state?

4. **Rollback Plans:** Should applying a plan generate a rollback plan automatically?

5. **Plan Expiration:** Should plans expire after N days to prevent applying stale plans?

6. **Resource Limits:** Should we limit concurrent applies per user/cluster/globally?

7. **Dry-run Validation:** Should `--dry-run` also run validation, or just simulation?

8. **Plan Signing:** Should plans be cryptographically signed for security?

---

## Implementation Phases

### Phase 1: Core Plan/Apply (2-3 weeks)
- Plan serialization and persistence (REQ-PES-001 to REQ-PES-007)
- Apply command with validation (REQ-PES-011 to REQ-PES-014)
- Basic checkpointing (REQ-PES-015 to REQ-PES-020)
- Typed parameters (Section 1)

### Phase 2: Safety and Reliability (1-2 weeks)
- Cluster locking (Section 6)
- Rich validation (Section 2)
- Check() method (Section 3)
- Plan versioning (Section 5)

### Phase 3: Observability (1 week)
- Multiple output formats (Section 7)
- Audit logging (Section 9)
- Enhanced error messages

### Phase 4: Advanced Features (2-3 weeks)
- Retry logic (Section 8)
- Parallel execution (Section 4)
- Plan diffs
- Rollback plans

**Total Estimated Time:** 6-9 weeks for complete implementation
