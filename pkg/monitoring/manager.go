package monitoring

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
func NewManager(baseDir string, config *Config, exec executor.Executor, supMgr *supervisor.Manager) (*Manager, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if exec == nil {
		return nil, fmt.Errorf("executor is required")
	}

	if supMgr == nil {
		return nil, fmt.Errorf("supervisor manager is required")
	}

	// Create base monitoring directory within cluster dir
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
		supervisorMgr:      supMgr,
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

	// Add monitoring programs to cluster supervisor config (without exporters initially)
	if err := m.addMonitoringToSupervisor(nil); err != nil {
		return fmt.Errorf("failed to add monitoring to supervisor: %w", err)
	}

	// Create Grafana provisioning configs
	if err := m.createGrafanaProvisioning(ctx); err != nil {
		return fmt.Errorf("failed to create grafana provisioning: %w", err)
	}

	return nil
}

// addMonitoringToSupervisor adds monitoring programs to the cluster's supervisor config
// If exporterRegistry is nil, only Victoria Metrics and Grafana are added (for initial setup)
// If exporterRegistry is provided, exporters are also added (for full deployment)
func (m *Manager) addMonitoringToSupervisor(exporterRegistry *scraper.ExporterRegistry) error {
	// Create monitoring-specific config file in cluster directory (parent of baseDir)
	clusterDir := filepath.Dir(m.baseDir)
	monitoringConfigPath := filepath.Join(clusterDir, "monitoring-supervisor.ini")

	// Get scrape config path
	scrapeConfigPath := filepath.Join(m.victoriaMetrics.GetConfigDir(), "promscrape.yaml")

	// Generate monitoring programs config (no [supervisord] section - that's in main supervisor.ini)
	var monitoringConfig string
	var programs []string

	// Add Victoria Metrics program
	monitoringConfig += m.victoriaMetrics.GenerateSupervisorConfig(scrapeConfigPath)
	monitoringConfig += "\n"
	programs = append(programs, VictoriaMetricsProgramName)

	// Add Grafana program
	monitoringConfig += m.grafana.GenerateSupervisorConfig()
	monitoringConfig += "\n"
	programs = append(programs, GrafanaProgramName)

	// Add exporter programs if registry is provided
	if exporterRegistry != nil {
		logsDir := filepath.Join(m.baseDir, "logs", "exporters")

		// Ensure binaries are downloaded
		nodeExporterBinary, err := m.nodeExporterMgr.EnsureBinary(context.Background())
		if err != nil {
			return fmt.Errorf("failed to ensure node_exporter binary: %w", err)
		}

		mongoDBExporterBinary, err := m.mongoDBExporterMgr.EnsureBinary(context.Background())
		if err != nil {
			return fmt.Errorf("failed to ensure mongodb_exporter binary: %w", err)
		}

		// Add node_exporter programs
		for i, ne := range exporterRegistry.NodeExporters {
			programName := fmt.Sprintf("node-exporter-%d", i)
			logFile := filepath.Join(logsDir, fmt.Sprintf("node_exporter-%s-%d.log", ne.Host, ne.Port))

			monitoringConfig += m.nodeExporterMgr.GenerateSupervisorConfig(
				programName,
				ne.Host,
				ne.Port,
				nodeExporterBinary,
				logFile,
			)
			monitoringConfig += "\n"
			programs = append(programs, programName)
		}

		// Add mongodb_exporter programs
		for i, me := range exporterRegistry.MongoDBExporters {
			programName := fmt.Sprintf("mongodb-exporter-%d", i)
			logFile := filepath.Join(logsDir, fmt.Sprintf("mongodb_exporter-%s-%d.log", me.Host, me.ExporterPort))

			monitoringConfig += m.mongoDBExporterMgr.GenerateSupervisorConfig(
				programName,
				me.Host,
				me.ExporterPort,
				me.MongoDBPort,
				mongoDBExporterBinary,
				logFile,
			)
			monitoringConfig += "\n"
			programs = append(programs, programName)
		}
	}

	// Add monitoring group with all programs
	monitoringConfig += "[group:monitoring]\n"
	monitoringConfig += fmt.Sprintf("programs = %s\n", joinPrograms(programs))

	// Write monitoring config
	if err := os.WriteFile(monitoringConfigPath, []byte(monitoringConfig), 0644); err != nil {
		return fmt.Errorf("failed to write monitoring supervisor config: %w", err)
	}

	return nil
}

// joinPrograms joins program names with commas
func joinPrograms(programs []string) string {
	result := ""
	for i, prog := range programs {
		if i > 0 {
			result += ","
		}
		result += prog
	}
	return result
}

// createGrafanaProvisioning creates datasource and dashboard provisioning configs
func (m *Manager) createGrafanaProvisioning(ctx context.Context) error {
	// Create datasource provisioning
	// Use host.docker.internal for Grafana in Docker to reach Victoria Metrics on host
	vmURL := m.victoriaMetrics.GetURL()
	// Replace localhost with host.docker.internal for Docker container access
	if strings.Contains(vmURL, "localhost") {
		vmURL = strings.Replace(vmURL, "localhost", "host.docker.internal", 1)
	}

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
		vmURL,
		m.config.ScrapeInterval,
	)

	datasourcePath := filepath.Join(m.grafana.GetProvisioningDir(), "datasources", "victoria-metrics.yaml")
	if err := os.WriteFile(datasourcePath, []byte(datasourceConfig), 0644); err != nil {
		return fmt.Errorf("failed to write datasource config: %w", err)
	}

	// Create dashboard provisioning
	// NOTE: Use container path, not host path, since this config is read inside the Grafana container
	dashboardConfig := `apiVersion: 1

providers:
  - name: 'Mup MongoDB Dashboards'
    orgId: 1
    folder: 'MongoDB'
    type: file
    disableDeletion: false
    updateIntervalSeconds: 10
    allowUiUpdates: true
    options:
      path: /var/lib/grafana/dashboards
      foldersFromFilesStructure: true
`

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
			status.VictoriaMetrics.Running = vmStatus.State == "Running"
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
			status.Grafana.Running = grafanaStatus.State == "Running"
			status.Grafana.PID = grafanaStatus.PID
			status.Grafana.Uptime = time.Duration(grafanaStatus.Uptime) * time.Second
		} else {
			status.Grafana.Message = fmt.Sprintf("failed to get status: %v", err)
		}
	}

	// Check exporters (from exporter registry if available)
	if m.exporterRegistry != nil {
		// Check node_exporters
		for i, ne := range m.exporterRegistry.NodeExporters {
			expHealth := ExporterHealth{
				Type: "node_exporter",
				Host: ne.Host,
				Port: ne.Port,
			}

			// Check via supervisord
			programName := fmt.Sprintf("node-exporter-%d", i)
			if m.supervisorMgr != nil && m.supervisorMgr.IsRunning() {
				procStatus, err := m.supervisorMgr.GetProcessStatus(programName)
				if err == nil {
					expHealth.Running = procStatus.State == "Running"
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
		for i, me := range m.exporterRegistry.MongoDBExporters {
			expHealth := ExporterHealth{
				Type: "mongodb_exporter",
				Host: me.Host,
				Port: me.ExporterPort,
			}

			// Check via supervisord
			programName := fmt.Sprintf("mongodb-exporter-%d", i)
			if m.supervisorMgr != nil && m.supervisorMgr.IsRunning() {
				procStatus, err := m.supervisorMgr.GetProcessStatus(programName)
				if err == nil {
					expHealth.Running = procStatus.State == "Running"
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

// DeployExporters deploys all exporters for a cluster using supervisord
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

	// Regenerate monitoring-supervisor.ini with exporters included
	if err := m.addMonitoringToSupervisor(m.exporterRegistry); err != nil {
		return nil, fmt.Errorf("failed to add exporters to supervisor: %w", err)
	}

	// Reload supervisord to pick up new exporter programs
	if err := m.supervisorMgr.Reload(); err != nil {
		return nil, fmt.Errorf("failed to reload supervisor config: %w", err)
	}

	// Start node_exporters via supervisord
	for i, ne := range m.exporterRegistry.NodeExporters {
		programName := fmt.Sprintf("node-exporter-%d", i)
		if err := m.supervisorMgr.StartProcess(programName); err != nil {
			return nil, fmt.Errorf("failed to start node_exporter on %s:%d: %w", ne.Host, ne.Port, err)
		}

		monitoringMeta.NodeExporters = append(monitoringMeta.NodeExporters, meta.NodeExporterMetadata{
			Host: ne.Host,
			Port: ne.Port,
			// PID not stored - managed by supervisord
		})
	}

	// Start mongodb_exporters via supervisord
	for i, me := range m.exporterRegistry.MongoDBExporters {
		programName := fmt.Sprintf("mongodb-exporter-%d", i)
		if err := m.supervisorMgr.StartProcess(programName); err != nil {
			return nil, fmt.Errorf("failed to start mongodb_exporter on %s:%d: %w", me.Host, me.ExporterPort, err)
		}

		monitoringMeta.MongoDBExporters = append(monitoringMeta.MongoDBExporters, meta.MongoDBExporterMetadata{
			Host:         me.Host,
			ExporterPort: me.ExporterPort,
			MongoDBPort:  me.MongoDBPort,
			// PID not stored - managed by supervisord
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

// StopExporters stops all exporters via supervisord
func (m *Manager) StopExporters(ctx context.Context, monitoringMeta *meta.MonitoringMetadata) error {
	if monitoringMeta == nil {
		return nil
	}

	// Stop node_exporters via supervisord
	for i := range monitoringMeta.NodeExporters {
		programName := fmt.Sprintf("node-exporter-%d", i)
		if err := m.supervisorMgr.StopProcess(programName); err != nil {
			// Log but don't fail on error
			fmt.Printf("Warning: failed to stop %s: %v\n", programName, err)
		}
	}

	// Stop mongodb_exporters via supervisord
	for i := range monitoringMeta.MongoDBExporters {
		programName := fmt.Sprintf("mongodb-exporter-%d", i)
		if err := m.supervisorMgr.StopProcess(programName); err != nil {
			// Log but don't fail on error
			fmt.Printf("Warning: failed to stop %s: %v\n", programName, err)
		}
	}

	return nil
}
