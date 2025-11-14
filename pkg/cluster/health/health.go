package health

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/supervisor"
)

// Checker performs comprehensive health checks on clusters
type Checker struct {
	metadata  *meta.ClusterMetadata
	executor  executor.Executor
	supervisor *supervisor.Manager
}

// NewChecker creates a new health checker
func NewChecker(metadata *meta.ClusterMetadata, exec executor.Executor) (*Checker, error) {
	// Load supervisor manager if available
	var supMgr *supervisor.Manager
	if metadata.SupervisorConfigPath != "" {
		// Extract cluster dir from metadata
		// The config path is typically ~/.mup/storage/clusters/<name>/supervisor.ini
		// So the cluster dir is the parent directory
		clusterDir := ""
		if len(metadata.SupervisorConfigPath) > 0 {
			// Get the directory containing supervisor.ini
			clusterDir = filepath.Dir(metadata.SupervisorConfigPath)
		}

		if clusterDir != "" {
			var err error
			supMgr, err = supervisor.LoadManager(clusterDir, metadata.Name)
			if err != nil {
				// If we can't load supervisor, that's okay - we'll work without it
				supMgr = nil
			}
		}
	}

	return &Checker{
		metadata:  metadata,
		executor:  exec,
		supervisor: supMgr,
	}, nil
}

// Check performs comprehensive health checks
func (c *Checker) Check(ctx context.Context) (*ClusterHealth, error) {
	health := &ClusterHealth{
		Nodes:      []NodeHealth{},
		Ports:      PortMapping{},
		Monitoring: MonitoringHealth{},
	}

	// Check each node
	for _, node := range c.metadata.Nodes {
		nodeHealth := c.checkNode(ctx, node)
		health.Nodes = append(health.Nodes, nodeHealth)

		// Add to port mapping
		portInfo := PortInfo{
			Host:        node.Host,
			Port:        node.Port,
			Service:     node.Type,
			Description: fmt.Sprintf("%s on %s:%d", node.Type, node.Host, node.Port),
			Status:      nodeHealth.Status,
		}
		health.Ports.MongoDB = append(health.Ports.MongoDB, portInfo)
	}

	// Check monitoring if enabled
	if c.metadata.Monitoring != nil && c.metadata.Monitoring.Enabled {
		health.Monitoring = c.checkMonitoring(ctx)

		// Add monitoring ports
		if c.metadata.Monitoring.VictoriaMetricsURL != "" {
			health.Ports.Monitoring = append(health.Ports.Monitoring, PortInfo{
				Host:        "localhost",
				Port:        8428,
				Service:     "victoria-metrics",
				Description: "Victoria Metrics",
				Status:      health.Monitoring.VictoriaMetrics.Status,
			})
		}

		if c.metadata.Monitoring.GrafanaURL != "" {
			health.Ports.Monitoring = append(health.Ports.Monitoring, PortInfo{
				Host:        "localhost",
				Port:        3000,
				Service:     "grafana",
				Description: "Grafana",
				Status:      health.Monitoring.Grafana.Status,
			})
		}

		// Add exporter ports
		for _, ne := range c.metadata.Monitoring.NodeExporters {
			status := c.checkPort(ne.Host, ne.Port)
			health.Ports.Monitoring = append(health.Ports.Monitoring, PortInfo{
				Host:        ne.Host,
				Port:        ne.Port,
				Service:     "node_exporter",
				Description: fmt.Sprintf("Node Exporter for %s", ne.Host),
				Status:      status,
			})
		}

		for _, me := range c.metadata.Monitoring.MongoDBExporters {
			status := c.checkPort(me.Host, me.ExporterPort)
			health.Ports.Monitoring = append(health.Ports.Monitoring, PortInfo{
				Host:        me.Host,
				Port:        me.ExporterPort,
				Service:     "mongodb_exporter",
				Description: fmt.Sprintf("MongoDB Exporter for :%d", me.MongoDBPort),
				Status:      status,
			})
		}
	}

	// Add supervisor API ports if configured
	if c.metadata.SupervisorConfigPath != "" {
		health.Ports.Supervisor = append(health.Ports.Supervisor, PortInfo{
			Host:        "localhost",
			Port:        9001,
			Service:     "supervisor-api",
			Description: "Cluster Supervisor API",
			Status:      "unknown",
		})
	}

	if c.metadata.Monitoring != nil && c.metadata.Monitoring.SupervisorConfigPath != "" {
		health.Ports.Supervisor = append(health.Ports.Supervisor, PortInfo{
			Host:        "localhost",
			Port:        9002,
			Service:     "supervisor-api",
			Description: "Monitoring Supervisor API",
			Status:      "unknown",
		})
	}

	return health, nil
}

// checkNode performs health checks on a single node
func (c *Checker) checkNode(ctx context.Context, node meta.NodeMetadata) NodeHealth {
	health := NodeHealth{
		Host:       node.Host,
		Port:       node.Port,
		Type:       node.Type,
		ReplicaSet: node.ReplicaSet,
		Status:     "unknown",
	}

	// Check 1: Supervisord process status
	if c.supervisor != nil && node.SupervisorProgramName != "" {
		status, err := c.supervisor.GetProcessStatus(node.SupervisorProgramName)
		if err == nil {
			health.SupervisorStatus = status.State
			health.PID = status.PID
			health.Uptime = time.Duration(status.Uptime) * time.Second

			// Map supervisor state to our status
			switch status.State {
			case "RUNNING":
				health.Status = "running"
			case "STOPPED":
				health.Status = "stopped"
			case "STARTING":
				health.Status = "starting"
			case "FATAL", "EXITED":
				health.Status = "failed"
			default:
				health.Status = status.State
			}
		}
	}

	// Check 2: Port availability (if supervisor check failed or unavailable)
	if health.Status == "unknown" {
		portStatus := c.checkPort(node.Host, node.Port)
		health.Status = portStatus
	}

	return health
}

// checkPort checks if a port is listening
func (c *Checker) checkPort(host string, port int) string {
	address := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return "stopped"
	}
	conn.Close()
	return "running"
}

// checkMonitoring checks monitoring infrastructure health
func (c *Checker) checkMonitoring(ctx context.Context) MonitoringHealth {
	health := MonitoringHealth{
		Enabled: true,
	}

	// Check Victoria Metrics
	if c.metadata.Monitoring.VictoriaMetricsURL != "" {
		vmStatus := c.checkPort("localhost", 8428)
		health.VictoriaMetrics = ComponentHealth{
			Status: vmStatus,
			URL:    c.metadata.Monitoring.VictoriaMetricsURL,
		}
	}

	// Check Grafana
	if c.metadata.Monitoring.GrafanaURL != "" {
		grafanaStatus := c.checkPort("localhost", 3000)
		health.Grafana = ComponentHealth{
			Status: grafanaStatus,
			URL:    c.metadata.Monitoring.GrafanaURL,
		}
	}

	// Check exporters
	for _, ne := range c.metadata.Monitoring.NodeExporters {
		status := c.checkPort(ne.Host, ne.Port)
		health.Exporters = append(health.Exporters, ExporterHealth{
			Type:   "node_exporter",
			Host:   ne.Host,
			Port:   ne.Port,
			Status: status,
		})
	}

	for _, me := range c.metadata.Monitoring.MongoDBExporters {
		status := c.checkPort(me.Host, me.ExporterPort)
		health.Exporters = append(health.Exporters, ExporterHealth{
			Type:   "mongodb_exporter",
			Host:   me.Host,
			Port:   me.ExporterPort,
			Status: status,
		})
	}

	return health
}

// ClusterHealth represents the complete health state of a cluster
type ClusterHealth struct {
	Nodes      []NodeHealth
	Ports      PortMapping
	Monitoring MonitoringHealth
}

// NodeHealth represents health information for a single node
type NodeHealth struct {
	Host             string
	Port             int
	Type             string
	ReplicaSet       string
	Status           string // "running", "stopped", "starting", "failed", "unknown"
	SupervisorStatus string // Supervisor state if available
	PID              int
	Uptime           time.Duration
}

// PortMapping contains all ports used by the cluster
type PortMapping struct {
	MongoDB    []PortInfo
	Monitoring []PortInfo
	Supervisor []PortInfo
}

// PortInfo represents information about a port
type PortInfo struct {
	Host        string
	Port        int
	Service     string
	Description string
	Status      string
}

// MonitoringHealth represents monitoring infrastructure health
type MonitoringHealth struct {
	Enabled         bool
	VictoriaMetrics ComponentHealth
	Grafana         ComponentHealth
	Exporters       []ExporterHealth
}

// ComponentHealth represents health of a monitoring component
type ComponentHealth struct {
	Status string
	URL    string
}

// ExporterHealth represents health of an exporter
type ExporterHealth struct {
	Type   string
	Host   string
	Port   int
	Status string
}
