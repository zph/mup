package monitoring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zph/mup/pkg/executor"
)

func TestNewManager(t *testing.T) {
	tmpDir := t.TempDir()

	config := DefaultConfig()
	exec := executor.NewLocalExecutor()
	mgr, err := NewManager(tmpDir, config, exec)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	if mgr == nil {
		t.Fatal("manager is nil")
	}

	// Verify directories created
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Errorf("base directory not created")
	}

	logsDir := filepath.Join(tmpDir, "logs")
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		t.Errorf("logs directory not created")
	}

	vmDataDir := filepath.Join(tmpDir, "victoria-metrics", "data")
	if _, err := os.Stat(vmDataDir); os.IsNotExist(err) {
		t.Errorf("victoria metrics data directory not created")
	}

	grafanaDataDir := filepath.Join(tmpDir, "grafana", "data")
	if _, err := os.Stat(grafanaDataDir); os.IsNotExist(err) {
		t.Errorf("grafana data directory not created")
	}
}

func TestGetURLs(t *testing.T) {
	tmpDir := t.TempDir()

	config := DefaultConfig()
	exec := executor.NewLocalExecutor()
	mgr, err := NewManager(tmpDir, config, exec)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	urls, err := mgr.GetURLs()
	if err != nil {
		t.Fatalf("failed to get URLs: %v", err)
	}

	expectedGrafana := "http://localhost:3000"
	if urls.Grafana != expectedGrafana {
		t.Errorf("expected grafana URL %s, got %s", expectedGrafana, urls.Grafana)
	}

	expectedVM := "http://localhost:8428"
	if urls.VictoriaMetrics != expectedVM {
		t.Errorf("expected victoria metrics URL %s, got %s", expectedVM, urls.VictoriaMetrics)
	}

	// Check dashboards
	if len(urls.Dashboards) != len(config.Grafana.Dashboards) {
		t.Errorf("expected %d dashboards, got %d", len(config.Grafana.Dashboards), len(urls.Dashboards))
	}
}

func TestGrafanaPasswordGeneration(t *testing.T) {
	tmpDir := t.TempDir()

	config := DefaultConfig()
	exec := executor.NewLocalExecutor()
	mgr, err := NewManager(tmpDir, config, exec)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	password, err := mgr.GetGrafanaAdminPassword()
	if err != nil {
		t.Fatalf("failed to get grafana password: %v", err)
	}

	if password == "" {
		t.Error("password is empty")
	}

	if len(password) != 32 {
		t.Errorf("expected password length 32, got %d", len(password))
	}

	// Verify password is persisted
	password2, err := mgr.GetGrafanaAdminPassword()
	if err != nil {
		t.Fatalf("failed to get password again: %v", err)
	}

	if password != password2 {
		t.Error("password changed between reads")
	}
}

func TestHealthCheck(t *testing.T) {
	tmpDir := t.TempDir()

	config := DefaultConfig()
	exec := executor.NewLocalExecutor()
	mgr, err := NewManager(tmpDir, config, exec)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	ctx := context.Background()
	health, err := mgr.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}

	// Without starting containers, they should not be running
	if health.VictoriaMetrics.Running {
		t.Error("victoria metrics should not be running")
	}

	if health.Grafana.Running {
		t.Error("grafana should not be running")
	}
}

func TestGenerateSupervisorConfig(t *testing.T) {
	tmpDir := t.TempDir()

	config := DefaultConfig()
	exec := executor.NewLocalExecutor()
	mgr, err := NewManager(tmpDir, config, exec)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	if err := mgr.generateSupervisorConfig(); err != nil {
		t.Fatalf("failed to generate supervisor config: %v", err)
	}

	// Verify config file created
	configPath := filepath.Join(tmpDir, "supervisor.ini")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("supervisor config file not created")
	}

	// Read and verify config contains expected sections
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	configStr := string(data)

	// Check for required sections
	requiredSections := []string{
		"[supervisord]",
		"[inet_http_server]",
		"[program:monitoring-victoria-metrics]",
		"[program:monitoring-grafana]",
		"[group:monitoring]",
	}

	for _, section := range requiredSections {
		if !containsString(configStr, section) {
			t.Errorf("config missing section: %s", section)
		}
	}
}

func TestCreateGrafanaProvisioning(t *testing.T) {
	tmpDir := t.TempDir()

	config := DefaultConfig()
	exec := executor.NewLocalExecutor()
	mgr, err := NewManager(tmpDir, config, exec)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	ctx := context.Background()
	if err := mgr.createGrafanaProvisioning(ctx); err != nil {
		t.Fatalf("failed to create grafana provisioning: %v", err)
	}

	// Check datasource config
	datasourcePath := filepath.Join(mgr.grafana.GetProvisioningDir(), "datasources", "victoria-metrics.yaml")
	if _, err := os.Stat(datasourcePath); os.IsNotExist(err) {
		t.Error("datasource config not created")
	}

	// Check dashboard provisioning config
	dashboardPath := filepath.Join(mgr.grafana.GetProvisioningDir(), "dashboards", "default.yaml")
	if _, err := os.Stat(dashboardPath); os.IsNotExist(err) {
		t.Error("dashboard provisioning config not created")
	}

	// Verify datasource config content
	datasourceData, err := os.ReadFile(datasourcePath)
	if err != nil {
		t.Fatalf("failed to read datasource config: %v", err)
	}

	datasourceStr := string(datasourceData)
	if !containsString(datasourceStr, "VictoriaMetrics") {
		t.Error("datasource config missing VictoriaMetrics")
	}

	if !containsString(datasourceStr, "http://localhost:8428") {
		t.Error("datasource config missing victoria metrics URL")
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if !config.Enabled {
		t.Error("monitoring should be enabled by default")
	}

	if config.VictoriaMetricsPort != 8428 {
		t.Errorf("expected victoria metrics port 8428, got %d", config.VictoriaMetricsPort)
	}

	if config.GrafanaPort != 3000 {
		t.Errorf("expected grafana port 3000, got %d", config.GrafanaPort)
	}

	if config.ScrapeInterval != "15s" {
		t.Errorf("expected scrape interval 15s, got %s", config.ScrapeInterval)
	}

	if config.RetentionPeriod != "30d" {
		t.Errorf("expected retention period 30d, got %s", config.RetentionPeriod)
	}

	if !config.Exporters.NodeExporter.Enabled {
		t.Error("node exporter should be enabled by default")
	}

	if !config.Exporters.MongoDBExporter.Enabled {
		t.Error("mongodb exporter should be enabled by default")
	}

	if !config.Grafana.Enabled {
		t.Error("grafana should be enabled by default")
	}

	expectedDashboards := 5
	if len(config.Grafana.Dashboards) != expectedDashboards {
		t.Errorf("expected %d default dashboards, got %d", expectedDashboards, len(config.Grafana.Dashboards))
	}
}

// Helper function
func containsString(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(s) > len(substr) &&
			(s[:len(substr)] == substr ||
				s[len(s)-len(substr):] == substr ||
				findInString(s, substr)))
}

func findInString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
