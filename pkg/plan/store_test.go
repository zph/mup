package plan_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/topology"
)

// REQ-PES-021: Test plan persistence with unique IDs
func TestPlanStore_SavePlan(t *testing.T) {
	// Create temporary storage directory
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	// Create test plan
	testPlan := createTestPlan("test-cluster", "deploy")

	// Save plan
	planID, err := store.SavePlan(testPlan)
	require.NoError(t, err)
	assert.NotEmpty(t, planID, "Plan ID should be generated")
	assert.Equal(t, planID, testPlan.PlanID, "Plan ID should be set on plan")
	assert.False(t, testPlan.CreatedAt.IsZero(), "CreatedAt should be set")

	// Verify plan file exists
	planPath := store.GetPlanPath("test-cluster", planID)
	assert.FileExists(t, planPath, "Plan file should exist")

	// Verify checksum file exists
	checksumPath := planPath + ".sha256"
	assert.FileExists(t, checksumPath, "Checksum file should exist")
}

// REQ-PES-022: Test atomic plan writes
func TestPlanStore_SavePlan_Atomic(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	testPlan := createTestPlan("test-cluster", "deploy")

	// Save plan
	planID, err := store.SavePlan(testPlan)
	require.NoError(t, err)

	// Verify no .tmp file remains
	tmpPath := store.GetPlanPath("test-cluster", planID) + ".tmp"
	assert.NoFileExists(t, tmpPath, "Temporary file should not exist after save")
}

// REQ-PES-023: Test plan verification with SHA-256
func TestPlanStore_VerifyPlan(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	testPlan := createTestPlan("test-cluster", "deploy")

	// Save plan
	planID, err := store.SavePlan(testPlan)
	require.NoError(t, err)

	// Verify plan
	verified, err := store.VerifyPlan("test-cluster", planID)
	require.NoError(t, err)
	assert.True(t, verified, "Plan should verify successfully")
}

// REQ-PES-023: Test plan verification detects tampering
func TestPlanStore_VerifyPlan_Tampered(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	testPlan := createTestPlan("test-cluster", "deploy")

	// Save plan
	planID, err := store.SavePlan(testPlan)
	require.NoError(t, err)

	// Tamper with plan file
	planPath := store.GetPlanPath("test-cluster", planID)
	err = os.WriteFile(planPath, []byte("tampered data"), 0644)
	require.NoError(t, err)

	// Verify should fail
	verified, err := store.VerifyPlan("test-cluster", planID)
	require.NoError(t, err)
	assert.False(t, verified, "Tampered plan should not verify")
}

// REQ-PES-024: Test loading plan by ID
func TestPlanStore_LoadPlan(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	testPlan := createTestPlan("test-cluster", "deploy")
	testPlan.Version = "7.0"
	testPlan.Variant = "percona"

	// Save plan
	planID, err := store.SavePlan(testPlan)
	require.NoError(t, err)

	// Load plan
	loadedPlan, err := store.LoadPlan("test-cluster", planID)
	require.NoError(t, err)
	assert.NotNil(t, loadedPlan)
	assert.Equal(t, planID, loadedPlan.PlanID)
	assert.Equal(t, "test-cluster", loadedPlan.ClusterName)
	assert.Equal(t, "deploy", loadedPlan.Operation)
	assert.Equal(t, "7.0", loadedPlan.Version)
	assert.Equal(t, "percona", loadedPlan.Variant)
}

// REQ-PES-024: Test loading non-existent plan
func TestPlanStore_LoadPlan_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	// Try to load non-existent plan
	_, err = store.LoadPlan("test-cluster", "non-existent-plan")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "plan not found")
}

// REQ-PES-025: Test listing plans
func TestPlanStore_ListPlans(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	// Create multiple plans
	plan1 := createTestPlan("test-cluster", "deploy")
	plan1.Version = "7.0"
	planID1, err := store.SavePlan(plan1)
	require.NoError(t, err)

	// Wait a bit to ensure different timestamps
	time.Sleep(10 * time.Millisecond)

	plan2 := createTestPlan("test-cluster", "upgrade")
	plan2.Version = "7.1"
	planID2, err := store.SavePlan(plan2)
	require.NoError(t, err)

	// List plans
	plans, err := store.ListPlans("test-cluster")
	require.NoError(t, err)
	assert.Len(t, plans, 2, "Should have 2 plans")

	// Plans should be sorted by creation time (newest first)
	assert.Equal(t, planID2, plans[0].PlanID, "Newest plan should be first")
	assert.Equal(t, planID1, plans[1].PlanID, "Older plan should be second")

	// Check metadata
	assert.Equal(t, "upgrade", plans[0].Operation)
	assert.Equal(t, "7.1", plans[0].Version)
	assert.Equal(t, "deploy", plans[1].Operation)
	assert.Equal(t, "7.0", plans[1].Version)
	assert.True(t, plans[0].Verified, "Plan should be verified")
	assert.True(t, plans[1].Verified, "Plan should be verified")
}

// REQ-PES-025: Test listing plans for empty cluster
func TestPlanStore_ListPlans_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	// List plans for non-existent cluster
	plans, err := store.ListPlans("non-existent-cluster")
	require.NoError(t, err)
	assert.Empty(t, plans, "Should return empty list for non-existent cluster")
}

// REQ-PES-026: Test getting plan metadata
func TestPlanStore_GetPlanMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	testPlan := createTestPlan("test-cluster", "deploy")
	testPlan.Version = "7.0"
	testPlan.Variant = "percona"

	// Save plan
	planID, err := store.SavePlan(testPlan)
	require.NoError(t, err)

	// Get metadata
	metadata, err := store.GetPlanMetadata("test-cluster", planID)
	require.NoError(t, err)
	assert.Equal(t, planID, metadata.PlanID)
	assert.Equal(t, "test-cluster", metadata.ClusterName)
	assert.Equal(t, "deploy", metadata.Operation)
	assert.Equal(t, "7.0", metadata.Version)
	assert.Equal(t, "percona", metadata.Variant)
	assert.True(t, metadata.IsValid)
	assert.Equal(t, 3, metadata.PhaseCount)
	assert.Greater(t, metadata.SizeBytes, int64(0))
	assert.True(t, metadata.Verified)
}

// REQ-PES-027: Test deleting plan
func TestPlanStore_DeletePlan(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	testPlan := createTestPlan("test-cluster", "deploy")

	// Save plan
	planID, err := store.SavePlan(testPlan)
	require.NoError(t, err)

	// Verify plan exists
	planPath := store.GetPlanPath("test-cluster", planID)
	assert.FileExists(t, planPath)

	// Delete plan
	err = store.DeletePlan("test-cluster", planID)
	require.NoError(t, err)

	// Verify plan is gone
	assert.NoFileExists(t, planPath)

	// Verify checksum is gone
	checksumPath := planPath + ".sha256"
	assert.NoFileExists(t, checksumPath)
}

// Test plan with pre-set ID and timestamp
func TestPlanStore_SavePlan_WithID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	testPlan := createTestPlan("test-cluster", "deploy")
	testPlan.PlanID = "custom-plan-id"
	testPlan.CreatedAt = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Save plan
	planID, err := store.SavePlan(testPlan)
	require.NoError(t, err)
	assert.Equal(t, "custom-plan-id", planID, "Should preserve custom plan ID")
	assert.Equal(t, time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC), testPlan.CreatedAt, "Should preserve custom timestamp")
}

// Test default storage directory
func TestPlanStore_DefaultStorageDir(t *testing.T) {
	store, err := plan.NewPlanStore("")
	require.NoError(t, err)
	assert.NotNil(t, store)
}

// Helper function to create a test plan
func createTestPlan(clusterName, operation string) *plan.Plan {
	return &plan.Plan{
		Operation:   operation,
		ClusterName: clusterName,
		Version:     "7.0",
		Variant:     "mongo",
		Topology: &topology.Topology{
			Mongod: []topology.MongodNode{
				{Port: 27017, DataDir: "/data/mongod-27017"},
			},
		},
		Validation: plan.ValidationResult{
			Valid:    true,
			Warnings: []plan.ValidationIssue{},
			Errors:   []plan.ValidationIssue{},
		},
		Phases: []plan.PlannedPhase{
			{Name: "prepare"},
			{Name: "deploy"},
			{Name: "initialize"},
		},
		DryRun: false,
	}
}
