package operation_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zph/mup/pkg/operation"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation"
)

// REQ-PES-036, REQ-PES-047, REQ-PES-048: Test UploadFileHandler
func TestUploadFileHandler(t *testing.T) {
	// Create a temporary local file for testing
	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "test.conf")
	if err := os.WriteFile(localPath, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Setup simulation executor
	simConfig := simulation.NewConfig()
	exec := simulation.NewExecutor(simConfig)

	// Create handler
	handler := &operation.UploadFileHandler{}

	// Create test operation
	op := &plan.PlannedOperation{
		ID:          "test-upload-001",
		Type:        plan.OpUploadFile,
		Description: "Upload test file",
		Target: plan.OperationTarget{
			Type: "filesystem",
			Name: "/remote/path",
		},
		Params: map[string]interface{}{
			"local_path":  localPath,
			"remote_path": "/remote/test.conf",
		},
	}

	// Run full test suite
	suite := NewHandlerTestSuite(t, handler, exec)
	suite.RunAll(op)
}

// REQ-PES-033: Test idempotency - uploading existing file should succeed
func TestUploadFileHandler_Idempotency(t *testing.T) {
	// Create local file
	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "test.conf")
	if err := os.WriteFile(localPath, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Pre-upload file in simulation
	simConfig := simulation.NewConfig()
	simConfig.AddExistingFile("/remote/test.conf", []byte("old content"))
	exec := simulation.NewExecutor(simConfig)

	handler := &operation.UploadFileHandler{}

	op := &plan.PlannedOperation{
		ID:   "test-upload-002",
		Type: plan.OpUploadFile,
		Params: map[string]interface{}{
			"local_path":  localPath,
			"remote_path": "/remote/test.conf",
		},
	}

	suite := NewHandlerTestSuite(t, handler, exec)

	// IsComplete should detect existing file
	completed, err := handler.IsComplete(suite.ctx, op, exec)
	if err != nil {
		t.Fatalf("IsComplete error: %v", err)
	}
	if !completed {
		t.Error("IsComplete should detect existing file")
	}

	// PreHook should warn about overwrite
	preResult, err := handler.PreHook(suite.ctx, op, exec)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}
	if len(preResult.Warnings) == 0 {
		t.Error("PreHook should warn about overwriting existing file")
	}
}

// REQ-PES-047: Test PreHook catches missing local file
func TestUploadFileHandler_PreHookValidation(t *testing.T) {
	simConfig := simulation.NewConfig()
	exec := simulation.NewExecutor(simConfig)

	handler := &operation.UploadFileHandler{}

	op := &plan.PlannedOperation{
		ID:   "test-upload-003",
		Type: plan.OpUploadFile,
		Params: map[string]interface{}{
			"local_path":  "/nonexistent/file.conf",
			"remote_path": "/remote/test.conf",
		},
	}

	// PreHook should fail validation
	preResult, err := handler.PreHook(context.Background(), op, exec)
	if err != nil {
		t.Fatalf("PreHook error: %v", err)
	}

	if preResult.Valid {
		t.Error("PreHook should return invalid for missing local file")
	}

	if len(preResult.Errors) == 0 {
		t.Error("PreHook should have errors for missing local file")
	}
}

// REQ-PES-048: Test PostHook verification
func TestUploadFileHandler_PostHook(t *testing.T) {
	// Create local file
	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "test.conf")
	if err := os.WriteFile(localPath, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	simConfig := simulation.NewConfig()
	exec := simulation.NewExecutor(simConfig)

	handler := &operation.UploadFileHandler{}

	op := &plan.PlannedOperation{
		ID:   "test-upload-004",
		Type: plan.OpUploadFile,
		Params: map[string]interface{}{
			"local_path":  localPath,
			"remote_path": "/remote/test.conf",
		},
	}

	// Execute upload
	_, err := handler.Execute(context.Background(), op, exec)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// PostHook should verify file exists
	postResult, err := handler.PostHook(context.Background(), op, exec)
	if err != nil {
		t.Fatalf("PostHook error: %v", err)
	}

	if !postResult.Valid {
		t.Errorf("PostHook should be valid, errors: %v", postResult.Errors)
	}

	if postResult.Metadata["verified"] != true {
		t.Error("PostHook should mark as verified")
	}
}
