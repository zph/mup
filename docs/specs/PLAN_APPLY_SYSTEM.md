# Plan/Apply System Specification

## Overview

This specification defines a Terraform-inspired plan/apply workflow for mup cluster operations. Unlike Terraform's rigid, declarative model, mup's system is **action-oriented** and **flexible**, focusing on simulating operations before execution with runtime safety checks.

**Core Principle:** Separate *what will happen* (plan) from *making it happen* (apply).

## Design Philosophy

### Inspired by Terraform, Not a Copy

**What we adopt:**
- Two-phase workflow (plan → apply)
- Plan serialization for review and audit
- Dry-run capabilities
- Change detection and preview
- State tracking with checkpoints
- Pre-flight validation

**What we differ on:**
- **Action-oriented vs. declarative:** Plans describe operations to perform, not desired end state
- **Runtime flexibility:** Apply can adapt to conditions, not locked to static plan
- **Loose coupling:** Plan is guidance, not strict contract
- **Imperative operations:** Focus on "do X then Y" not "ensure X exists"

### Key Concepts

1. **Plan:** A detailed, ordered list of operations with validation results
2. **Apply:** Execute planned operations with runtime safety checks
3. **Checkpoint:** Persistent state snapshot after each major step
4. **Hook:** User-defined script executed at lifecycle events
5. **Safety Check:** Runtime validation before executing each operation

## Phase 1: Deploy Operations

### Scope

Starting with cluster deployment operations:
- `mup cluster deploy` - Create new clusters
- Future: extend to start/stop/destroy/restart

### Architecture

```
┌─────────────┐
│   Command   │
│  (Cobra)    │
└──────┬──────┘
       │
       ▼
┌─────────────────────┐
│   Planner           │
│  - Parse topology   │
│  - Run pre-flight   │
│  - Generate plan    │
│  - Validate plan    │
└──────┬──────────────┘
       │
       ▼ (plan serialized to disk)
┌─────────────────────┐
│   Applier           │
│  - Load plan        │
│  - Execute phases   │
│  - Save checkpoints │
│  - Run hooks        │
│  - Safety checks    │
└─────────────────────┘
```

### Plan Structure

```go
// Plan represents a complete operation plan
type Plan struct {
    // Identity
    PlanID      string    `json:"plan_id"`      // UUID for this plan
    Operation   string    `json:"operation"`    // "deploy", "upgrade", "start", etc.
    ClusterName string    `json:"cluster_name"`
    CreatedAt   time.Time `json:"created_at"`

    // Input Configuration
    Version     string          `json:"version"`
    Variant     string          `json:"variant"`
    TopologyFile string         `json:"topology_file"`
    Topology    *Topology       `json:"topology"`

    // Validation Results
    Validation  ValidationResult `json:"validation"`

    // Execution Plan
    Phases      []PlannedPhase   `json:"phases"`

    // Resource Estimates
    Resources   ResourceEstimate `json:"resources"`

    // Metadata
    DryRun      bool             `json:"dry_run"`
    Environment map[string]string `json:"environment"`
}

// PlannedPhase represents one phase of execution
type PlannedPhase struct {
    Name        string          `json:"name"`         // "prepare", "deploy", "initialize"
    Description string          `json:"description"`
    Order       int             `json:"order"`

    // Operations in this phase
    Operations  []PlannedOperation `json:"operations"`

    // Hooks
    BeforeHook  *Hook           `json:"before_hook,omitempty"`
    AfterHook   *Hook           `json:"after_hook,omitempty"`

    // Estimated duration (informational only)
    EstimatedDuration string    `json:"estimated_duration,omitempty"`
}

// PlannedOperation is a single atomic action
type PlannedOperation struct {
    ID          string          `json:"id"`           // Unique operation ID
    Type        OperationType   `json:"type"`         // "download_binary", "create_dir", etc.
    Description string          `json:"description"`  // Human-readable
    Target      OperationTarget `json:"target"`       // What/where

    // Pre-conditions (checked at apply time)
    PreConditions []SafetyCheck `json:"pre_conditions"`

    // Expected changes
    Changes     []Change        `json:"changes"`

    // Dependencies
    DependsOn   []string        `json:"depends_on"`   // Operation IDs

    // Parallelization
    Parallel    bool            `json:"parallel"`     // Can run in parallel with siblings
}

// OperationType enumerates all operation types
type OperationType string

const (
    OpDownloadBinary      OperationType = "download_binary"
    OpCreateDirectory     OperationType = "create_directory"
    OpUploadFile          OperationType = "upload_file"
    OpGenerateConfig      OperationType = "generate_config"
    OpStartProcess        OperationType = "start_process"
    OpWaitForProcess      OperationType = "wait_for_process"
    OpInitReplicaSet      OperationType = "init_replica_set"
    OpAddShard            OperationType = "add_shard"
    OpVerifyHealth        OperationType = "verify_health"
    OpSaveMetadata        OperationType = "save_metadata"
    OpStopProcess         OperationType = "stop_process"
    OpRemoveDirectory     OperationType = "remove_directory"
)

// OperationTarget describes what the operation acts on
type OperationTarget struct {
    Type     string            `json:"type"`      // "host", "process", "replica_set", "cluster"
    Name     string            `json:"name"`      // Identifier
    Host     string            `json:"host,omitempty"`
    Port     int               `json:"port,omitempty"`
    Params   map[string]string `json:"params"`    // Operation-specific parameters
}

// Change describes an expected state change
type Change struct {
    ResourceType string      `json:"resource_type"`  // "file", "process", "port", "directory"
    ResourceID   string      `json:"resource_id"`
    Action       ActionType  `json:"action"`         // "create", "update", "delete", "start", "stop"
    Before       interface{} `json:"before,omitempty"`
    After        interface{} `json:"after,omitempty"`
}

type ActionType string

const (
    ActionCreate ActionType = "create"
    ActionUpdate ActionType = "update"
    ActionDelete ActionType = "delete"
    ActionStart  ActionType = "start"
    ActionStop   ActionType = "stop"
    ActionNone   ActionType = "none"
)

// SafetyCheck is a runtime validation
type SafetyCheck struct {
    ID          string   `json:"id"`
    Description string   `json:"description"`
    CheckType   string   `json:"check_type"`    // "port_available", "disk_space", etc.
    Target      string   `json:"target"`
    Params      map[string]interface{} `json:"params"`
    Required    bool     `json:"required"`      // Fail if check fails?
}

// ValidationResult aggregates all pre-flight checks
type ValidationResult struct {
    Valid       bool              `json:"valid"`
    Errors      []ValidationIssue `json:"errors"`
    Warnings    []ValidationIssue `json:"warnings"`
    Checks      []CheckResult     `json:"checks"`
}

type ValidationIssue struct {
    Code        string `json:"code"`
    Message     string `json:"message"`
    Host        string `json:"host,omitempty"`
    Severity    string `json:"severity"`  // "error", "warning", "info"
}

type CheckResult struct {
    Name        string        `json:"name"`
    Status      string        `json:"status"`  // "passed", "failed", "warning"
    Message     string        `json:"message"`
    Host        string        `json:"host,omitempty"`
    Duration    time.Duration `json:"duration"`
    Details     interface{}   `json:"details,omitempty"`
}

// ResourceEstimate shows expected resource usage
type ResourceEstimate struct {
    Hosts           int               `json:"hosts"`
    TotalProcesses  int               `json:"total_processes"`
    PortsUsed       []int             `json:"ports_used"`
    DiskSpaceGB     float64           `json:"disk_space_gb"`
    MemoryMB        int               `json:"memory_mb,omitempty"`
    DownloadSizeMB  int               `json:"download_size_mb"`
    ProcessesPerHost map[string]int   `json:"processes_per_host"`
}
```

### Apply State Structure

```go
// ApplyState tracks execution progress
type ApplyState struct {
    // Identity
    StateID     string    `json:"state_id"`      // UUID for this apply
    PlanID      string    `json:"plan_id"`       // Which plan we're executing
    ClusterName string    `json:"cluster_name"`
    Operation   string    `json:"operation"`

    // Status
    Status      ApplyStatus `json:"status"`      // "pending", "running", "paused", "completed", "failed"
    StartedAt   time.Time   `json:"started_at"`
    UpdatedAt   time.Time   `json:"updated_at"`
    CompletedAt *time.Time  `json:"completed_at,omitempty"`

    // Progress
    CurrentPhase    string          `json:"current_phase"`
    PhaseStates     map[string]*PhaseState   `json:"phase_states"`
    OperationStates map[string]*OperationState `json:"operation_states"`

    // Checkpoints
    Checkpoints []Checkpoint `json:"checkpoints"`

    // Errors
    Errors      []ExecutionError `json:"errors"`

    // Runtime Info
    ExecutionLog []LogEntry `json:"execution_log"`
}

type ApplyStatus string

const (
    StatusPending   ApplyStatus = "pending"
    StatusRunning   ApplyStatus = "running"
    StatusPaused    ApplyStatus = "paused"
    StatusCompleted ApplyStatus = "completed"
    StatusFailed    ApplyStatus = "failed"
    StatusRolledBack ApplyStatus = "rolled_back"
)

type PhaseState struct {
    Name        string      `json:"name"`
    Status      ApplyStatus `json:"status"`
    StartedAt   *time.Time  `json:"started_at,omitempty"`
    CompletedAt *time.Time  `json:"completed_at,omitempty"`
    Error       string      `json:"error,omitempty"`
}

type OperationState struct {
    ID          string          `json:"id"`
    Status      ApplyStatus     `json:"status"`
    StartedAt   *time.Time      `json:"started_at,omitempty"`
    CompletedAt *time.Time      `json:"completed_at,omitempty"`
    Error       string          `json:"error,omitempty"`
    Result      OperationResult `json:"result,omitempty"`
    Retries     int             `json:"retries"`
}

type OperationResult struct {
    Success     bool                   `json:"success"`
    Output      string                 `json:"output,omitempty"`
    Changes     []Change               `json:"changes"`
    Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type Checkpoint struct {
    ID          string    `json:"id"`
    Description string    `json:"description"`
    Timestamp   time.Time `json:"timestamp"`
    Phase       string    `json:"phase"`
    Operation   string    `json:"operation,omitempty"`
    State       string    `json:"state"`  // JSON serialized state snapshot
}

type ExecutionError struct {
    Timestamp   time.Time `json:"timestamp"`
    Phase       string    `json:"phase"`
    Operation   string    `json:"operation"`
    Error       string    `json:"error"`
    Recoverable bool      `json:"recoverable"`
}

type LogEntry struct {
    Timestamp   time.Time `json:"timestamp"`
    Level       string    `json:"level"`   // "info", "warn", "error", "debug"
    Phase       string    `json:"phase"`
    Operation   string    `json:"operation,omitempty"`
    Message     string    `json:"message"`
}
```

### Lifecycle Hooks

```go
// Hook represents a user-defined script
type Hook struct {
    Name        string            `json:"name"`
    Command     string            `json:"command"`      // Shell command to execute
    Timeout     time.Duration     `json:"timeout"`
    Environment map[string]string `json:"environment"`
    ContinueOnError bool          `json:"continue_on_error"`
}

// HookEvent represents when hooks can run
type HookEvent string

const (
    // Deploy hooks
    HookBeforePlan       HookEvent = "before_plan"
    HookAfterPlan        HookEvent = "after_plan"
    HookBeforeApply      HookEvent = "before_apply"
    HookAfterApply       HookEvent = "after_apply"

    // Phase hooks
    HookBeforePhase      HookEvent = "before_phase"
    HookAfterPhase       HookEvent = "after_phase"

    // Operation hooks
    HookBeforeOperation  HookEvent = "before_operation"
    HookAfterOperation   HookEvent = "after_operation"

    // Error hooks
    HookOnError          HookEvent = "on_error"
    HookOnSuccess        HookEvent = "on_success"
)

// HookContext passed to hook scripts as environment variables
type HookContext struct {
    ClusterName string
    Operation   string
    Phase       string
    PlanPath    string
    StatePath   string
    // ... additional context
}
```

## Command Interface

### Deploy Command

```bash
# Plan only (dry-run)
mup cluster deploy my-cluster topology.yaml --version 7.0 --plan-only
mup cluster deploy my-cluster topology.yaml --version 7.0 --dry-run

# Generate plan and save to file
mup cluster deploy my-cluster topology.yaml --version 7.0 --plan-file ./my-plan.json

# Review existing plan
mup plan show ./my-plan.json
mup plan show my-cluster  # Uses saved plan from cluster state

# Apply with confirmation (interactive)
mup cluster deploy my-cluster topology.yaml --version 7.0
# Shows plan, prompts: "Do you want to apply this plan? (yes/no)"

# Apply saved plan
mup plan apply ./my-plan.json
mup plan apply my-cluster  # Uses saved plan

# Auto-approve (no confirmation)
mup cluster deploy my-cluster topology.yaml --version 7.0 --auto-approve

# Apply with specific plan ID (for audit trail)
mup plan apply --plan-id abc-123

# Resume failed apply
mup plan resume my-cluster
mup plan resume --state-id xyz-789
```

### Plan Management Commands

```bash
# List all plans
mup plan list
mup plan list my-cluster

# Show plan details
mup plan show <plan-id>
mup plan show my-cluster  # Latest plan

# Show diff format
mup plan diff my-cluster

# Validate plan
mup plan validate ./my-plan.json

# Delete plan
mup plan delete <plan-id>

# Export plan for sharing
mup plan export <plan-id> > plan.json

# Import plan
mup plan import < plan.json
```

### State Management Commands

```bash
# Show current apply state
mup state show my-cluster
mup state show --state-id xyz-789

# List apply history
mup state list my-cluster

# Show checkpoints
mup state checkpoints my-cluster

# Rollback to checkpoint
mup state rollback my-cluster --checkpoint <id>
```

## Workflow Examples

### Example 1: Basic Deploy with Plan Review

```bash
# Step 1: Generate plan
$ mup cluster deploy prod-cluster topology.yaml --version 7.0 --plan-only

Planning deployment for cluster: prod-cluster
MongoDB version: 7.0.5
Topology: Sharded cluster (2 shards, 3 nodes each)

Running pre-flight checks...
✓ Connectivity to all hosts (5/5)
✓ Disk space available (50GB required, 500GB available)
✓ All ports available (8 ports on 5 hosts)
✓ MongoDB 7.0.5 binary available (cached)

Plan generated: plan-abc123
Plan saved to: ~/.mup/storage/plans/prod-cluster/plan-abc123.json

# Step 2: Review plan
$ mup plan show prod-cluster

Plan ID: plan-abc123
Operation: deploy
Created: 2025-01-15 10:30:00

Resources:
  Hosts: 5
  Total processes: 10 (6 mongod, 3 config, 1 mongos)
  Ports: 27017-27024
  Disk space: 10GB
  Download size: 120MB

Phases:
  1. Prepare (estimated: 2m)
     • Download MongoDB 7.0.5 binary for linux-x86_64
     • Create directories on 5 hosts (data, logs, config)
     • Upload configuration files (10 configs)

  2. Deploy (estimated: 1m)
     • Start 3 config servers (parallel)
     • Wait for config servers ready
     • Start 6 shard mongod processes (parallel)
     • Wait for mongod processes ready

  3. Initialize (estimated: 3m)
     • Initialize config replica set
     • Initialize shard1 replica set
     • Initialize shard2 replica set
     • Start mongos process
     • Add shard1 to cluster
     • Add shard2 to cluster

  4. Finalize (estimated: 30s)
     • Verify cluster health
     • Save cluster metadata
     • Display connection info

Total operations: 24
Estimated duration: 6.5 minutes

Warnings:
  • Config servers will use default storage engine (WiredTiger)

Validation: ✓ Plan is valid

# Step 3: Apply plan
$ mup plan apply prod-cluster

Apply plan-abc123 to cluster prod-cluster?

This will:
  • Create 5 MongoDB processes on localhost
  • Use ports 27017-27024
  • Create directories at ~/.mup/storage/clusters/prod-cluster/
  • Download 120MB of binaries

Do you want to continue? (yes/no): yes

Applying plan...
Phase 1/4: Prepare
  ✓ Downloaded MongoDB 7.0.5 (120MB) [1m 23s]
  ✓ Created directories on 5 hosts [2s]
  ✓ Uploaded configuration files [1s]
Checkpoint saved: phase1-complete

Phase 2/4: Deploy
  ✓ Started 3 config servers [5s]
  ✓ Config servers ready [10s]
  ✓ Started 6 shard mongod processes [8s]
  ✓ Shard processes ready [15s]
Checkpoint saved: phase2-complete

Phase 3/4: Initialize
  ✓ Initialized config replica set [30s]
  ✓ Initialized shard1 replica set [28s]
  ✓ Initialized shard2 replica set [29s]
  ✓ Started mongos process [5s]
  ✓ Added shard1 to cluster [3s]
  ✓ Added shard2 to cluster [3s]
Checkpoint saved: phase3-complete

Phase 4/4: Finalize
  ✓ Verified cluster health [2s]
  ✓ Saved cluster metadata [1s]

Deployment completed successfully! [2m 35s]

Cluster: prod-cluster
Status: running
Connection: mongosh 'mongodb://localhost:27017'
```

### Example 2: Failed Deploy with Resume

```bash
$ mup cluster deploy test-cluster topology.yaml --version 7.0

Planning deployment...
✓ Plan generated: plan-xyz789

Applying plan...
Phase 1/4: Prepare
  ✓ Downloaded MongoDB 7.0.5
  ✓ Created directories
Checkpoint saved: phase1-complete

Phase 2/4: Deploy
  ✓ Started config servers
  ✗ Error starting shard1-node1: port 27018 already in use

Apply failed at phase 2, operation op-012.
State saved: state-xyz789
Last checkpoint: phase1-complete

To resume: mup plan resume test-cluster

# Fix the port conflict, then resume
$ mup plan resume test-cluster

Resuming apply state-xyz789 from checkpoint phase1-complete

Phase 2/4: Deploy
  ✓ Started shard1-node1 [3s]
  ✓ All processes started [10s]

Phase 3/4: Initialize
  ...continues...
```

### Example 3: Dry-run with Hooks

```bash
# topology.yaml includes hooks:
# hooks:
#   before_apply:
#     command: "./scripts/notify-team.sh"
#   after_phase:
#     command: "./scripts/checkpoint-slack.sh"
#   on_error:
#     command: "./scripts/alert-oncall.sh"

$ mup cluster deploy prod-cluster topology.yaml --version 7.0 --dry-run

Planning deployment for cluster: prod-cluster

Running pre-flight checks...
✓ All checks passed

Hooks configured:
  • before_apply: ./scripts/notify-team.sh
  • after_phase: ./scripts/checkpoint-slack.sh
  • on_error: ./scripts/alert-oncall.sh

[Dry-run mode] Would execute:
  1. Hook: before_apply
  2. Phase: Prepare (5 operations)
  3. Hook: after_phase (prepare)
  4. Phase: Deploy (8 operations)
  5. Hook: after_phase (deploy)
  6. Phase: Initialize (6 operations)
  7. Hook: after_phase (initialize)
  8. Phase: Finalize (2 operations)
  9. Hook: after_phase (finalize)

Total: 21 operations across 4 phases
No changes will be made (dry-run mode)
```

## State Persistence

### File Structure

```
~/.mup/storage/
├── clusters/
│   └── my-cluster/
│       ├── meta.yaml                    # Current cluster metadata
│       ├── plans/
│       │   ├── plan-abc123.json        # Saved plans
│       │   └── plan-def456.json
│       └── state/
│           ├── apply-xyz789.json       # Apply states
│           ├── apply-xyz789-checkpoints/
│           │   ├── checkpoint-001.json
│           │   └── checkpoint-002.json
│           └── current-apply.json → apply-xyz789.json
└── plans/
    └── global/                          # Plans not yet applied
        └── plan-orphaned.json
```

### State Transitions

```
┌──────────┐
│  PLAN    │ (plan-only or dry-run)
└──────────┘

┌──────────┐     ┌─────────┐     ┌───────────┐     ┌───────────┐
│  PLAN    │ --> │ PENDING │ --> │  RUNNING  │ --> │ COMPLETED │
└──────────┘     └─────────┘     └───────────┘     └───────────┘
                                       │                    │
                                       ▼                    ▼
                                  ┌─────────┐         ┌──────────┐
                                  │ PAUSED  │         │ SUCCESS  │
                                  └─────────┘         └──────────┘
                                       │
                                       ▼
                                  ┌─────────┐
                                  │ FAILED  │
                                  └─────────┘
                                       │
                                       ▼
                                  ┌──────────────┐
                                  │ ROLLED_BACK  │
                                  └──────────────┘
```

## Safety Checks at Apply Time

Even with a valid plan, apply performs runtime checks before each operation:

```go
// Example: Before starting a process
SafetyChecks:
- Port still available (could have been taken since plan)
- Binary still exists and matches checksum
- Config file still valid
- Process not already running
- Required dependencies met (e.g., config servers running)
```

### Check Failures

```bash
# If safety check fails during apply:
Phase 2/4: Deploy
  ✓ Started config servers
  ✗ Safety check failed for operation op-012:
    Check: port_available
    Port: 27018
    Status: Port now in use by PID 12345

Options:
  1. Fix the issue and retry operation
  2. Skip this operation (dangerous)
  3. Abort apply
  4. Pause apply

Choice:
```

## Testing Interface

The plan/apply separation provides excellent testing hooks:

### Unit Testing Operations

```go
// Test individual operations
func TestCreateDirectoryOperation(t *testing.T) {
    op := &PlannedOperation{
        Type: OpCreateDirectory,
        Target: OperationTarget{
            Type: "host",
            Host: "localhost",
            Params: map[string]string{
                "path": "/tmp/test",
                "mode": "0755",
            },
        },
    }

    result := ExecuteOperation(mockExecutor, op)
    assert.True(t, result.Success)
}
```

### Integration Testing Plans

```go
// Test complete plans without execution
func TestDeployPlanGeneration(t *testing.T) {
    planner := NewPlanner(...)
    plan, err := planner.GeneratePlan(topology)

    require.NoError(t, err)
    assert.Len(t, plan.Phases, 4)
    assert.True(t, plan.Validation.Valid)

    // Verify operation order
    assert.Equal(t, OpDownloadBinary, plan.Phases[0].Operations[0].Type)
    assert.Equal(t, OpStartProcess, plan.Phases[1].Operations[0].Type)
}
```

### Mock Apply

```go
// Simulate apply without side effects
func TestApplyWithMockExecutor(t *testing.T) {
    mockExec := &MockExecutor{
        SimulateFailure: "op-015",  // Fail at operation 15
    }

    applier := NewApplier(plan, mockExec)
    state, err := applier.Apply(ctx)

    assert.Error(t, err)
    assert.Equal(t, StatusFailed, state.Status)
    assert.Len(t, state.Checkpoints, 2)  // Should have saved 2 checkpoints
}
```

## Implementation Plan

### Phase 1: Core Plan/Apply for Deploy (Week 1-2)

**Files to create:**
```
pkg/plan/
  plan.go          - Plan structure and serialization
  planner.go       - Plan generation interface
  validator.go     - Plan validation

pkg/apply/
  apply.go         - Apply structure and state
  applier.go       - Apply execution engine
  checkpoint.go    - Checkpoint management
  safety.go        - Runtime safety checks

pkg/operation/
  operation.go     - Operation types and interface
  executor.go      - Operation execution

cmd/mup/
  plan.go          - Plan subcommands (show, validate, list)
  state.go         - State subcommands (show, list, checkpoints)
```

**Modifications:**
```
pkg/deploy/
  deployer.go      - Split into Planner + Applier
  prepare.go       - Extract into operations
  deploy.go        - Extract into operations
  initialize.go    - Extract into operations

cmd/mup/
  cluster.go       - Add --plan-only, --dry-run, --auto-approve flags
```

**Tests:**
```
pkg/plan/
  planner_test.go

pkg/apply/
  applier_test.go
  checkpoint_test.go

pkg/operation/
  operations_test.go
```

### Phase 2: Hooks and Advanced Features (Week 3)

- Implement lifecycle hooks
- Add hook execution engine
- Environment variable injection
- Hook timeout and error handling

### Phase 3: Extend to Other Operations (Week 4)

- Apply to `cluster start/stop/restart`
- Apply to `cluster destroy` (with confirmation)
- Apply to configuration changes

### Phase 4: Upgrade Integration (Week 5+)

- Refactor existing upgrade to use new plan/apply
- Merge upgrade-specific features (FCV checks, rolling operations)
- Unified operation framework

## Backward Compatibility

### Transition Strategy

1. **Phase 1:** New flag-based opt-in
   ```bash
   # Old behavior (direct execution)
   mup cluster deploy my-cluster topology.yaml --version 7.0

   # New behavior (requires --auto-approve)
   mup cluster deploy my-cluster topology.yaml --version 7.0 --auto-approve
   ```

2. **Phase 2:** Interactive prompt by default
   ```bash
   # Shows plan, prompts for confirmation
   mup cluster deploy my-cluster topology.yaml --version 7.0
   Do you want to apply this plan? (yes/no):
   ```

3. **Phase 3:** Plan required
   ```bash
   # Must explicitly plan or auto-approve
   mup cluster deploy my-cluster topology.yaml --version 7.0 --plan-only
   mup plan apply my-cluster --auto-approve
   ```

### Upgrade Path for Existing Clusters

Existing clusters continue to work without plans:
- Meta structure unchanged
- Operations still use executors
- Plans are additive, not required
- State directory added alongside meta.yaml

## Benefits

### For Users

1. **Visibility:** See exactly what will happen before it happens
2. **Safety:** Catch issues before making changes
3. **Audit:** Full record of what was planned and executed
4. **Recovery:** Resume from checkpoints on failure
5. **Testing:** Validate plans without execution
6. **Automation:** Save and replay plans

### For Development

1. **Testability:** Operations testable in isolation
2. **Debuggability:** Clear operation boundaries
3. **Extensibility:** Easy to add new operations
4. **Maintainability:** Separation of concerns
5. **Consistency:** Same pattern across all operations

### For Operations

1. **Change management:** Plans can be reviewed before apply
2. **Compliance:** Audit trail of all changes
3. **Collaboration:** Share plans between team members
4. **Risk reduction:** Dry-run in production
5. **Rollback:** Return to known checkpoints

## Future Enhancements

### Advanced Planning

1. **What-if scenarios:** Compare multiple topology configurations
2. **Cost estimation:** Cloud resource costs
3. **Performance modeling:** Expected throughput/latency
4. **Dependency graphing:** Visual operation dependencies

### Advanced Apply

1. **Parallel execution:** Optimize operation parallelization
2. **Conditional operations:** Dynamic plan adjustment
3. **Rollback automation:** Auto-rollback on failure
4. **Progress streaming:** Real-time status updates via websocket

### Advanced State

1. **State locking:** Prevent concurrent applies
2. **State versioning:** Track all historical states
3. **State migration:** Upgrade state format automatically
4. **Distributed state:** Multi-node coordination

### Integration

1. **CI/CD pipelines:** Integrate with GitHub Actions, GitLab CI
2. **Monitoring:** Prometheus metrics, Grafana dashboards
3. **Notifications:** Slack, PagerDuty, email
4. **GitOps:** Store plans in Git, apply via CD

## Open Questions

1. **Plan expiry:** Should plans expire after some time?
2. **Plan conflicts:** How to handle concurrent plans for same cluster?
3. **Partial apply:** Should users be able to apply only certain phases?
4. **State garbage collection:** When to delete old apply states?
5. **Cross-cluster plans:** Should plans support multi-cluster operations?

## References

- Terraform plan/apply: https://developer.hashicorp.com/terraform/cli/commands/plan
- Kubernetes apply: https://kubernetes.io/docs/reference/kubectl/apply/
- Ansible check mode: https://docs.ansible.com/ansible/latest/playbook_guide/playbooks_checkmode.html
