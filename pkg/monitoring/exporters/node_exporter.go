package exporters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zph/mup/pkg/executor"
)

// NodeExporterManager manages node_exporter instances
type NodeExporterManager struct {
	downloader *Downloader
	logsDir    string
	version    string
	executor   executor.Executor
	// listenAddress is the address the exporter binds; empty means DefaultListenAddress.
	listenAddress string
}

// DefaultListenAddress is the address exporters bind to. It is every
// interface because the metrics scraper runs in a container and reaches the
// host through the container network; a loopback-only exporter is not
// reachable from there (confirmed with podman on macOS).
// LOCAL-037
const DefaultListenAddress = "0.0.0.0"

// SetListenAddress overrides the address the exporter binds to.
func (m *NodeExporterManager) SetListenAddress(address string) {
	m.listenAddress = address
}

// exporterListenAddress returns the configured address or the default.
func exporterListenAddress(configured string) string {
	if configured == "" {
		return DefaultListenAddress
	}
	return configured
}

// NodeExporterInstance represents a running node_exporter instance
type NodeExporterInstance struct {
	Host       string
	Port       int
	PID        int
	LogFile    string
	BinaryPath string
}

// NewNodeExporterManager creates a new node_exporter manager
func NewNodeExporterManager(cacheDir, logsDir, version string, exec executor.Executor) (*NodeExporterManager, error) {
	downloader, err := NewDownloader(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create downloader: %w", err)
	}

	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	return &NodeExporterManager{
		downloader: downloader,
		logsDir:    logsDir,
		version:    version,
		executor:   exec,
	}, nil
}

// EnsureBinary ensures the node_exporter binary is downloaded
func (m *NodeExporterManager) EnsureBinary(ctx context.Context) (string, error) {
	return m.downloader.DownloadNodeExporter(ctx, m.version)
}

// Start starts a node_exporter instance
func (m *NodeExporterManager) Start(ctx context.Context, host string, port int) (*NodeExporterInstance, error) {
	// Ensure binary is downloaded
	binaryPath, err := m.EnsureBinary(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure binary: %w", err)
	}

	// Create log file
	logFile := filepath.Join(m.logsDir, fmt.Sprintf("node_exporter-%s-%d.log", host, port))

	// Build command
	cmd := fmt.Sprintf("%s --web.listen-address=%s:%d > %s 2>&1 &",
		binaryPath,
		host,
		port,
		logFile,
	)

	// Start process in background
	pid, err := m.executor.Background(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start node_exporter: %w", err)
	}

	return &NodeExporterInstance{
		Host:       host,
		Port:       port,
		PID:        pid,
		LogFile:    logFile,
		BinaryPath: binaryPath,
	}, nil
}

// Stop stops a node_exporter instance
func (m *NodeExporterManager) Stop(ctx context.Context, pid int) error {
	if err := m.executor.StopProcess(pid); err != nil {
		return fmt.Errorf("failed to stop node_exporter: %w", err)
	}
	return nil
}

// IsRunning checks if a node_exporter instance is running
func (m *NodeExporterManager) IsRunning(pid int) (bool, error) {
	return m.executor.IsProcessRunning(pid)
}

// GenerateSupervisorConfig generates supervisord config for node_exporter
func (m *NodeExporterManager) GenerateSupervisorConfig(programName, host string, port int, binaryPath, logFile string) string {
	listenAddr := exporterListenAddress(m.listenAddress)

	return fmt.Sprintf(`[program:%s]
command = %s --web.listen-address=%s:%d
autostart = false
autorestart = unexpected
startsecs = 3
startretries = 3
stdout_logfile = %s
stderr_logfile = %s
stopwaitsecs = 10
stopsignal = TERM
`,
		programName,
		binaryPath,
		listenAddr,
		port,
		logFile,
		logFile,
	)
}
