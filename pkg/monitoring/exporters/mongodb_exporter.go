package exporters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zph/mup/pkg/executor"
)

// MongoDBExporterManager manages mongodb_exporter instances
type MongoDBExporterManager struct {
	downloader *Downloader
	logsDir    string
	version    string
	executor   executor.Executor
	extraArgs  []string
}

// MongoDBExporterInstance represents a running mongodb_exporter instance
type MongoDBExporterInstance struct {
	Host         string
	ExporterPort int
	MongoDBPort  int
	MongoDBURI   string
	PID          int
	LogFile      string
	BinaryPath   string
}

// NewMongoDBExporterManager creates a new mongodb_exporter manager
func NewMongoDBExporterManager(cacheDir, logsDir, version string, extraArgs []string, exec executor.Executor) (*MongoDBExporterManager, error) {
	downloader, err := NewDownloader(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create downloader: %w", err)
	}

	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	return &MongoDBExporterManager{
		downloader: downloader,
		logsDir:    logsDir,
		version:    version,
		executor:   exec,
		extraArgs:  extraArgs,
	}, nil
}

// EnsureBinary ensures the mongodb_exporter binary is downloaded
func (m *MongoDBExporterManager) EnsureBinary(ctx context.Context) (string, error) {
	return m.downloader.DownloadMongoDBExporter(ctx, m.version)
}

// Start starts a mongodb_exporter instance
func (m *MongoDBExporterManager) Start(ctx context.Context, host string, exporterPort, mongoDBPort int) (*MongoDBExporterInstance, error) {
	// Ensure binary is downloaded
	binaryPath, err := m.EnsureBinary(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure binary: %w", err)
	}

	// Build MongoDB URI
	mongoURI := fmt.Sprintf("mongodb://%s:%d", host, mongoDBPort)

	// Create log file
	logFile := filepath.Join(m.logsDir, fmt.Sprintf("mongodb_exporter-%s-%d.log", host, exporterPort))

	// Build command with extra args
	args := []string{
		fmt.Sprintf("--mongodb.uri=%s", mongoURI),
		fmt.Sprintf("--web.listen-address=%s:%d", host, exporterPort),
	}
	args = append(args, m.extraArgs...)

	cmd := fmt.Sprintf("%s %s > %s 2>&1 &",
		binaryPath,
		strings.Join(args, " "),
		logFile,
	)

	// Start process in background
	pid, err := m.executor.Background(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start mongodb_exporter: %w", err)
	}

	return &MongoDBExporterInstance{
		Host:         host,
		ExporterPort: exporterPort,
		MongoDBPort:  mongoDBPort,
		MongoDBURI:   mongoURI,
		PID:          pid,
		LogFile:      logFile,
		BinaryPath:   binaryPath,
	}, nil
}

// Stop stops a mongodb_exporter instance
func (m *MongoDBExporterManager) Stop(ctx context.Context, pid int) error {
	if err := m.executor.StopProcess(pid); err != nil {
		return fmt.Errorf("failed to stop mongodb_exporter: %w", err)
	}
	return nil
}

// IsRunning checks if a mongodb_exporter instance is running
func (m *MongoDBExporterManager) IsRunning(pid int) (bool, error) {
	return m.executor.IsProcessRunning(pid)
}

// GenerateSupervisorConfig generates supervisord config for mongodb_exporter
func (m *MongoDBExporterManager) GenerateSupervisorConfig(programName, host string, exporterPort, mongoDBPort int, binaryPath, logFile string) string {
	// For local deployments, listen on 0.0.0.0 so Victoria Metrics in Docker can reach via host.docker.internal
	listenAddr := "0.0.0.0"
	if host != "localhost" && host != "127.0.0.1" {
		listenAddr = host
	}

	args := []string{
		fmt.Sprintf("--mongodb.uri=mongodb://%s:%d", host, mongoDBPort),
		fmt.Sprintf("--web.listen-address=%s:%d", listenAddr, exporterPort),
	}
	args = append(args, m.extraArgs...)

	return fmt.Sprintf(`[program:%s]
command = %s %s
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
		strings.Join(args, " "),
		logFile,
		logFile,
	)
}
