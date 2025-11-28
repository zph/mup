package plan

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlan_SaveAndLoad(t *testing.T) {
	// Create a test plan
	plan := &Plan{
		PlanID:      "test-plan-123",
		Operation:   "deploy",
		ClusterName: "test-cluster",
		CreatedAt:   time.Now(),
		Version:     "7.0.5",
		Variant:     "mongo",
		Validation: ValidationResult{
			Valid:    true,
			Errors:   []ValidationIssue{},
			Warnings: []ValidationIssue{},
			Checks:   []CheckResult{},
		},
		Phases: []PlannedPhase{
			{
				Name:        "prepare",
				Description: "Prepare deployment",
				Order:       1,
				Operations: []PlannedOperation{
					{
						ID:          "op-001",
						Type:        OpDownloadBinary,
						Description: "Download MongoDB binary",
						Target: OperationTarget{
							Type: "binary",
							Name: "mongodb-7.0.5",
						},
						Changes:  []Change{},
						Parallel: false,
					},
				},
			},
		},
		Resources: ResourceEstimate{
			Hosts:          1,
			TotalProcesses: 3,
			PortsUsed:      []int{27017, 27018, 27019},
			DiskSpaceGB:    10.0,
		},
		DryRun: false,
	}

	// Save to temp file
	tmpFile, err := os.CreateTemp("", "test-plan-*.json")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	err = plan.SaveToFile(tmpPath)
	require.NoError(t, err)

	// Load from file
	loadedPlan, err := LoadFromFile(tmpPath)
	require.NoError(t, err)

	// Verify loaded plan matches original
	assert.Equal(t, plan.PlanID, loadedPlan.PlanID)
	assert.Equal(t, plan.Operation, loadedPlan.Operation)
	assert.Equal(t, plan.ClusterName, loadedPlan.ClusterName)
	assert.Equal(t, plan.Version, loadedPlan.Version)
	assert.Equal(t, plan.Variant, loadedPlan.Variant)
	assert.True(t, loadedPlan.IsValid())
	assert.Equal(t, len(plan.Phases), len(loadedPlan.Phases))
	assert.Equal(t, plan.Phases[0].Name, loadedPlan.Phases[0].Name)
}

func TestPlan_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		plan     *Plan
		expected bool
	}{
		{
			name: "valid plan with no errors",
			plan: &Plan{
				Validation: ValidationResult{
					Valid:  true,
					Errors: []ValidationIssue{},
				},
			},
			expected: true,
		},
		{
			name: "invalid plan with errors",
			plan: &Plan{
				Validation: ValidationResult{
					Valid: false,
					Errors: []ValidationIssue{
						{Code: "test-error", Message: "Test error"},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.plan.IsValid())
		})
	}
}

func TestPlan_HasErrors(t *testing.T) {
	plan := &Plan{
		Validation: ValidationResult{
			Valid: false,
			Errors: []ValidationIssue{
				{Code: "error1", Message: "Error 1"},
			},
		},
	}
	assert.True(t, plan.HasErrors())

	planNoErrors := &Plan{
		Validation: ValidationResult{
			Valid:  true,
			Errors: []ValidationIssue{},
		},
	}
	assert.False(t, planNoErrors.HasErrors())
}

func TestPlan_HasWarnings(t *testing.T) {
	plan := &Plan{
		Validation: ValidationResult{
			Valid: true,
			Warnings: []ValidationIssue{
				{Code: "warning1", Message: "Warning 1"},
			},
		},
	}
	assert.True(t, plan.HasWarnings())

	planNoWarnings := &Plan{
		Validation: ValidationResult{
			Valid:    true,
			Warnings: []ValidationIssue{},
		},
	}
	assert.False(t, planNoWarnings.HasWarnings())
}

func TestPlan_TotalOperations(t *testing.T) {
	plan := &Plan{
		Phases: []PlannedPhase{
			{
				Operations: []PlannedOperation{
					{ID: "op-1"},
					{ID: "op-2"},
				},
			},
			{
				Operations: []PlannedOperation{
					{ID: "op-3"},
					{ID: "op-4"},
					{ID: "op-5"},
				},
			},
		},
	}

	assert.Equal(t, 5, plan.TotalOperations())
}

func TestPlan_GetOperationByID(t *testing.T) {
	plan := &Plan{
		Phases: []PlannedPhase{
			{
				Operations: []PlannedOperation{
					{ID: "op-1", Description: "Operation 1"},
					{ID: "op-2", Description: "Operation 2"},
				},
			},
		},
	}

	op := plan.GetOperationByID("op-2")
	require.NotNil(t, op)
	assert.Equal(t, "op-2", op.ID)
	assert.Equal(t, "Operation 2", op.Description)

	nilOp := plan.GetOperationByID("non-existent")
	assert.Nil(t, nilOp)
}

func TestPlan_GetPhaseByName(t *testing.T) {
	plan := &Plan{
		Phases: []PlannedPhase{
			{Name: "prepare", Description: "Prepare phase"},
			{Name: "deploy", Description: "Deploy phase"},
		},
	}

	phase := plan.GetPhaseByName("deploy")
	require.NotNil(t, phase)
	assert.Equal(t, "deploy", phase.Name)
	assert.Equal(t, "Deploy phase", phase.Description)

	nilPhase := plan.GetPhaseByName("non-existent")
	assert.Nil(t, nilPhase)
}

func TestNewPlanID(t *testing.T) {
	id1 := NewPlanID()
	id2 := NewPlanID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2, "Plan IDs should be unique")
}

func TestNewOperationID(t *testing.T) {
	id1 := NewOperationID("prepare", 0)
	id2 := NewOperationID("prepare", 1)
	id3 := NewOperationID("deploy", 0)

	assert.Equal(t, "prepare-000", id1)
	assert.Equal(t, "prepare-001", id2)
	assert.Equal(t, "deploy-000", id3)
}
