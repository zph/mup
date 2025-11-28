package apply_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/topology"
)

// TestIntegration_PlanStoreAndLocking tests the integrated flow of:
// 1. Saving a plan to PlanStore
// 2. Acquiring a cluster lock
// 3. Loading and verifying the plan
// 4. Releasing the lock
func TestIntegration_PlanStoreAndLocking(t *testing.T) {
	tmpDir := t.TempDir()
	clusterName := "test-cluster"
	operation := "deploy"

	// Step 1: Create and save a plan
	testPlan := &plan.Plan{
		ClusterName: clusterName,
		Operation:   operation,
		Version:     "7.0.0",
		Topology: &topology.Topology{
			Global: topology.GlobalConfig{
				User:    "test",
				SSHPort: 22,
			},
			Mongod: []topology.MongodNode{
				{Host: "localhost", Port: 27017},
			},
		},
		Phases: []plan.PlannedPhase{
			{
				Name:        "prepare",
				Description: "Preparation phase",
				Order:       1,
				Operations: []plan.PlannedOperation{
					{
						Type:        "create_directory",
						Description: "Create test directory",
						Target: plan.OperationTarget{
							Host: "localhost",
							Port: 27017,
						},
						Params: map[string]interface{}{
							"path": "/tmp/test",
							"mode": float64(0755),
						},
					},
				},
			},
		},
		Validation: plan.ValidationResult{
			Valid:    true,
			Errors:   []plan.ValidationIssue{},
			Warnings: []plan.ValidationIssue{},
		},
	}

	// Create PlanStore and save plan
	planStore, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	planID, err := planStore.SavePlan(testPlan)
	require.NoError(t, err)
	assert.NotEmpty(t, planID)
	t.Logf("✅ Plan saved with ID: %s", planID)

	// Step 2: Verify plan integrity
	verified, err := planStore.VerifyPlan(clusterName, planID)
	require.NoError(t, err)
	assert.True(t, verified)
	t.Logf("✅ Plan integrity verified (SHA-256)")

	// Step 3: Acquire cluster lock
	lockMgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	lock, err := lockMgr.AcquireLock(clusterName, planID, operation, 1*time.Hour)
	require.NoError(t, err)
	assert.NotNil(t, lock)
	assert.Equal(t, clusterName, lock.ClusterName)
	assert.Equal(t, planID, lock.PlanID)
	t.Logf("✅ Cluster lock acquired by: %s", lock.LockedBy)

	// Step 4: Verify cluster is locked
	locked, err := lockMgr.IsLocked(clusterName)
	require.NoError(t, err)
	assert.True(t, locked)

	// Step 5: Try to acquire another lock (should fail)
	_, err = lockMgr.AcquireLock(clusterName, "another-plan", "upgrade", 1*time.Hour)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is locked by")
	t.Logf("✅ Second lock acquisition prevented (as expected)")

	// Step 6: Load plan from store
	loadedPlan, err := planStore.LoadPlan(clusterName, planID)
	require.NoError(t, err)
	assert.Equal(t, clusterName, loadedPlan.ClusterName)
	assert.Equal(t, planID, loadedPlan.PlanID)
	assert.Equal(t, operation, loadedPlan.Operation)
	t.Logf("✅ Plan loaded successfully")

	// Step 7: Renew lock (simulate long-running operation)
	originalExpiry := lock.ExpiresAt
	time.Sleep(10 * time.Millisecond)

	err = lockMgr.RenewLock(lock, 1*time.Hour)
	require.NoError(t, err)
	assert.True(t, lock.ExpiresAt.After(originalExpiry))
	assert.Equal(t, 1, lock.RenewCount)
	t.Logf("✅ Lock renewed successfully (count: %d)", lock.RenewCount)

	// Step 8: Release lock
	err = lockMgr.ReleaseLock(clusterName, lock)
	require.NoError(t, err)
	t.Logf("✅ Lock released")

	// Step 9: Verify cluster is no longer locked
	locked, err = lockMgr.IsLocked(clusterName)
	require.NoError(t, err)
	assert.False(t, locked)
	t.Logf("✅ Cluster unlocked successfully")

	// Step 10: Verify we can acquire a new lock now
	lock2, err := lockMgr.AcquireLock(clusterName, "new-plan", "upgrade", 1*time.Hour)
	require.NoError(t, err)
	assert.NotNil(t, lock2)
	assert.Equal(t, "new-plan", lock2.PlanID)
	t.Logf("✅ New lock acquired after release")

	// Cleanup
	lockMgr.ReleaseLock(clusterName, lock2)
}

// TestIntegration_LockRenewalDuringApply tests automatic lock renewal during plan execution
func TestIntegration_LockRenewalDuringApply(t *testing.T) {
	tmpDir := t.TempDir()
	clusterName := "renewal-test"

	// Create lock manager
	lockMgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock with short timeout
	lock, err := lockMgr.AcquireLock(clusterName, "plan-123", "deploy", 100*time.Millisecond)
	require.NoError(t, err)

	// Start automatic renewal
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	lockMgr.StartLockRenewal(ctx, lock, 50*time.Millisecond, 100*time.Millisecond)

	// Wait for renewals to happen
	time.Sleep(120 * time.Millisecond)

	// Verify lock was renewed multiple times
	assert.Greater(t, lock.RenewCount, 0, "Lock should have been renewed at least once")
	t.Logf("✅ Lock auto-renewed %d times", lock.RenewCount)

	// Verify lock is still active (not expired despite short original timeout)
	locked, err := lockMgr.IsLocked(clusterName)
	require.NoError(t, err)
	assert.True(t, locked, "Lock should still be active due to renewals")
	t.Logf("✅ Lock still active after multiple renewals")

	// Wait for context to complete
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)

	// Cleanup
	lockMgr.ReleaseLock(clusterName, lock)
}

// TestIntegration_PlanListingAndMetadata tests plan metadata operations
func TestIntegration_PlanListingAndMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	planStore, err := plan.NewPlanStore(tmpDir)
	require.NoError(t, err)

	// Create multiple plans for different clusters
	clusters := []string{"cluster-a", "cluster-b", "cluster-a"}
	planIDs := make([]string, 0)

	for i, clusterName := range clusters {
		testPlan := &plan.Plan{
			ClusterName: clusterName,
			Operation:   "deploy",
			Version:     "7.0.0",
			Topology: &topology.Topology{
				Global: topology.GlobalConfig{
					User: "test",
				},
			},
			Phases: []plan.PlannedPhase{},
			Validation: plan.ValidationResult{
				Valid:    true,
				Errors:   []plan.ValidationIssue{},
				Warnings: []plan.ValidationIssue{},
			},
		}

		planID, err := planStore.SavePlan(testPlan)
		require.NoError(t, err)
		planIDs = append(planIDs, planID)
		t.Logf("Created plan %d: %s for cluster: %s", i+1, planID, clusterName)
	}

	// List plans for cluster-a (should have 2)
	plansA, err := planStore.ListPlans("cluster-a")
	require.NoError(t, err)
	assert.Len(t, plansA, 2)
	t.Logf("✅ Found 2 plans for cluster-a")

	// List plans for cluster-b (should have 1)
	plansB, err := planStore.ListPlans("cluster-b")
	require.NoError(t, err)
	assert.Len(t, plansB, 1)
	t.Logf("✅ Found 1 plan for cluster-b")

	// Get metadata for first plan
	metadata, err := planStore.GetPlanMetadata("cluster-a", planIDs[0])
	require.NoError(t, err)
	assert.Equal(t, "cluster-a", metadata.ClusterName)
	assert.Equal(t, planIDs[0], metadata.PlanID)
	assert.True(t, metadata.IsValid)
	t.Logf("✅ Retrieved metadata for plan: %s", metadata.PlanID)

	// Delete one plan
	err = planStore.DeletePlan("cluster-a", planIDs[0])
	require.NoError(t, err)
	t.Logf("✅ Deleted plan: %s", planIDs[0])

	// Verify plan count decreased
	plansA, err = planStore.ListPlans("cluster-a")
	require.NoError(t, err)
	assert.Len(t, plansA, 1)
	t.Logf("✅ Verified plan deletion - cluster-a now has 1 plan")
}
