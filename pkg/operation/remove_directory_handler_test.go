package operation_test

import (
	"context"
	"testing"

	"github.com/zph/mup/pkg/operation"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation"
)

// REQ-PES-036, REQ-PES-047, REQ-PES-048: Test RemoveDirectoryHandler
func TestRemoveDirectoryHandler(t *testing.T) {
	// Setup simulation with existing directory
	simConfig := simulation.NewConfig()
	simConfig.AddExistingDirectory("/test/remove-me")
	exec := simulation.NewExecutor(simConfig)

	handler := &operation.RemoveDirectoryHandler{}

	op := &plan.PlannedOperation{
		ID:          "test-remove-001",
		Type:        plan.OpRemoveDirectory,
		Description: "Remove test directory",
		Params: map[string]interface{}{
			"path":      "/test/remove-me",
			"recursive": false,
		},
	}

	ctx := context.Background()

	// Phase 1: IsComplete (should be false initially - directory exists)
	completed, err := handler.IsComplete(ctx, op, exec)
	if err != nil {
		t.Fatalf("IsComplete error: %v", err)
	}
	if completed {
		t.Error("IsComplete should be false before removal")
	}

	// Phase 2: PreHook (should validate and pass)
	preResult, err := handler.PreHook(ctx, op, exec)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}
	if !preResult.Valid {
		t.Errorf("PreHook should be valid, errors: %v", preResult.Errors)
	}

	// Phase 3: Execute
	result, err := handler.Execute(ctx, op, exec)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Error("Execute should succeed")
	}

	// Phase 4: PostHook (should verify)
	postResult, err := handler.PostHook(ctx, op, exec)
	if err != nil {
		t.Fatalf("PostHook error: %v", err)
	}
	if !postResult.Valid {
		t.Errorf("PostHook should be valid, errors: %v", postResult.Errors)
	}
	if postResult.Metadata["verified"] != true {
		t.Error("PostHook should mark as verified")
	}

	// Phase 1 Again: IsComplete (should be true now - directory removed)
	completed, err = handler.IsComplete(ctx, op, exec)
	if err != nil {
		t.Fatalf("IsComplete error after removal: %v", err)
	}
	if !completed {
		t.Error("IsComplete should be true after removal")
	}
}

// REQ-PES-033: Test idempotency - removing non-existent directory should succeed
func TestRemoveDirectoryHandler_Idempotency(t *testing.T) {
	// Setup simulation without the directory
	simConfig := simulation.NewConfig()
	exec := simulation.NewExecutor(simConfig)

	handler := &operation.RemoveDirectoryHandler{}

	op := &plan.PlannedOperation{
		ID:   "test-remove-002",
		Type: plan.OpRemoveDirectory,
		Params: map[string]interface{}{
			"path":      "/test/already-removed",
			"recursive": false,
		},
	}

	ctx := context.Background()

	// IsComplete should detect directory already removed
	completed, err := handler.IsComplete(ctx, op, exec)
	if err != nil {
		t.Fatalf("IsComplete error: %v", err)
	}
	if !completed {
		t.Error("IsComplete should detect directory already removed")
	}

	// PreHook should warn about already removed
	preResult, err := handler.PreHook(ctx, op, exec)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}
	if !preResult.Valid {
		t.Errorf("PreHook should be valid, errors: %v", preResult.Errors)
	}
	if len(preResult.Warnings) == 0 {
		t.Error("PreHook should warn about directory already removed")
	}
}

// REQ-PES-047: Test PreHook validates empty path
func TestRemoveDirectoryHandler_PreHookValidation(t *testing.T) {
	simConfig := simulation.NewConfig()
	exec := simulation.NewExecutor(simConfig)

	handler := &operation.RemoveDirectoryHandler{}

	op := &plan.PlannedOperation{
		ID:   "test-remove-003",
		Type: plan.OpRemoveDirectory,
		Params: map[string]interface{}{
			"path":      "",
			"recursive": false,
		},
	}

	ctx := context.Background()

	// PreHook should fail validation
	preResult, err := handler.PreHook(ctx, op, exec)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}

	if preResult.Valid {
		t.Error("PreHook should return invalid for empty path")
	}

	if len(preResult.Errors) == 0 {
		t.Error("PreHook should have errors for empty path")
	}
}

// REQ-PES-047: Test recursive removal warning
func TestRemoveDirectoryHandler_RecursiveWarning(t *testing.T) {
	// Setup simulation with existing directory
	simConfig := simulation.NewConfig()
	simConfig.AddExistingDirectory("/test/remove-recursive")
	exec := simulation.NewExecutor(simConfig)

	handler := &operation.RemoveDirectoryHandler{}

	op := &plan.PlannedOperation{
		ID:   "test-remove-004",
		Type: plan.OpRemoveDirectory,
		Params: map[string]interface{}{
			"path":      "/test/remove-recursive",
			"recursive": true,
		},
	}

	ctx := context.Background()

	// PreHook should warn about recursive removal
	preResult, err := handler.PreHook(ctx, op, exec)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}
	if !preResult.Valid {
		t.Errorf("PreHook should be valid, errors: %v", preResult.Errors)
	}

	// Check for recursive warning
	hasRecursiveWarning := false
	for _, warning := range preResult.Warnings {
		if len(warning) > 0 && (warning[0:9] == "recursive" || warning[0:9] == "Recursive") {
			hasRecursiveWarning = true
			break
		}
	}
	if !hasRecursiveWarning {
		t.Error("PreHook should warn about recursive removal")
	}

	// Execute should succeed
	result, err := handler.Execute(ctx, op, exec)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Error("Execute should succeed with recursive removal")
	}
}

