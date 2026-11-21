package operation

import (
	"context"
	"fmt"
	"time"

	"github.com/zph/mup/pkg/paths"

	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation"
	"github.com/zph/mup/pkg/supervisor"
)

// isSimulated reports whether exec is the simulation executor.
func isSimulated(exec executor.Executor) bool {
	_, ok := exec.(*simulation.SimulationExecutor)
	return ok
}

// simulatedResult records description on the simulation executor and returns
// a successful result without touching the real filesystem.
// LOCAL-008: handlers with side effects outside the Executor interface use this
// so --simulate makes no real changes.
func simulatedResult(exec executor.Executor, op *plan.PlannedOperation, description string) (*apply.OperationResult, error) {
	if _, err := exec.Execute(description); err != nil {
		return nil, fmt.Errorf("simulated %s failed: %w", op.Type, err)
	}
	return &apply.OperationResult{
		Success: true,
		Output:  "[simulated] " + description,
		Changes: op.Changes,
	}, nil
}

// simulatedFileResult is simulatedResult for operations that create a file:
// the path is registered in the simulated filesystem so later existence
// checks made through the executor see it.
func simulatedFileResult(exec executor.Executor, op *plan.PlannedOperation, path, description string) (*apply.OperationResult, error) {
	if err := exec.UploadContent([]byte(description), path); err != nil {
		return nil, fmt.Errorf("simulated %s failed: %w", op.Type, err)
	}
	return &apply.OperationResult{
		Success: true,
		Output:  "[simulated] " + description,
		Changes: op.Changes,
	}, nil
}

// simulatedSupervisordBinary stands in for the supervisord path in simulated commands.
const simulatedSupervisordBinary = "supervisord"

// supervisordBinaryPath returns the cached supervisord binary, downloading it
// if needed. Under simulation it returns a placeholder and downloads nothing.
func supervisordBinaryPath(exec executor.Executor) (string, error) {
	if isSimulated(exec) {
		return simulatedSupervisordBinary, nil
	}

	cacheDir, err := paths.BinCacheDir()
	if err != nil {
		return "", err
	}
	binaryPath, err := supervisor.GetSupervisordBinary(cacheDir)
	if err != nil {
		return "", fmt.Errorf("failed to get supervisord binary: %w", err)
	}
	return binaryPath, nil
}

// ensureSupervisorPortFree fails when the supervisor's control port is taken.
// The port is derived from the cluster directory, so two clusters can map to
// the same one; without this check the second supervisord exits at once, or
// supervisorctl ends up talking to the other cluster's daemon.
// LOCAL-026
func ensureSupervisorPortFree(exec executor.Executor, port int, clusterName string) error {
	available, err := exec.CheckPortAvailable(port)
	if err != nil {
		return fmt.Errorf("failed to check supervisor port %d: %w", port, err)
	}
	if !available {
		return fmt.Errorf("supervisor control port %d for cluster %s is already in use (another cluster's supervisor or an unrelated process holds it)", port, clusterName)
	}
	return nil
}

const (
	// supervisorStatusTimeout bounds a supervisor status query.
	supervisorStatusTimeout = 30 * time.Second
	// supervisorStartTimeout bounds one supervisor start call. A start
	// normally returns within the program's startsecs; a call that takes
	// minutes is stuck.
	supervisorStartTimeout = 2 * time.Minute
)

// executeBounded runs a command that is stopped if it outlives timeout.
// Executors that cannot stop a command run it unbounded.
// LOCAL-042
func executeBounded(ctx context.Context, exec executor.Executor, command string, timeout time.Duration) (string, error) {
	bounded, ok := exec.(executor.ContextExecutor)
	if !ok {
		return exec.Execute(command)
	}
	boundedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return bounded.ExecuteContext(boundedCtx, command)
}

// supervisorctl runs a supervisord control command against a cluster's
// supervisor and returns its output.
func supervisorctl(ctx context.Context, exec executor.Executor, configPath string, port int, args string, timeout time.Duration) (string, error) {
	binaryPath, err := supervisordBinaryPath(exec)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("%s ctl -c %s -s http://localhost:%d %s", binaryPath, configPath, port, args)
	return executeBounded(ctx, exec, command, timeout)
}

// supervisorResponding reports whether the supervisor answers on its control port.
// LOCAL-031
func supervisorResponding(ctx context.Context, exec executor.Executor, configPath string, port int) bool {
	_, err := supervisorctl(ctx, exec, configPath, port, "status", supervisorStatusTimeout)
	return err == nil
}

// supervisorProgramRunning reports whether the supervisor has a program in the RUNNING state.
// LOCAL-031
func supervisorProgramRunning(ctx context.Context, exec executor.Executor, configPath string, port int, programName string) bool {
	output, err := supervisorctl(ctx, exec, configPath, port, "status "+programName, supervisorStatusTimeout)
	if err != nil {
		return false
	}
	return supervisorReportsRunning(output)
}

// startOutcome decides the result of a supervisor start call. A call that
// errored or timed out still counts as started if the supervisor reports the
// program RUNNING, since that is what the operation is for.
// LOCAL-042
func startOutcome(programName string, startErr error, running func() bool) error {
	if startErr == nil {
		return nil
	}
	if running() {
		fmt.Printf("  ⚠️  Start call for %s did not return cleanly (%v), but the process is RUNNING\n", programName, startErr)
		return nil
	}
	return fmt.Errorf("failed to start process %s: %w", programName, startErr)
}

// supervisorReportsRunning interprets one line of supervisor status output.
func supervisorReportsRunning(statusOutput string) bool {
	return supervisor.ParseStatusState(statusOutput).IsRunning()
}
