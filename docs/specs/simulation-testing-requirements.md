# Simulation Testing Framework Requirements

## Overview

This document specifies the functional requirements for the Simulation Testing Framework in mup. The framework enables fast, token-efficient testing of CLI commands without modifying the filesystem, starting processes, or making network calls. This is critical for rapid development and validation of command logic.

**System Name:** Simulation Testing Framework (SimTest)
**Version:** 1.0
**Last Updated:** 2025-11-25

**Purpose:**
- **User-Facing**: Replace --dry-run with comprehensive simulation mode for previewing operations
- **Developer-Facing**: Provide testing harness for validating command logic without infrastructure
- Reduce test execution time from minutes to milliseconds
- Conserve LLM tokens by avoiding actual file/process operations
- Validate command logic independently from infrastructure
- Support TDD workflow with instant feedback

**Dual Role:**
1. **Dry-Run Replacement**: Users invoke `--simulate` to preview what a command will do before executing
2. **Testing Harness**: Developers and CI/CD use simulation mode for fast, deterministic testing

**Scope:**
All cluster-changing operations: `deploy`, `upgrade`, `import`, `scale-out`, `scale-in`, `start`, `stop`, `restart`, `destroy`

---

## Requirements

### Core Simulation Mode

**REQ-SIM-001:** Event Driven

**Requirement:**
When a CLI command is invoked with the `--simulate` flag, the SimTest shall execute all command logic without performing any filesystem, process, or network operations.

**Rationale:**
Users and tests need to validate command behavior without side effects or infrastructure dependencies.

**Verification:**
Execute `mup cluster deploy test topology.yaml --simulate` and verify no files are created, no processes started, and no network calls made.

---

**REQ-SIM-002:** State Driven

**Requirement:**
While simulation mode is active, the SimTest shall track all operations that would have been performed.

**Rationale:**
Users need visibility into what would happen during actual execution.

**Verification:**
Run simulated command and verify operation log contains all intended actions with parameters.

---

**REQ-SIM-003:** Ubiquitous

**Requirement:**
The SimTest shall complete simulation of any CLI command in less than 5 seconds.

**Rationale:**
Fast feedback is essential for TDD workflow and token efficiency.

**Verification:**
Measure execution time of simulated deploy, upgrade, and import commands.

---

### Filesystem Simulation

**REQ-SIM-004:** State Driven

**Requirement:**
While simulation mode is active, the SimTest shall record all filesystem operations (create, read, write, delete, mkdir, symlink) without modifying the actual filesystem.

**Rationale:**
Command logic depends on filesystem operations, but tests should not create actual files.

**Verification:**
Run simulated deploy and verify operation log contains all directory creation and file write operations, but filesystem remains unchanged.

---

**REQ-SIM-005:** Event Driven

**Requirement:**
When a simulated operation checks for file existence, the SimTest shall return results based on the simulated filesystem state.

**Rationale:**
Commands must be able to query filesystem state to make decisions.

**Verification:**
Simulate creating a file, then simulate checking if it exists. Verify check returns true without actual file creation.

---

**REQ-SIM-006:** State Driven

**Requirement:**
While simulation mode is active, the SimTest shall maintain an in-memory representation of filesystem state including directories, files, symlinks, and permissions.

**Rationale:**
Commands may perform multiple filesystem operations that depend on previous operations.

**Verification:**
Simulate a sequence: mkdir, create file, check existence, read file. Verify all operations work correctly in memory.

---

**REQ-SIM-007:** Ubiquitous

**Requirement:**
The SimTest shall support reading from actual files on disk while preventing writes.

**Rationale:**
Commands need to read input files (topology.yaml, config templates) but should not write outputs.

**Verification:**
Simulate deploy with real topology.yaml file. Verify file is read correctly but no output files are created.

---

### Process Simulation

**REQ-SIM-008:** State Driven

**Requirement:**
While simulation mode is active, the SimTest shall record all process operations (start, stop, restart, kill, status check) without starting actual processes.

**Rationale:**
Commands that manage MongoDB processes must be testable without running MongoDB.

**Verification:**
Simulate starting 3 mongod processes and verify operation log shows all starts but no actual processes running.

---

**REQ-SIM-009:** Event Driven

**Requirement:**
When a simulated operation starts a process, the SimTest shall record the process as running in simulated state.

**Rationale:**
Subsequent operations may check if processes are running.

**Verification:**
Simulate starting mongod, then simulate checking if it's running. Verify status check returns running without actual process.

---

**REQ-SIM-010:** State Driven

**Requirement:**
While simulation mode is active, the SimTest shall simulate process readiness checks by returning success after a configured delay.

**Rationale:**
Commands wait for processes to be ready before proceeding.

**Verification:**
Simulate starting mongod and waiting for readiness. Verify readiness check succeeds instantly without actual MongoDB process.

---

**REQ-SIM-011:** Event Driven

**Requirement:**
When a simulated operation queries process status, the SimTest shall return simulated status based on recorded process state.

**Rationale:**
Commands need to query whether processes are running, stopped, or failed.

**Verification:**
Simulate starting, stopping, and querying process status. Verify all queries return correct simulated state.

---

### Network Simulation

**REQ-SIM-012:** State Driven

**Requirement:**
While simulation mode is active, the SimTest shall record all network operations (SSH connections, SSH commands, file transfers, HTTP requests) without making actual network calls.

**Rationale:**
Commands that work with remote hosts must be testable without network access.

**Verification:**
Simulate SSH-based deploy and verify all SSH operations are recorded but no actual SSH connections made.

---

**REQ-SIM-013:** Event Driven

**Requirement:**
When a simulated operation executes an SSH command, the SimTest shall return a configurable simulated response.

**Rationale:**
Commands make decisions based on SSH command output.

**Verification:**
Configure simulation to return specific output for "mongod --version" command. Verify command receives expected output without SSH.

---

**REQ-SIM-014:** Event Driven

**Requirement:**
When a simulated operation checks SSH connectivity, the SimTest shall return success.

**Rationale:**
Commands verify connectivity before proceeding with operations.

**Verification:**
Simulate connectivity check for multiple hosts. Verify all checks succeed without network calls.

---

**REQ-SIM-015:** State Driven

**Requirement:**
While simulation mode is active, the SimTest shall simulate file transfers (upload/download) by recording source and destination without actual transfer.

**Rationale:**
Commands transfer binaries and config files to remote hosts.

**Verification:**
Simulate uploading mongod binary to remote host. Verify transfer is recorded but no network I/O occurs.

---

### Executor Abstraction

**REQ-SIM-016:** Ubiquitous

**Requirement:**
The SimTest shall provide a SimulationExecutor that implements the Executor interface.

**Rationale:**
All operations use the Executor interface, allowing transparent simulation.

**Verification:**
Code review: verify SimulationExecutor implements all Executor interface methods.

---

**REQ-SIM-017:** Event Driven

**Requirement:**
When any operation calls an Executor method, the SimulationExecutor shall record the operation and return a simulated result.

**Rationale:**
Operations should work identically with real or simulated executors.

**Verification:**
Run deploy with LocalExecutor and SimulationExecutor. Verify both follow same code path except for actual execution.

---

**REQ-SIM-018:** Ubiquitous

**Requirement:**
The SimulationExecutor shall support configurable behaviors for different operations (success, failure, delay).

**Rationale:**
Tests need to simulate various scenarios including failures.

**Verification:**
Configure SimulationExecutor to fail on operation #5. Verify deploy fails at correct operation with proper error handling.

---

### Plan/Apply Integration

**REQ-SIM-019:** Event Driven

**Requirement:**
When a plan is generated with `--simulate` flag, the SimTest shall create a valid plan without performing pre-flight checks that require infrastructure.

**Rationale:**
Plans should be generatable without access to target infrastructure.

**Verification:**
Generate plan with --simulate for deploy operation. Verify plan is created without SSH connectivity checks.

---

**REQ-SIM-020:** Event Driven

**Requirement:**
When a plan is applied with `--simulate` flag, the SimTest shall execute all plan operations in simulation mode.

**Rationale:**
Complete plan/apply workflow must be testable in simulation.

**Verification:**
Generate and apply plan with --simulate. Verify all phases execute and state is tracked without side effects.

---

**REQ-SIM-021:** State Driven

**Requirement:**
While simulation mode is active, the SimTest shall create apply state files in memory without persisting to disk.

**Rationale:**
State tracking logic must be testable without filesystem writes.

**Verification:**
Apply plan in simulation mode. Verify state operations succeed but no state files created on disk.

---

**REQ-SIM-022:** Ubiquitous

**Requirement:**
The SimTest shall support persisting simulation results to an optional output file.

**Rationale:**
Users may want to review simulation results or use them for analysis.

**Verification:**
Run simulation with `--simulate-output results.json`. Verify results file contains complete operation log.

---

### Testing Support

**REQ-SIM-023:** Event Driven

**Requirement:**
When a test invokes a CLI command in simulation mode, the SimTest shall return deterministic results for the same input.

**Rationale:**
Tests must be reproducible and reliable.

**Verification:**
Run same simulation 10 times. Verify all runs produce identical results.

---

**REQ-SIM-024:** Ubiquitous

**Requirement:**
The SimTest shall execute simulated operations at least 100x faster than actual operations.

**Rationale:**
Simulation must provide significant performance benefit for rapid testing.

**Verification:**
Measure actual deploy time vs simulated deploy time. Verify simulation is at least 100x faster.

---

**REQ-SIM-025:** Event Driven

**Requirement:**
When a test requires specific environmental conditions, the SimTest shall support preconfiguring simulated state.

**Rationale:**
Tests need to simulate various starting conditions (existing files, running processes, etc.).

**Verification:**
Preconfigure simulation with existing cluster state, then simulate upgrade. Verify upgrade logic handles existing state correctly.

---

**REQ-SIM-026:** Ubiquitous

**Requirement:**
The SimTest shall support both local and SSH simulation modes transparently.

**Rationale:**
Commands work with both local and remote executors.

**Verification:**
Run same command with --simulate and --ssh-host. Verify both modes work without network calls.

---

### Output and Reporting

**REQ-SIM-027:** Ubiquitous

**Requirement:**
The SimTest shall prefix all output with "[SIMULATION]" indicator.

**Rationale:**
Users must clearly know when simulation mode is active.

**Verification:**
Run command with --simulate. Verify all output lines start with [SIMULATION] prefix.

---

**REQ-SIM-028:** Event Driven

**Requirement:**
When simulation completes, the SimTest shall output a summary report showing all operations that would have been performed.

**Rationale:**
Users need complete visibility into what simulation did.

**Verification:**
Complete simulated deploy. Verify summary shows all filesystem, process, and network operations with parameters.

---

**REQ-SIM-029:** Event Driven

**Requirement:**
When simulation encounters a would-be error, the SimTest shall report the error without exiting.

**Rationale:**
Users want to see all potential errors in a single simulation run.

**Verification:**
Configure simulation to encounter multiple errors. Verify all errors are reported with continue option.

---

**REQ-SIM-030:** Ubiquitous

**Requirement:**
The SimTest shall output simulated operation timing to help identify performance bottlenecks.

**Rationale:**
Users can optimize command logic even without actual execution.

**Verification:**
Review simulation output. Verify each phase and operation shows simulated duration.

---

### Safety and Validation

**REQ-SIM-031:** Event Driven

**Requirement:**
When a command is executed with `--simulate`, the SimTest shall validate all command parameters and configuration without executing.

**Rationale:**
Simulation catches configuration errors before attempting actual execution.

**Verification:**
Run simulation with invalid topology file. Verify validation error is caught in simulation.

---

**REQ-SIM-032:** State Driven

**Requirement:**
While simulation mode is active, the SimTest shall perform all safety checks that do not require infrastructure.

**Rationale:**
Logic errors should be caught in simulation even if infrastructure checks are skipped.

**Verification:**
Simulate deploy with invalid configuration. Verify configuration validation fails in simulation.

---

**REQ-SIM-033:** If-Then Unwanted Behaviour

**Requirement:**
If a command attempts to write to a protected system directory in simulation mode, then the SimTest shall record the operation and emit a warning.

**Rationale:**
Detect potential permission issues before actual execution.

**Verification:**
Simulate operation that would write to /etc. Verify warning is emitted but simulation continues.

---

**REQ-SIM-034:** Event Driven

**Requirement:**
When simulation detects a logic error (e.g., using file before creating), the SimTest shall report a validation error.

**Rationale:**
Catch bugs in command implementation before actual execution.

**Verification:**
Implement command that reads file before creating it. Verify simulation reports validation error.

---

### Performance and Efficiency

**REQ-SIM-035:** Ubiquitous

**Requirement:**
The SimTest shall use no more than 100MB of memory during simulation of any command.

**Rationale:**
Simulations should be resource-efficient to enable parallel test execution.

**Verification:**
Monitor memory usage during simulation of large cluster deploy. Verify peak usage under 100MB.

---

**REQ-SIM-036:** Event Driven

**Requirement:**
When multiple simulations run concurrently, the SimTest shall isolate their simulated state.

**Rationale:**
Parallel test execution must not have cross-test contamination.

**Verification:**
Run 10 simulations in parallel with different configurations. Verify no state leakage between simulations.

---

**REQ-SIM-037:** Ubiquitous

**Requirement:**
The SimTest shall minimize LLM token usage by producing concise output summaries.

**Rationale:**
Token efficiency is critical for AI-assisted development.

**Verification:**
Compare output verbosity of actual vs simulated deploy. Verify simulation output is at least 10x smaller.

---

### Integration with Existing Commands

**REQ-SIM-038:** Ubiquitous

**Requirement:**
The SimTest shall support simulation for all cluster operations: deploy, upgrade, import, start, stop, restart, destroy, scale-out, scale-in.

**Rationale:**
All operations must be testable in simulation.

**Verification:**
Code review: verify --simulate flag is supported by all cluster command implementations.

---

**REQ-SIM-039:** Event Driven

**Requirement:**
When a command uses the PathResolver interface in simulation mode, the SimTest shall provide a simulated path resolver.

**Rationale:**
Path resolution logic must work in simulation without filesystem access.

**Verification:**
Simulate deploy and verify PathResolver operations are recorded without filesystem I/O.

---

**REQ-SIM-040:** Event Driven

**Requirement:**
When a command generates plans in simulation mode, the SimTest shall create valid plan structures without requiring infrastructure validation.

**Rationale:**
Plan generation logic must be testable independently.

**Verification:**
Generate plan with --simulate. Verify plan structure is valid and complete.

---

### Simulation Configuration

**REQ-SIM-041:** Ubiquitous

**Requirement:**
The SimTest shall support loading simulation scenarios from YAML configuration files.

**Rationale:**
Complex test scenarios should be reusable and version-controlled.

**Verification:**
Create simulation scenario file with preconfigured state and expected behaviors. Run simulation and verify it follows scenario.

---

**REQ-SIM-042:** Event Driven

**Requirement:**
When a simulation scenario specifies custom responses for operations, the SimTest shall use those responses instead of defaults.

**Rationale:**
Tests need to simulate specific conditions like network failures or permission errors.

**Verification:**
Configure scenario with SSH command failure. Verify simulation uses configured failure response.

---

**REQ-SIM-043:** Ubiquitous

**Requirement:**
The SimTest shall provide default simulated responses for all common operations (MongoDB commands, system commands, file operations).

**Rationale:**
Most simulations should work without extensive configuration.

**Verification:**
Run simulation without scenario file. Verify all common operations have reasonable default responses.

---

### Backwards Compatibility

**REQ-SIM-044:** Ubiquitous

**Requirement:**
The SimTest shall not affect existing command behavior when simulation mode is not active.

**Rationale:**
Adding simulation should not break existing functionality.

**Verification:**
Run full integration test suite without --simulate flag. Verify all tests pass.

---

**REQ-SIM-045:** Ubiquitous

**Requirement:**
The SimTest shall maintain the same Executor interface contract for both real and simulated executors.

**Rationale:**
Commands should work with any Executor implementation.

**Verification:**
Code review: verify SimulationExecutor satisfies Executor interface and maintains same method signatures.

---

**REQ-SIM-046-COMPAT:** Ubiquitous

**Requirement:**
The SimTest shall support `--dry-run` as an alias for `--simulate` for backwards compatibility.

**Rationale:**
Existing scripts and documentation may use --dry-run flag.

**Verification:**
Run command with --dry-run. Verify it behaves identically to --simulate with deprecation warning.

---

**REQ-SIM-047-COMPAT:** Event Driven

**Requirement:**
When a command is invoked with `--dry-run`, the SimTest shall emit a deprecation warning recommending `--simulate`.

**Rationale:**
Users should migrate to the new --simulate flag with clearer semantics.

**Verification:**
Run command with --dry-run. Verify deprecation warning appears in output.

---

### Documentation and Debugging

**REQ-SIM-048:** Event Driven

**Requirement:**
When a simulation fails, the SimTest shall output a detailed trace of all operations leading to the failure.

**Rationale:**
Debugging simulated failures requires understanding the sequence of operations.

**Verification:**
Cause simulation to fail. Verify output includes complete operation trace with parameters and intermediate state.

---

**REQ-SIM-049:** Ubiquitous

**Requirement:**
The SimTest shall support verbose mode (`--simulate-verbose`) that outputs detailed information about each simulated operation.

**Rationale:**
Deep debugging may require visibility into every simulated action.

**Verification:**
Run simulation with --simulate-verbose. Verify output includes details of every operation, parameter, and state change.

---

**REQ-SIM-050:** Event Driven

**Requirement:**
When simulation mode is active, the SimTest shall log all executor method calls with parameters to support debugging.

**Rationale:**
Developers need to understand what operations commands are attempting.

**Verification:**
Run simulation and review log. Verify all Executor method calls are logged with full parameters.

---

## Implementation Architecture

### Core Components

```
pkg/simulation/
  executor.go        - SimulationExecutor implementation
  filesystem.go      - In-memory filesystem simulation
  process.go         - Process state simulation
  network.go         - Network operation simulation
  state.go           - Simulated state tracking
  reporter.go        - Simulation result reporting
  scenario.go        - Scenario configuration loading

pkg/executor/
  interface.go       - Executor interface (unchanged)
  factory.go         - Executor factory supporting simulation mode
```

### Key Design Decisions

1. **Executor Interface Reuse**: SimulationExecutor implements same interface as LocalExecutor and SSHExecutor, enabling transparent substitution.

2. **In-Memory State**: All simulation state kept in memory for speed, with optional persistence for debugging.

3. **Configurable Behaviors**: Simulation scenarios allow customizing responses for specific operations.

4. **Layered Simulation**: Separate simulators for filesystem, process, and network allow independent testing.

5. **Fail-Fast vs Fail-Slow**: By default, simulation continues after errors to report all issues. Tests can opt into fail-fast behavior.

### Traceability

All implementation code MUST include EARS requirement IDs in comments:

```go
// REQ-SIM-016: SimulationExecutor implements Executor interface
type SimulationExecutor struct {
    fs      *FilesystemSimulator
    proc    *ProcessSimulator
    net     *NetworkSimulator
    state   *SimulationState
    config  *SimulationConfig
}

// REQ-SIM-017: Record operation and return simulated result
func (e *SimulationExecutor) Execute(ctx context.Context, host, command string) (string, error) {
    e.state.RecordOperation("execute", host, command)
    return e.config.GetResponse(host, command), nil
}
```

### Integration with Plan/Apply

```go
// In command execution:
var executor executor.Executor
if simulateMode {
    // REQ-SIM-016, REQ-SIM-026
    executor = simulation.NewExecutor(simulationConfig)
} else {
    executor = createRealExecutor(sshHost)
}

planner := deploy.NewPlanner(executor)
plan, err := planner.GeneratePlan(topology)

if simulateMode {
    // REQ-SIM-019: Skip infrastructure checks in simulation
    plan.SkipInfrastructureValidation = true
}

applier := apply.NewApplier(plan, executor)
state, err := applier.Apply(ctx)
```

### Testing Workflow

```bash
# Generate and review plan in simulation
mup cluster deploy test-cluster topology.yaml --version 7.0 --simulate --plan-only

# Apply simulated deploy to validate logic
mup cluster deploy test-cluster topology.yaml --version 7.0 --simulate --auto-approve

# Review simulation results
cat /tmp/simulation-results.json

# Run actual deploy after simulation validates logic
mup cluster deploy test-cluster topology.yaml --version 7.0 --auto-approve
```

### Token Efficiency Example

**Actual Deploy Output** (2000+ lines):
- SSH connection messages
- File upload progress
- Binary download progress
- Process startup logs
- MongoDB logs
- Health check details

**Simulated Deploy Output** (50 lines):
- [SIMULATION] Plan generated
- [SIMULATION] Phase 1: Prepare (5 operations)
- [SIMULATION] Phase 2: Deploy (3 operations)
- [SIMULATION] Phase 3: Initialize (2 operations)
- [SIMULATION] Summary: 10 operations would be executed
- [SIMULATION] Estimated time: 2m 30s

**Token Savings**: ~40x reduction in output tokens

---

## Future Enhancements

### Phase 2: Advanced Simulation

- **Time Travel**: Rewind and replay simulation from any point
- **Chaos Engineering**: Inject failures at random points to test resilience
- **Parallel Simulation**: Simulate multiple cluster states simultaneously
- **Performance Modeling**: Predict actual execution time based on simulation
- **Cost Estimation**: Estimate cloud costs before actual deployment

### Phase 3: Simulation UI

- **Interactive Simulation**: Step through operations one at a time
- **Visual State Inspection**: GUI showing simulated filesystem/process state
- **Scenario Builder**: Visual tool for creating simulation scenarios
- **Diff Viewer**: Compare simulation results across versions

---

## References

- **Plan/Apply System**: `docs/specs/PLAN_APPLY_SYSTEM.md`
- **Path Management**: `docs/specs/path-management-requirements.md`
- **Executor Interface**: `pkg/executor/interface.go`
- **Testing Philosophy**: TDD with EARS traceability per CLAUDE.md
