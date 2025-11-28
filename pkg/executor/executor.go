package executor

import (
	"io"
	"os"
)

// Executor provides a unified interface for executing operations
// either locally or remotely via SSH. This abstraction allows the same
// deployment code to work in both local and remote scenarios.
type Executor interface {
	// File Operations
	CreateDirectory(path string, mode os.FileMode) error
	UploadFile(localPath, remotePath string) error
	UploadContent(content []byte, remotePath string) error
	DownloadFile(remotePath, localPath string) error
	FileExists(path string) (bool, error)
	RemoveFile(path string) error
	RemoveDirectory(path string) error

	// Command Execution
	Execute(command string) (output string, err error)
	ExecuteWithInput(command string, stdin io.Reader) (output string, err error)
	Background(command string) (pid int, err error)

	// MongoDB Operations (for tracking in simulation)
	MongoExecute(host string, command string) (output string, err error)

	// Process Management
	IsProcessRunning(pid int) (bool, error)
	KillProcess(pid int) error
	StopProcess(pid int) error // Send SIGINT for graceful shutdown

	// System Information
	GetOSInfo() (*OSInfo, error)
	GetDiskSpace(path string) (available uint64, err error)
	CheckPortAvailable(port int) (bool, error)
	UserExists(username string) (bool, error)

	// Connection Management
	CheckConnectivity() error
	Close() error
}

// OSInfo contains operating system information
type OSInfo struct {
	OS      string // "linux", "darwin", etc.
	Arch    string // "amd64", "arm64", etc.
	Version string // OS version string
}
