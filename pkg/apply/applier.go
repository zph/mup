package apply

import (
	"context"
	"fmt"

	"github.com/zph/mup/pkg/plan"
)

// Applier executes a plan
type Applier interface {
	// Apply executes the plan
	Apply(ctx context.Context, p *plan.Plan) (*ApplyState, error)

	// Resume resumes a paused or failed apply from a checkpoint
	Resume(ctx context.Context, state *ApplyState) (*ApplyState, error)

	// Pause pauses the current apply
	Pause(ctx context.Context) error

	// Rollback rolls back to a checkpoint
	Rollback(ctx context.Context, checkpointID string) error
}

// OperationExecutor executes individual operations
type OperationExecutor interface {
	// Execute executes a single operation
	Execute(ctx context.Context, op *plan.PlannedOperation) (*OperationResult, error)

	// Validate performs runtime safety checks before executing
	Validate(ctx context.Context, op *plan.PlannedOperation) error
}

// DefaultApplier is the default implementation of the Applier interface
type DefaultApplier struct {
	executor     OperationExecutor
	stateManager *StateManager
	hooks        *HookManager
	checkpointer *Checkpointer
	paused       bool
}

// NewDefaultApplier creates a new default applier
func NewDefaultApplier(executor OperationExecutor, stateManager *StateManager) *DefaultApplier {
	return &DefaultApplier{
		executor:     executor,
		stateManager: stateManager,
		hooks:        NewHookManager(),
		checkpointer: NewCheckpointer(stateManager),
		paused:       false,
	}
}

// Apply executes the plan
func (a *DefaultApplier) Apply(ctx context.Context, p *plan.Plan) (*ApplyState, error) {
	// Create new apply state
	state := NewApplyState(p.PlanID, p.ClusterName, p.Operation)
	state.UpdateStatus(StatusRunning)

	// Save initial state
	if err := a.stateManager.SaveState(state); err != nil {
		return nil, fmt.Errorf("failed to save initial state: %w", err)
	}

	// Execute before_apply hook
	if err := a.hooks.ExecuteHook(ctx, plan.HookBeforeApply, p, state); err != nil {
		state.Log("error", "", "", fmt.Sprintf("before_apply hook failed: %v", err))
		// Continue anyway unless hook is critical
	}

	// Execute each phase
	for _, phase := range p.Phases {
		if err := a.executePhase(ctx, &phase, p, state); err != nil {
			state.UpdateStatus(StatusFailed)
			state.FailPhase(phase.Name, err)
			if err := a.stateManager.SaveState(state); err != nil {
				state.Log("error", phase.Name, "", fmt.Sprintf("failed to save state: %v", err))
			}

			// Execute on_error hook
			if hookErr := a.hooks.ExecuteHook(ctx, plan.HookOnError, p, state); hookErr != nil {
				state.Log("error", phase.Name, "", fmt.Sprintf("on_error hook failed: %v", hookErr))
			}

			return state, fmt.Errorf("phase %s failed: %w", phase.Name, err)
		}

		// Check if paused
		if a.paused {
			state.UpdateStatus(StatusPaused)
			if err := a.stateManager.SaveState(state); err != nil {
				return nil, fmt.Errorf("failed to save paused state: %w", err)
			}
			return state, fmt.Errorf("apply paused by user")
		}
	}

	// Mark as completed
	state.UpdateStatus(StatusCompleted)
	if err := a.stateManager.SaveState(state); err != nil {
		return nil, fmt.Errorf("failed to save completed state: %w", err)
	}

	// Execute after_apply hook
	if err := a.hooks.ExecuteHook(ctx, plan.HookAfterApply, p, state); err != nil {
		state.Log("error", "", "", fmt.Sprintf("after_apply hook failed: %v", err))
		// Don't fail the apply for post-completion hook failures
	}

	// Execute on_success hook
	if err := a.hooks.ExecuteHook(ctx, plan.HookOnSuccess, p, state); err != nil {
		state.Log("error", "", "", fmt.Sprintf("on_success hook failed: %v", err))
	}

	return state, nil
}

// executePhase executes a single phase
func (a *DefaultApplier) executePhase(ctx context.Context, phase *plan.PlannedPhase, p *plan.Plan, state *ApplyState) error {
	state.StartPhase(phase.Name)

	// Execute before_phase hook
	if phase.BeforeHook != nil {
		if err := a.hooks.ExecuteCustomHook(ctx, phase.BeforeHook, p, state); err != nil {
			if !phase.BeforeHook.ContinueOnError {
				return fmt.Errorf("before_phase hook failed: %w", err)
			}
			state.Log("warn", phase.Name, "", fmt.Sprintf("before_phase hook failed but continuing: %v", err))
		}
	}

	// Group operations by parallelization
	parallelGroups := a.groupOperationsByParallelization(phase.Operations)

	// Execute each group
	for groupIndex, group := range parallelGroups {
		if err := a.executeOperationGroup(ctx, group, p, state); err != nil {
			return fmt.Errorf("operation group %d failed: %w", groupIndex, err)
		}
	}

	// Execute after_phase hook
	if phase.AfterHook != nil {
		if err := a.hooks.ExecuteCustomHook(ctx, phase.AfterHook, p, state); err != nil {
			if !phase.AfterHook.ContinueOnError {
				return fmt.Errorf("after_phase hook failed: %w", err)
			}
			state.Log("warn", phase.Name, "", fmt.Sprintf("after_phase hook failed but continuing: %v", err))
		}
	}

	// Create checkpoint after phase
	if err := a.checkpointer.CreateCheckpoint(state, fmt.Sprintf("Completed phase: %s", phase.Name)); err != nil {
		state.Log("warn", phase.Name, "", fmt.Sprintf("failed to create checkpoint: %v", err))
		// Don't fail the phase for checkpoint failures
	}

	state.CompletePhase(phase.Name)
	if err := a.stateManager.SaveState(state); err != nil {
		return fmt.Errorf("failed to save state after phase: %w", err)
	}

	return nil
}

// groupOperationsByParallelization groups operations that can run in parallel
func (a *DefaultApplier) groupOperationsByParallelization(operations []plan.PlannedOperation) [][]plan.PlannedOperation {
	if len(operations) == 0 {
		return nil
	}

	groups := make([][]plan.PlannedOperation, 0)
	currentGroup := make([]plan.PlannedOperation, 0)

	for _, op := range operations {
		if op.Parallel && len(currentGroup) > 0 && currentGroup[0].Parallel {
			// Add to current parallel group
			currentGroup = append(currentGroup, op)
		} else {
			// Start new group
			if len(currentGroup) > 0 {
				groups = append(groups, currentGroup)
			}
			currentGroup = []plan.PlannedOperation{op}
		}
	}

	// Add last group
	if len(currentGroup) > 0 {
		groups = append(groups, currentGroup)
	}

	return groups
}

// executeOperationGroup executes a group of operations (parallel or sequential)
func (a *DefaultApplier) executeOperationGroup(ctx context.Context, operations []plan.PlannedOperation, p *plan.Plan, state *ApplyState) error {
	if len(operations) == 0 {
		return nil
	}

	// Check if this group can run in parallel
	if operations[0].Parallel && len(operations) > 1 {
		return a.executeParallel(ctx, operations, p, state)
	}

	// Execute sequentially
	for _, op := range operations {
		if err := a.executeOperation(ctx, &op, p, state); err != nil {
			return err
		}
	}

	return nil
}

// executeParallel executes operations in parallel
func (a *DefaultApplier) executeParallel(ctx context.Context, operations []plan.PlannedOperation, p *plan.Plan, state *ApplyState) error {
	type result struct {
		op  plan.PlannedOperation
		err error
	}

	results := make(chan result, len(operations))

	// Start all operations
	for _, op := range operations {
		go func(operation plan.PlannedOperation) {
			err := a.executeOperation(ctx, &operation, p, state)
			results <- result{op: operation, err: err}
		}(op)
	}

	// Wait for all to complete
	var firstError error
	for i := 0; i < len(operations); i++ {
		res := <-results
		if res.err != nil && firstError == nil {
			firstError = res.err
		}
	}

	return firstError
}

// executeOperation executes a single operation
func (a *DefaultApplier) executeOperation(ctx context.Context, op *plan.PlannedOperation, p *plan.Plan, state *ApplyState) error {
	state.StartOperation(op.ID)
	state.Log("info", state.CurrentPhase, op.ID, fmt.Sprintf("Executing: %s", op.Description))

	// Validate pre-conditions (runtime safety checks)
	if err := a.executor.Validate(ctx, op); err != nil {
		state.FailOperation(op.ID, fmt.Errorf("pre-condition check failed: %w", err), true)
		if err := a.stateManager.SaveState(state); err != nil {
			state.Log("error", state.CurrentPhase, op.ID, fmt.Sprintf("failed to save state: %v", err))
		}
		return fmt.Errorf("operation %s pre-condition check failed: %w", op.ID, err)
	}

	// Execute the operation
	result, err := a.executor.Execute(ctx, op)
	if err != nil {
		state.FailOperation(op.ID, err, false)
		if saveErr := a.stateManager.SaveState(state); saveErr != nil {
			state.Log("error", state.CurrentPhase, op.ID, fmt.Sprintf("failed to save state: %v", saveErr))
		}
		return fmt.Errorf("operation %s failed: %w", op.ID, err)
	}

	// Mark as completed
	state.CompleteOperation(op.ID, result)
	state.Log("info", state.CurrentPhase, op.ID, fmt.Sprintf("Completed: %s", op.Description))

	// Save state after each operation
	if err := a.stateManager.SaveState(state); err != nil {
		state.Log("warn", state.CurrentPhase, op.ID, fmt.Sprintf("failed to save state: %v", err))
		// Don't fail the operation for state save failures
	}

	return nil
}

// Resume resumes a paused or failed apply from a checkpoint
func (a *DefaultApplier) Resume(ctx context.Context, state *ApplyState) (*ApplyState, error) {
	if !state.CanResume() {
		return nil, fmt.Errorf("cannot resume apply in status: %s", state.Status)
	}

	state.UpdateStatus(StatusRunning)
	if err := a.stateManager.SaveState(state); err != nil {
		return nil, fmt.Errorf("failed to save resumed state: %w", err)
	}

	// Load the original plan
	planPath := a.stateManager.GetPlanPath(state.PlanID)
	p, err := plan.LoadFromFile(planPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load plan: %w", err)
	}

	// Find where we left off and continue
	currentPhaseIndex := a.findPhaseIndex(p, state.CurrentPhase)
	if currentPhaseIndex == -1 {
		return nil, fmt.Errorf("could not find current phase: %s", state.CurrentPhase)
	}

	// Resume from current phase
	for i := currentPhaseIndex; i < len(p.Phases); i++ {
		phase := p.Phases[i]
		if err := a.executePhase(ctx, &phase, p, state); err != nil {
			state.UpdateStatus(StatusFailed)
			if saveErr := a.stateManager.SaveState(state); saveErr != nil {
				state.Log("error", phase.Name, "", fmt.Sprintf("failed to save state: %v", saveErr))
			}
			return state, fmt.Errorf("phase %s failed: %w", phase.Name, err)
		}
	}

	state.UpdateStatus(StatusCompleted)
	if err := a.stateManager.SaveState(state); err != nil {
		return nil, fmt.Errorf("failed to save completed state: %w", err)
	}

	return state, nil
}

// Pause pauses the current apply
func (a *DefaultApplier) Pause(ctx context.Context) error {
	a.paused = true
	return nil
}

// Rollback rolls back to a checkpoint
func (a *DefaultApplier) Rollback(ctx context.Context, checkpointID string) error {
	// TODO: Implement rollback logic
	return fmt.Errorf("rollback not yet implemented")
}

// findPhaseIndex finds the index of a phase by name
func (a *DefaultApplier) findPhaseIndex(p *plan.Plan, phaseName string) int {
	for i, phase := range p.Phases {
		if phase.Name == phaseName {
			return i
		}
	}
	return -1
}

// GetPlanPath returns the path to a plan file (helper for StateManager)
func (m *StateManager) GetPlanPath(planID string) string {
	// Plans are stored in the cluster's plans directory
	return fmt.Sprintf("%s/plans/%s.json", m.clusterDir, planID)
}
