# Plan/Apply System Quick Reference

## One-Page Summary

### Core Concept

**Terraform-like workflow:** Simulate → Review → Apply → Verify

```
┌─────────────┐     ┌──────────┐     ┌───────────┐     ┌──────────┐
│   PLAN      │────▶│  REVIEW  │────▶│   APPLY   │────▶│ VERIFY   │
│ (Simulate)  │     │  (Human) │     │ (Execute) │     │ (Confirm)│
└─────────────┘     └──────────┘     └───────────┘     └──────────┘
      │                                      │
      └──────────────────┬───────────────────┘
                         │
                    Same Plan
                  (Immutable)
```

---

## CLI Workflow

```bash
# 1. Generate plan (simulation mode)
mup cluster deploy test-rs --plan
# → Saves to ~/.mup/storage/clusters/test-rs/plans/<uuid>.json

# 2. Review plan
mup cluster plan test-rs --plan-id=4c243baf
# → Shows all operations, changes, and metadata

# 3. Apply plan
mup cluster apply test-rs --plan-id=4c243baf
# → Executes operations with validation

# 4. Resume on failure
mup cluster apply test-rs --plan-id=4c243baf --resume
# → Continues from last checkpoint
```

---

## Plan File Structure

```json
{
  "metadata": {
    "plan_id": "4c243baf-fde1-4440-b9d6-4cd6093978be",
    "plan_version": "1.0",
    "cluster_name": "test-rs",
    "operation_type": "deploy",
    "created_at": "2025-11-26T10:30:00Z",
    "mup_version": "0.1.0",
    "sha256": "a3b2c1d4e5f6..."
  },
  "operations": [
    {
      "id": "deploy-001",
      "type": "create_directory",
      "description": "Create cluster base directory",
      "target": {"type": "filesystem", "name": "/path/to/dir"},
      "params": {"path": "/path/to/dir", "mode": 493},
      "changes": {...},
      "dependencies": [],
      "phase": "prepare"
    }
  ]
}
```

---

## Operation Handler Interface

```go
type OperationHandler interface {
    IsComplete(ctx, op, exec) (bool, error)
    PreHook(ctx, op, exec) (*HookResult, error)
    Execute(ctx, op, exec) (*OperationResult, error)
    PostHook(ctx, op, exec) (*HookResult, error)
}
```

**Execution Order:**
1. **IsComplete**: Skip if already done (for resume)
2. **PreHook**: Validate + user hooks + prepare
3. **Execute**: Do the work (idempotent)
4. **PostHook**: Verify + user hooks + update

---

## Key Types

### Typed Parameters

```go
type StartProcessParams struct {
    ProgramName      string `json:"program_name" validate:"required"`
    SupervisorConfig string `json:"supervisor_config" validate:"required,file"`
    SupervisorPort   int    `json:"supervisor_port" validate:"required,min=1,max=65535"`
}
```

### HookResult

```go
type HookResult struct {
    Valid        bool
    Warnings     []string      // Non-blocking
    Errors       []string      // Blocking
    StateChanges []StateChange // Divergence detected
    Metadata     map[string]interface{}
}
```

### OperationResult

```go
type OperationResult struct {
    Success  bool
    Message  string
    Changes  map[string]interface{}
    Metadata map[string]interface{}
}
```

---

## Checkpointing

```json
{
  "plan_id": "4c243baf...",
  "cluster_name": "test-rs",
  "status": "failed",
  "last_completed_operation_id": "deploy-026",
  "failed_operation_id": "deploy-027",
  "error_message": "supervisord failed to start",
  "operations_completed": 26,
  "operations_total": 50
}
```

**Location:** `~/.mup/storage/clusters/<name>/checkpoints/<plan_id>.json`

---

## Safety Features

### 1. Plan Verification
- SHA-256 hash ensures plan not modified
- Version compatibility checking
- Prevents applying stale/corrupted plans

### 2. Validation Before Each Operation
- Check preconditions still hold
- Detect state divergence
- Warn/abort if world changed

### 3. Cluster Locking with Timeout and Renewal
```
~/.mup/storage/clusters/<name>/.lock
{
  "plan_id": "4c243baf...",
  "operation": "deploy",
  "locked_by": "alice@workstation:12345",
  "locked_at": "2025-11-26T10:35:00Z",
  "expires_at": "2025-11-27T10:35:00Z",
  "lock_timeout": "24h"
}
```

**Lock Lifecycle:**
- Acquired with 24h timeout
- Renewed every 1h during apply
- Auto-expires if timeout reached
- Same plan can renew lock on resume

**CLI Commands:**
```bash
mup cluster lock status test-rs    # View lock info
mup cluster lock clear test-rs     # Clear expired/stale
mup cluster lock clear test-rs --force  # Force clear
mup cluster lock list               # List all locks
```

### 4. Idempotency
- All operations safe to retry
- `IsComplete()` skips completed work
- Prevents duplicate state changes

---

## Error Classification

| Type | Examples | Retry? | Strategy |
|------|----------|--------|----------|
| **Precondition** | File missing, port unavailable | ❌ No | Abort, user fixes state |
| **Transient** | Network timeout, temp unavailable | ✅ Yes | Exponential backoff |
| **Partial** | Process started but unconfirmed | ⚠️ Check | `IsComplete()` detects |
| **Invariant** | Unexpected MongoDB response | ❌ No | Abort, log detailed error |

---

## Directory Structure

```
~/.mup/storage/clusters/<cluster_name>/
├── meta.yaml                    # Cluster metadata
├── plans/
│   ├── 4c243baf-....json       # Plan files
│   ├── 8a9b1c2d-....json
│   └── (10 most recent)
├── checkpoints/
│   └── 4c243baf-....json       # Checkpoint for in-progress apply
├── audit/
│   ├── 20251126-103000-4c243baf.json  # Apply audit logs
│   └── 20251126-140000-8a9b1c2d.json
└── .lock                        # Cluster lock file
```

---

## Implementation Phases

### Phase 1: Core Plan/Apply (MVP)
- ✅ Plan serialization/persistence
- ✅ Apply command with validation
- ✅ Checkpointing and resume
- ✅ Typed parameters
- ✅ Four-phase handler interface

### Phase 2: Safety
- ✅ Cluster locking
- ✅ Rich validation (warnings/errors)
- ✅ Plan versioning
- ✅ IsComplete for idempotency

### Phase 3: Observability
- ✅ Multiple output formats (JSON, Markdown)
- ✅ Audit logging
- ✅ Enhanced error messages

### Phase 4: Advanced
- ⏳ Retry logic with exponential backoff
- ⏳ Parallel execution (DAG)
- ⏳ Plan diffs
- ⏳ Automatic rollback plans

---

## Requirements Traceability

| Component | Requirements | Files |
|-----------|-------------|-------|
| Plan generation | REQ-PES-001 to 004 | `pkg/plan/` |
| Plan persistence | REQ-PES-005 to 007 | `pkg/apply/applier.go` |
| Plan verification | REQ-PES-008 to 010 | `pkg/apply/verify.go` |
| Operation execution | REQ-PES-011 to 017 | `pkg/apply/applier.go` |
| Checkpointing | REQ-PES-018 to 020 | `pkg/apply/checkpoint.go` |
| Hook integration | REQ-PES-021 to 023 | Handler methods |
| Typed parameters | REQ-PES-002, 003 | `pkg/operation/params.go` |
| Four-phase handlers | REQ-PES-036, 047, 048 | `pkg/operation/handler.go` |

---

## Testing Strategy

### Unit Tests
```go
// Test each handler phase independently
func TestHandler_IsComplete(t *testing.T)
func TestHandler_PreHook(t *testing.T)
func TestHandler_Execute(t *testing.T)
func TestHandler_PostHook(t *testing.T)
```

### Integration Tests
```go
// Test full plan/apply workflow in simulation
func TestPlanApply_Deploy(t *testing.T)
func TestPlanApply_Resume(t *testing.T)
func TestPlanApply_StateDivergence(t *testing.T)
```

### Simulation Tests
```go
// Verify simulation matches real execution
func TestSimulation_OutputMatchesReal(t *testing.T)
```

---

## Example: Complete Flow

```go
// 1. PLAN: Generate operations via simulation
planner := deploy.NewPlanner(clusterName, topology)
simExec := simulation.NewExecutor(simConfig)
plan, err := planner.GeneratePlan(ctx, simExec)
// → Plan with 50 operations

// 2. SAVE: Persist plan to disk
planID := uuid.New()
planPath := fmt.Sprintf("~/.mup/storage/clusters/%s/plans/%s.json", clusterName, planID)
SavePlan(planPath, plan)

// 3. REVIEW: User examines plan
// (Human reviews operations, decides to proceed)

// 4. APPLY: Execute plan with real executor
sshExec := ssh.NewExecutor(host, port, user, keyPath)
applier := apply.NewApplier(sshExec)
err = applier.ApplyPlan(ctx, plan)

// For each operation:
//   - IsComplete() → skip if done
//   - PreHook()    → validate + user hooks
//   - Execute()    → perform operation
//   - PostHook()   → verify + user hooks
//   - Checkpoint() → record progress

// 5. VERIFY: Check cluster health
// (Automatic in PostHook, or manual health check)
```

---

## References

- **EARS Requirements:** `docs/specs/plan-execution-system-requirements.md`
- **Critical Analysis:** `docs/specs/plan-execution-critical-analysis.md`
- **Interface Spec:** `docs/specs/operation-handler-interface.md`
- **Original Design:** `docs/specs/PLAN_APPLY_SYSTEM.md`

---

## Key Insights

1. **Simulation is not approximate** - It must show EXACTLY what will execute
2. **Plans are immutable** - SHA-256 verification prevents tampering
3. **Validation is refresh** - World state checked before each operation
4. **Idempotency is mandatory** - Every operation safe to retry
5. **Four phases enable safety** - IsComplete/PreHook/Execute/PostHook
6. **Type safety matters** - JSON unmarshaling loses types, need typed structs
7. **Locking prevents chaos** - Concurrent applies corrupt state
8. **PostHook catches silent failures** - Execution "success" != actual success
