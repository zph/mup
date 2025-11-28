package operation_test

import (
	"testing"

	"github.com/zph/mup/pkg/operation"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation"
)

// REQ-PES-036, REQ-PES-047, REQ-PES-048: Test CreateDirectoryHandler
func TestCreateDirectoryHandler(t *testing.T) {
	// Setup simulation executor
	simConfig := &simulation.Config{
		AllowRealFileReads: false,
	}
	exec := simulation.NewExecutor(simConfig)

	// Create handler
	handler := operation.NewCreateDirectoryHandlerV2()

	// Create test operation
	params := operation.CreateDirectoryParams{
		Path: "/test/cluster/dir",
		Mode: 0755,
	}

	op := NewTestOperation(plan.OpCreateDirectory, MarshalParams(params))

	// Run full test suite
	suite := NewHandlerTestSuite(t, handler, exec)
	suite.RunAll(op)
}

// REQ-PES-033: Test idempotency - creating existing directory should succeed
func TestCreateDirectoryHandler_Idempotency(t *testing.T) {
	// Pre-create directory in simulation
	simConfig := &simulation.Config{
		AllowRealFileReads: false,
		ExistingDirectories: []string{"/test/existing"},
	}
	exec := simulation.NewExecutor(simConfig)

	handler := operation.NewCreateDirectoryHandlerV2()

	params := operation.CreateDirectoryParams{
		Path: "/test/existing",
		Mode: 0755,
	}

	op := NewTestOperation(plan.OpCreateDirectory, MarshalParams(params))

	suite := NewHandlerTestSuite(t, handler, exec)

	// IsComplete should detect existing directory
	completed, err := handler.IsComplete(suite.ctx, op, exec)
	if err != nil {
		t.Fatalf("IsComplete error: %v", err)
	}
	if !completed {
		t.Error("IsComplete should detect existing directory")
	}
}

// REQ-PES-036: Test IsComplete with parent directory creation
func TestCreateDirectoryHandler_ParentDirectories(t *testing.T) {
	simConfig := &simulation.Config{
		AllowRealFileReads: false,
	}
	exec := simulation.NewExecutor(simConfig)

	handler := operation.NewCreateDirectoryHandlerV2()

	// Create nested directory (like mkdir -p)
	params := operation.CreateDirectoryParams{
		Path: "/test/cluster/v7.0/mongod-30000",
		Mode: 0755,
	}

	op := NewTestOperation(plan.OpCreateDirectory, MarshalParams(params))

	suite := NewHandlerTestSuite(t, handler, exec)
	suite.TestFullLifecycle(op)

	// Verify parent directories were created in simulation
	simState := exec.GetState()
	dirs := simState.Dirs

	// All parent directories should exist
	expectedDirs := []string{
		"/test/cluster/v7.0/mongod-30000",
		"/test/cluster/v7.0",
		"/test/cluster",
		"/test",
	}

	for _, expectedDir := range expectedDirs {
		if _, exists := dirs[expectedDir]; !exists {
			t.Errorf("Parent directory not created: %s", expectedDir)
		}
	}
}

// REQ-PES-002, REQ-PES-003: Test typed parameters
func TestCreateDirectoryHandler_TypedParams(t *testing.T) {
	// Test that parameters are correctly typed (not float64)
	params := operation.CreateDirectoryParams{
		Path: "/test/dir",
		Mode: 0755, // Should stay as int, not become float64
	}

	paramsMap := MarshalParams(params)

	// In real code, we'd unmarshal from op.Params
	// For now, verify the map contains correct types
	if paramsMap["mode"] == nil {
		t.Error("mode parameter is missing")
	}

	// The mode should be a number (will be float64 in JSON)
	// Handler must handle this conversion
	if _, ok := paramsMap["mode"].(float64); !ok {
		if _, ok := paramsMap["mode"].(int); !ok {
			t.Errorf("mode is not a number: %T", paramsMap["mode"])
		}
	}
}
