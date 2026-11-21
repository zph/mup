package plan

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const integrityCluster = "integrity-test"

func integrityPlan() *Plan {
	return &Plan{
		PlanID:      NewPlanID(),
		Operation:   "deploy",
		ClusterName: integrityCluster,
		Validation:  ValidationResult{Valid: true},
		Phases: []PlannedPhase{
			{Name: "prepare", Order: 1, Operations: []PlannedOperation{{ID: "prepare-001", Type: OpCreateDirectory}}},
			{Name: "deploy", Order: 2, Operations: []PlannedOperation{{ID: "deploy-001", Type: OpStartProcess}}},
		},
	}
}

// LOCAL-032: a plan is applied only if it still matches its recorded checksum.
func TestLoadVerifiedPlan(t *testing.T) {
	newStore := func(t *testing.T) (*PlanStore, string) {
		store, err := NewPlanStore(t.TempDir())
		require.NoError(t, err)
		planID, err := store.SavePlan(integrityPlan())
		require.NoError(t, err)
		return store, planID
	}

	t.Run("intact plan loads", func(t *testing.T) {
		store, planID := newStore(t)

		p, err := store.LoadVerifiedPlan(integrityCluster, planID)

		require.NoError(t, err)
		assert.Equal(t, planID, p.PlanID)
	})

	t.Run("edited plan is refused", func(t *testing.T) {
		store, planID := newStore(t)
		planPath := store.GetPlanPath(integrityCluster, planID)
		data, err := os.ReadFile(planPath)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(planPath, append(data, ' '), 0o644))

		_, err = store.LoadVerifiedPlan(integrityCluster, planID)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum")
	})

	t.Run("plan without a checksum is refused", func(t *testing.T) {
		store, planID := newStore(t)
		require.NoError(t, os.Remove(store.GetPlanPath(integrityCluster, planID)+".sha256"))

		_, err := store.LoadVerifiedPlan(integrityCluster, planID)

		require.Error(t, err)
	})

	t.Run("unknown plan", func(t *testing.T) {
		store, _ := newStore(t)

		_, err := store.LoadVerifiedPlan(integrityCluster, "missing")

		require.Error(t, err)
	})
}

// LOCAL-033
func TestStructuralProblems(t *testing.T) {
	assert.Empty(t, integrityPlan().StructuralProblems())

	empty := integrityPlan()
	empty.Phases = nil
	assert.NotEmpty(t, empty.StructuralProblems())

	// A standalone cluster has nothing to initialize: one empty phase is fine.
	emptyPhase := integrityPlan()
	emptyPhase.Phases[0].Operations = nil
	assert.Empty(t, emptyPhase.StructuralProblems())

	noOps := integrityPlan()
	noOps.Phases[0].Operations = nil
	noOps.Phases[1].Operations = nil
	assert.Contains(t, noOps.StructuralProblems()[0], "no operations")

	duplicate := integrityPlan()
	duplicate.Phases[1].Operations[0].ID = "prepare-001"
	assert.Contains(t, duplicate.StructuralProblems()[0], "prepare-001")

	untyped := integrityPlan()
	untyped.Phases[0].Operations[0].Type = ""
	assert.NotEmpty(t, untyped.StructuralProblems())

	nameless := integrityPlan()
	nameless.ClusterName = ""
	assert.NotEmpty(t, nameless.StructuralProblems())
}
