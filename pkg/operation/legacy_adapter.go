package operation

import (
	"context"

	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/plan"
)

// LegacyHandler is a temporary interface for handlers that haven't been migrated yet
type LegacyHandler interface {
	Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error)
	Validate(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) error
}

// LegacyHandlerAdapter adapts old-style handlers to new four-phase interface
// TODO: Remove this once all handlers are migrated to four-phase pattern
type LegacyHandlerAdapter struct {
	legacy LegacyHandler
}

// NewLegacyHandlerAdapter creates an adapter for an unmigrated handler
func NewLegacyHandlerAdapter(legacy LegacyHandler) *LegacyHandlerAdapter {
	return &LegacyHandlerAdapter{legacy: legacy}
}

// IsComplete always returns false (no resume support for legacy handlers)
func (a *LegacyHandlerAdapter) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	return false, nil
}

// PreHook delegates to Validate
func (a *LegacyHandlerAdapter) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()
	if err := a.legacy.Validate(ctx, op, exec); err != nil {
		result.AddError(err.Error())
	}
	return result, nil
}

// Execute delegates to legacy Execute
func (a *LegacyHandlerAdapter) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	return a.legacy.Execute(ctx, op, exec)
}

// PostHook does no verification (legacy handlers don't have post-hooks)
func (a *LegacyHandlerAdapter) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	return NewHookResult(), nil
}
