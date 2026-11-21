package apply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/plan"
)

const (
	applierTestCluster = "applier-test"
	opPrepare          = "prepare-001"
	opDeployFirst      = "deploy-001"
	opDeploySecond     = "deploy-002"
	opFinalize         = "finalize-001"
)

// fakeExecutor counts executions per operation and fails the IDs in failing.
type fakeExecutor struct {
	mu      sync.Mutex
	runs    map[string]int
	failing map[string]bool
}

func newFakeExecutor() *fakeExecutor {
	return &fakeExecutor{runs: map[string]int{}, failing: map[string]bool{}}
}

func (f *fakeExecutor) Validate(context.Context, *plan.PlannedOperation) error { return nil }

func (f *fakeExecutor) Execute(_ context.Context, op *plan.PlannedOperation) (*OperationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[op.ID]++
	if f.failing[op.ID] {
		return nil, errors.New("boom")
	}
	return &OperationResult{Success: true}, nil
}

func applierTestPlan() *plan.Plan {
	op := func(id string) plan.PlannedOperation {
		return plan.PlannedOperation{ID: id, Type: plan.OpCreateDirectory, Description: id}
	}
	return &plan.Plan{
		PlanID:      plan.NewPlanID(),
		Operation:   "deploy",
		ClusterName: applierTestCluster,
		Phases: []plan.PlannedPhase{
			{Name: "prepare", Order: 1, Operations: []plan.PlannedOperation{op(opPrepare)}},
			{Name: "deploy", Order: 2, Operations: []plan.PlannedOperation{op(opDeployFirst), op(opDeploySecond)}},
			{Name: "finalize", Order: 3, Operations: []plan.PlannedOperation{op(opFinalize)}},
		},
	}
}

// newFailedApply runs a plan whose second deploy operation fails and returns
// everything needed to resume it.
func newFailedApply(t *testing.T) (*DefaultApplier, *fakeExecutor, *StateManager, *plan.Plan, *ApplyState) {
	t.Helper()
	clusterDir := t.TempDir()
	stateManager := NewStateManager(clusterDir)
	p := applierTestPlan()
	planPath := stateManager.GetPlanPath(p.PlanID)
	require.NoError(t, os.MkdirAll(filepath.Dir(planPath), 0o755))
	require.NoError(t, p.SaveToFile(planPath))

	exec := newFakeExecutor()
	exec.failing[opDeploySecond] = true
	applier := NewDefaultApplier(exec, stateManager)

	state, err := applier.Apply(context.Background(), p)
	require.Error(t, err)
	require.NotNil(t, state)
	require.Equal(t, StatusFailed, state.Status)
	return applier, exec, stateManager, p, state
}

// LOCAL-013: resume loads the plan from <cluster-dir>/plans and runs only the
// operations that have not completed.
func TestResume_SkipsCompletedOperations(t *testing.T) {
	applier, exec, stateManager, _, failed := newFailedApply(t)
	exec.failing[opDeploySecond] = false

	loaded, err := stateManager.LoadState(failed.StateID)
	require.NoError(t, err)

	resumed, err := applier.Resume(context.Background(), loaded)

	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, resumed.Status)
	assert.Equal(t, map[string]int{
		opPrepare:      1,
		opDeployFirst:  1,
		opDeploySecond: 2,
		opFinalize:     1,
	}, exec.runs)
}

// LOCAL-014: a resume that cannot start leaves the apply resumable.
func TestResume_MissingPlanKeepsStateResumable(t *testing.T) {
	applier, _, stateManager, p, failed := newFailedApply(t)
	require.NoError(t, os.Remove(stateManager.GetPlanPath(p.PlanID)))

	_, err := applier.Resume(context.Background(), failed)

	require.Error(t, err)
	onDisk, loadErr := stateManager.LoadState(failed.StateID)
	require.NoError(t, loadErr)
	assert.True(t, onDisk.CanResume(), "status on disk: %s", onDisk.Status)
}

// LOCAL-014: a resume that fails again is still resumable.
func TestResume_FailureStaysResumable(t *testing.T) {
	applier, _, stateManager, _, failed := newFailedApply(t)

	_, err := applier.Resume(context.Background(), failed)

	require.Error(t, err)
	onDisk, loadErr := stateManager.LoadState(failed.StateID)
	require.NoError(t, loadErr)
	assert.Equal(t, StatusFailed, onDisk.Status)
}

// LOCAL-015: Apply always returns the state alongside an error.
func TestApply_FailureReturnsState(t *testing.T) {
	_, _, _, _, failed := newFailedApply(t)

	assert.NotEmpty(t, failed.StateID)
	assert.Equal(t, "deploy", failed.CurrentPhase)
}
