# Monitoring and Metrics Design for Mup

> **Note**: This design document describes the original monitoring architecture. The implementation has evolved to use a **single supervisord instance per cluster** where monitoring lives within each cluster directory (`~/.mup/storage/clusters/<name>/monitoring/`) rather than a global monitoring directory. See the "Architecture Evolution" section below for details.

## Overview

This document outlines the design for comprehensive monitoring and metrics collection for MongoDB clusters managed by Mup. The solution uses Victoria Metrics (a Prometheus-compatible time-series database), appropriate exporters, and pre-configured Grafana dashboards for visualization.

### Design Goals

1. **Zero-configuration defaults**: Monitoring should work out-of-the-box with sensible defaults
2. **Local & Remote support**: Architecture must work for both local playground and remote production deployments
3. **Integrated lifecycle**: Monitoring components managed alongside MongoDB processes (start/stop/destroy)
4. **Pre-built dashboards**: Include production-ready Grafana dashboards that auto-load
5. **Resource efficiency**: Minimal overhead on monitored systems
6. **Version compatibility**: Support MongoDB 3.6-8.0 metrics collection

### Architecture Evolution

**Original Design**: Global monitoring directory at `~/.mup/monitoring/` with separate supervisord instance.

**Current Implementation**: Cluster-specific monitoring with unified supervisord:
- Monitoring lives in `~/.mup/storage/clusters/<name>/monitoring/`
- Single supervisord instance per cluster manages both MongoDB and monitoring processes
- Monitoring configuration in `monitoring-supervisor.ini` alongside main `supervisor.ini`
- Monitoring processes organized as supervisord group: `[group:monitoring]`
- Lifecycle commands (`mup cluster start/stop`) automatically manage monitoring

**Benefits**:
- Simplified architecture: one supervisor per cluster instead of N+1 supervisors
- Automatic lifecycle management: monitoring starts/stops with cluster
- Per-cluster isolation: each cluster has its own monitoring stack
- Cleaner directory structure: all cluster resources in one location
- Easier cleanup: `mup cluster destroy` removes all monitoring data

---

## Architecture

### Component Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         Grafana UI                              │
│                    (Dashboards & Visualization)                  │
└────────────────────────────┬────────────────────────────────────┘
                             │ HTTP/API
┌────────────────────────────┴────────────────────────────────────┐
│                    Victoria Metrics                             │
│              (Time-Series Database & Query Engine)               │
│                  - vmstorage (data storage)                      │
│                  - vmselect (query handler)                      │
│                  - vminsert (data ingestion)                     │
│                  Single-node for simplicity                      │
└────────────────────────────┬────────────────────────────────────┘
                             │ Remote Write / Scrape
           ┌─────────────────┼─────────────────┐
           │                 │                 │
    ┏━━━━━━┷━━━━━━┓   ┏━━━━━┷━━━━━┓   ┏━━━━━┷━━━━━┓
    ┃ Host 1      ┃   ┃ Host 2     ┃   ┃ Host N     ┃
    ┃─────────────┃   ┃────────────┃   ┃────────────┃
    ┃ node_exporter   ┃ node_exporter   ┃ node_exporter
    ┃ :9100       ┃   ┃ :9100      ┃   ┃ :9100      ┃
    ┃             ┃   ┃            ┃   ┃            ┃
    ┃ mongodb_exporter mongodb_exporter mongodb_exporter
    ┃ :9216       ┃   ┃ :9216      ┃   ┃ :9216      ┃
    ┃             ┃   ┃            ┃   ┃            ┃
    ┃ mongod      ┃   ┃ mongod     ┃   ┃ mongod     ┃
    ┃ :27017      ┃   ┃ :27017     ┃   ┃ :27017     ┃
    ┗━━━━━━━━━━━━━┛   ┗━━━━━━━━━━━━┛   ┗━━━━━━━━━━━━┛
```

### Component Responsibilities

#### 1. Victoria Metrics (Single-Node)
- **Purpose**: Time-series data storage and querying
- **Deployment**: Docker container managed by embedded supervisord
- **Container**: `victoriametrics/victoria-metrics:latest`
- **Location**:
  - Data: `~/.mup/monitoring/victoria-metrics/data/`
  - Config: `~/.mup/monitoring/victoria-metrics/config/`
  - Listens on `localhost:8428` (configurable)
- **Management**: Supervisord program `victoria-metrics-docker`
- **Features**:
  - Prometheus-compatible remote write endpoint
  - PromQL query language support
  - Built-in de-duplication and downsampling
  - Low resource footprint
  - Long-term storage retention
  - Easy updates via container image pulls

#### 2. node_exporter (Per Host)
- **Purpose**: OS and hardware metrics collection
- **Deployment**: One per physical/virtual host
- **Default Port**: 9100
- **Metrics Collected**:
  - CPU usage, load average
  - Memory usage (total, free, cached, buffers)
  - Disk I/O, throughput, latency
  - Network traffic, errors, drops
  - Filesystem usage and inode counts
  - System uptime

#### 3. mongodb_exporter (Per MongoDB Process)
- **Purpose**: MongoDB-specific metrics collection
- **Deployment**: One per mongod/mongos instance
- **Default Port**: 9216 (auto-allocated for multiple instances)
- **Metrics Collected**:
  - Server status (opcounters, connections, network)
  - Replica set status (state, lag, oplog window)
  - Sharding metrics (chunk distribution, balancer)
  - Storage engine metrics (cache, tickets)
  - Collection and index statistics
  - Query performance
  - Lock contention

#### 4. Grafana
- **Purpose**: Visualization and dashboarding
- **Deployment**: Docker container managed by embedded supervisord
- **Container**: `grafana/grafana:latest`
- **Location**:
  - Data: `~/.mup/monitoring/grafana/data/`
  - Provisioning: `~/.mup/monitoring/grafana/provisioning/`
  - Dashboards: `~/.mup/monitoring/grafana/dashboards/`
- **Default Port**: 3000
- **Management**: Supervisord program `grafana-docker`
- **Features**:
  - Pre-provisioned datasource (Victoria Metrics)
  - Auto-loaded dashboards
  - Alert rule provisioning
  - Multi-cluster support via labels
  - Easy updates via container image pulls

---

## Topology Configuration

### Global Monitoring Configuration

```yaml
# topology.yaml
global:
  user: mongodb
  deploy_dir: /opt/mongodb

  # Monitoring configuration
  monitoring:
    enabled: true
    # Where to send metrics (defaults to local Victoria Metrics)
    victoria_metrics_url: "http://localhost:8428"

    # Scrape interval
    scrape_interval: 15s

    # Retention period
    retention_period: 30d

    # Enable/disable specific exporters
    exporters:
      node_exporter:
        enabled: true
        version: "1.7.0"  # Latest stable
        port: 9100
        extra_args: []

      mongodb_exporter:
        enabled: true
        version: "0.40.0"  # Latest stable
        port_base: 9216  # Auto-increment for multiple instances
        collect_all: false  # Set true for detailed metrics
        extra_args:
          - "--collect-diagnosticdata"
          - "--collect-replicasetstatus"

    # Grafana configuration
    grafana:
      enabled: true
      port: 3000
      admin_user: admin
      admin_password_file: ~/.mup/monitoring/grafana/.password

      # Auto-load dashboards
      dashboards:
        - mongodb-overview
        - mongodb-replication
        - mongodb-sharding
        - mongodb-wiredtiger
        - system-overview

    # Alerting (future)
    alerting:
      enabled: false
      rules_dir: ~/.mup/monitoring/alert-rules

mongod_servers:
  - host: localhost
    port: 0
    replica_set: rs0

    # Per-node monitoring overrides
    monitoring:
      exporters:
        mongodb_exporter:
          # Override specific exporter settings
          collect_all: true  # More detailed for this node
```

### Minimal Configuration (Defaults)

```yaml
# topology.yaml - monitoring enabled by default
global:
  user: mongodb
  deploy_dir: /opt/mongodb

  # Simple enable/disable
  monitoring:
    enabled: true  # This is all you need!

mongod_servers:
  - host: localhost
    port: 0
    replica_set: rs0
```

---

## Directory Structure

```
~/.mup/
├── monitoring/
│   ├── supervisor.ini         # Supervisord config for monitoring components
│   ├── supervisor.pid         # Supervisord PID
│   ├── supervisor.log         # Supervisord log
│   │
│   ├── victoria-metrics/
│   │   ├── data/              # Time-series data (mounted in container)
│   │   ├── config/            # VM config files
│   │   │   └── promscrape.yaml
│   │   └── docker.json        # Container metadata (image, ID, etc.)
│   │
│   ├── exporters/
│   │   ├── node_exporter/
│   │   │   ├── versions/
│   │   │   │   ├── 1.7.0/
│   │   │   │   │   └── node_exporter
│   │   │   └── current -> versions/1.7.0
│   │   │
│   │   └── mongodb_exporter/
│   │       ├── versions/
│   │       │   ├── 0.40.0/
│   │       │   │   └── mongodb_exporter
│   │       └── current -> versions/0.40.0
│   │
│   ├── grafana/
│   │   ├── data/              # Grafana data (mounted in container)
│   │   ├── provisioning/      # Provisioning configs (mounted)
│   │   │   ├── datasources/
│   │   │   │   └── victoria-metrics.yaml
│   │   │   ├── dashboards/
│   │   │   │   └── default.yaml
│   │   │   └── alerting/
│   │   ├── dashboards/        # Dashboard JSON files (mounted)
│   │   │   ├── mongodb-overview.json
│   │   │   ├── mongodb-replication.json
│   │   │   ├── mongodb-sharding.json
│   │   │   ├── mongodb-wiredtiger.json
│   │   │   └── system-overview.json
│   │   ├── .password          # Admin password
│   │   └── docker.json        # Container metadata
│   │
│   ├── scrape-configs/
│   │   └── playground.yaml    # Generated scrape configs
│   │
│   └── logs/
│       ├── supervisord.log    # Main supervisor log
│       ├── victoria-metrics.log
│       ├── grafana.log
│       └── exporters/
│           ├── node_exporter-host1.log
│           └── mongodb_exporter-host1-27017.log
│
├── storage/
│   └── clusters/
│       └── my-cluster/
│           ├── meta.yaml
│           └── monitoring/
│               ├── scrape-config.yaml    # Cluster-specific scrape targets
│               └── exporter-pids.json    # Running exporter PIDs
```

---

## Supervisord Configuration for Docker Containers

### Overview

Mup uses the embedded golang supervisord (github.com/ochinchina/supervisord) to manage Docker containers for Victoria Metrics and Grafana. This provides:
- Automatic container restart on failure
- Unified log management
- Process lifecycle control (start/stop/status)
- Health monitoring

### Supervisord Configuration

```ini
# ~/.mup/monitoring/supervisor.ini
[supervisord]
logfile = ~/.mup/monitoring/supervisor.log
loglevel = info
pidfile = ~/.mup/monitoring/supervisor.pid
nodaemon = false

[inet_http_server]
port = 127.0.0.1:9002  # Monitoring supervisor API

# Victoria Metrics container
[program:monitoring-victoria-metrics]
command = docker run --rm \
  --name mup-victoria-metrics \
  -p 127.0.0.1:8428:8428 \
  -v ~/.mup/monitoring/victoria-metrics/data:/victoria-metrics-data \
  -v ~/.mup/monitoring/victoria-metrics/config:/etc/victoria-metrics \
  victoriametrics/victoria-metrics:latest \
  -promscrape.config=/etc/victoria-metrics/promscrape.yaml \
  -retentionPeriod=30d \
  -storageDataPath=/victoria-metrics-data
autostart = false
autorestart = unexpected
startsecs = 5
startretries = 3
stdout_logfile = ~/.mup/monitoring/logs/victoria-metrics.log
stderr_logfile = ~/.mup/monitoring/logs/victoria-metrics.log
stopwaitsecs = 30
stopsignal = TERM

# Grafana container
[program:monitoring-grafana]
command = docker run --rm \
  --name mup-grafana \
  -p 127.0.0.1:3000:3000 \
  -v ~/.mup/monitoring/grafana/data:/var/lib/grafana \
  -v ~/.mup/monitoring/grafana/provisioning:/etc/grafana/provisioning \
  -v ~/.mup/monitoring/grafana/dashboards:/var/lib/grafana/dashboards \
  -e GF_AUTH_ANONYMOUS_ENABLED=false \
  -e GF_SECURITY_ADMIN_PASSWORD__FILE=/var/lib/grafana/.password \
  grafana/grafana:latest
autostart = false
autorestart = unexpected
startsecs = 10
startretries = 3
stdout_logfile = ~/.mup/monitoring/logs/grafana.log
stderr_logfile = ~/.mup/monitoring/logs/grafana.log
stopwaitsecs = 30
stopsignal = TERM

[group:monitoring]
programs = monitoring-victoria-metrics,monitoring-grafana
```

### Docker Container Management

The monitoring system uses Docker for local deployments with these considerations:

**Advantages:**
- Isolated execution environment
- Easy version upgrades (pull new image)
- Consistent behavior across platforms
- No binary download/extraction needed
- Official images maintained by vendors

**Container Lifecycle:**
1. **Start**: Supervisord runs `docker run` with appropriate volume mounts
2. **Monitor**: Supervisord monitors container process
3. **Restart**: On unexpected exit, supervisord restarts container
4. **Stop**: Supervisord sends SIGTERM to gracefully stop container
5. **Cleanup**: `--rm` flag ensures container removal on stop

**Volume Mounts:**
- Data persistence: Host directories mounted for data, config, dashboards
- Configuration: Provisioning files mounted read-only
- Logs: Stdout/stderr captured by supervisord

**Networking:**
- Ports bound to `127.0.0.1` for security (local-only access)
- Host network mode not used (explicit port mapping)

**Resource Limits:**
```bash
# Can add Docker resource constraints:
docker run ... \
  --memory=2g \
  --cpus=1.0 \
  ...
```

### Exporter Management

Node and MongoDB exporters run as native binaries (not containers) because:
1. Need host-level access for OS metrics
2. Lower overhead than containerization
3. Simpler deployment for per-process exporters
4. Direct access to MongoDB processes

These are managed by a separate supervisord instance per cluster (same pattern as MongoDB processes).

---

## State Management

### Cluster Metadata Extension

```yaml
# ~/.mup/storage/clusters/my-cluster/meta.yaml
cluster_name: my-cluster
version: "7.0.5"
...

# New monitoring section
monitoring:
  enabled: true
  victoria_metrics_url: "http://localhost:8428"

  # Tracking running monitoring processes
  processes:
    victoria_metrics:
      pid: 12345
      port: 8428
      started_at: "2025-01-15T10:00:00Z"

    grafana:
      pid: 12346
      port: 3000
      started_at: "2025-01-15T10:00:05Z"

  # Per-node exporter info
  exporters:
    # node_exporter instances (one per unique host)
    node_exporters:
      - host: localhost
        pid: 12347
        port: 9100
        version: "1.7.0"
        started_at: "2025-01-15T10:00:10Z"

    # mongodb_exporter instances (one per MongoDB process)
    mongodb_exporters:
      - host: localhost
        mongodb_port: 27017
        exporter_port: 9216
        pid: 12348
        version: "0.40.0"
        started_at: "2025-01-15T10:00:15Z"
        connection_uri: "mongodb://localhost:27017"

      - host: localhost
        mongodb_port: 27018
        exporter_port: 9217
        pid: 12349
        version: "0.40.0"
        started_at: "2025-01-15T10:00:20Z"
        connection_uri: "mongodb://localhost:27018"
```

---

## Implementation Design

### Package Structure

```
pkg/
├── monitoring/
│   ├── manager.go              # Main monitoring manager
│   ├── victoria_metrics.go     # Victoria Metrics lifecycle
│   ├── grafana.go              # Grafana lifecycle
│   ├── exporters/
│   │   ├── exporter.go         # Base exporter interface
│   │   ├── node_exporter.go    # node_exporter management
│   │   ├── mongodb_exporter.go # mongodb_exporter management
│   │   └── port_allocator.go   # Port allocation for exporters
│   ├── dashboards/
│   │   ├── loader.go           # Dashboard provisioning
│   │   └── templates/          # Embedded dashboard JSON files
│   │       ├── mongodb-overview.json
│   │       ├── mongodb-replication.json
│   │       ├── mongodb-sharding.json
│   │       ├── mongodb-wiredtiger.json
│   │       └── system-overview.json
│   ├── scraper/
│   │   ├── config_generator.go # Generate Prometheus scrape configs
│   │   └── targets.go          # Service discovery for targets
│   └── binary/
│       ├── downloader.go       # Download monitoring binaries
│       └── versions.go         # Version management
```

### Core Interfaces

```go
// pkg/monitoring/manager.go
package monitoring

import (
	"context"
	"time"
)

// Manager handles lifecycle of all monitoring components
type Manager interface {
	// Initialize monitoring infrastructure (Victoria Metrics, Grafana)
	Initialize(ctx context.Context) error

	// Start monitoring for a cluster
	StartClusterMonitoring(ctx context.Context, clusterName string) error

	// Stop monitoring for a cluster
	StopClusterMonitoring(ctx context.Context, clusterName string) error

	// Update monitoring configuration (e.g., after scale-out)
	UpdateMonitoring(ctx context.Context, clusterName string) error

	// Check health of monitoring components
	HealthCheck(ctx context.Context) (*HealthStatus, error)

	// Get monitoring URLs (Grafana, Victoria Metrics)
	GetURLs() (*MonitoringURLs, error)

	// Cleanup monitoring (on destroy)
	Cleanup(ctx context.Context, clusterName string) error
}

type MonitoringURLs struct {
	Grafana        string // http://localhost:3000
	VictoriaMetrics string // http://localhost:8428
	Dashboards     []DashboardURL
}

type DashboardURL struct {
	Name string
	URL  string
}

type HealthStatus struct {
	VictoriaMetrics ComponentHealth
	Grafana         ComponentHealth
	Exporters       []ExporterHealth
}

type ComponentHealth struct {
	Running bool
	PID     int
	Uptime  time.Duration
	Message string
}

type ExporterHealth struct {
	Type    string // "node_exporter" or "mongodb_exporter"
	Host    string
	Port    int
	Running bool
	PID     int
	Message string
}

// Exporter is the interface for all metric exporters
type Exporter interface {
	// Start the exporter process
	Start(ctx context.Context, config ExporterConfig) error

	// Stop the exporter process
	Stop(ctx context.Context) error

	// Check if exporter is running
	IsRunning() (bool, error)

	// Get exporter metrics endpoint
	GetEndpoint() string

	// Get process ID
	GetPID() int
}

type ExporterConfig struct {
	Host          string
	Port          int
	Version       string
	BinaryPath    string
	ExtraArgs     []string
	LogFile       string

	// MongoDB-specific
	MongoDBURI    string
	MongoDBPort   int
}
```

### Deployment Workflow Integration

The monitoring system integrates with the existing 5-phase deployment process:

```go
// pkg/deploy/deploy.go - Phase 3 extension
func (d *Deployer) Deploy(ctx context.Context) error {
	// ... existing phases ...

	// Phase 3: Deploy (extended)
	if err := d.deployMongoDBProcesses(ctx); err != nil {
		return err
	}

	// NEW: Deploy monitoring components if enabled
	if d.topology.Global.Monitoring.Enabled {
		if err := d.deployMonitoring(ctx); err != nil {
			log.Warn("Failed to deploy monitoring (non-fatal): %v", err)
			// Don't fail deployment if monitoring fails
		}
	}

	// ... continue with phases 4-5 ...
}

func (d *Deployer) deployMonitoring(ctx context.Context) error {
	log.Info("Deploying monitoring components...")

	// 1. Initialize monitoring infrastructure (Victoria Metrics, Grafana)
	if err := d.monitoringManager.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize monitoring: %w", err)
	}

	// 2. Deploy exporters per host
	hosts := d.topology.GetAllHosts()
	for _, host := range hosts {
		// Deploy node_exporter (one per host)
		if err := d.deployNodeExporter(ctx, host); err != nil {
			log.Warn("Failed to deploy node_exporter on %s: %v", host, err)
		}
	}

	// 3. Deploy mongodb_exporter (one per MongoDB process)
	for _, node := range d.topology.Mongod {
		if err := d.deployMongoDBExporter(ctx, &node); err != nil {
			log.Warn("Failed to deploy mongodb_exporter for %s:%d: %v",
				node.Host, node.Port, err)
		}
	}

	// 4. Generate scrape configuration
	if err := d.generateScrapeConfig(ctx); err != nil {
		return fmt.Errorf("failed to generate scrape config: %w", err)
	}

	// 5. Start cluster monitoring
	if err := d.monitoringManager.StartClusterMonitoring(ctx, d.clusterName); err != nil {
		return fmt.Errorf("failed to start cluster monitoring: %w", err)
	}

	// 6. Display monitoring URLs
	urls, _ := d.monitoringManager.GetURLs()
	log.Info("Monitoring URLs:")
	log.Info("  Grafana: %s", urls.Grafana)
	log.Info("  Victoria Metrics: %s", urls.VictoriaMetrics)

	return nil
}
```

---

## Scrape Configuration Generation

### Victoria Metrics Scrape Config

```yaml
# ~/.mup/storage/clusters/my-cluster/monitoring/scrape-config.yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s
  external_labels:
    cluster: 'my-cluster'
    mup_version: '0.1.0'

scrape_configs:
  # Node exporters (OS metrics)
  - job_name: 'node-exporter'
    static_configs:
      - targets:
          - 'localhost:9100'
        labels:
          host: 'localhost'
          role: 'mongodb-host'

  # MongoDB exporters
  - job_name: 'mongodb-exporter'
    static_configs:
      - targets:
          - 'localhost:9216'
        labels:
          host: 'localhost'
          port: '27017'
          replica_set: 'rs0'
          role: 'mongod'

      - targets:
          - 'localhost:9217'
        labels:
          host: 'localhost'
          port: '27018'
          replica_set: 'rs0'
          role: 'mongod'
```

### Scrape Config Generation Code

```go
// pkg/monitoring/scraper/config_generator.go
package scraper

import (
	"fmt"
	"github.com/zph/mup/pkg/topology"
	"gopkg.in/yaml.v3"
)

type ScrapeConfig struct {
	Global        GlobalConfig   `yaml:"global"`
	ScrapeConfigs []JobConfig    `yaml:"scrape_configs"`
}

type GlobalConfig struct {
	ScrapeInterval     string            `yaml:"scrape_interval"`
	EvaluationInterval string            `yaml:"evaluation_interval"`
	ExternalLabels     map[string]string `yaml:"external_labels"`
}

type JobConfig struct {
	JobName       string         `yaml:"job_name"`
	StaticConfigs []StaticConfig `yaml:"static_configs"`
}

type StaticConfig struct {
	Targets []string          `yaml:"targets"`
	Labels  map[string]string `yaml:"labels"`
}

func GenerateScrapeConfig(clusterName string, topo *topology.Topology, exporters *ExporterRegistry) (*ScrapeConfig, error) {
	config := &ScrapeConfig{
		Global: GlobalConfig{
			ScrapeInterval:     "15s",
			EvaluationInterval: "15s",
			ExternalLabels: map[string]string{
				"cluster":     clusterName,
				"mup_version": "0.1.0", // TODO: Get from version
			},
		},
		ScrapeConfigs: []JobConfig{},
	}

	// Add node_exporter targets (one per unique host)
	nodeExporterJob := JobConfig{
		JobName:       "node-exporter",
		StaticConfigs: []StaticConfig{},
	}

	seenHosts := make(map[string]bool)
	for _, ne := range exporters.NodeExporters {
		if seenHosts[ne.Host] {
			continue
		}
		seenHosts[ne.Host] = true

		nodeExporterJob.StaticConfigs = append(nodeExporterJob.StaticConfigs, StaticConfig{
			Targets: []string{fmt.Sprintf("%s:%d", ne.Host, ne.Port)},
			Labels: map[string]string{
				"host": ne.Host,
				"role": "mongodb-host",
			},
		})
	}
	config.ScrapeConfigs = append(config.ScrapeConfigs, nodeExporterJob)

	// Add mongodb_exporter targets (one per MongoDB process)
	mongoExporterJob := JobConfig{
		JobName:       "mongodb-exporter",
		StaticConfigs: []StaticConfig{},
	}

	for _, me := range exporters.MongoDBExporters {
		labels := map[string]string{
			"host": me.Host,
			"port": fmt.Sprintf("%d", me.MongoDBPort),
		}

		// Find node in topology to get additional labels
		for _, node := range topo.Mongod {
			if node.Host == me.Host && node.Port == me.MongoDBPort {
				if node.ReplicaSet != "" {
					labels["replica_set"] = node.ReplicaSet
				}
				labels["role"] = "mongod"
				break
			}
		}

		mongoExporterJob.StaticConfigs = append(mongoExporterJob.StaticConfigs, StaticConfig{
			Targets: []string{fmt.Sprintf("%s:%d", me.Host, me.ExporterPort)},
			Labels:  labels,
		})
	}
	config.ScrapeConfigs = append(config.ScrapeConfigs, mongoExporterJob)

	return config, nil
}
```

---

## Grafana Dashboard Provisioning

### Datasource Configuration

```yaml
# ~/.mup/monitoring/grafana/provisioning/datasources/victoria-metrics.yaml
apiVersion: 1

datasources:
  - name: VictoriaMetrics
    type: prometheus
    access: proxy
    url: http://localhost:8428
    isDefault: true
    editable: false
    jsonData:
      timeInterval: 15s
      httpMethod: POST
```

### Dashboard Provisioning

```yaml
# ~/.mup/monitoring/grafana/provisioning/dashboards/default.yaml
apiVersion: 1

providers:
  - name: 'Mup MongoDB Dashboards'
    orgId: 1
    folder: 'MongoDB'
    type: file
    disableDeletion: false
    updateIntervalSeconds: 10
    allowUiUpdates: true
    options:
      path: /Users/zph/.mup/monitoring/grafana/dashboards
      foldersFromFilesStructure: true
```

### Pre-built Dashboards

**1. MongoDB Overview Dashboard**
- Cluster topology visualization
- Ops/sec across all nodes
- Connection counts
- Query efficiency
- Memory usage
- Network I/O
- Alert summary

**2. Replication Dashboard**
- Replica set status
- Replication lag (seconds behind primary)
- Oplog window
- Election events
- Heartbeat latency

**3. Sharding Dashboard**
- Chunk distribution per shard
- Balancer activity
- Migration progress
- Jumbo chunks
- Shard key efficiency

**4. WiredTiger Dashboard**
- Cache hit ratio
- Tickets available/used
- Checkpoint metrics
- Cache eviction rate
- Dirty bytes in cache

**5. System Overview Dashboard**
- CPU usage per host
- Memory usage per host
- Disk I/O per host
- Network traffic per host
- Disk space usage

---

## Command-Line Interface

### New Commands

```bash
# Initialize monitoring infrastructure (done automatically on first deploy)
mup monitoring init

# Start monitoring for a cluster
mup monitoring start <cluster-name>

# Stop monitoring for a cluster
mup monitoring stop <cluster-name>

# Check monitoring status
mup monitoring status [<cluster-name>]
# Output:
# Monitoring Status for cluster: my-cluster
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
# Victoria Metrics: ✓ Running (PID: 12345, Port: 8428)
# Grafana:          ✓ Running (PID: 12346, Port: 3000)
#
# Exporters:
#   node_exporter (localhost:9100):    ✓ Running (PID: 12347)
#   mongodb_exporter (localhost:9216): ✓ Running (PID: 12348)
#   mongodb_exporter (localhost:9217): ✓ Running (PID: 12349)
#
# URLs:
#   Grafana:         http://localhost:3000
#   Victoria Metrics: http://localhost:8428

# Open Grafana in browser
mup monitoring dashboard [<cluster-name>]

# Show monitoring URLs
mup monitoring urls [<cluster-name>]

# Restart monitoring components
mup monitoring restart [<cluster-name>]

# View exporter logs
mup monitoring logs <cluster-name> [--exporter node|mongodb] [--follow]

# Update monitoring configuration (e.g., after topology change)
mup monitoring reload <cluster-name>
```

### Integration with Existing Commands

```bash
# Deploy cluster with monitoring (default)
mup cluster deploy my-cluster topology.yaml --version 7.0

# Deploy without monitoring
mup cluster deploy my-cluster topology.yaml --version 7.0 --no-monitoring

# Start cluster (also starts monitoring if enabled)
mup cluster start my-cluster

# Stop cluster (keeps monitoring running by default)
mup cluster stop my-cluster

# Stop cluster and monitoring
mup cluster stop my-cluster --stop-monitoring

# Display cluster (includes monitoring status)
mup cluster display my-cluster
# Output includes:
# ...
# Monitoring:
#   Status: Running
#   Grafana: http://localhost:3000
#   Dashboards: 5 available

# Destroy cluster (also removes monitoring)
mup cluster destroy my-cluster
```

---

## Binary and Image Management

### Docker Images (Victoria Metrics & Grafana)

Victoria Metrics and Grafana use Docker images rather than native binaries:

```go
// pkg/monitoring/docker/images.go
package docker

const (
	VictoriaMetricsImage = "victoriametrics/victoria-metrics"
	VictoriaMetricsTag   = "latest"  // Or pin to specific version
	GrafanaImage         = "grafana/grafana"
	GrafanaTag           = "latest"  // Or pin to specific version
)

// PullImage pulls a Docker image if not already present
func PullImage(image, tag string) error {
	// docker pull image:tag
}

// ImageExists checks if an image is present locally
func ImageExists(image, tag string) (bool, error) {
	// docker images -q image:tag
}
```

**Image Management:**
- Images pulled on first `mup monitoring init`
- Can be updated with `mup monitoring upgrade`
- No binary download/extraction needed
- Docker handles caching and version management

### Exporter Binaries

Node and MongoDB exporters are downloaded as native binaries:

```go
// pkg/monitoring/binary/versions.go
package binary

const (
	NodeExporterVersion    = "1.7.0"
	MongoDBExporterVersion = "0.40.0"
)

var DownloadURLs = map[string]string{
	"node_exporter":    "https://github.com/prometheus/node_exporter/releases/download/v%s/node_exporter-%s.%s-%s.tar.gz",
	"mongodb_exporter": "https://github.com/percona/mongodb_exporter/releases/download/v%s/mongodb_exporter-%s.%s-%s.tar.gz",
}

func GetBinaryURL(component, version, os, arch string) string {
	template := DownloadURLs[component]
	// Format URL based on component
	// ...
}
```

### Binary Cache Structure

```
~/.mup/monitoring/exporters/
├── node_exporter/
│   └── versions/
│       ├── 1.7.0/
│       │   └── node_exporter  # Native binary
│       └── 1.8.0/
└── mongodb_exporter/
    └── versions/
        ├── 0.40.0/
        │   └── mongodb_exporter  # Native binary
        └── 0.41.0/
```

---

## Security Considerations

### Authentication

1. **MongoDB Exporter Authentication**:
   - Support connection via connection string with credentials
   - Use `.mongodb.uri` secret file for credentials (not in meta.yaml)
   - Example: `mongodb://exporter:password@localhost:27017`

2. **Grafana Authentication**:
   - Default admin credentials stored in `~/.mup/monitoring/grafana/.password`
   - Auto-generate secure password on first run
   - Display on first initialization
   - Support custom auth via configuration

3. **Victoria Metrics**:
   - Local-only binding by default (127.0.0.1)
   - Optional HTTP basic auth for remote access
   - TLS support for production deployments

### Network Security

1. **Exporter Ports**:
   - Bind to localhost for local deployments
   - Bind to specific interface for remote deployments
   - Firewall rules management (future feature)

2. **Victoria Metrics Access**:
   - Default: localhost-only
   - Remote access: require authentication
   - Optional: TLS certificate configuration

---

## Resource Management

### Resource Limits

```yaml
# topology.yaml
global:
  monitoring:
    resource_limits:
      victoria_metrics:
        memory: "2GB"
        storage: "50GB"
        retention: "30d"

      grafana:
        memory: "512MB"

      node_exporter:
        memory: "64MB"
        cpu_quota: "10%"

      mongodb_exporter:
        memory: "128MB"
        cpu_quota: "10%"
```

### Systemd Integration (Remote Deployments)

```ini
# /etc/systemd/system/mongodb_exporter-27017.service
[Unit]
Description=MongoDB Exporter for port 27017
After=network.target

[Service]
Type=simple
User=mongodb
ExecStart=/opt/mup/monitoring/mongodb_exporter \
  --mongodb.uri=mongodb://localhost:27017 \
  --web.listen-address=:9216 \
  --collect.diagnosticdata \
  --collect.replicasetstatus
Restart=always
RestartSec=10

# Resource limits
MemoryLimit=128M
CPUQuota=10%

[Install]
WantedBy=multi-user.target
```

---

## Enhanced Health Display

### Current `mup cluster display` Limitations

The current `mup cluster display` command shows basic cluster information from metadata, but doesn't actively query cluster health. This limits its usefulness for operational monitoring.

### Proposed Enhanced Display

```bash
mup cluster display my-cluster
```

**Output:**

```
Cluster: my-cluster
Version: 7.0.5
Deployed: 2025-01-15 10:30:00

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
TOPOLOGY
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Type: Replica Set (rs0)

NODES                                                    HEALTH
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
localhost:27017  PRIMARY    ✓  Up 2h 34m  (PID: 12345)
localhost:27018  SECONDARY  ✓  Up 2h 34m  (PID: 12346)
localhost:27019  SECONDARY  ✓  Up 2h 34m  (PID: 12347)

REPLICATION STATUS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Primary:          localhost:27017
Replication Lag:  0s (all secondaries in sync)
Oplog Window:     24h

CONNECTIONS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Active:    5
Available: 51195
Total:     51200

OPERATIONS (last 1m)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Queries:   1234/s
Inserts:   456/s
Updates:   789/s
Deletes:   12/s

STORAGE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Data Size:  2.4 GB
Index Size: 512 MB
Total:      2.9 GB

MONITORING
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Status:   ✓ Running
Grafana:  http://localhost:3000
Victoria: http://localhost:8428

Exporters:
  node_exporter (localhost:9100)    ✓ Running
  mongodb_exporter (localhost:9216) ✓ Running
  mongodb_exporter (localhost:9217) ✓ Running
  mongodb_exporter (localhost:9218) ✓ Running

CONNECTION STRING
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
mongodb://localhost:27017,localhost:27018,localhost:27019/?replicaSet=rs0

Use 'mup cluster connect my-cluster' to connect with mongosh
Use 'mup monitoring dashboard my-cluster' to view metrics
```

### Implementation Requirements

**Active Health Queries:**
1. Connect to each MongoDB node
2. Execute health commands:
   - `db.serverStatus()` - server metrics
   - `rs.status()` - replica set status
   - `db.adminCommand({replSetGetStatus: 1})` - replication details
   - `db.serverStatus().connections` - connection stats
   - `db.serverStatus().opcounters` - operation stats

3. Check process health:
   - Query supervisord for process status
   - Verify ports are listening
   - Check exporter endpoints (HTTP GET /metrics)

4. Calculate derived metrics:
   - Replication lag (difference between primary and secondary optime)
   - Oplog window (oplog size / write rate)
   - Resource utilization from exporters

**Expected Ports Display:**

The display should show all expected ports for the cluster:

```
PORTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
MongoDB:
  localhost:27017  (mongod - PRIMARY)
  localhost:27018  (mongod - SECONDARY)
  localhost:27019  (mongod - SECONDARY)

Monitoring:
  localhost:8428   (victoria-metrics)
  localhost:3000   (grafana)
  localhost:9100   (node_exporter)
  localhost:9216   (mongodb_exporter for :27017)
  localhost:9217   (mongodb_exporter for :27018)
  localhost:9218   (mongodb_exporter for :27019)

Supervisor APIs:
  localhost:9001   (cluster supervisor)
  localhost:9002   (monitoring supervisor)
```

### Implementation Package

```go
// pkg/cluster/health/health.go
package health

import (
	"context"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/topology"
	"go.mongodb.org/mongo-driver/mongo"
)

type HealthChecker struct {
	meta     *meta.ClusterMeta
	topology *topology.Topology
}

type ClusterHealth struct {
	Nodes           []NodeHealth
	Replication     ReplicationHealth
	Connections     ConnectionStats
	Operations      OperationStats
	Storage         StorageStats
	Monitoring      MonitoringHealth
	Ports           PortMapping
}

type NodeHealth struct {
	Host      string
	Port      int
	Role      string  // PRIMARY, SECONDARY, ARBITER
	State     string  // UP, DOWN, UNKNOWN
	Uptime    int64   // seconds
	PID       int
	Reachable bool
}

type ReplicationHealth struct {
	Primary         string
	MaxLag          int64  // seconds
	OplogWindow     int64  // hours
	InSync          bool
}

type ConnectionStats struct {
	Current   int
	Available int
	Total     int
}

type OperationStats struct {
	QueriesPerSec int64
	InsertsPerSec int64
	UpdatesPerSec int64
	DeletesPerSec int64
}

type StorageStats struct {
	DataSize  int64
	IndexSize int64
	Total     int64
}

type MonitoringHealth struct {
	Enabled          bool
	GrafanaURL       string
	VictoriaMetricsURL string
	Exporters        []ExporterHealth
}

type ExporterHealth struct {
	Type      string  // node_exporter, mongodb_exporter
	Endpoint  string
	Reachable bool
}

type PortMapping struct {
	MongoDB      []PortInfo
	Monitoring   []PortInfo
	Supervisor   []PortInfo
}

type PortInfo struct {
	Host        string
	Port        int
	Service     string
	Description string
}

func (h *HealthChecker) Check(ctx context.Context) (*ClusterHealth, error) {
	// 1. Query each MongoDB node
	// 2. Get replica set status
	// 3. Calculate replication lag
	// 4. Get connection stats
	// 5. Get operation stats
	// 6. Get storage stats
	// 7. Check exporter health
	// 8. Build port mapping
}
```

---

## Implementation Phases

### Phase 1: Foundation (Week 1-2)
- [ ] Create `pkg/monitoring` package structure
- [ ] Implement Docker image management (pull/check)
- [ ] Integrate with pkg/supervisor for Docker container management
- [ ] Generate supervisord configs for monitoring-victoria-metrics and monitoring-grafana
- [ ] Implement Victoria Metrics container lifecycle via supervisord
- [ ] Implement Grafana container lifecycle via supervisord
- [ ] Basic scrape config generation
- [ ] Unit tests for core functionality
- [ ] Verify Docker is installed and accessible

**Deliverable**: Can start/stop Victoria Metrics and Grafana containers via supervisord

### Phase 2: Exporter Integration (Week 2-3)
- [ ] Implement node_exporter management
- [ ] Implement mongodb_exporter management
- [ ] Port allocation for multiple exporters
- [ ] Exporter lifecycle integration with cluster deployment
- [ ] Update meta.yaml schema for monitoring state
- [ ] Integration tests with local executor

**Deliverable**: Exporters automatically deployed with clusters

### Phase 3: Dashboard Provisioning (Week 3-4)
- [ ] Embed dashboard JSON files
- [ ] Implement Grafana provisioning
- [ ] Create 5 pre-built dashboards:
  - MongoDB Overview
  - Replication
  - Sharding
  - WiredTiger
  - System Overview
- [ ] Dashboard auto-loading on Grafana startup
- [ ] Test dashboards with sample data

**Deliverable**: Working dashboards on cluster deployment

### Phase 4: CLI Integration (Week 4)
- [ ] Add `mup monitoring` command group
- [ ] Integrate with `mup cluster deploy/start/stop/destroy`
- [ ] Add `--no-monitoring` flag support
- [ ] Status display in `mup cluster display`
- [ ] Monitoring URL display
- [ ] Log viewing commands

**Deliverable**: Full CLI integration

### Phase 5: Remote Deployment (Week 5-6)
- [ ] SSH executor integration for exporters
- [ ] Systemd service template generation
- [ ] Remote exporter deployment
- [ ] Remote scrape config updates
- [ ] Firewall rules management (optional)
- [ ] Multi-host monitoring coordination

**Deliverable**: Monitoring works for remote clusters

### Phase 6: Enhanced Health Display (Week 6)
- [ ] Create `pkg/cluster/health` package
- [ ] Implement active MongoDB health queries (serverStatus, replSetGetStatus)
- [ ] Implement replication lag calculation
- [ ] Implement connection stats display
- [ ] Implement operation stats display (ops/sec)
- [ ] Implement storage stats display
- [ ] Implement exporter health checks (HTTP /metrics endpoints)
- [ ] Build port mapping (MongoDB, monitoring, supervisor)
- [ ] Update `mup cluster display` to use health checker
- [ ] Add `--watch` flag for continuous monitoring
- [ ] Format output with clear sections and visual indicators

**Deliverable**: `mup cluster display` actively queries and displays comprehensive cluster health

### Phase 7: Polish & Documentation (Week 7)
- [ ] Error handling and recovery
- [ ] Comprehensive logging
- [ ] User documentation
- [ ] Troubleshooting guide (including Docker issues)
- [ ] Performance tuning guide
- [ ] Update DESIGN.md and README.md
- [ ] Document Docker requirements

**Deliverable**: Production-ready monitoring with comprehensive documentation

---

## Testing Strategy

### Unit Tests
- Binary download and version management
- Scrape config generation
- Port allocation logic
- Process lifecycle management

### Integration Tests
- Local deployment with monitoring
- Exporter connectivity
- Victoria Metrics data ingestion
- Grafana dashboard loading
- CLI command execution

### End-to-End Tests
1. Deploy local 3-node replica set
2. Verify all exporters running
3. Verify metrics collection in Victoria Metrics
4. Verify dashboards load in Grafana
5. Stop cluster, verify monitoring still accessible
6. Destroy cluster, verify monitoring cleanup

---

## Future Enhancements

### Phase 7+: Advanced Features
- [ ] Alert rule provisioning
- [ ] Alert notifications (Slack, email, PagerDuty)
- [ ] Long-term metrics retention and downsampling
- [ ] Multi-cluster dashboards (compare clusters)
- [ ] Metrics-based auto-scaling recommendations
- [ ] Cost analysis dashboards
- [ ] Custom dashboard templates
- [ ] Metrics export (CSV, JSON)
- [ ] Integration with external monitoring systems
- [ ] Performance anomaly detection
- [ ] Capacity planning dashboards

---

## Example Workflows

### Local Playground with Monitoring

```bash
# Start playground (monitoring enabled by default)
mup playground start --version 7.0

# Check monitoring status
mup monitoring status

# Open Grafana
mup monitoring dashboard
# Opens: http://localhost:3000
# Login: admin / <generated-password>

# View MongoDB Overview dashboard
# Navigate to: MongoDB -> MongoDB Overview

# Stop playground (keeps monitoring running)
mup playground stop

# Destroy playground (removes monitoring)
mup playground destroy
```

### Production Cluster with Monitoring

```bash
# Create topology with monitoring
cat > production.yaml <<EOF
global:
  user: mongodb
  deploy_dir: /opt/mongodb
  monitoring:
    enabled: true
    retention_period: 90d

mongod_servers:
  - host: prod1.example.com
    port: 27017
    replica_set: rs0
  - host: prod2.example.com
    port: 27017
    replica_set: rs0
  - host: prod3.example.com
    port: 27017
    replica_set: rs0
EOF

# Deploy cluster
mup cluster deploy prod-cluster production.yaml --version 7.0

# Monitoring deployed automatically:
# ✓ Victoria Metrics running at http://localhost:8428
# ✓ Grafana running at http://localhost:3000
# ✓ 3 node_exporters deployed (one per host)
# ✓ 3 mongodb_exporters deployed (one per mongod)
# ✓ 5 dashboards loaded

# Check monitoring
mup monitoring status prod-cluster

# View dashboards
mup monitoring dashboard prod-cluster

# View replication lag
# Navigate to: MongoDB -> Replication Dashboard
# Check: Replication Lag panel

# Scale out (adds monitoring automatically)
# Edit topology: add new node
mup cluster scale-out prod-cluster new-node.yaml
# Monitoring automatically updated with new targets

# Stop cluster
mup cluster stop prod-cluster --stop-monitoring

# Restart with monitoring
mup cluster start prod-cluster
```

---

## Comparison with Other Solutions

| Feature | Mup Monitoring | PMM (Percona) | MongoDB Ops Manager |
|---------|----------------|---------------|---------------------|
| **Architecture** | Local Victoria Metrics | PMM Server + Agents | Cloud/On-Prem Server |
| **Deployment** | Integrated with Mup | Separate installation | Separate installation |
| **Cost** | Free (OSS) | Free (OSS) | Commercial |
| **Footprint** | Lightweight | Medium | Heavy |
| **Setup Time** | Automatic | Manual | Manual |
| **Dashboards** | 5 pre-built | 80+ dashboards | Many dashboards |
| **Customization** | Full Grafana access | Full Grafana access | Limited |
| **Backup Integration** | No | Yes | Yes |
| **Query Analyzer** | No | Yes | Yes |
| **Best For** | Mup users | Production MongoDB | Enterprise MongoDB |

**Mup Monitoring Advantages**:
- Zero-configuration setup
- Integrated lifecycle management
- Lightweight resource usage
- Perfect for Mup-managed clusters
- Simple and focused

**When to use PMM instead**:
- Need query analyzer
- Need backup management
- Running non-Mup MongoDB clusters
- Want extensive dashboard library

---

## Configuration Reference

### Complete Monitoring Configuration Example

```yaml
# topology.yaml - Complete monitoring configuration
global:
  user: mongodb
  deploy_dir: /opt/mongodb

  monitoring:
    # Global enable/disable
    enabled: true

    # Victoria Metrics configuration
    victoria_metrics:
      url: "http://localhost:8428"
      retention_period: 30d
      storage_path: ~/.mup/monitoring/victoria-metrics/data
      memory_limit: 2GB
      port: 8428
      bind_address: "127.0.0.1"
      extra_args:
        - "--search.maxQueryDuration=60s"
        - "--search.maxPointsPerTimeseries=10000"

    # Grafana configuration
    grafana:
      enabled: true
      port: 3000
      bind_address: "127.0.0.1"
      admin_user: admin
      admin_password_file: ~/.mup/monitoring/grafana/.password
      data_dir: ~/.mup/monitoring/grafana/data

      # Auto-load dashboards
      dashboards:
        - mongodb-overview
        - mongodb-replication
        - mongodb-sharding
        - mongodb-wiredtiger
        - system-overview

      # Dashboard refresh intervals
      dashboard_refresh: "30s"

      # Theme
      theme: dark  # or "light"

      # Anonymous access (for read-only dashboards)
      anonymous_access:
        enabled: false
        role: Viewer

    # Scrape configuration
    scrape_interval: 15s
    scrape_timeout: 10s

    # Exporter configuration
    exporters:
      node_exporter:
        enabled: true
        version: "1.7.0"
        port: 9100
        collectors:
          - cpu
          - diskstats
          - filesystem
          - loadavg
          - meminfo
          - netdev
          - stat
          - time
          - vmstat
        extra_args:
          - "--collector.filesystem.mount-points-exclude=^/(sys|proc|dev|run)($|/)"

      mongodb_exporter:
        enabled: true
        version: "0.40.0"
        port_base: 9216

        # Authentication
        auth:
          # Read from file (not in YAML for security)
          uri_file: ~/.mup/monitoring/.mongodb-exporter-uri

        # Collection options
        collect_all: false  # Set true for all metrics (higher overhead)
        collectors:
          - diagnosticdata
          - replicasetstatus
          - dbstats
          - topmetrics
          - indexstats
          - collstats

        # Specific collections to collect stats for
        collecting_limit: 200

        extra_args:
          - "--compatible-mode"
          - "--discovering-mode"

    # Alerting (future)
    alerting:
      enabled: false
      rules_dir: ~/.mup/monitoring/alert-rules
      notification_channels:
        - type: slack
          webhook_url_file: ~/.mup/monitoring/.slack-webhook
        - type: email
          smtp_host: smtp.gmail.com
          smtp_port: 587
          from: alerts@example.com
          to: team@example.com

mongod_servers:
  - host: localhost
    port: 27017
    replica_set: rs0

    # Per-node monitoring overrides
    monitoring:
      exporters:
        mongodb_exporter:
          collect_all: true
          collectors:
            - collstats  # Additional collector for this node
```

---

## Troubleshooting Guide

### Common Issues

**1. Exporters not starting**
```bash
# Check exporter logs
mup monitoring logs my-cluster --exporter mongodb

# Common causes:
# - Port already in use
# - MongoDB not accessible
# - Authentication failure

# Solutions:
# Check port availability
lsof -i :9216

# Test MongoDB connection
mongosh mongodb://localhost:27017

# Verify exporter binary
~/.mup/monitoring/exporters/mongodb_exporter/current/mongodb_exporter --version
```

**2. No metrics in Victoria Metrics**
```bash
# Check Victoria Metrics is running
mup monitoring status

# Check scrape targets
curl http://localhost:8428/api/v1/targets

# Check if exporters are accessible
curl http://localhost:9100/metrics  # node_exporter
curl http://localhost:9216/metrics  # mongodb_exporter

# Reload scrape config
mup monitoring reload my-cluster
```

**3. Grafana dashboards not loading**
```bash
# Check Grafana logs
tail -f ~/.mup/monitoring/logs/grafana.log

# Check dashboard provisioning
ls -la ~/.mup/monitoring/grafana/dashboards/

# Check datasource configuration
cat ~/.mup/monitoring/grafana/provisioning/datasources/victoria-metrics.yaml

# Restart Grafana
mup monitoring restart my-cluster
```

**4. High resource usage**
```bash
# Reduce scrape frequency
# Edit topology.yaml:
monitoring:
  scrape_interval: 30s  # Increase from 15s

# Reduce mongodb_exporter metrics
monitoring:
  exporters:
    mongodb_exporter:
      collect_all: false
      collectors:
        - diagnosticdata  # Only essential metrics

# Reduce Victoria Metrics retention
monitoring:
  victoria_metrics:
    retention_period: 7d  # Reduce from 30d

# Apply changes
mup monitoring reload my-cluster
```

---

## Conclusion

This monitoring design provides:
- **Zero-configuration**: Works out-of-the-box with sensible defaults
- **Integrated lifecycle**: Monitoring managed alongside MongoDB clusters
- **Comprehensive coverage**: OS and MongoDB metrics with pre-built dashboards
- **Local & Remote**: Architecture scales from playground to production
- **Lightweight**: Minimal overhead using Victoria Metrics
- **Extensible**: Easy to add custom dashboards and alerts

The implementation follows Mup's design principles:
- Template-based configuration
- Executor abstraction for local/remote deployment
- State management in meta.yaml
- Clean CLI integration
- Idempotent operations

This positions Mup as a complete cluster management solution with built-in observability.
