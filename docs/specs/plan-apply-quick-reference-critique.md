# Critique: Plan/Apply Quick Reference

## Executive Summary

This document identifies **logic flaws** and **operational excellence risks** in `docs/specs/plan-apply-quick-reference.md`. While the document provides a useful overview, it contains several critical gaps and inconsistencies that could lead to implementation errors, operational failures, or security issues.

**Severity Levels:**
- 🔴 **Critical**: Could cause data loss, security issues, or system corruption
- 🟡 **High**: Could cause operational failures or incorrect behavior
- 🟢 **Medium**: Could cause confusion or inefficiency

---

## Critical Issues

### 1. 🔴 Interface Mismatch: Four-Phase vs Two-Phase Handler

**Location:** Lines 75-83, "Operation Handler Interface"

**Problem:**
The quick reference documents a four-phase interface:
```go
type OperationHandler interface {
    IsComplete(ctx, op, exec) (bool, error)
    PreHook(ctx, op, exec) (*HookResult, error)
    Execute(ctx, op, exec) (*OperationResult, error)
    PostHook(ctx, op, exec) (*HookResult, error)
}
```

However, the actual implementation in `pkg/operation/executor.go` only has:
```go
type OperationHandler interface {
    Execute(ctx, op, exec) (*OperationResult, error)
    Validate(ctx, op, exec) error
}
```

**Impact:**
- Developers implementing handlers will be confused about which interface to use
- The quick reference is not a reliable guide for implementation
- The documented workflow (IsComplete → PreHook → Execute → PostHook) cannot be followed

**Recommendation:**
1. Either update the quick reference to match the current implementation, OR
2. Update the implementation to match the documented four-phase interface (as specified in `operation-handler-interface.md`)
3. Add a clear note about which version is current/planned

**Related Documents:**
- `docs/specs/operation-handler-interface.md` - Documents four-phase interface
- `pkg/operation/executor.go` - Implements two-phase interface

---

### 2. 🔴 SHA-256 Hash Verification: Missing Critical Details

**Location:** Lines 152-155, "Plan Verification"

**Problem:**
The document states "SHA-256 hash ensures plan not modified" but fails to specify:
- **When is the hash computed?** (at plan generation time, or at apply time?)
- **Where is the hash stored?** (in plan metadata, or separately?)
- **What exactly is hashed?** (the entire JSON file, or just the operations array?)
- **What happens if hash mismatch?** (error message, abort behavior, user notification)

**Operational Risk:**
If the hash is computed at apply time and compared against a stored value, but the stored value is in the plan file itself, then modifying the plan file could also modify the hash, defeating the purpose.

**Example Attack Vector:**
1. Attacker modifies plan file operations
2. Attacker also updates the `sha256` field in metadata to match new hash
3. Apply proceeds with malicious plan

**Recommendation:**
1. Specify that hash is computed **at plan generation time** and stored in metadata
2. Specify that hash is computed over the **entire plan file** (excluding the hash field itself)
3. Specify that apply **aborts immediately** with clear error message if hash mismatch
4. Consider storing hash separately (e.g., `plan.json.sha256`) for additional security

**Related Requirements:**
- REQ-PES-008: SHA-256 hash verification

---

### 3. 🔴 Resume Logic: IsComplete() vs Checkpoint Conflict

**Location:** Lines 86-90, "Execution Order" and Lines 131-144, "Checkpointing"

**Problem:**
The document doesn't explain what happens when:
- Checkpoint says operation `deploy-027` **failed**
- On resume, `IsComplete()` returns **true** (operation actually succeeded)
- OR: Checkpoint says operation `deploy-027` **completed**
- On resume, `IsComplete()` returns **false** (operation was rolled back)

**Operational Risk:**
- **False Positive (checkpoint says failed, IsComplete says done):** Operation might be skipped, leaving cluster in inconsistent state
- **False Negative (checkpoint says done, IsComplete says not done):** Operation might be re-executed, potentially causing duplicate state changes

**Missing Logic:**
The document doesn't specify:
1. Which source of truth takes precedence: checkpoint or `IsComplete()`?
2. What happens if they disagree?
3. Should there be a reconciliation step?

**Recommendation:**
1. Define precedence: `IsComplete()` is authoritative (it checks actual state)
2. If checkpoint says "completed" but `IsComplete()` says "not done", log warning and re-execute
3. If checkpoint says "failed" but `IsComplete()` says "done", log warning and skip
4. Add a `--force-recheck` flag to ignore checkpoint and rely solely on `IsComplete()`

**Related Requirements:**
- REQ-PES-016: Resume capability
- REQ-PES-036: IsComplete() method

---

### 4. 🟡 Checkpoint Structure Inconsistency

**Location:** Lines 133-143, "Checkpointing"

**Problem:**
The quick reference shows a simplified checkpoint structure:
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

But the actual implementation in `pkg/apply/checkpoint.go` and `pkg/apply/state.go` uses a more complex structure with:
- `Checkpoint` objects with `ID`, `Description`, `Timestamp`, `Phase`, `StatePath`
- `ApplyState` with `OperationStates` map tracking each operation individually
- Multiple checkpoints per apply (not just one)

**Impact:**
- Users reviewing checkpoints will see different structure than documented
- Developers implementing checkpoint logic will be confused
- The simplified structure doesn't support multiple checkpoints or detailed operation tracking

**Recommendation:**
1. Update quick reference to show actual checkpoint structure, OR
2. Add note that this is a "simplified view" and link to full specification
3. Show both the checkpoint metadata and the state snapshot structure

---

### 5. 🟡 Version Compatibility: plan_version vs mup_version Confusion

**Location:** Lines 50-56, "Plan File Structure" and Line 154, "Version compatibility checking"

**Problem:**
The document shows both `plan_version` and `mup_version` in metadata:
```json
{
  "metadata": {
    "plan_version": "1.0",
    "mup_version": "0.1.0",
    ...
  }
}
```

But it's unclear:
- Which version is checked during apply?
- What's the relationship between them?
- Can a plan with `plan_version: "1.0"` created by `mup_version: "0.1.0"` be applied by `mup_version: "0.2.0"`?

**Operational Risk:**
- Applying plans with incompatible versions could cause undefined behavior
- Users might not understand why their plan is rejected

**Recommendation:**
1. Clarify that `plan_version` is the format version (checked for compatibility)
2. Clarify that `mup_version` is informational (shows which binary created the plan)
3. Specify compatibility matrix: which `plan_version` values are supported by which `mup_version` ranges
4. Add example: "Plan created with mup v0.1.0 (plan_version 1.0) can be applied by mup v0.1.x but not v0.2.0"

**Related Documents:**
- `docs/specs/plan-execution-critical-analysis.md` Section 5: Plan File Versioning

---

## High-Priority Issues

### 6. 🟡 Error Classification Table: Incomplete

**Location:** Lines 179-187, "Error Classification"

**Problem:**
The table is too simplistic and doesn't address edge cases:

| Issue | Description |
|-------|-------------|
| **Partial Failure After IsComplete()** | What if `IsComplete()` returns `true`, but `PostHook()` fails? Should this be treated as "Partial" or "Invariant"? |
| **Transient Error in PreHook()** | The table says "Transient" errors are retried, but what if `PreHook()` validation fails due to network timeout? Should it retry or abort? |
| **IsComplete() False Negative** | What if `IsComplete()` incorrectly returns `false` (operation is actually done)? This could cause duplicate execution. |
| **Checkpoint Save Failure** | What if checkpoint save fails after operation succeeds? Is the operation considered complete? |

**Recommendation:**
1. Expand table to include error locations (PreHook, Execute, PostHook, IsComplete)
2. Add decision tree: "If error in PreHook → abort; if error in Execute → retry if transient; if error in PostHook → mark as failed"
3. Add section on "Error Recovery Strategies" with specific examples

---

### 7. 🟡 Cluster Locking: Missing Failure Modes

**Location:** Lines 162-170, "Cluster Locking"

**Problem:**
The document shows lock file structure but doesn't explain:
- **What happens if lock acquisition fails?** (abort? retry? wait?)
- **How are stale locks detected?** (process alive check? timeout?)
- **What if process crashes while holding lock?** (lock remains forever?)
- **What if lock file is manually deleted?** (can another process acquire lock?)

**Operational Risk:**
- Deadlocks if stale locks aren't cleaned up
- Race conditions if lock detection is flawed
- Cluster corruption if two applies proceed concurrently

**Recommendation:**
1. Specify lock acquisition algorithm (try-exclusive-create, check-stale, retry)
2. Specify stale lock detection (check process PID, timeout threshold)
3. Specify lock cleanup on process exit (defer cleanup, signal handlers)
4. Add `--force-unlock` flag for manual lock removal (with warnings)

**Related Documents:**
- `docs/specs/plan-execution-critical-analysis.md` Section 6: Concurrency Control

---

### 8. 🟡 State Divergence: Undefined Detection Logic

**Location:** Lines 157-160, "Validation Before Each Operation"

**Problem:**
The document mentions "Detect state divergence" but doesn't specify:
- **What exactly is "divergence"?** (port changed? file deleted? process stopped?)
- **How is it detected?** (compare against plan? compare against last checkpoint?)
- **What triggers a warning vs. error?** (all divergences abort, or only critical ones?)
- **Can user override divergence detection?** (--ignore-divergence flag?)

**Operational Risk:**
- False positives: legitimate state changes (e.g., process restarted by admin) could abort apply
- False negatives: critical divergences (e.g., topology changed) could be missed

**Recommendation:**
1. Define divergence types: **Critical** (abort), **Warning** (prompt user), **Info** (log only)
2. Specify detection method: compare current state against plan's expected state
3. Add examples: "Port in use → Critical divergence; Process already running → Warning"
4. Add `--ignore-warnings` flag to proceed despite non-critical divergences

**Related Documents:**
- `docs/specs/plan-execution-critical-analysis.md` Section 2: Simulation Accuracy

---

## Medium-Priority Issues

### 9. 🟢 CLI Workflow: Missing Error Scenarios

**Location:** Lines 23-41, "CLI Workflow"

**Problem:**
The workflow shows happy path but doesn't show:
- What if `--plan-id` doesn't exist?
- What if `--resume` is used but no checkpoint exists?
- What if plan file is deleted between plan generation and apply?
- What if apply is interrupted (Ctrl+C)?

**Recommendation:**
1. Add "Error Scenarios" section with common failure modes
2. Show error messages users will see
3. Show recovery steps

---

### 10. 🟢 Directory Structure: Missing File Permissions

**Location:** Lines 190-205, "Directory Structure"

**Problem:**
The document shows directory structure but doesn't specify:
- File permissions for plan files (mentioned in REQ-PES-007 as 0600, but not in quick ref)
- File permissions for checkpoint files
- File permissions for lock file
- Who can read/write these files?

**Security Risk:**
- Plan files may contain sensitive information (paths, ports, topology)
- Checkpoint files may contain state information
- Lock files should be readable to detect stale locks

**Recommendation:**
1. Add file permission column to directory structure
2. Specify: plans (0600), checkpoints (0600), lock (0644 for stale detection), audit (0644)

---

### 11. 🟢 Implementation Phases: Status Inconsistency

**Location:** Lines 209-234, "Implementation Phases"

**Problem:**
All phases are marked as "✅" (completed), but based on the interface mismatch (Issue #1), it's unclear if Phase 1 is actually complete. The document should reflect actual implementation status.

**Recommendation:**
1. Verify actual implementation status against codebase
2. Update checkmarks to reflect reality
3. Add "Status: In Progress" or "Status: Planned" indicators

---

## Logic Flaws

### 12. 🟡 Plan Immutability: SHA-256 in Plan File

**Location:** Lines 19, "Same Plan (Immutable)" and Line 56, "sha256" field

**Logic Flaw:**
If the SHA-256 hash is stored **inside** the plan file, then modifying the plan file and updating the hash field would result in a valid hash. The hash should be computed over the plan **excluding the hash field itself**, or stored separately.

**Recommendation:**
1. Specify that hash is computed over plan JSON with `sha256` field set to empty string
2. OR store hash in separate file (`plan.json.sha256`)
3. Document the exact algorithm: `sha256(plan_json_with_empty_hash_field)`

---

### 13. 🟡 Idempotency: IsComplete() Not Guaranteed

**Location:** Lines 172-175, "Idempotency"

**Logic Flaw:**
The document states "All operations safe to retry" and "`IsComplete()` skips completed work", but:
- What if `IsComplete()` has a bug and returns wrong result?
- What if `IsComplete()` can't determine completion (network error)?
- What if operation completed externally (admin intervention)?

**Recommendation:**
1. Clarify that idempotency is a **handler contract**, not a guarantee
2. Specify that handlers MUST implement idempotent `Execute()` regardless of `IsComplete()`
3. Add note: "Even if `IsComplete()` returns false, `Execute()` should be safe to call multiple times"

---

## Operational Excellence Risks

### 14. 🟡 Checkpoint Save Failure: No Recovery Strategy

**Risk:**
If checkpoint save fails after operation succeeds, the operation is marked complete in memory but not persisted. On crash/resume, the operation might be re-executed.

**Impact:**
- Duplicate operations
- Potential state corruption
- Wasted time re-executing completed work

**Recommendation:**
1. Make checkpoint save **synchronous and blocking** (don't proceed until saved)
2. If checkpoint save fails, mark operation as "completed but not checkpointed"
3. On resume, check both checkpoint AND `IsComplete()` for each operation

---

### 15. 🟡 Plan File Deletion: No Protection

**Risk:**
If plan file is deleted during apply (accidental or malicious), apply cannot continue or verify operations.

**Impact:**
- Apply might fail mid-execution
- Cannot verify plan integrity
- Cannot resume from checkpoint

**Recommendation:**
1. Load entire plan into memory at start of apply
2. Verify plan file exists and is readable before starting
3. Add `--plan-file` flag to allow applying from different location (backup)

---

### 16. 🟡 Concurrent Apply: Lock Bypass Scenarios

**Risk:**
Even with locking, there are scenarios where concurrent applies could proceed:
- Lock file on NFS with stale cache
- Lock file on different filesystem than plan files
- Manual lock file deletion
- Process crash leaving stale lock

**Impact:**
- Cluster corruption
- Conflicting state changes
- Data loss

**Recommendation:**
1. Use file locking (flock) in addition to lock file
2. Add lock timeout (auto-release after N minutes)
3. Add lock heartbeat (update timestamp every 30 seconds)
4. Detect and abort if lock heartbeat stops

---

### 17. 🟢 Plan Expiration: Not Addressed

**Risk:**
Plans could be applied months after generation, when cluster state has changed significantly.

**Impact:**
- Plans based on stale assumptions
- Operations might fail or cause unexpected results

**Recommendation:**
1. Add `plan_expires_at` timestamp to plan metadata
2. Warn if plan is older than N days (configurable, default 7 days)
3. Require `--force` flag to apply expired plans

---

## Missing Critical Information

### 18. 🟡 No Rollback Strategy

**Missing:**
The document mentions checkpoints but doesn't explain:
- How to rollback to a checkpoint?
- What operations are rolled back?
- Is rollback automatic or manual?

**Recommendation:**
1. Add "Rollback" section explaining rollback process
2. Specify which operations can be rolled back (not all are reversible)
3. Add `mup cluster rollback <cluster> --checkpoint <id>` command example

---

### 19. 🟡 No Audit Trail Details

**Missing:**
The document shows audit directory but doesn't explain:
- What's in audit logs?
- How long are they retained?
- Can they be queried?

**Recommendation:**
1. Add "Audit Logging" section
2. Show audit log structure
3. Add `mup cluster audit <cluster>` command example

---

### 20. 🟢 No Performance Considerations

**Missing:**
The document doesn't address:
- How long does plan generation take? (large clusters)
- How long does apply take? (50 operations = ?)
- Can operations be parallelized?

**Recommendation:**
1. Add "Performance" section with benchmarks/estimates
2. Mention parallel execution as future enhancement
3. Add guidance on plan size limits

---

## Recommendations Summary

### Immediate Actions (Critical)
1. **Fix interface mismatch** - Align quick reference with actual implementation OR update implementation
2. **Clarify SHA-256 verification** - Specify when/where/how hash is computed and verified
3. **Define resume logic** - Specify precedence between checkpoint and `IsComplete()`
4. **Document error handling** - Expand error classification with edge cases

### Short-term Improvements (High Priority)
5. **Complete locking specification** - Add failure modes and stale lock detection
6. **Define state divergence** - Specify detection logic and user options
7. **Update checkpoint structure** - Match documented structure to implementation
8. **Clarify version compatibility** - Explain `plan_version` vs `mup_version` relationship

### Long-term Enhancements (Medium Priority)
9. **Add rollback documentation** - Explain rollback process and limitations
10. **Add audit trail details** - Document audit log structure and querying
11. **Add error scenarios** - Show common failure modes and recovery
12. **Add performance guidance** - Include timing estimates and optimization tips

---

## Conclusion

The quick reference document provides a useful overview but contains **critical gaps** that could lead to:
- Implementation confusion (interface mismatch)
- Security vulnerabilities (SHA-256 verification)
- Operational failures (resume logic, locking)
- Data corruption (concurrent applies)

**Priority:** Address critical issues (#1-5) before using this document as implementation guide. Address high-priority issues (#6-8) before production deployment.

**Recommendation:** Treat this document as a **work in progress** and cross-reference with:
- `docs/specs/plan-execution-system-requirements.md` (requirements)
- `docs/specs/plan-execution-critical-analysis.md` (design decisions)
- `docs/specs/operation-handler-interface.md` (interface spec)
- `pkg/apply/` and `pkg/operation/` (actual implementation)
