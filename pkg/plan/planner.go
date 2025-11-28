package plan

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Planner defines the interface for generating operation plans
type Planner interface {
	// GeneratePlan creates a plan for the operation
	GeneratePlan(ctx context.Context) (*Plan, error)

	// Validate performs pre-flight validation
	Validate(ctx context.Context) (ValidationResult, error)
}

// PlannerConfig contains common configuration for all planners
type PlannerConfig struct {
	ClusterName string
	Operation   string
	DryRun      bool
	Environment map[string]string
}

// NewPlanID generates a new plan ID
func NewPlanID() string {
	return uuid.New().String()
}

// NewOperationID generates a new operation ID
func NewOperationID(phase string, index int) string {
	return fmt.Sprintf("%s-%03d", phase, index)
}

// ValidationError represents a validation failure
type ValidationError struct {
	Issues []ValidationIssue
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "validation failed"
	}
	return fmt.Sprintf("validation failed: %s", e.Issues[0].Message)
}

// NewValidationError creates a new validation error
func NewValidationError(issues []ValidationIssue) *ValidationError {
	return &ValidationError{Issues: issues}
}

// IsValidationError checks if an error is a validation error
func IsValidationError(err error) bool {
	_, ok := err.(*ValidationError)
	return ok
}
