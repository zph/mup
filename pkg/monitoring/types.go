package monitoring

import "time"

// Config represents the monitoring configuration
type Config struct {
	Enabled             bool   `yaml:"enabled"`
	VictoriaMetricsURL  string `yaml:"victoria_metrics_url"`
	ScrapeInterval      string `yaml:"scrape_interval"`
	RetentionPeriod     string `yaml:"retention_period"`
	VictoriaMetricsPort int    `yaml:"victoria_metrics_port"`
	GrafanaPort         int    `yaml:"grafana_port"`

	Exporters ExportersConfig `yaml:"exporters"`
	Grafana   GrafanaConfig   `yaml:"grafana"`
}

// ExportersConfig configures metric exporters
type ExportersConfig struct {
	NodeExporter    NodeExporterConfig    `yaml:"node_exporter"`
	MongoDBExporter MongoDBExporterConfig `yaml:"mongodb_exporter"`
}

// NodeExporterConfig configures node_exporter
type NodeExporterConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Version   string   `yaml:"version"`
	Port      int      `yaml:"port"`
	ExtraArgs []string `yaml:"extra_args"`
}

// MongoDBExporterConfig configures mongodb_exporter
type MongoDBExporterConfig struct {
	Enabled    bool     `yaml:"enabled"`
	Version    string   `yaml:"version"`
	PortBase   int      `yaml:"port_base"`
	CollectAll bool     `yaml:"collect_all"`
	ExtraArgs  []string `yaml:"extra_args"`
}

// GrafanaConfig configures Grafana
type GrafanaConfig struct {
	Enabled           bool     `yaml:"enabled"`
	Port              int      `yaml:"port"`
	AdminUser         string   `yaml:"admin_user"`
	AdminPasswordFile string   `yaml:"admin_password_file"`
	Dashboards        []string `yaml:"dashboards"`
}

// MonitoringURLs contains URLs for monitoring services
type MonitoringURLs struct {
	Grafana         string
	VictoriaMetrics string
	Dashboards      []DashboardURL
}

// DashboardURL represents a Grafana dashboard
type DashboardURL struct {
	Name string
	URL  string
}

// HealthStatus represents the health of monitoring components
type HealthStatus struct {
	VictoriaMetrics ComponentHealth
	Grafana         ComponentHealth
	Exporters       []ExporterHealth
}

// ComponentHealth represents health of a single component
type ComponentHealth struct {
	Running bool
	PID     int
	Uptime  time.Duration
	Message string
}

// ExporterHealth represents health of an exporter
type ExporterHealth struct {
	Type    string // "node_exporter" or "mongodb_exporter"
	Host    string
	Port    int
	Running bool
	PID     int
	Message string
}

// ExporterConfig configures an individual exporter instance
type ExporterConfig struct {
	Host        string
	Port        int
	Version     string
	BinaryPath  string
	ExtraArgs   []string
	LogFile     string
	MongoDBURI  string // For mongodb_exporter
	MongoDBPort int    // For mongodb_exporter
}

// DefaultConfig returns default monitoring configuration
func DefaultConfig() *Config {
	return &Config{
		Enabled:             true,
		VictoriaMetricsURL:  "http://localhost:8428",
		ScrapeInterval:      "15s",
		RetentionPeriod:     "30d",
		VictoriaMetricsPort: 8428,
		GrafanaPort:         3000,
		Exporters: ExportersConfig{
			NodeExporter: NodeExporterConfig{
				Enabled: true,
				Version: "1.7.0",
				Port:    9100,
			},
			MongoDBExporter: MongoDBExporterConfig{
				Enabled:  true,
				Version:  "0.40.0",
				PortBase: 9216,
				ExtraArgs: []string{
					"--collector.diagnosticdata",
					"--collector.replicasetstatus",
				},
			},
		},
		Grafana: GrafanaConfig{
			Enabled:   true,
			Port:      3000,
			AdminUser: "admin",
			Dashboards: []string{
				"mongodb-overview",
				"mongodb-replication",
				"mongodb-sharding",
				"mongodb-wiredtiger",
				"system-overview",
			},
		},
	}
}
