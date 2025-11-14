package monitoring

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zph/mup/pkg/monitoring/docker"
)

const (
	GrafanaContainerName = "mup-grafana"
	GrafanaProgramName   = "monitoring-grafana"
)

// GrafanaManager manages Grafana lifecycle
type GrafanaManager struct {
	dataDir         string
	provisioningDir string
	dashboardsDir   string
	port            int
	adminUser       string
	passwordFile    string
	dockerClient    *docker.Client
}

// NewGrafanaManager creates a new Grafana manager
func NewGrafanaManager(baseDir string, port int, adminUser string) (*GrafanaManager, error) {
	dataDir := filepath.Join(baseDir, "grafana", "data")
	provisioningDir := filepath.Join(baseDir, "grafana", "provisioning")
	dashboardsDir := filepath.Join(baseDir, "grafana", "dashboards")
	passwordFile := filepath.Join(dataDir, ".password")

	// Create directories
	dirs := []string{
		dataDir,
		filepath.Join(provisioningDir, "datasources"),
		filepath.Join(provisioningDir, "dashboards"),
		dashboardsDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	gm := &GrafanaManager{
		dataDir:         dataDir,
		provisioningDir: provisioningDir,
		dashboardsDir:   dashboardsDir,
		port:            port,
		adminUser:       adminUser,
		passwordFile:    passwordFile,
		dockerClient:    docker.NewClient(),
	}

	// Generate admin password if not exists
	if err := gm.ensureAdminPassword(); err != nil {
		return nil, fmt.Errorf("failed to ensure admin password: %w", err)
	}

	return gm, nil
}

// ensureAdminPassword generates a secure admin password if not exists
func (gm *GrafanaManager) ensureAdminPassword() error {
	if _, err := os.Stat(gm.passwordFile); err == nil {
		return nil // Password already exists
	}

	// Generate random password
	passwordBytes := make([]byte, 32)
	if _, err := rand.Read(passwordBytes); err != nil {
		return fmt.Errorf("failed to generate random password: %w", err)
	}

	password := base64.URLEncoding.EncodeToString(passwordBytes)[:32]

	if err := os.WriteFile(gm.passwordFile, []byte(password), 0600); err != nil {
		return fmt.Errorf("failed to write password file: %w", err)
	}

	return nil
}

// GetAdminPassword reads the admin password
func (gm *GrafanaManager) GetAdminPassword() (string, error) {
	data, err := os.ReadFile(gm.passwordFile)
	if err != nil {
		return "", fmt.Errorf("failed to read password file: %w", err)
	}
	return string(data), nil
}

// EnsureImage ensures the Grafana Docker image is available
func (gm *GrafanaManager) EnsureImage(ctx context.Context) error {
	return gm.dockerClient.PullImage(ctx, docker.GrafanaImage, docker.GrafanaTag)
}

// GenerateSupervisorConfig generates supervisord configuration for Grafana
func (gm *GrafanaManager) GenerateSupervisorConfig() string {
	return fmt.Sprintf(`[program:%s]
command = docker run --rm \
  --name %s \
  -p 127.0.0.1:%d:3000 \
  -v %s:/var/lib/grafana \
  -v %s:/etc/grafana/provisioning \
  -v %s:/var/lib/grafana/dashboards \
  -e GF_AUTH_ANONYMOUS_ENABLED=false \
  -e GF_SECURITY_ADMIN_USER=%s \
  -e GF_SECURITY_ADMIN_PASSWORD__FILE=/var/lib/grafana/.password \
  %s:%s
autostart = false
autorestart = unexpected
startsecs = 10
startretries = 3
stopwaitsecs = 30
stopsignal = TERM
`,
		GrafanaProgramName,
		GrafanaContainerName,
		gm.port,
		gm.dataDir,
		gm.provisioningDir,
		gm.dashboardsDir,
		gm.adminUser,
		docker.GrafanaImage,
		docker.GrafanaTag,
	)
}

// IsRunning checks if Grafana is running
func (gm *GrafanaManager) IsRunning(ctx context.Context) (bool, error) {
	return gm.dockerClient.ContainerRunning(ctx, GrafanaContainerName)
}

// Stop stops Grafana container
func (gm *GrafanaManager) Stop(ctx context.Context) error {
	running, err := gm.IsRunning(ctx)
	if err != nil {
		return err
	}

	if !running {
		return nil // Already stopped
	}

	if err := gm.dockerClient.StopContainer(ctx, GrafanaContainerName); err != nil {
		return fmt.Errorf("failed to stop grafana: %w", err)
	}

	return nil
}

// Cleanup removes Grafana container
func (gm *GrafanaManager) Cleanup(ctx context.Context) error {
	return gm.dockerClient.RemoveContainer(ctx, GrafanaContainerName)
}

// GetURL returns the Grafana URL
func (gm *GrafanaManager) GetURL() string {
	return fmt.Sprintf("http://localhost:%d", gm.port)
}

// GetProvisioningDir returns the provisioning directory path
func (gm *GrafanaManager) GetProvisioningDir() string {
	return gm.provisioningDir
}

// GetDashboardsDir returns the dashboards directory path
func (gm *GrafanaManager) GetDashboardsDir() string {
	return gm.dashboardsDir
}
