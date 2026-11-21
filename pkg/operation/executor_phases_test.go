package operation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation"
)

const phaseTestOp plan.OperationType = "phase_test"

// phaseHandler records which handler phases ran.
type phaseHandler struct {
	complete    bool
	completeErr error
	postValid   bool
	postErr     error
	calls       []string
}

func (h *phaseHandler) IsComplete(context.Context, *plan.PlannedOperation, executor.Executor) (bool, error) {
	h.calls = append(h.calls, "is_complete")
	return h.complete, h.completeErr
}

func (h *phaseHandler) PreHook(context.Context, *plan.PlannedOperation, executor.Executor) (*HookResult, error) {
	h.calls = append(h.calls, "pre")
	return NewHookResult(), nil
}

func (h *phaseHandler) Execute(context.Context, *plan.PlannedOperation, executor.Executor) (*apply.OperationResult, error) {
	h.calls = append(h.calls, "execute")
	return &apply.OperationResult{Success: true}, nil
}

func (h *phaseHandler) PostHook(context.Context, *plan.PlannedOperation, executor.Executor) (*HookResult, error) {
	h.calls = append(h.calls, "post")
	if h.postErr != nil {
		return nil, h.postErr
	}
	result := NewHookResult()
	if !h.postValid {
		result.AddError("not verified")
	}
	return result, nil
}

func newPhaseExecutor(handler *phaseHandler, exec executor.Executor) (*Executor, *plan.PlannedOperation) {
	e := &Executor{
		executors: map[string]executor.Executor{"localhost": exec},
		handlers:  map[plan.OperationType]OperationHandler{phaseTestOp: handler},
	}
	op := &plan.PlannedOperation{
		ID:   "phase-001",
		Type: phaseTestOp,
		// A required check that can never pass: only a skipped operation gets by it.
		PreConditions: []plan.SafetyCheck{{ID: "impossible", CheckType: "unknown_check", Required: true}},
	}
	return e, op
}

// LOCAL-030: an operation whose effect is already in place is skipped,
// including its pre-conditions, so a resume does not trip over its own work.
func TestExecutor_SkipsCompletedOperation(t *testing.T) {
	handler := &phaseHandler{complete: true, postValid: true}
	e, op := newPhaseExecutor(handler, executor.NewLocalExecutor())

	require.NoError(t, e.Validate(context.Background(), op))
	result, err := e.Execute(context.Background(), op)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.NotContains(t, handler.calls, "execute")
	assert.NotContains(t, handler.calls, "pre")
	assert.NotContains(t, handler.calls, "post")
}

// LOCAL-030: an incomplete operation runs every phase in order.
func TestExecutor_RunsAllPhases(t *testing.T) {
	handler := &phaseHandler{postValid: true}
	e, op := newPhaseExecutor(handler, executor.NewLocalExecutor())
	op.PreConditions = nil

	require.NoError(t, e.Validate(context.Background(), op))
	_, err := e.Execute(context.Background(), op)

	require.NoError(t, err)
	assert.Equal(t, []string{"is_complete", "pre", "is_complete", "execute", "post"}, handler.calls)
}

// LOCAL-030: failed post-verification fails the operation.
func TestExecutor_PostHookFailureFailsOperation(t *testing.T) {
	for name, handler := range map[string]*phaseHandler{
		"invalid result": {postValid: false},
		"hook error":     {postValid: true, postErr: errors.New("boom")},
	} {
		t.Run(name, func(t *testing.T) {
			e, op := newPhaseExecutor(handler, executor.NewLocalExecutor())

			_, err := e.Execute(context.Background(), op)

			require.Error(t, err)
			assert.Contains(t, handler.calls, "execute")
		})
	}
}

// LOCAL-030: a failing IsComplete check means "not complete", never "skip".
func TestExecutor_IsCompleteErrorDoesNotSkip(t *testing.T) {
	handler := &phaseHandler{complete: true, completeErr: errors.New("cannot tell"), postValid: true}
	e, op := newPhaseExecutor(handler, executor.NewLocalExecutor())

	_, err := e.Execute(context.Background(), op)

	require.NoError(t, err)
	assert.Contains(t, handler.calls, "execute")
}

// LOCAL-030: simulation plans every operation; real-state checks are not consulted.
func TestExecutor_SimulationRunsEveryOperation(t *testing.T) {
	handler := &phaseHandler{complete: true, postValid: false}
	e, op := newPhaseExecutor(handler, simulation.NewExecutor(simulation.NewConfig()))

	_, err := e.Execute(context.Background(), op)

	require.NoError(t, err)
	assert.Equal(t, []string{"execute"}, handler.calls)
}

// LOCAL-031
func TestSupervisorReportsRunning(t *testing.T) {
	assert.True(t, supervisorReportsRunning("mongod-30000  Running  pid 42, uptime 0:00:05"))
	assert.True(t, supervisorReportsRunning("mongod-30000  \x1b[0;32mRUNNING\x1b[0m  pid 42"))
	assert.False(t, supervisorReportsRunning("mongod-30000  Stopped  Not started"))
	assert.False(t, supervisorReportsRunning("mongod-30000  Fatal  Exited too quickly"))
	assert.False(t, supervisorReportsRunning(""))
	assert.False(t, supervisorReportsRunning("unix:///tmp/x.sock refused connection"))
}
