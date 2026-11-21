package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zph/mup/pkg/paths"
)

// ErrNoLock is returned by GetLock when a cluster has no lock file.
var ErrNoLock = errors.New("no lock found")

const (
	lockFileName      = "cluster.lock"
	lockGuardFileName = "cluster.lock.guard"
	unknownHostname   = "unknown-host"
	unknownUser       = "unknown"
)

// ClusterLock represents a lock on a cluster to prevent concurrent operations
// REQ-PES-039: Cluster locking prevents concurrent operations
// REQ-PES-040: Lock timeout with default 24h
// REQ-PES-041: Lock renewal mechanism
type ClusterLock struct {
	ClusterName string    `json:"cluster_name"`
	PlanID      string    `json:"plan_id"`
	Operation   string    `json:"operation"` // "deploy", "upgrade", "import", etc.
	LockedBy    string    `json:"locked_by"` // "user@host:pid"
	Hostname    string    `json:"hostname,omitempty"`
	PID         int       `json:"pid,omitempty"`
	LockedAt    time.Time `json:"locked_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	LockTimeout string    `json:"lock_timeout"` // Duration string like "24h"
	RenewCount  int       `json:"renew_count"`  // Number of times lock has been renewed
}

// LockManager manages cluster locks
// REQ-PES-039: Lock manager for cluster operations
type LockManager struct {
	storageDir string // Base storage directory (~/.mup/storage)
}

// NewLockManager creates a new lock manager
func NewLockManager(storageDir string) (*LockManager, error) {
	if storageDir == "" {
		dir, err := paths.StorageDir()
		if err != nil {
			return nil, err
		}
		storageDir = dir
	}

	return &LockManager{
		storageDir: storageDir,
	}, nil
}

// AcquireLock attempts to acquire a lock on a cluster
// REQ-PES-039: Acquire lock with timeout
// REQ-PES-040: Default timeout is 24 hours
// REQ-PES-042: Returns error if cluster is already locked
func (m *LockManager) AcquireLock(clusterName, planID, operation string, timeout time.Duration) (*ClusterLock, error) {
	// Use default timeout if not specified
	if timeout == 0 {
		timeout = 24 * time.Hour
	}

	// LOCAL-017: the check-then-write below runs under an OS-level guard so
	// concurrent mup processes cannot both observe "no lock" and both write.
	release, err := m.guard(clusterName)
	if err != nil {
		return nil, err
	}
	defer release()

	existingLock, err := m.GetLock(clusterName)
	switch {
	case err == nil:
		if !existingLock.IsStale() {
			return nil, fmt.Errorf("cluster %s is locked by %s (plan: %s, operation: %s, expires: %s)",
				clusterName, existingLock.LockedBy, existingLock.PlanID, existingLock.Operation,
				existingLock.ExpiresAt.Format(time.RFC3339))
		}
		// LOCAL-018
		fmt.Printf("Found stale lock from %s, acquiring new lock\n", existingLock.LockedBy)
	case errors.Is(err, ErrNoLock):
	default:
		// LOCAL-019: never overwrite a lock file that cannot be read
		return nil, fmt.Errorf("cannot determine lock state for cluster %s (remove it with 'mup cluster unlock %s'): %w", clusterName, clusterName, err)
	}

	hostname, user, pid := lockOwner()
	lock := &ClusterLock{
		ClusterName: clusterName,
		PlanID:      planID,
		Operation:   operation,
		LockedBy:    fmt.Sprintf("%s@%s:%d", user, hostname, pid),
		Hostname:    hostname,
		PID:         pid,
		LockedAt:    time.Now(),
		ExpiresAt:   time.Now().Add(timeout),
		LockTimeout: timeout.String(),
		RenewCount:  0,
	}

	if err := m.saveLock(lock); err != nil {
		return nil, fmt.Errorf("failed to save lock: %w", err)
	}

	return lock, nil
}

// RenewLock extends the expiration time of an existing lock
// REQ-PES-041: Lock renewal mechanism
// REQ-PES-043: Only the lock owner can renew
func (m *LockManager) RenewLock(lock *ClusterLock, extension time.Duration) error {
	// Get current lock
	currentLock, err := m.GetLock(lock.ClusterName)
	if err != nil {
		return fmt.Errorf("lock not found: %w", err)
	}

	// Verify we own the lock
	if currentLock.LockedBy != lock.LockedBy {
		return fmt.Errorf("cannot renew lock: owned by %s, not %s", currentLock.LockedBy, lock.LockedBy)
	}

	// Verify lock hasn't expired
	if time.Now().After(currentLock.ExpiresAt) {
		return fmt.Errorf("cannot renew expired lock")
	}

	// Extend expiration
	lock.ExpiresAt = time.Now().Add(extension)
	lock.RenewCount++

	// Save updated lock
	if err := m.saveLock(lock); err != nil {
		return fmt.Errorf("failed to save renewed lock: %w", err)
	}

	return nil
}

// ReleaseLock removes a lock
// REQ-PES-044: Lock release mechanism
// REQ-PES-045: Only the lock owner can release
func (m *LockManager) ReleaseLock(clusterName string, lock *ClusterLock) error {
	// Get current lock
	currentLock, err := m.GetLock(clusterName)
	if errors.Is(err, ErrNoLock) {
		return nil
	}
	if err != nil {
		return err
	}

	// Verify we own the lock (allow release if lock is expired)
	if currentLock.LockedBy != lock.LockedBy && time.Now().Before(currentLock.ExpiresAt) {
		return fmt.Errorf("cannot release lock: owned by %s, not %s", currentLock.LockedBy, lock.LockedBy)
	}

	// Remove lock file
	lockPath := m.GetLockPath(clusterName)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove lock file: %w", err)
	}

	return nil
}

// GetLock retrieves the current lock for a cluster
// REQ-PES-046: Query lock status
func (m *LockManager) GetLock(clusterName string) (*ClusterLock, error) {
	lockPath := m.GetLockPath(clusterName)

	// Read lock file
	data, err := os.ReadFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w for cluster %s", ErrNoLock, clusterName)
		}
		return nil, fmt.Errorf("failed to read lock file: %w", err)
	}

	// Parse lock
	var lock ClusterLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("failed to parse lock file: %w", err)
	}

	return &lock, nil
}

// IsLocked checks if a cluster is currently locked
// REQ-PES-046: Check if cluster is locked
func (m *LockManager) IsLocked(clusterName string) (bool, error) {
	lock, err := m.GetLock(clusterName)
	if errors.Is(err, ErrNoLock) {
		return false, nil
	}
	if err != nil {
		// LOCAL-019: an unreadable lock is treated as held
		return true, err
	}

	return !lock.IsStale(), nil
}

// StartLockRenewal starts a background goroutine that periodically renews the lock
// REQ-PES-041: Automatic lock renewal
// REQ-PES-050: Background renewal goroutine
//
// The returned channel is closed once renewal has stopped. Cancel ctx and wait
// for it before releasing the lock, so a renewal in flight cannot rewrite the
// lock file after it has been released.
func (m *LockManager) StartLockRenewal(ctx context.Context, lock *ClusterLock, renewInterval, extension time.Duration) <-chan struct{} {
	// The goroutine renews its own copy so it never mutates the caller's lock
	// concurrently; the lock file holds the current expiry and renew count.
	renewing := *lock
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(renewInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.RenewLock(&renewing, extension); err != nil {
					fmt.Printf("Warning: failed to renew lock: %v\n", err)
					return
				}
				fmt.Printf("Lock renewed for cluster %s (renew count: %d, expires: %s)\n",
					renewing.ClusterName, renewing.RenewCount, renewing.ExpiresAt.Format(time.RFC3339))
			}
		}
	}()

	return done
}

// CleanupExpiredLocks removes all expired locks
// REQ-PES-051: Cleanup expired locks
func (m *LockManager) CleanupExpiredLocks() error {
	clustersDir := filepath.Join(m.storageDir, "clusters")

	// Read all cluster directories
	entries, err := os.ReadDir(clustersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read clusters directory: %w", err)
	}

	var cleanedCount int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		clusterName := entry.Name()
		lock, err := m.GetLock(clusterName)
		if err != nil {
			// No lock or error reading - skip
			continue
		}

		// Check if expired
		if time.Now().After(lock.ExpiresAt) {
			lockPath := m.GetLockPath(clusterName)
			if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
				fmt.Printf("Warning: failed to remove expired lock for %s: %v\n", clusterName, err)
				continue
			}
			cleanedCount++
			fmt.Printf("Removed expired lock for cluster %s (was locked by %s)\n", clusterName, lock.LockedBy)
		}
	}

	if cleanedCount > 0 {
		fmt.Printf("Cleaned up %d expired lock(s)\n", cleanedCount)
	}

	return nil
}

// ForceUnlock removes a lock regardless of ownership (admin operation)
// REQ-PES-052: Force unlock for admin operations
func (m *LockManager) ForceUnlock(clusterName string) error {
	lockPath := m.GetLockPath(clusterName)

	// Remove lock file
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove lock file: %w", err)
	}

	return nil
}

// GetLockPath returns the path to a lock file
func (m *LockManager) GetLockPath(clusterName string) string {
	return filepath.Join(m.storageDir, "clusters", clusterName, lockFileName)
}

// saveLock saves a lock to disk atomically
// REQ-PES-053: Atomic lock file writes
func (m *LockManager) saveLock(lock *ClusterLock) error {
	lockPath := m.GetLockPath(lock.ClusterName)

	// Create directory if needed
	lockDir := filepath.Dir(lockPath)
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return fmt.Errorf("failed to create lock directory: %w", err)
	}

	// Serialize lock
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize lock: %w", err)
	}

	// Write atomically (write to temp, then rename)
	tempPath := fmt.Sprintf("%s.%d.tmp", lockPath, os.Getpid())
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write lock file: %w", err)
	}

	if err := os.Rename(tempPath, lockPath); err != nil {
		_ = os.Remove(tempPath) // Clean up temp file
		return fmt.Errorf("failed to rename lock file: %w", err)
	}

	return nil
}

// lockOwner identifies the current process.
func lockOwner() (hostname, user string, pid int) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = unknownHostname
	}
	user = os.Getenv("USER")
	if user == "" {
		user = unknownUser
	}
	return hostname, user, os.Getpid()
}

// IsStale reports whether a lock no longer protects anything: it has expired,
// or the process that took it on this host has exited.
// LOCAL-018
func (l *ClusterLock) IsStale() bool {
	if time.Now().After(l.ExpiresAt) {
		return true
	}
	if l.PID <= 0 {
		return false
	}
	hostname, _, _ := lockOwner()
	if l.Hostname != hostname {
		return false
	}
	return errors.Is(syscall.Kill(l.PID, 0), syscall.ESRCH)
}

// guard takes an exclusive OS-level lock that serializes lock acquisition for
// a cluster across processes. The returned function releases it.
// LOCAL-017
func (m *LockManager) guard(clusterName string) (func(), error) {
	lockDir := filepath.Dir(m.GetLockPath(clusterName))
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory: %w", err)
	}

	guardFile, err := os.OpenFile(filepath.Join(lockDir, lockGuardFileName), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock guard: %w", err)
	}
	if err := syscall.Flock(int(guardFile.Fd()), syscall.LOCK_EX); err != nil {
		closeErr := guardFile.Close()
		return nil, errors.Join(fmt.Errorf("failed to take lock guard: %w", err), closeErr)
	}

	return func() {
		// Closing the descriptor releases the flock.
		if err := guardFile.Close(); err != nil {
			fmt.Printf("Warning: failed to release lock guard: %v\n", err)
		}
	}, nil
}
