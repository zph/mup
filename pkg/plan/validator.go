package plan

import (
	"context"
	"fmt"
	"time"
)

// Validator performs validation checks
type Validator interface {
	// Check performs the validation check
	Check(ctx context.Context) CheckResult
}

// CheckFunc is a function that performs a validation check
type CheckFunc func(ctx context.Context) CheckResult

// Check implements the Validator interface for CheckFunc
func (f CheckFunc) Check(ctx context.Context) CheckResult {
	return f(ctx)
}

// ValidationRunner runs multiple validators
type ValidationRunner struct {
	validators []Validator
	parallel   bool
}

// NewValidationRunner creates a new validation runner
func NewValidationRunner(parallel bool) *ValidationRunner {
	return &ValidationRunner{
		validators: make([]Validator, 0),
		parallel:   parallel,
	}
}

// Add adds a validator to the runner
func (r *ValidationRunner) Add(validator Validator) {
	r.validators = append(r.validators, validator)
}

// AddFunc adds a validation function to the runner
func (r *ValidationRunner) AddFunc(f CheckFunc) {
	r.Add(f)
}

// Run executes all validators and returns the results
func (r *ValidationRunner) Run(ctx context.Context) ValidationResult {
	if r.parallel {
		return r.runParallel(ctx)
	}
	return r.runSequential(ctx)
}

func (r *ValidationRunner) runSequential(ctx context.Context) ValidationResult {
	result := ValidationResult{
		Valid:    true,
		Errors:   make([]ValidationIssue, 0),
		Warnings: make([]ValidationIssue, 0),
		Checks:   make([]CheckResult, 0, len(r.validators)),
	}

	for _, validator := range r.validators {
		checkResult := validator.Check(ctx)
		result.Checks = append(result.Checks, checkResult)

		if checkResult.Status == "failed" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationIssue{
				Code:     checkResult.Name,
				Message:  checkResult.Message,
				Host:     checkResult.Host,
				Severity: "error",
			})
		} else if checkResult.Status == "warning" {
			result.Warnings = append(result.Warnings, ValidationIssue{
				Code:     checkResult.Name,
				Message:  checkResult.Message,
				Host:     checkResult.Host,
				Severity: "warning",
			})
		}
	}

	return result
}

func (r *ValidationRunner) runParallel(ctx context.Context) ValidationResult {
	resultChan := make(chan CheckResult, len(r.validators))

	for _, validator := range r.validators {
		v := validator // capture for goroutine
		go func() {
			resultChan <- v.Check(ctx)
		}()
	}

	result := ValidationResult{
		Valid:    true,
		Errors:   make([]ValidationIssue, 0),
		Warnings: make([]ValidationIssue, 0),
		Checks:   make([]CheckResult, 0, len(r.validators)),
	}

	for i := 0; i < len(r.validators); i++ {
		checkResult := <-resultChan
		result.Checks = append(result.Checks, checkResult)

		if checkResult.Status == "failed" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationIssue{
				Code:     checkResult.Name,
				Message:  checkResult.Message,
				Host:     checkResult.Host,
				Severity: "error",
			})
		} else if checkResult.Status == "warning" {
			result.Warnings = append(result.Warnings, ValidationIssue{
				Code:     checkResult.Name,
				Message:  checkResult.Message,
				Host:     checkResult.Host,
				Severity: "warning",
			})
		}
	}

	return result
}

// NewCheckResult creates a successful check result
func NewCheckResult(name, message string) CheckResult {
	return CheckResult{
		Name:    name,
		Status:  "passed",
		Message: message,
	}
}

// NewCheckFailure creates a failed check result
func NewCheckFailure(name, message string) CheckResult {
	return CheckResult{
		Name:    name,
		Status:  "failed",
		Message: message,
	}
}

// NewCheckWarning creates a warning check result
func NewCheckWarning(name, message string) CheckResult {
	return CheckResult{
		Name:    name,
		Status:  "warning",
		Message: message,
	}
}

// WithHost adds a host to a check result
func (c CheckResult) WithHost(host string) CheckResult {
	c.Host = host
	return c
}

// WithDuration adds a duration to a check result
func (c CheckResult) WithDuration(duration time.Duration) CheckResult {
	c.Duration = duration
	return c
}

// WithDetails adds details to a check result
func (c CheckResult) WithDetails(details interface{}) CheckResult {
	c.Details = details
	return c
}

// MeasuredCheck wraps a check function and measures its duration
func MeasuredCheck(name string, f func(ctx context.Context) (string, error)) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		status, err := f(ctx)
		duration := time.Since(start)

		if err != nil {
			return NewCheckFailure(name, err.Error()).WithDuration(duration)
		}

		return NewCheckResult(name, status).WithDuration(duration)
	}
}

// MeasuredCheckWithHost wraps a check function for a specific host
func MeasuredCheckWithHost(name, host string, f func(ctx context.Context) (string, error)) CheckFunc {
	return func(ctx context.Context) CheckResult {
		start := time.Now()
		status, err := f(ctx)
		duration := time.Since(start)

		if err != nil {
			return NewCheckFailure(name, err.Error()).
				WithHost(host).
				WithDuration(duration)
		}

		return NewCheckResult(name, status).
			WithHost(host).
			WithDuration(duration)
	}
}

// ValidateTopology performs basic topology validation
func ValidateTopology(topo interface{}) []ValidationIssue {
	issues := make([]ValidationIssue, 0)

	if topo == nil {
		issues = append(issues, ValidationIssue{
			Code:     "topology_nil",
			Message:  "Topology is nil",
			Severity: "error",
		})
	}

	return issues
}

// CombineValidationResults merges multiple validation results
func CombineValidationResults(results ...ValidationResult) ValidationResult {
	combined := ValidationResult{
		Valid:    true,
		Errors:   make([]ValidationIssue, 0),
		Warnings: make([]ValidationIssue, 0),
		Checks:   make([]CheckResult, 0),
	}

	for _, result := range results {
		if !result.Valid {
			combined.Valid = false
		}
		combined.Errors = append(combined.Errors, result.Errors...)
		combined.Warnings = append(combined.Warnings, result.Warnings...)
		combined.Checks = append(combined.Checks, result.Checks...)
	}

	return combined
}

// FormatValidationResult formats a validation result for display
func FormatValidationResult(result ValidationResult) string {
	if result.Valid && len(result.Warnings) == 0 {
		return "✓ All validation checks passed"
	}

	output := ""
	if len(result.Errors) > 0 {
		output += fmt.Sprintf("✗ %d error(s):\n", len(result.Errors))
		for _, err := range result.Errors {
			if err.Host != "" {
				output += fmt.Sprintf("  - [%s] %s\n", err.Host, err.Message)
			} else {
				output += fmt.Sprintf("  - %s\n", err.Message)
			}
		}
	}

	if len(result.Warnings) > 0 {
		output += fmt.Sprintf("⚠ %d warning(s):\n", len(result.Warnings))
		for _, warn := range result.Warnings {
			if warn.Host != "" {
				output += fmt.Sprintf("  - [%s] %s\n", warn.Host, warn.Message)
			} else {
				output += fmt.Sprintf("  - %s\n", warn.Message)
			}
		}
	}

	return output
}
