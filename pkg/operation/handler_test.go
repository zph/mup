package operation_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/operation"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/simulation"
)

// HandlerTestSuite provides comprehensive testing for OperationHandler
// REQ-PES-036, REQ-PES-047, REQ-PES-048
type HandlerTestSuite struct {
	t       *testing.T
	handler operation.OperationHandler
	exec    executor.Executor
	ctx     context.Context
}

// NewHandlerTestSuite creates a test suite for a handler
func NewHandlerTestSuite(t *testing.T, handler operation.OperationHandler, exec executor.Executor) *HandlerTestSuite {
	return &HandlerTestSuite{
		t:       t,
		handler: handler,
		exec:    exec,
		ctx:     context.Background(),
	}
}

// TestFullLifecycle tests all four phases in order
// REQ-PES-036: IsComplete should be false initially
// REQ-PES-047: PreHook should validate and pass
// Execute should succeed
// REQ-PES-048: PostHook should verify
// REQ-PES-036: IsComplete should be true after execution
func (s *HandlerTestSuite) TestFullLifecycle(op *plan.PlannedOperation) {
	s.t.Run("FullLifecycle", func(t *testing.T) {
		// Phase 1: IsComplete (should be false initially)
		completed, err := s.handler.IsComplete(s.ctx, op, s.exec)
		require.NoError(t, err, "IsComplete should not error")
		assert.False(t, completed, "Operation should not be complete initially")

		// Phase 2: PreHook (should validate and pass)
		preResult, err := s.handler.PreHook(s.ctx, op, s.exec)
		require.NoError(t, err, "PreHook should not error")
		assert.True(t, preResult.Valid, "PreHook should pass validation")
		if len(preResult.Warnings) > 0 {
			t.Logf("PreHook warnings: %v", preResult.Warnings)
		}

		// Phase 3: Execute
		result, err := s.handler.Execute(s.ctx, op, s.exec)
		require.NoError(t, err, "Execute should not error")
		assert.True(t, result.Success, "Execute should succeed")
		assert.NotEmpty(t, result.Output, "Execute should return output")

		// Phase 4: PostHook (should verify)
		postResult, err := s.handler.PostHook(s.ctx, op, s.exec)
		require.NoError(t, err, "PostHook should not error")
		assert.True(t, postResult.Valid, "PostHook should pass verification")
		if len(postResult.Warnings) > 0 {
			t.Logf("PostHook warnings: %v", postResult.Warnings)
		}

		// Phase 1 Again: IsComplete (should be true now)
		completed, err = s.handler.IsComplete(s.ctx, op, s.exec)
		require.NoError(t, err, "IsComplete should not error")
		assert.True(t, completed, "Operation should be complete after execution")
	})
}

// TestIdempotency tests that executing twice is safe
// REQ-PES-033: Idempotent behavior
func (s *HandlerTestSuite) TestIdempotency(op *plan.PlannedOperation) {
	s.t.Run("Idempotency", func(t *testing.T) {
		// First execution
		_, err := s.handler.PreHook(s.ctx, op, s.exec)
		require.NoError(t, err)

		result1, err := s.handler.Execute(s.ctx, op, s.exec)
		require.NoError(t, err)
		assert.True(t, result1.Success)

		_, err = s.handler.PostHook(s.ctx, op, s.exec)
		require.NoError(t, err)

		// Second execution (should not fail)
		_, err = s.handler.PreHook(s.ctx, op, s.exec)
		require.NoError(t, err, "PreHook should handle already-completed state")

		result2, err := s.handler.Execute(s.ctx, op, s.exec)
		require.NoError(t, err, "Execute should be idempotent")
		assert.True(t, result2.Success)

		_, err = s.handler.PostHook(s.ctx, op, s.exec)
		require.NoError(t, err)
	})
}

// TestResume tests that resume can skip completed operations
// REQ-PES-016: Resume logic
func (s *HandlerTestSuite) TestResume(op *plan.PlannedOperation) {
	s.t.Run("Resume", func(t *testing.T) {
		// Execute operation
		_, err := s.handler.PreHook(s.ctx, op, s.exec)
		require.NoError(t, err)

		_, err = s.handler.Execute(s.ctx, op, s.exec)
		require.NoError(t, err)

		_, err = s.handler.PostHook(s.ctx, op, s.exec)
		require.NoError(t, err)

		// Simulate resume: IsComplete should detect completion
		completed, err := s.handler.IsComplete(s.ctx, op, s.exec)
		require.NoError(t, err)
		assert.True(t, completed, "Resume should detect completed operation")
	})
}

// TestPreHookValidation tests that PreHook catches invalid states
// REQ-PES-047: PreHook validation
func (s *HandlerTestSuite) TestPreHookValidation(op *plan.PlannedOperation, invalidExec executor.Executor, expectedError string) {
	s.t.Run("PreHookValidation", func(t *testing.T) {
		preResult, err := s.handler.PreHook(s.ctx, op, invalidExec)

		if expectedError != "" {
			// Should either error or return invalid result
			if err == nil {
				assert.False(t, preResult.Valid, "PreHook should return invalid result")
				if assert.NotEmpty(t, preResult.Errors, "PreHook should have errors") {
					assert.Contains(t, preResult.Errors[0], expectedError, "Error message should contain expected text")
				}
			} else {
				assert.Contains(t, err.Error(), expectedError, "Error should contain expected text")
			}
		}
	})
}

// TestPostHookVerification tests that PostHook catches failed operations
// REQ-PES-048: PostHook verification
func (s *HandlerTestSuite) TestPostHookVerification(op *plan.PlannedOperation, brokenExec executor.Executor) {
	s.t.Run("PostHookVerification", func(t *testing.T) {
		// Execute with broken executor
		_, err := s.handler.PreHook(s.ctx, op, brokenExec)
		if err != nil {
			t.Skip("PreHook failed, can't test PostHook")
		}

		_, err = s.handler.Execute(s.ctx, op, brokenExec)
		// Execute might succeed even if result is bad

		postResult, err := s.handler.PostHook(s.ctx, op, brokenExec)
		// PostHook should either error or return invalid
		if err == nil {
			assert.False(t, postResult.Valid, "PostHook should detect failed operation")
		}
	})
}

// RunAll runs all test scenarios
func (s *HandlerTestSuite) RunAll(op *plan.PlannedOperation) {
	s.TestFullLifecycle(op)
	s.TestIdempotency(op)
	s.TestResume(op)
}

// Helper functions for creating test operations

// MarshalParams converts a struct to Params map
func MarshalParams(v interface{}) map[string]interface{} {
	data, _ := json.Marshal(v)
	var params map[string]interface{}
	json.Unmarshal(data, &params)
	return params
}

// NewSimulationExecutor creates a simulation executor for testing
func NewSimulationExecutor(config *simulation.Config) executor.Executor {
	return simulation.NewExecutor(config)
}

// NewTestOperation creates a PlannedOperation for testing
func NewTestOperation(opType plan.OperationType, params map[string]interface{}) *plan.PlannedOperation {
	return &plan.PlannedOperation{
		ID:          "test-001",
		Type:        opType,
		Description: "Test operation",
		Target: plan.OperationTarget{
			Type: "test",
			Name: "test-target",
		},
		Params: params,
	}
}
