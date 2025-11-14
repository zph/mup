package monitoring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zph/mup/pkg/monitoring/docker"
)

const (
	VictoriaMetricsContainerName = "mup-victoria-metrics"
	VictoriaMetricsProgramName   = "monitoring-victoria-metrics"
)

// VictoriaMetricsManager manages Victoria Metrics lifecycle
type VictoriaMetricsManager struct {
	dataDir    string
	configDir  string
	port       int
	retention  string
	dockerClient *docker.Client
}

// NewVictoriaMetricsManager creates a new Victoria Metrics manager
func NewVictoriaMetricsManager(baseDir string, port int, retention string) (*VictoriaMetricsManager, error) {
	dataDir := filepath.Join(baseDir, "victoria-metrics", "data")
	configDir := filepath.Join(baseDir, "victoria-metrics", "config")

	// Create directories
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	return &VictoriaMetricsManager{
		dataDir:      dataDir,
		configDir:    configDir,
		port:         port,
		retention:    retention,
		dockerClient: docker.NewClient(),
	}, nil
}

// EnsureImage ensures the Victoria Metrics Docker image is available
func (vm *VictoriaMetricsManager) EnsureImage(ctx context.Context) error {
	return vm.dockerClient.PullImage(ctx, docker.VictoriaMetricsImage, docker.VictoriaMetricsTag)
}

// GenerateSupervisorConfig generates supervisord configuration for Victoria Metrics
func (vm *VictoriaMetricsManager) GenerateSupervisorConfig(scrapeConfigPath string) string {
	return fmt.Sprintf(`[program:%s]
command = docker run --rm \
  --name %s \
  -p 127.0.0.1:%d:8428 \
  -v %s:/victoria-metrics-data \
  -v %s:/etc/victoria-metrics \
  %s:%s \
  -promscrape.config=/etc/victoria-metrics/promscrape.yaml \
  -retentionPeriod=%s \
  -storageDataPath=/victoria-metrics-data
autostart = false
autorestart = unexpected
startsecs = 5
startretries = 3
stopwaitsecs = 30
stopsignal = TERM
`,
		VictoriaMetricsProgramName,
		VictoriaMetricsContainerName,
		vm.port,
		vm.dataDir,
		vm.configDir,
		docker.VictoriaMetricsImage,
		docker.VictoriaMetricsTag,
		vm.retention,
	)
}

// IsRunning checks if Victoria Metrics is running
func (vm *VictoriaMetricsManager) IsRunning(ctx context.Context) (bool, error) {
	return vm.dockerClient.ContainerRunning(ctx, VictoriaMetricsContainerName)
}

// Stop stops Victoria Metrics container
func (vm *VictoriaMetricsManager) Stop(ctx context.Context) error {
	running, err := vm.IsRunning(ctx)
	if err != nil {
		return err
	}

	if !running {
		return nil // Already stopped
	}

	if err := vm.dockerClient.StopContainer(ctx, VictoriaMetricsContainerName); err != nil {
		return fmt.Errorf("failed to stop victoria metrics: %w", err)
	}

	return nil
}

// Cleanup removes Victoria Metrics container
func (vm *VictoriaMetricsManager) Cleanup(ctx context.Context) error {
	return vm.dockerClient.RemoveContainer(ctx, VictoriaMetricsContainerName)
}

// GetURL returns the Victoria Metrics URL
func (vm *VictoriaMetricsManager) GetURL() string {
	return fmt.Sprintf("http://localhost:%d", vm.port)
}

// GetDataDir returns the data directory path
func (vm *VictoriaMetricsManager) GetDataDir() string {
	return vm.dataDir
}

// GetConfigDir returns the config directory path
func (vm *VictoriaMetricsManager) GetConfigDir() string {
	return vm.configDir
}
