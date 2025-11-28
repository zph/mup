package operation_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zph/mup/pkg/operation"
	"github.com/zph/mup/pkg/plan"
)

// REQ-PES-036, REQ-PES-047, REQ-PES-048: Test CreateSymlinkHandler
func TestCreateSymlinkHandler(t *testing.T) {
	// Create temporary directory structure for testing
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "target-dir")
	if err := os.Mkdir(targetPath, 0755); err != nil {
		t.Fatalf("Failed to create target directory: %v", err)
	}

	linkPath := filepath.Join(tmpDir, "link")

	// Create handler
	handler := &operation.CreateSymlinkHandler{}

	// Create test operation
	op := &plan.PlannedOperation{
		ID:          "test-symlink-001",
		Type:        plan.OpCreateSymlink,
		Description: "Create test symlink",
		Params: map[string]interface{}{
			"link_path":   linkPath,
			"target_path": targetPath,
		},
	}

	ctx := context.Background()

	// Phase 1: IsComplete (should be false initially)
	completed, err := handler.IsComplete(ctx, op, nil)
	if err != nil {
		t.Fatalf("IsComplete error: %v", err)
	}
	if completed {
		t.Error("IsComplete should be false before creation")
	}

	// Phase 2: PreHook (should validate and pass)
	preResult, err := handler.PreHook(ctx, op, nil)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}
	if !preResult.Valid {
		t.Errorf("PreHook should be valid, errors: %v", preResult.Errors)
	}

	// Phase 3: Execute
	result, err := handler.Execute(ctx, op, nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Error("Execute should succeed")
	}

	// Phase 4: PostHook (should verify)
	postResult, err := handler.PostHook(ctx, op, nil)
	if err != nil {
		t.Fatalf("PostHook error: %v", err)
	}
	if !postResult.Valid {
		t.Errorf("PostHook should be valid, errors: %v", postResult.Errors)
	}
	if postResult.Metadata["verified"] != true {
		t.Error("PostHook should mark as verified")
	}

	// Phase 1 Again: IsComplete (should be true now)
	completed, err = handler.IsComplete(ctx, op, nil)
	if err != nil {
		t.Fatalf("IsComplete error after creation: %v", err)
	}
	if !completed {
		t.Error("IsComplete should be true after creation")
	}
}

// REQ-PES-033: Test idempotency - creating existing symlink should succeed
func TestCreateSymlinkHandler_Idempotency(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "target-dir")
	if err := os.Mkdir(targetPath, 0755); err != nil {
		t.Fatalf("Failed to create target directory: %v", err)
	}

	linkPath := filepath.Join(tmpDir, "link")

	// Pre-create symlink
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	handler := &operation.CreateSymlinkHandler{}

	op := &plan.PlannedOperation{
		ID:   "test-symlink-002",
		Type: plan.OpCreateSymlink,
		Params: map[string]interface{}{
			"link_path":   linkPath,
			"target_path": targetPath,
		},
	}

	ctx := context.Background()

	// IsComplete should detect existing symlink
	completed, err := handler.IsComplete(ctx, op, nil)
	if err != nil {
		t.Fatalf("IsComplete error: %v", err)
	}
	if !completed {
		t.Error("IsComplete should detect existing symlink")
	}

	// PreHook should warn about existing symlink
	preResult, err := handler.PreHook(ctx, op, nil)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}
	if !preResult.Valid {
		t.Errorf("PreHook should be valid, errors: %v", preResult.Errors)
	}
	if len(preResult.Warnings) == 0 {
		t.Error("PreHook should warn about existing symlink")
	}

	// Execute should still succeed (idempotent)
	result, err := handler.Execute(ctx, op, nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Error("Execute should succeed on existing symlink")
	}

	// PostHook should verify
	postResult, err := handler.PostHook(ctx, op, nil)
	if err != nil {
		t.Fatalf("PostHook error: %v", err)
	}
	if !postResult.Valid {
		t.Errorf("PostHook should be valid, errors: %v", postResult.Errors)
	}
}

// REQ-PES-047: Test PreHook catches missing target
func TestCreateSymlinkHandler_PreHookValidation(t *testing.T) {
	tmpDir := t.TempDir()
	linkPath := filepath.Join(tmpDir, "link")

	handler := &operation.CreateSymlinkHandler{}

	op := &plan.PlannedOperation{
		ID:   "test-symlink-003",
		Type: plan.OpCreateSymlink,
		Params: map[string]interface{}{
			"link_path":   linkPath,
			"target_path": "/nonexistent/target",
		},
	}

	ctx := context.Background()

	// PreHook should fail validation
	preResult, err := handler.PreHook(ctx, op, nil)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}

	if preResult.Valid {
		t.Error("PreHook should return invalid for missing target")
	}

	if len(preResult.Errors) == 0 {
		t.Error("PreHook should have errors for missing target")
	}
}

// REQ-PES-036: Test symlink update (different target)
func TestCreateSymlinkHandler_UpdateTarget(t *testing.T) {
	tmpDir := t.TempDir()

	// Create two target directories
	oldTarget := filepath.Join(tmpDir, "old-target")
	newTarget := filepath.Join(tmpDir, "new-target")
	if err := os.Mkdir(oldTarget, 0755); err != nil {
		t.Fatalf("Failed to create old target: %v", err)
	}
	if err := os.Mkdir(newTarget, 0755); err != nil {
		t.Fatalf("Failed to create new target: %v", err)
	}

	linkPath := filepath.Join(tmpDir, "link")

	// Create symlink pointing to old target
	if err := os.Symlink(oldTarget, linkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	handler := &operation.CreateSymlinkHandler{}

	// Operation to update to new target
	op := &plan.PlannedOperation{
		ID:   "test-symlink-004",
		Type: plan.OpCreateSymlink,
		Params: map[string]interface{}{
			"link_path":   linkPath,
			"target_path": newTarget,
		},
	}

	ctx := context.Background()

	// IsComplete should be false (points to wrong target)
	completed, err := handler.IsComplete(ctx, op, nil)
	if err != nil {
		t.Fatalf("IsComplete error: %v", err)
	}
	if completed {
		t.Error("IsComplete should be false when target differs")
	}

	// PreHook should warn about update
	preResult, err := handler.PreHook(ctx, op, nil)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}
	if !preResult.Valid {
		t.Errorf("PreHook should be valid, errors: %v", preResult.Errors)
	}
	if len(preResult.Warnings) == 0 {
		t.Error("PreHook should warn about updating symlink target")
	}

	// Execute should update the symlink
	result, err := handler.Execute(ctx, op, nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Error("Execute should succeed")
	}

	// Verify symlink now points to new target
	currentTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Failed to read symlink: %v", err)
	}
	if currentTarget != newTarget {
		t.Errorf("Symlink should point to new target, got: %s, want: %s", currentTarget, newTarget)
	}

	// PostHook should verify
	postResult, err := handler.PostHook(ctx, op, nil)
	if err != nil {
		t.Fatalf("PostHook error: %v", err)
	}
	if !postResult.Valid {
		t.Errorf("PostHook should be valid, errors: %v", postResult.Errors)
	}
}

// REQ-PES-047: Test relative symlink paths
func TestCreateSymlinkHandler_RelativePath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create target directory
	targetPath := filepath.Join(tmpDir, "v3.6")
	if err := os.Mkdir(targetPath, 0755); err != nil {
		t.Fatalf("Failed to create target directory: %v", err)
	}

	linkPath := filepath.Join(tmpDir, "current")

	handler := &operation.CreateSymlinkHandler{}

	// Use relative path (like version symlinks)
	op := &plan.PlannedOperation{
		ID:   "test-symlink-005",
		Type: plan.OpCreateSymlink,
		Params: map[string]interface{}{
			"link_path":   linkPath,
			"target_path": "v3.6", // Relative path
		},
	}

	ctx := context.Background()

	// PreHook should not fail on relative path (can't validate without working dir)
	preResult, err := handler.PreHook(ctx, op, nil)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}
	if !preResult.Valid {
		t.Errorf("PreHook should be valid for relative paths, errors: %v", preResult.Errors)
	}

	// Execute should succeed
	result, err := handler.Execute(ctx, op, nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Error("Execute should succeed with relative path")
	}

	// Verify symlink was created with relative target
	currentTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Failed to read symlink: %v", err)
	}
	if currentTarget != "v3.6" {
		t.Errorf("Symlink should have relative target, got: %s, want: v3.6", currentTarget)
	}
}
