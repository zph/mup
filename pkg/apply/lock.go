package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ClusterLock represents a lock on a cluster to prevent concurrent operations
// REQ-PES-039: Cluster locking prevents concurrent operations
// REQ-PES-040: Lock timeout with default 24h
// REQ-PES-041: Lock renewal mechanism
type ClusterLock struct {
	ClusterName string    `json:"cluster_name"`
	PlanID      string    `json:"plan_id"`
	Operation   string    `json:"operation"`   // "deploy", "upgrade", "import", etc.
	LockedBy    string    `json:"locked_by"`   // "user@host:pid"
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
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		storageDir = filepath.Join(homeDir, ".mup", "storage")
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

	// Check if lock already exists
	existingLock, err := m.GetLock(clusterName)
	if err == nil {
		// Lock exists - check if it's expired
		if time.Now().Before(existingLock.ExpiresAt) {
			return nil, fmt.Errorf("cluster %s is locked by %s (plan: %s, operation: %s, expires: %s)",
				clusterName, existingLock.LockedBy, existingLock.PlanID, existingLock.Operation,
				existingLock.ExpiresAt.Format(time.RFC3339))
		}
		// Lock is expired - we can take it
		fmt.Printf("Found expired lock from %s, acquiring new lock\n", existingLock.LockedBy)
	}

	// Create lock
	lock := &ClusterLock{
		ClusterName: clusterName,
		PlanID:      planID,
		Operation:   operation,
		LockedBy:    getLockedByIdentifier(),
		LockedAt:    time.Now(),
		ExpiresAt:   time.Now().Add(timeout),
		LockTimeout: timeout.String(),
		RenewCount:  0,
	}

	// Save lock
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
	if err != nil {
		// Lock doesn't exist - that's fine
		return nil
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
			return nil, fmt.Errorf("no lock found for cluster %s", clusterName)
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
	if err != nil {
		// No lock found
		return false, nil
	}

	// Check if lock is expired
	if time.Now().After(lock.ExpiresAt) {
		return false, nil
	}

	return true, nil
}

// StartLockRenewal starts a background goroutine that periodically renews the lock
// REQ-PES-041: Automatic lock renewal
// REQ-PES-050: Background renewal goroutine
func (m *LockManager) StartLockRenewal(ctx context.Context, lock *ClusterLock, renewInterval, extension time.Duration) {
	go func() {
		ticker := time.NewTicker(renewInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.RenewLock(lock, extension); err != nil {
					fmt.Printf("Warning: failed to renew lock: %v\n", err)
					return
				}
				fmt.Printf("Lock renewed for cluster %s (renew count: %d, expires: %s)\n",
					lock.ClusterName, lock.RenewCount, lock.ExpiresAt.Format(time.RFC3339))
			}
		}
	}()
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
	return filepath.Join(m.storageDir, "clusters", clusterName, "cluster.lock")
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
	tempPath := lockPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write lock file: %w", err)
	}

	if err := os.Rename(tempPath, lockPath); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return fmt.Errorf("failed to rename lock file: %w", err)
	}

	return nil
}

// getLockedByIdentifier returns a string identifying the current process
// Format: "user@hostname:pid"
func getLockedByIdentifier() string {
	hostname, _ := os.Hostname()
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}
	pid := os.Getpid()
	return fmt.Sprintf("%s@%s:%d", user, hostname, pid)
}
