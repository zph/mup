package plan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidationRunner_Sequential(t *testing.T) {
	runner := NewValidationRunner(false) // sequential

	// Add some test validators
	runner.AddFunc(func(ctx context.Context) CheckResult {
		return NewCheckResult("check1", "Check 1 passed")
	})

	runner.AddFunc(func(ctx context.Context) CheckResult {
		return NewCheckFailure("check2", "Check 2 failed")
	})

	runner.AddFunc(func(ctx context.Context) CheckResult {
		return NewCheckWarning("check3", "Check 3 warning")
	})

	// Run validations
	result := runner.Run(context.Background())

	// Verify results
	assert.False(t, result.Valid, "Should be invalid due to failure")
	assert.Len(t, result.Checks, 3)
	assert.Len(t, result.Errors, 1)
	assert.Len(t, result.Warnings, 1)

	// Check that errors are properly recorded
	assert.Equal(t, "check2", result.Errors[0].Code)
	assert.Equal(t, "Check 2 failed", result.Errors[0].Message)

	// Check that warnings are properly recorded
	assert.Equal(t, "check3", result.Warnings[0].Code)
	assert.Equal(t, "Check 3 warning", result.Warnings[0].Message)
}

func TestValidationRunner_Parallel(t *testing.T) {
	runner := NewValidationRunner(true) // parallel

	callCount := 0
	for i := 0; i < 5; i++ {
		runner.AddFunc(func(ctx context.Context) CheckResult {
			callCount++
			time.Sleep(10 * time.Millisecond)
			return NewCheckResult("check", "passed")
		})
	}

	start := time.Now()
	result := runner.Run(context.Background())
	duration := time.Since(start)

	// Parallel execution should be faster than sequential
	// If sequential, would take 50ms+, parallel should be ~10ms
	assert.Less(t, duration, 30*time.Millisecond, "Parallel execution should be faster")
	assert.True(t, result.Valid)
	assert.Len(t, result.Checks, 5)
}

func TestNewCheckResult(t *testing.T) {
	result := NewCheckResult("test-check", "Test passed")

	assert.Equal(t, "test-check", result.Name)
	assert.Equal(t, "Test passed", result.Message)
	assert.Equal(t, "passed", result.Status)
}

func TestNewCheckFailure(t *testing.T) {
	result := NewCheckFailure("test-check", "Test failed")

	assert.Equal(t, "test-check", result.Name)
	assert.Equal(t, "Test failed", result.Message)
	assert.Equal(t, "failed", result.Status)
}

func TestNewCheckWarning(t *testing.T) {
	result := NewCheckWarning("test-check", "Test warning")

	assert.Equal(t, "test-check", result.Name)
	assert.Equal(t, "Test warning", result.Message)
	assert.Equal(t, "warning", result.Status)
}

func TestCheckResult_WithHost(t *testing.T) {
	result := NewCheckResult("test", "passed").WithHost("localhost")

	assert.Equal(t, "localhost", result.Host)
}

func TestCheckResult_WithDuration(t *testing.T) {
	duration := 100 * time.Millisecond
	result := NewCheckResult("test", "passed").WithDuration(duration)

	assert.Equal(t, duration, result.Duration)
}

func TestCheckResult_WithDetails(t *testing.T) {
	details := map[string]interface{}{
		"key": "value",
	}
	result := NewCheckResult("test", "passed").WithDetails(details)

	assert.Equal(t, details, result.Details)
}

func TestMeasuredCheck(t *testing.T) {
	checkFunc := MeasuredCheck("test-check", func(ctx context.Context) (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "Success", nil
	})

	result := checkFunc.Check(context.Background())

	assert.Equal(t, "test-check", result.Name)
	assert.Equal(t, "Success", result.Message)
	assert.Equal(t, "passed", result.Status)
	assert.Greater(t, result.Duration, 40*time.Millisecond)
}

func TestMeasuredCheck_WithError(t *testing.T) {
	checkFunc := MeasuredCheck("test-check", func(ctx context.Context) (string, error) {
		return "", errors.New("check failed")
	})

	result := checkFunc.Check(context.Background())

	assert.Equal(t, "test-check", result.Name)
	assert.Equal(t, "check failed", result.Message)
	assert.Equal(t, "failed", result.Status)
}

func TestMeasuredCheckWithHost(t *testing.T) {
	checkFunc := MeasuredCheckWithHost("test-check", "server1", func(ctx context.Context) (string, error) {
		return "Success", nil
	})

	result := checkFunc.Check(context.Background())

	assert.Equal(t, "test-check", result.Name)
	assert.Equal(t, "server1", result.Host)
	assert.Equal(t, "Success", result.Message)
	assert.Equal(t, "passed", result.Status)
}

func TestValidateTopology_Nil(t *testing.T) {
	issues := ValidateTopology(nil)

	assert.Len(t, issues, 1)
	assert.Equal(t, "topology_nil", issues[0].Code)
	assert.Equal(t, "error", issues[0].Severity)
}

func TestCombineValidationResults(t *testing.T) {
	result1 := ValidationResult{
		Valid: true,
		Errors: []ValidationIssue{
			{Code: "error1", Message: "Error 1"},
		},
		Warnings: []ValidationIssue{
			{Code: "warning1", Message: "Warning 1"},
		},
		Checks: []CheckResult{
			{Name: "check1", Status: "passed"},
		},
	}

	result2 := ValidationResult{
		Valid: false,
		Errors: []ValidationIssue{
			{Code: "error2", Message: "Error 2"},
		},
		Warnings: []ValidationIssue{
			{Code: "warning2", Message: "Warning 2"},
		},
		Checks: []CheckResult{
			{Name: "check2", Status: "failed"},
		},
	}

	combined := CombineValidationResults(result1, result2)

	assert.False(t, combined.Valid, "Should be invalid if any result is invalid")
	assert.Len(t, combined.Errors, 2)
	assert.Len(t, combined.Warnings, 2)
	assert.Len(t, combined.Checks, 2)
}

func TestFormatValidationResult(t *testing.T) {
	tests := []struct {
		name     string
		result   ValidationResult
		contains []string
	}{
		{
			name: "all passed",
			result: ValidationResult{
				Valid:    true,
				Errors:   []ValidationIssue{},
				Warnings: []ValidationIssue{},
			},
			contains: []string{"✓ All validation checks passed"},
		},
		{
			name: "with errors",
			result: ValidationResult{
				Valid: false,
				Errors: []ValidationIssue{
					{Code: "error1", Message: "Test error", Host: "host1"},
				},
				Warnings: []ValidationIssue{},
			},
			contains: []string{"✗", "error(s)", "[host1]", "Test error"},
		},
		{
			name: "with warnings",
			result: ValidationResult{
				Valid:  true,
				Errors: []ValidationIssue{},
				Warnings: []ValidationIssue{
					{Code: "warning1", Message: "Test warning", Host: "host2"},
				},
			},
			contains: []string{"⚠", "warning(s)", "[host2]", "Test warning"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := FormatValidationResult(tt.result)
			for _, expected := range tt.contains {
				assert.Contains(t, output, expected)
			}
		})
	}
}

func TestNewValidationError(t *testing.T) {
	issues := []ValidationIssue{
		{Code: "error1", Message: "Error 1"},
		{Code: "error2", Message: "Error 2"},
	}

	err := NewValidationError(issues)
	require.NotNil(t, err)
	assert.Len(t, err.Issues, 2)
	assert.Contains(t, err.Error(), "Error 1")
}

func TestIsValidationError(t *testing.T) {
	validationErr := NewValidationError([]ValidationIssue{
		{Code: "test", Message: "Test error"},
	})

	assert.True(t, IsValidationError(validationErr))
	assert.False(t, IsValidationError(errors.New("regular error")))
	assert.False(t, IsValidationError(nil))
}
