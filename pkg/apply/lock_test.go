package apply_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/apply"
)

// REQ-PES-039: Test basic lock acquisition
func TestLockManager_AcquireLock(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock
	lock, err := mgr.AcquireLock("test-cluster", "plan-123", "deploy", 24*time.Hour)
	require.NoError(t, err)
	assert.NotNil(t, lock)
	assert.Equal(t, "test-cluster", lock.ClusterName)
	assert.Equal(t, "plan-123", lock.PlanID)
	assert.Equal(t, "deploy", lock.Operation)
	assert.NotEmpty(t, lock.LockedBy)
	assert.False(t, lock.LockedAt.IsZero())
	assert.False(t, lock.ExpiresAt.IsZero())
	assert.Equal(t, 0, lock.RenewCount)
}

// REQ-PES-040: Test default timeout
func TestLockManager_AcquireLock_DefaultTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock with zero timeout (should use default)
	lock, err := mgr.AcquireLock("test-cluster", "plan-123", "deploy", 0)
	require.NoError(t, err)

	// Check that expiration is approximately 24 hours from now
	expectedExpiration := time.Now().Add(24 * time.Hour)
	assert.WithinDuration(t, expectedExpiration, lock.ExpiresAt, 5*time.Second)
}

// REQ-PES-042: Test that second lock acquisition fails
func TestLockManager_AcquireLock_AlreadyLocked(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire first lock
	_, err = mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Hour)
	require.NoError(t, err)

	// Try to acquire second lock
	_, err = mgr.AcquireLock("test-cluster", "plan-456", "upgrade", 1*time.Hour)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is locked by")
}

// REQ-PES-041: Test lock renewal
func TestLockManager_RenewLock(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock
	lock, err := mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Hour)
	require.NoError(t, err)
	originalExpiration := lock.ExpiresAt

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Renew lock
	err = mgr.RenewLock(lock, 1*time.Hour)
	require.NoError(t, err)

	// Check that expiration was extended
	assert.True(t, lock.ExpiresAt.After(originalExpiration))
	assert.Equal(t, 1, lock.RenewCount)
}

// REQ-PES-043: Test that only lock owner can renew
func TestLockManager_RenewLock_NotOwner(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock
	lock, err := mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Hour)
	require.NoError(t, err)

	// Create fake lock with different owner
	fakeLock := *lock
	fakeLock.LockedBy = "fake@host:999"

	// Try to renew with fake lock
	err = mgr.RenewLock(&fakeLock, 1*time.Hour)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot renew lock: owned by")
}

// REQ-PES-044, REQ-PES-045: Test lock release
func TestLockManager_ReleaseLock(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock
	lock, err := mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Hour)
	require.NoError(t, err)

	// Release lock
	err = mgr.ReleaseLock("test-cluster", lock)
	require.NoError(t, err)

	// Verify lock is gone
	locked, err := mgr.IsLocked("test-cluster")
	require.NoError(t, err)
	assert.False(t, locked)
}

// REQ-PES-045: Test that only lock owner can release
func TestLockManager_ReleaseLock_NotOwner(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock
	lock, err := mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Hour)
	require.NoError(t, err)

	// Create fake lock with different owner
	fakeLock := *lock
	fakeLock.LockedBy = "fake@host:999"

	// Try to release with fake lock
	err = mgr.ReleaseLock("test-cluster", &fakeLock)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot release lock: owned by")
}

// REQ-PES-046: Test getting lock
func TestLockManager_GetLock(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock
	lock, err := mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Hour)
	require.NoError(t, err)

	// Get lock
	retrieved, err := mgr.GetLock("test-cluster")
	require.NoError(t, err)
	assert.Equal(t, lock.ClusterName, retrieved.ClusterName)
	assert.Equal(t, lock.PlanID, retrieved.PlanID)
	assert.Equal(t, lock.Operation, retrieved.Operation)
	assert.Equal(t, lock.LockedBy, retrieved.LockedBy)
}

// REQ-PES-046: Test IsLocked
func TestLockManager_IsLocked(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Check unlocked cluster
	locked, err := mgr.IsLocked("test-cluster")
	require.NoError(t, err)
	assert.False(t, locked)

	// Acquire lock
	_, err = mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Hour)
	require.NoError(t, err)

	// Check locked cluster
	locked, err = mgr.IsLocked("test-cluster")
	require.NoError(t, err)
	assert.True(t, locked)
}

// Test expired lock can be acquired
func TestLockManager_AcquireLock_Expired(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock with very short timeout
	_, err = mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Millisecond)
	require.NoError(t, err)

	// Wait for lock to expire
	time.Sleep(10 * time.Millisecond)

	// Should be able to acquire new lock
	lock2, err := mgr.AcquireLock("test-cluster", "plan-456", "upgrade", 1*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, "plan-456", lock2.PlanID)
}

// Test expired lock is not considered locked
func TestLockManager_IsLocked_Expired(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock with very short timeout
	_, err = mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Millisecond)
	require.NoError(t, err)

	// Wait for lock to expire
	time.Sleep(10 * time.Millisecond)

	// Should not be locked
	locked, err := mgr.IsLocked("test-cluster")
	require.NoError(t, err)
	assert.False(t, locked)
}

// REQ-PES-050: Test automatic lock renewal
func TestLockManager_StartLockRenewal(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock with short timeout
	lock, err := mgr.AcquireLock("test-cluster", "plan-123", "deploy", 100*time.Millisecond)
	require.NoError(t, err)
	originalExpiration := lock.ExpiresAt

	// Start renewal with shorter duration
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	mgr.StartLockRenewal(ctx, lock, 50*time.Millisecond, 100*time.Millisecond)

	// Wait for at least one renewal
	time.Sleep(75 * time.Millisecond)

	// Check that lock was renewed
	assert.True(t, lock.ExpiresAt.After(originalExpiration))
	assert.Greater(t, lock.RenewCount, 0)

	// Wait for goroutine to finish
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)
}

// REQ-PES-051: Test cleanup of expired locks
func TestLockManager_CleanupExpiredLocks(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Create multiple locks with different timeouts
	_, err = mgr.AcquireLock("cluster1", "plan-1", "deploy", 1*time.Millisecond)
	require.NoError(t, err)

	_, err = mgr.AcquireLock("cluster2", "plan-2", "deploy", 1*time.Hour)
	require.NoError(t, err)

	_, err = mgr.AcquireLock("cluster3", "plan-3", "deploy", 1*time.Millisecond)
	require.NoError(t, err)

	// Wait for some locks to expire
	time.Sleep(10 * time.Millisecond)

	// Cleanup expired locks
	err = mgr.CleanupExpiredLocks()
	require.NoError(t, err)

	// Check that expired locks are gone
	locked1, _ := mgr.IsLocked("cluster1")
	assert.False(t, locked1, "cluster1 should not be locked")

	locked3, _ := mgr.IsLocked("cluster3")
	assert.False(t, locked3, "cluster3 should not be locked")

	// Check that non-expired lock is still there
	locked2, _ := mgr.IsLocked("cluster2")
	assert.True(t, locked2, "cluster2 should still be locked")
}

// REQ-PES-052: Test force unlock
func TestLockManager_ForceUnlock(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock
	_, err = mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Hour)
	require.NoError(t, err)

	// Verify locked
	locked, _ := mgr.IsLocked("test-cluster")
	assert.True(t, locked)

	// Force unlock
	err = mgr.ForceUnlock("test-cluster")
	require.NoError(t, err)

	// Verify unlocked
	locked, _ = mgr.IsLocked("test-cluster")
	assert.False(t, locked)
}

// Test releasing expired lock is allowed
func TestLockManager_ReleaseLock_Expired(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock with short timeout
	lock, err := mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Millisecond)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Should be able to release expired lock
	err = mgr.ReleaseLock("test-cluster", lock)
	require.NoError(t, err)
}

// Test default storage directory
func TestLockManager_DefaultStorageDir(t *testing.T) {
	mgr, err := apply.NewLockManager("")
	require.NoError(t, err)
	assert.NotNil(t, mgr)
}

// Test atomic lock file writes
func TestLockManager_AtomicWrites(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := apply.NewLockManager(tmpDir)
	require.NoError(t, err)

	// Acquire lock
	_, err = mgr.AcquireLock("test-cluster", "plan-123", "deploy", 1*time.Hour)
	require.NoError(t, err)

	// Verify no .tmp file remains
	lockPath := mgr.GetLockPath("test-cluster")
	tmpPath := lockPath + ".tmp"
	assert.NoFileExists(t, tmpPath, "Temporary file should not exist")

	// Verify lock file exists
	assert.FileExists(t, lockPath, "Lock file should exist")
}
