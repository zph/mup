package upgrade

import (
	"context"
	"time"

	"github.com/zph/mup/pkg/deploy"
)

// NodeOperations defines executor-agnostic node lifecycle operations
// This interface abstracts process management to support both local (supervisor-based)
// and remote (SSH-based) deployments.
//
// Implementations:
//   - LocalNodeOperations: Supervisor-based process management for local deployments
//   - SSHNodeOperations: SSH-based process management for remote deployments (future)
type NodeOperations interface {
	// Process Lifecycle Operations
	// These operations manage the lifecycle of MongoDB processes (mongod/mongos)

	// StopNode stops a MongoDB process
	// nodeID: Process identifier (e.g., "mongod-27017", "mongos-27018")
	StopNode(ctx context.Context, nodeID string) error

	// StartNode starts a MongoDB process with the new version
	// nodeID: Process identifier
	StartNode(ctx context.Context, nodeID string) error

	// RestartNode restarts a MongoDB process
	// nodeID: Process identifier
	RestartNode(ctx context.Context, nodeID string) error

	// GetNodeStatus retrieves the current status of a MongoDB process
	// nodeID: Process identifier
	GetNodeStatus(ctx context.Context, nodeID string) (*ProcessStatus, error)

	// WaitForNodeHealthy waits for a MongoDB process to be healthy
	// nodeID: Process identifier
	// timeout: Maximum time to wait
	WaitForNodeHealthy(ctx context.Context, nodeID string, timeout time.Duration) error

	// Binary and Configuration Management
	// These operations handle binary downloads and configuration updates

	// PrepareBinaries downloads and prepares MongoDB binaries for the target version
	// version: Target MongoDB version (e.g., "7.0.0")
	// variant: MongoDB variant (community, enterprise, percona, etc.)
	PrepareBinaries(ctx context.Context, version string, variant deploy.Variant) error

	// UpdateNodeConfig updates the configuration file for a specific node
	// This generates version-specific config using templates
	// nodeID: Process identifier
	// version: Target MongoDB version
	UpdateNodeConfig(ctx context.Context, nodeID string, version string) error

	// VerifyNodeVersion verifies that a node is running the expected MongoDB version
	// nodeID: Process identifier
	// expectedVersion: Expected version string
	VerifyNodeVersion(ctx context.Context, nodeID string, expectedVersion string) error

	// Version Environment Management
	// These operations manage the deployment environment for version switching

	// SetupVersionEnvironment prepares the environment for the new version
	// For local: Creates version directories, symlinks, starts new supervisor
	// For SSH: Creates remote directories, uploads binaries
	// version: Target MongoDB version
	SetupVersionEnvironment(ctx context.Context, version string) error

	// SwitchToNewVersion atomically switches from old to new version
	// For local: Updates symlinks (current→next, previous→old)
	// For SSH: Updates systemd units, reloads daemon
	// oldVersion: Previous version
	// newVersion: Target version
	SwitchToNewVersion(ctx context.Context, oldVersion, newVersion string) error

	// CleanupOldVersion cleans up after a failed upgrade
	// For local: Removes 'next' symlink
	// For SSH: Removes temporary files
	// version: Version to clean up
	CleanupOldVersion(ctx context.Context, version string) error
}

// ProcessStatus represents the health state of a MongoDB process
type ProcessStatus struct {
	// State is the process state (RUNNING, STOPPED, FATAL, etc.)
	State string

	// IsHealthy indicates if the node is healthy and ready to serve requests
	IsHealthy bool

	// Version is the detected MongoDB version (if available)
	Version string

	// Uptime is how long the process has been running
	Uptime time.Duration

	// LastError is the last error encountered (if any)
	LastError error

	// PID is the process ID (if running)
	PID int
}
