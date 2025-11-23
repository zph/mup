package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zph/mup/pkg/deploy"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/supervisor"
	"github.com/zph/mup/pkg/topology"
)

// LocalNodeOperations implements NodeOperations for local supervisor-based deployments
// Uses dual-supervisor pattern: old supervisor manages old version, new supervisor manages new version
type LocalNodeOperations struct {
	// Old supervisor manager (points to specific old version directory)
	supervisorMgr *supervisor.Manager

	// New supervisor manager (points to new version directory)
	newSupervisorMgr *supervisor.Manager

	// Binary manager for downloading MongoDB binaries
	binaryMgr *deploy.BinaryManager

	// Cluster configuration
	clusterName string
	clusterMeta *meta.ClusterMetadata
	metaDir     string
	topology    *topology.Topology

	// Version paths
	oldVersionDir string // e.g., ~/.mup/storage/clusters/my-cluster/v4.0
	newVersionDir string // e.g., ~/.mup/storage/clusters/my-cluster/v4.2
	oldBinPath    string // Old version bin directory
	newBinPath    string // New version bin directory

	// Version strings
	fromVersion string // e.g., "4.0"
	toVersion   string // e.g., "4.2"
}

// NewLocalNodeOperations creates a new LocalNodeOperations instance
// Accepts pre-initialized supervisor and binary managers to share resources with LocalUpgrader
func NewLocalNodeOperations(
	supervisorMgr *supervisor.Manager,
	binaryMgr *deploy.BinaryManager,
	config UpgradeConfig,
	clusterMeta *meta.ClusterMetadata,
	oldVersionDir string,
) *LocalNodeOperations {
	return &LocalNodeOperations{
		supervisorMgr: supervisorMgr,
		binaryMgr:     binaryMgr,
		clusterName:   config.ClusterName,
		clusterMeta:   clusterMeta,
		metaDir:       config.MetaDir,
		topology:      config.Topology,
		oldVersionDir: oldVersionDir,
		fromVersion:   config.FromVersion,
		toVersion:     config.ToVersion,
	}
}

// SetNewSupervisor sets the new supervisor manager (called after SetupVersionEnvironment)
func (ops *LocalNodeOperations) SetNewSupervisor(newSupervisorMgr *supervisor.Manager) {
	ops.newSupervisorMgr = newSupervisorMgr
}

// SetVersionDirectories sets the version directory paths (called after SetupVersionEnvironment)
func (ops *LocalNodeOperations) SetVersionDirectories(newVersionDir, newBinPath string) {
	ops.newVersionDir = newVersionDir
	ops.newBinPath = newBinPath
}

// StopNode stops a MongoDB process in the OLD supervisor
func (ops *LocalNodeOperations) StopNode(ctx context.Context, nodeID string) error {
	return ops.supervisorMgr.StopProcess(nodeID)
}

// StartNode starts a MongoDB process in the NEW supervisor
func (ops *LocalNodeOperations) StartNode(ctx context.Context, nodeID string) error {
	if ops.newSupervisorMgr == nil {
		return fmt.Errorf("new supervisor not initialized (call SetupVersionEnvironment first)")
	}
	return ops.newSupervisorMgr.StartProcess(nodeID)
}

// RestartNode restarts a MongoDB process
func (ops *LocalNodeOperations) RestartNode(ctx context.Context, nodeID string) error {
	if err := ops.StopNode(ctx, nodeID); err != nil {
		return fmt.Errorf("failed to stop: %w", err)
	}
	time.Sleep(1 * time.Second)
	return ops.StartNode(ctx, nodeID)
}

// GetNodeStatus retrieves the current status of a MongoDB process
func (ops *LocalNodeOperations) GetNodeStatus(ctx context.Context, nodeID string) (*ProcessStatus, error) {
	if ops.newSupervisorMgr == nil {
		return nil, fmt.Errorf("new supervisor not initialized")
	}

	status, err := ops.newSupervisorMgr.GetProcessStatus(nodeID)
	if err != nil {
		return &ProcessStatus{
			State:     "UNKNOWN",
			IsHealthy: false,
			LastError: err,
		}, err
	}

	return &ProcessStatus{
		State:     status.State,
		IsHealthy: strings.ToUpper(status.State) == "RUNNING",
		PID:       status.PID,
		Uptime:    time.Duration(status.Uptime) * time.Second,
	}, nil
}

// WaitForNodeHealthy waits for a MongoDB process to be healthy
func (ops *LocalNodeOperations) WaitForNodeHealthy(ctx context.Context, nodeID string, timeout time.Duration) error {
	if ops.newSupervisorMgr == nil {
		return fmt.Errorf("new supervisor not initialized")
	}

	start := time.Now()
	var lastErr error
	var lastState string

	for time.Since(start) < timeout {
		status, err := ops.newSupervisorMgr.GetProcessStatus(nodeID)
		if err == nil {
			lastState = status.State
			if strings.ToUpper(status.State) == "RUNNING" {
				return nil
			}

			// If process is in FATAL state, fail immediately with log
			if strings.ToUpper(status.State) == "FATAL" {
				return ops.readLogsAndFail(nodeID)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}

	// Timeout - provide detailed diagnostics
	errMsg := fmt.Sprintf("process did not start within %v", timeout)
	if lastState != "" {
		errMsg += fmt.Sprintf(" (last state: %s)", lastState)
	}
	if lastErr != nil {
		errMsg += fmt.Sprintf(" (last error: %v)", lastErr)
	}

	return fmt.Errorf("%s", errMsg)
}

// readLogsAndFail reads the last 20 lines of a process log and returns an error
func (ops *LocalNodeOperations) readLogsAndFail(nodeID string) error {
	logFile := filepath.Join(ops.newVersionDir, nodeID, "log", fmt.Sprintf("supervisor-%s.log", nodeID))
	logContent := ""

	if content, err := os.ReadFile(logFile); err == nil {
		lines := strings.Split(string(content), "\n")
		start := len(lines) - 20
		if start < 0 {
			start = 0
		}
		logContent = strings.Join(lines[start:], "\n")
	}

	return fmt.Errorf("process in FATAL state\nLog file: %s\n%s", logFile, logContent)
}

// PrepareBinaries downloads and prepares MongoDB binaries for the target version
func (ops *LocalNodeOperations) PrepareBinaries(ctx context.Context, version string, variant deploy.Variant) error {
	// Parse platform
	platform := deploy.Platform{
		OS:   "darwin", // TODO: Get from runtime
		Arch: "arm64",  // TODO: Get from runtime
	}

	// Download binaries to cache
	binPath, err := ops.binaryMgr.GetBinPathWithVariant(version, variant, platform)
	if err != nil {
		return fmt.Errorf("failed to get binaries: %w", err)
	}

	// Verify mongod binary exists
	mongodPath := filepath.Join(binPath, "mongod")
	if _, err := os.Stat(mongodPath); err != nil {
		return fmt.Errorf("mongod binary not found at %s: %w", mongodPath, err)
	}

	ops.newBinPath = binPath
	return nil
}

// UpdateNodeConfig updates the configuration file for a specific node
// This is a placeholder - actual implementation would use ConfigRegenerator
func (ops *LocalNodeOperations) UpdateNodeConfig(ctx context.Context, nodeID string, version string) error {
	// Configuration is already generated during SetupVersionEnvironment
	// This method exists for interface compatibility and future per-node config updates
	return nil
}

// VerifyNodeVersion verifies that a node is running the expected MongoDB version
// TODO: Connect to MongoDB and verify version via buildInfo command
func (ops *LocalNodeOperations) VerifyNodeVersion(ctx context.Context, nodeID string, expectedVersion string) error {
	// For now, just check that the process is running
	// Full implementation would connect via MongoDB driver and run buildInfo
	status, err := ops.GetNodeStatus(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node status: %w", err)
	}

	if !status.IsHealthy {
		return fmt.Errorf("node is not healthy (state: %s)", status.State)
	}

	return nil
}

// SetupVersionEnvironment prepares the environment for the new version
// Creates version directories, copies binaries, creates 'next' symlink, starts new supervisor
func (ops *LocalNodeOperations) SetupVersionEnvironment(ctx context.Context, version string) error {
	// This will be implemented by extracting logic from local.go's:
	// - setupVersionDirectories()
	// - regenerateSupervisorConfig()
	// - startNewSupervisor()

	// For now, return an error indicating this needs to be called from LocalUpgrader
	return fmt.Errorf("SetupVersionEnvironment must be called through LocalUpgrader (not yet refactored)")
}

// SwitchToNewVersion atomically switches from old to new version
// Updates symlinks: current→next, previous→old, removes next
func (ops *LocalNodeOperations) SwitchToNewVersion(ctx context.Context, oldVersion, newVersion string) error {
	clusterDir := ops.metaDir
	currentLink := filepath.Join(clusterDir, SymlinkCurrent)
	previousLink := filepath.Join(clusterDir, SymlinkPrevious)
	nextLink := filepath.Join(clusterDir, SymlinkNext)

	// 1. Get current target (will become previous)
	oldCurrentTarget := ""
	if target, err := os.Readlink(currentLink); err == nil {
		oldCurrentTarget = target
	}

	// 2. Get next target (will become current)
	nextTarget, err := os.Readlink(nextLink)
	if err != nil {
		return fmt.Errorf("failed to read next symlink: %w (upgrade state may be corrupted)", err)
	}

	// 3. Remove old symlinks
	os.Remove(previousLink) // Remove old previous
	os.Remove(currentLink)  // Remove current

	// 4. Create previous → old current (for rollback)
	if oldCurrentTarget != "" {
		if err := os.Symlink(oldCurrentTarget, previousLink); err != nil {
			// Log warning but continue - this is not critical
			fmt.Printf("\n  ⚠️  Warning: failed to create previous symlink: %v\n", err)
		}
	}

	// 5. Create current → next (atomic switch to new version)
	if err := os.Symlink(nextTarget, currentLink); err != nil {
		return fmt.Errorf("failed to create current symlink: %w", err)
	}

	// 6. Remove next symlink (upgrade complete)
	os.Remove(nextLink)

	fmt.Printf("\n  ℹ️  Symlinks updated: current -> %s, previous -> %s\n", nextTarget, oldCurrentTarget)
	return nil
}

// CleanupOldVersion cleans up after a failed upgrade
// Removes the 'next' symlink to keep cluster stable on 'current' version
func (ops *LocalNodeOperations) CleanupOldVersion(ctx context.Context, version string) error {
	clusterDir := ops.metaDir
	nextLink := filepath.Join(clusterDir, SymlinkNext)

	// Check if next symlink exists
	if _, err := os.Lstat(nextLink); err == nil {
		// Remove it
		if err := os.Remove(nextLink); err != nil {
			fmt.Printf("\n⚠️  Warning: failed to cleanup 'next' symlink: %v\n", err)
			return err
		}
		fmt.Printf("\n  ✓ Cleaned up 'next' symlink (upgrade failed, cluster remains on 'current')\n")
	}

	return nil
}

// StopOldSupervisor stops the old supervisor and switches to new one
// This is called after all processes have been migrated
func (ops *LocalNodeOperations) StopOldSupervisor(ctx context.Context) error {
	if err := ops.supervisorMgr.Stop(ctx); err != nil {
		// Log error but don't fail - supervisor might already be stopped
		fmt.Printf("  ⚠️  Warning: failed to stop old supervisor: %v\n", err)
	} else {
		fmt.Printf("  ✓ Old supervisor stopped\n")
	}

	// Switch to new supervisor as primary
	ops.supervisorMgr = ops.newSupervisorMgr

	fmt.Printf("  ✓ Now running entirely on new version supervisor\n")
	return nil
}
