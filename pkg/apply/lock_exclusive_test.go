package apply

import (
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	lockTestCluster    = "lock-exclusive"
	lockTestPlan       = "plan-1"
	lockTestOperation  = "deploy"
	lockTestContenders = 16
)

// exitedPID returns the PID of a process that has already exited.
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}

// LOCAL-017: only one of several concurrent acquirers gets the lock.
func TestAcquireLock_IsExclusiveUnderContention(t *testing.T) {
	mgr, err := NewLockManager(t.TempDir())
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make(chan error, lockTestContenders)
	start := make(chan struct{})
	for i := 0; i < lockTestContenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := mgr.AcquireLock(lockTestCluster, lockTestPlan, lockTestOperation, time.Hour)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var acquired int
	for err := range results {
		if err == nil {
			acquired++
		}
	}
	assert.Equal(t, 1, acquired)
}

// LOCAL-018: a lock whose owning process on this host has exited is stale and
// can be taken over without waiting for expiry.
func TestAcquireLock_TakesOverLockOfDeadProcess(t *testing.T) {
	mgr, err := NewLockManager(t.TempDir())
	require.NoError(t, err)

	stale, err := mgr.AcquireLock(lockTestCluster, lockTestPlan, lockTestOperation, time.Hour)
	require.NoError(t, err)
	stale.PID = exitedPID(t)
	require.NoError(t, mgr.saveLock(stale))

	lock, err := mgr.AcquireLock(lockTestCluster, "plan-2", lockTestOperation, time.Hour)

	require.NoError(t, err)
	assert.Equal(t, "plan-2", lock.PlanID)
	assert.Equal(t, os.Getpid(), lock.PID)
}

// LOCAL-018: a lock held by a live process is respected.
func TestAcquireLock_RespectsLiveOwner(t *testing.T) {
	mgr, err := NewLockManager(t.TempDir())
	require.NoError(t, err)

	_, err = mgr.AcquireLock(lockTestCluster, lockTestPlan, lockTestOperation, time.Hour)
	require.NoError(t, err)

	_, err = mgr.AcquireLock(lockTestCluster, "plan-2", lockTestOperation, time.Hour)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is locked by")
}

// LOCAL-019: an unreadable lock file is never silently replaced.
func TestAcquireLock_RefusesCorruptLockFile(t *testing.T) {
	mgr, err := NewLockManager(t.TempDir())
	require.NoError(t, err)

	lock, err := mgr.AcquireLock(lockTestCluster, lockTestPlan, lockTestOperation, time.Hour)
	require.NoError(t, err)
	require.NoError(t, mgr.ReleaseLock(lockTestCluster, lock))
	lockPath := mgr.GetLockPath(lockTestCluster)
	corrupt := []byte("{not json")
	require.NoError(t, os.WriteFile(lockPath, corrupt, 0o644))

	_, err = mgr.AcquireLock(lockTestCluster, lockTestPlan, lockTestOperation, time.Hour)

	require.Error(t, err)
	onDisk, readErr := os.ReadFile(lockPath)
	require.NoError(t, readErr)
	assert.Equal(t, corrupt, onDisk)

	locked, err := mgr.IsLocked(lockTestCluster)
	require.Error(t, err)
	assert.True(t, locked)
}
