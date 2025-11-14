package monitoring

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/monitoring/docker"
	"github.com/zph/mup/pkg/monitoring/exporters"
	"github.com/zph/mup/pkg/monitoring/scraper"
	"github.com/zph/mup/pkg/supervisor"
	"github.com/zph/mup/pkg/topology"
)

// Manager handles lifecycle of all monitoring components
type Manager struct {
	baseDir             string
	config              *Config
	victoriaMetrics     *VictoriaMetricsManager
	grafana             *GrafanaManager
	supervisorMgr       *supervisor.Manager
	dockerClient        *docker.Client
	nodeExporterMgr     *exporters.NodeExporterManager
	mongoDBExporterMgr  *exporters.MongoDBExporterManager
	executor            executor.Executor
	exporterRegistry    *scraper.ExporterRegistry
}

// NewManager creates a new monitoring manager
func NewManager(baseDir string, config *Config, exec executor.Executor) (*Manager, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if exec == nil {
		return nil, fmt.Errorf("executor is required")
	}

	// Create base monitoring directory
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create monitoring directory: %w", err)
	}

	// Create logs directory
	logsDir := filepath.Join(baseDir, "logs", "exporters")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create cache directory for exporters
	cacheDir := filepath.Join(baseDir, "exporters")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Initialize Victoria Metrics manager
	vmManager, err := NewVictoriaMetricsManager(
		baseDir,
		config.VictoriaMetricsPort,
		config.RetentionPeriod,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create victoria metrics manager: %w", err)
	}

	// Initialize Grafana manager
	grafanaManager, err := NewGrafanaManager(
		baseDir,
		config.GrafanaPort,
		config.Grafana.AdminUser,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create grafana manager: %w", err)
	}

	// Initialize node_exporter manager
	nodeExporterMgr, err := exporters.NewNodeExporterManager(
		cacheDir,
		logsDir,
		config.Exporters.NodeExporter.Version,
		exec,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create node exporter manager: %w", err)
	}

	// Initialize mongodb_exporter manager
	mongoExporterMgr, err := exporters.NewMongoDBExporterManager(
		cacheDir,
		logsDir,
		config.Exporters.MongoDBExporter.Version,
		config.Exporters.MongoDBExporter.ExtraArgs,
		exec,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create mongodb exporter manager: %w", err)
	}

	return &Manager{
		baseDir:            baseDir,
		config:             config,
		victoriaMetrics:    vmManager,
		grafana:            grafanaManager,
		dockerClient:       docker.NewClient(),
		nodeExporterMgr:    nodeExporterMgr,
		mongoDBExporterMgr: mongoExporterMgr,
		executor:           exec,
		exporterRegistry:   &scraper.ExporterRegistry{},
	}, nil
}

// Initialize initializes monitoring infrastructure (Victoria Metrics, Grafana)
func (m *Manager) Initialize(ctx context.Context) error {
	// Check Docker is installed
	if err := m.dockerClient.CheckDockerInstalled(ctx); err != nil {
		return fmt.Errorf("docker required for monitoring: %w", err)
	}

	// Pull Docker images
	if err := m.victoriaMetrics.EnsureImage(ctx); err != nil {
		return fmt.Errorf("failed to ensure victoria metrics image: %w", err)
	}

	if err := m.grafana.EnsureImage(ctx); err != nil {
		return fmt.Errorf("failed to ensure grafana image: %w", err)
	}

	// Initialize supervisord for monitoring
	if err := m.initializeSupervisor(ctx); err != nil {
		return fmt.Errorf("failed to initialize supervisor: %w", err)
	}

	// Create Grafana provisioning configs
	if err := m.createGrafanaProvisioning(ctx); err != nil {
		return fmt.Errorf("failed to create grafana provisioning: %w", err)
	}

	return nil
}

// initializeSupervisor sets up supervisord for monitoring components
func (m *Manager) initializeSupervisor(ctx context.Context) error {
	// Create supervisor manager for monitoring
	supMgr, err := supervisor.NewManager(m.baseDir, "monitoring")
	if err != nil {
		return fmt.Errorf("failed to create supervisor manager: %w", err)
	}

	m.supervisorMgr = supMgr

	// Generate supervisor config with monitoring programs
	if err := m.generateSupervisorConfig(); err != nil {
		return fmt.Errorf("failed to generate supervisor config: %w", err)
	}

	return nil
}

// generateSupervisorConfig generates the main supervisord config for monitoring
func (m *Manager) generateSupervisorConfig() error {
	configPath := filepath.Join(m.baseDir, "supervisor.ini")
	logsDir := filepath.Join(m.baseDir, "logs")

	// Will implement with actual scrape config path
	scrapeConfigPath := filepath.Join(m.victoriaMetrics.GetConfigDir(), "promscrape.yaml")

	// Generate main config
	mainConfig := fmt.Sprintf(`[supervisord]
logfile = %s/supervisord.log
loglevel = info
pidfile = %s/supervisor.pid
nodaemon = false

[inet_http_server]
port = 127.0.0.1:9002

`, logsDir, m.baseDir)

	// Add Victoria Metrics program
	mainConfig += m.victoriaMetrics.GenerateSupervisorConfig(scrapeConfigPath)
	mainConfig += "\n"

	// Add Grafana program
	mainConfig += m.grafana.GenerateSupervisorConfig()
	mainConfig += "\n"

	// Add monitoring group
	mainConfig += fmt.Sprintf(`[group:monitoring]
programs = %s,%s
`,
		VictoriaMetricsProgramName,
		GrafanaProgramName,
	)

	// Write config
	if err := os.WriteFile(configPath, []byte(mainConfig), 0644); err != nil {
		return fmt.Errorf("failed to write supervisor config: %w", err)
	}

	return nil
}

// createGrafanaProvisioning creates datasource and dashboard provisioning configs
func (m *Manager) createGrafanaProvisioning(ctx context.Context) error {
	// Create datasource provisioning
	datasourceConfig := fmt.Sprintf(`apiVersion: 1

datasources:
  - name: VictoriaMetrics
    type: prometheus
    access: proxy
    url: %s
    isDefault: true
    editable: false
    jsonData:
      timeInterval: %s
      httpMethod: POST
`,
		m.victoriaMetrics.GetURL(),
		m.config.ScrapeInterval,
	)

	datasourcePath := filepath.Join(m.grafana.GetProvisioningDir(), "datasources", "victoria-metrics.yaml")
	if err := os.WriteFile(datasourcePath, []byte(datasourceConfig), 0644); err != nil {
		return fmt.Errorf("failed to write datasource config: %w", err)
	}

	// Create dashboard provisioning
	dashboardConfig := fmt.Sprintf(`apiVersion: 1

providers:
  - name: 'Mup MongoDB Dashboards'
    orgId: 1
    folder: 'MongoDB'
    type: file
    disableDeletion: false
    updateIntervalSeconds: 10
    allowUiUpdates: true
    options:
      path: %s
      foldersFromFilesStructure: true
`,
		m.grafana.GetDashboardsDir(),
	)

	dashboardProvPath := filepath.Join(m.grafana.GetProvisioningDir(), "dashboards", "default.yaml")
	if err := os.WriteFile(dashboardProvPath, []byte(dashboardConfig), 0644); err != nil {
		return fmt.Errorf("failed to write dashboard provisioning config: %w", err)
	}

	// Copy dashboard JSON files to dashboards directory
	if err := m.copyDashboards(); err != nil {
		return fmt.Errorf("failed to copy dashboards: %w", err)
	}

	return nil
}

// copyDashboards copies dashboard JSON files to the Grafana dashboards directory
func (m *Manager) copyDashboards() error {
	// Get the path to the dashboards source directory
	// In production, dashboards are embedded in the binary via //go:embed
	// For now, we'll copy from the source directory
	dashboardsSourceDir := filepath.Join(getRootDir(), "pkg", "monitoring", "dashboards")
	dashboardsDestDir := m.grafana.GetDashboardsDir()

	// Ensure destination directory exists
	if err := os.MkdirAll(dashboardsDestDir, 0755); err != nil {
		return fmt.Errorf("failed to create dashboards directory: %w", err)
	}

	// List of dashboard files to copy
	dashboards := []string{
		"mongodb-overview.json",
		"mongodb-wiredtiger.json",
		"mongodb-sharding.json",
		"mongodb-replication.json",
		"system-overview.json",
	}

	for _, dashboard := range dashboards {
		srcPath := filepath.Join(dashboardsSourceDir, dashboard)
		dstPath := filepath.Join(dashboardsDestDir, dashboard)

		// Check if source file exists
		if _, err := os.Stat(srcPath); err != nil {
			// File doesn't exist, skip (might be in embedded FS in production)
			continue
		}

		// Read source file
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("failed to read dashboard %s: %w", dashboard, err)
		}

		// Write to destination
		if err := os.WriteFile(dstPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write dashboard %s: %w", dashboard, err)
		}
	}

	return nil
}

// getRootDir attempts to find the project root directory
func getRootDir() string {
	// Try to find go.mod by walking up from current directory
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root without finding go.mod
			return "."
		}
		dir = parent
	}
}

// StartClusterMonitoring starts monitoring for a specific cluster
func (m *Manager) StartClusterMonitoring(ctx context.Context, clusterName string) error {
	// Start supervisord if not running
	if !m.supervisorMgr.IsRunning() {
		if err := m.supervisorMgr.Start(ctx); err != nil {
			return fmt.Errorf("failed to start supervisor: %w", err)
		}
	}

	// Start monitoring group
	if err := m.supervisorMgr.StartGroup("monitoring"); err != nil {
		return fmt.Errorf("failed to start monitoring group: %w", err)
	}

	return nil
}

// StopClusterMonitoring stops monitoring for a specific cluster
func (m *Manager) StopClusterMonitoring(ctx context.Context, clusterName string) error {
	if m.supervisorMgr != nil && m.supervisorMgr.IsRunning() {
		if err := m.supervisorMgr.StopGroup("monitoring"); err != nil {
			return fmt.Errorf("failed to stop monitoring group: %w", err)
		}

		if err := m.supervisorMgr.Stop(ctx); err != nil {
			return fmt.Errorf("failed to stop supervisor: %w", err)
		}
	}

	return nil
}

// GetURLs returns URLs for monitoring services
func (m *Manager) GetURLs() (*MonitoringURLs, error) {
	urls := &MonitoringURLs{
		Grafana:         m.grafana.GetURL(),
		VictoriaMetrics: m.victoriaMetrics.GetURL(),
		Dashboards:      []DashboardURL{},
	}

	// Add dashboard URLs
	for _, dashName := range m.config.Grafana.Dashboards {
		urls.Dashboards = append(urls.Dashboards, DashboardURL{
			Name: dashName,
			URL:  fmt.Sprintf("%s/d/%s", m.grafana.GetURL(), dashName),
		})
	}

	return urls, nil
}

// HealthCheck checks health of monitoring components
func (m *Manager) HealthCheck(ctx context.Context) (*HealthStatus, error) {
	status := &HealthStatus{
		Exporters: []ExporterHealth{},
	}

	// Check Victoria Metrics via supervisord
	if m.supervisorMgr != nil && m.supervisorMgr.IsRunning() {
		vmStatus, err := m.supervisorMgr.GetProcessStatus(VictoriaMetricsProgramName)
		if err == nil {
			status.VictoriaMetrics.Running = vmStatus.State == "RUNNING"
			status.VictoriaMetrics.PID = vmStatus.PID
			status.VictoriaMetrics.Uptime = time.Duration(vmStatus.Uptime) * time.Second
		} else {
			status.VictoriaMetrics.Message = fmt.Sprintf("failed to get status: %v", err)
		}
	}

	// Check Grafana via supervisord
	if m.supervisorMgr != nil && m.supervisorMgr.IsRunning() {
		grafanaStatus, err := m.supervisorMgr.GetProcessStatus(GrafanaProgramName)
		if err == nil {
			status.Grafana.Running = grafanaStatus.State == "RUNNING"
			status.Grafana.PID = grafanaStatus.PID
			status.Grafana.Uptime = time.Duration(grafanaStatus.Uptime) * time.Second
		} else {
			status.Grafana.Message = fmt.Sprintf("failed to get status: %v", err)
		}
	}

	// Check exporters (from exporter registry if available)
	if m.exporterRegistry != nil {
		// Check node_exporters
		for _, ne := range m.exporterRegistry.NodeExporters {
			expHealth := ExporterHealth{
				Type: "node_exporter",
				Host: ne.Host,
				Port: ne.Port,
			}

			// Try to check via supervisord
			programName := fmt.Sprintf("node_exporter-%d", ne.Port)
			if m.supervisorMgr != nil && m.supervisorMgr.IsRunning() {
				procStatus, err := m.supervisorMgr.GetProcessStatus(programName)
				if err == nil {
					expHealth.Running = procStatus.State == "RUNNING"
					expHealth.PID = procStatus.PID
				} else {
					// Fallback to port check
					expHealth.Running = m.checkPort(ne.Host, ne.Port)
				}
			} else {
				// Fallback to port check
				expHealth.Running = m.checkPort(ne.Host, ne.Port)
			}

			status.Exporters = append(status.Exporters, expHealth)
		}

		// Check mongodb_exporters
		for _, me := range m.exporterRegistry.MongoDBExporters {
			expHealth := ExporterHealth{
				Type: "mongodb_exporter",
				Host: me.Host,
				Port: me.ExporterPort,
			}

			// Try to check via supervisord
			programName := fmt.Sprintf("mongodb_exporter-%d", me.ExporterPort)
			if m.supervisorMgr != nil && m.supervisorMgr.IsRunning() {
				procStatus, err := m.supervisorMgr.GetProcessStatus(programName)
				if err == nil {
					expHealth.Running = procStatus.State == "RUNNING"
					expHealth.PID = procStatus.PID
				} else {
					// Fallback to port check
					expHealth.Running = m.checkPort(me.Host, me.ExporterPort)
				}
			} else {
				// Fallback to port check
				expHealth.Running = m.checkPort(me.Host, me.ExporterPort)
			}

			status.Exporters = append(status.Exporters, expHealth)
		}
	}

	return status, nil
}

// checkPort checks if a TCP port is listening
func (m *Manager) checkPort(host string, port int) bool {
	address := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Cleanup removes all monitoring resources
func (m *Manager) Cleanup(ctx context.Context) error {
	// Stop monitoring
	if err := m.StopClusterMonitoring(ctx, ""); err != nil {
		return fmt.Errorf("failed to stop monitoring: %w", err)
	}

	// Cleanup containers
	if err := m.victoriaMetrics.Cleanup(ctx); err != nil {
		return fmt.Errorf("failed to cleanup victoria metrics: %w", err)
	}

	if err := m.grafana.Cleanup(ctx); err != nil {
		return fmt.Errorf("failed to cleanup grafana: %w", err)
	}

	return nil
}

// GetConfig returns the monitoring configuration
func (m *Manager) GetConfig() *Config {
	return m.config
}

// GetGrafanaAdminPassword returns the Grafana admin password
func (m *Manager) GetGrafanaAdminPassword() (string, error) {
	return m.grafana.GetAdminPassword()
}

// DeployExporters deploys all exporters for a cluster
func (m *Manager) DeployExporters(ctx context.Context, clusterName string, topo *topology.Topology) (*meta.MonitoringMetadata, error) {
	// Build exporter registry
	m.exporterRegistry = scraper.BuildExporterRegistry(
		topo,
		m.config.Exporters.NodeExporter.Port,
		m.config.Exporters.MongoDBExporter.PortBase,
	)

	monitoringMeta := &meta.MonitoringMetadata{
		Enabled:            true,
		VictoriaMetricsURL: m.victoriaMetrics.GetURL(),
		GrafanaURL:         m.grafana.GetURL(),
		NodeExporters:      []meta.NodeExporterMetadata{},
		MongoDBExporters:   []meta.MongoDBExporterMetadata{},
	}

	// Deploy node_exporters
	for _, ne := range m.exporterRegistry.NodeExporters {
		instance, err := m.nodeExporterMgr.Start(ctx, ne.Host, ne.Port)
		if err != nil {
			return nil, fmt.Errorf("failed to start node_exporter on %s:%d: %w", ne.Host, ne.Port, err)
		}

		monitoringMeta.NodeExporters = append(monitoringMeta.NodeExporters, meta.NodeExporterMetadata{
			Host: instance.Host,
			Port: instance.Port,
			PID:  instance.PID,
		})
	}

	// Deploy mongodb_exporters
	for _, me := range m.exporterRegistry.MongoDBExporters {
		instance, err := m.mongoDBExporterMgr.Start(ctx, me.Host, me.ExporterPort, me.MongoDBPort)
		if err != nil {
			return nil, fmt.Errorf("failed to start mongodb_exporter on %s:%d: %w", me.Host, me.ExporterPort, err)
		}

		monitoringMeta.MongoDBExporters = append(monitoringMeta.MongoDBExporters, meta.MongoDBExporterMetadata{
			Host:         instance.Host,
			ExporterPort: instance.ExporterPort,
			MongoDBPort:  instance.MongoDBPort,
			PID:          instance.PID,
		})
	}

	// Generate and write scrape config
	if err := m.updateScrapeConfig(ctx, clusterName, topo); err != nil {
		return nil, fmt.Errorf("failed to update scrape config: %w", err)
	}

	return monitoringMeta, nil
}

// updateScrapeConfig generates and writes the scrape configuration
func (m *Manager) updateScrapeConfig(ctx context.Context, clusterName string, topo *topology.Topology) error {
	scrapeConfig, err := scraper.GenerateScrapeConfig(
		clusterName,
		topo,
		m.exporterRegistry,
		m.config.ScrapeInterval,
	)
	if err != nil {
		return fmt.Errorf("failed to generate scrape config: %w", err)
	}

	scrapeConfigPath := filepath.Join(m.victoriaMetrics.GetConfigDir(), "promscrape.yaml")
	if err := scraper.WriteScrapeConfig(scrapeConfig, scrapeConfigPath); err != nil {
		return fmt.Errorf("failed to write scrape config: %w", err)
	}

	return nil
}

// StopExporters stops all exporters
func (m *Manager) StopExporters(ctx context.Context, monitoringMeta *meta.MonitoringMetadata) error {
	if monitoringMeta == nil {
		return nil
	}

	// Stop node_exporters
	for _, ne := range monitoringMeta.NodeExporters {
		if ne.PID > 0 {
			if err := m.nodeExporterMgr.Stop(ctx, ne.PID); err != nil {
				// Log but don't fail on error
				fmt.Printf("Warning: failed to stop node_exporter (PID %d): %v\n", ne.PID, err)
			}
		}
	}

	// Stop mongodb_exporters
	for _, me := range monitoringMeta.MongoDBExporters {
		if me.PID > 0 {
			if err := m.mongoDBExporterMgr.Stop(ctx, me.PID); err != nil {
				// Log but don't fail on error
				fmt.Printf("Warning: failed to stop mongodb_exporter (PID %d): %v\n", me.PID, err)
			}
		}
	}

	return nil
}
