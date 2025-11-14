# Monitoring Guide

This guide explains how to use Mup's integrated monitoring system for MongoDB clusters.

## Overview

Mup includes comprehensive monitoring capabilities powered by:
- **Victoria Metrics** - High-performance time-series database
- **Grafana** - Visualization platform with pre-configured dashboards
- **node_exporter** - System and hardware metrics
- **mongodb_exporter** - MongoDB-specific metrics

## Enabling Monitoring

### Default Behavior (Monitoring Enabled)

**Monitoring is enabled by default** for all cluster deployments:

```bash
# Monitoring will be automatically deployed
mup cluster deploy my-cluster examples/replica-set.yaml
```

During deployment, you'll see Phase 4.5 which deploys monitoring:

```
Phase 4.5: Deploy Monitoring
============================
Initializing monitoring infrastructure...
  ✓ Victoria Metrics and Grafana images pulled
  ✓ Supervisord configured for monitoring
  ✓ Grafana provisioning configured

Starting monitoring infrastructure...
  ✓ Victoria Metrics started
  ✓ Grafana started

Deploying metric exporters...
  ✓ 1 node_exporter(s) deployed
  ✓ 3 mongodb_exporter(s) deployed
  ✓ Scrape configuration generated

Monitoring URLs:
----------------------------------------
  Grafana:          http://localhost:3000
  Grafana User:     admin
  Grafana Password: <generated-password>
  Victoria Metrics: http://localhost:8428

  Dashboards:
    - MongoDB Overview
    - MongoDB WiredTiger Storage Engine
    - MongoDB Sharding
    - MongoDB Replication
    - System Overview

✓ Phase 4.5 complete: Monitoring deployed
```

### Disabling Monitoring

To deploy without monitoring:

```bash
mup cluster deploy my-cluster examples/replica-set.yaml --no-monitoring
```

## Accessing Monitoring

### Quick Access

After deployment, you'll see monitoring URLs in the output. You can also access them via:

```bash
# View all monitoring URLs and credentials
mup monitoring urls my-cluster

# Open Grafana in your browser
mup monitoring dashboard my-cluster

# Check monitoring component status
mup monitoring status my-cluster
```

### Default URLs

- **Grafana**: http://localhost:3000
  - User: `admin`
  - Password: Displayed during deployment or via `mup monitoring urls`

- **Victoria Metrics**: http://localhost:8428
  - Query API: http://localhost:8428/api/v1/query

### Dashboards

Five pre-configured dashboards are automatically loaded:

1. **MongoDB Overview** - Operations per second, connections, memory, network
2. **MongoDB WiredTiger** - Cache usage, eviction, transactions, tickets, checkpoints
3. **MongoDB Sharding** - Operations per shard, chunk distribution, balancer status
4. **MongoDB Replication** - Replication lag, oplog, elections, heartbeats
5. **System Overview** - CPU, disk latency, memory, network, filesystem

## Pre-Configured Dashboards

### 1. MongoDB Overview

Focus: High-level cluster performance

**Key Panels:**
- Operations Per Second by Instance (with breakdown: query/insert/update/delete)
- Operation Types Distribution (stacked area)
- Connections (current vs available)
- Query Executor Metrics (scanned documents)
- Document Operations
- Network Traffic (RX/TX)
- Memory Usage (resident/virtual)
- Current Queue Sizes (with thresholds)
- Active Clients
- Uptime and Version Info

**Use Case:** Daily cluster health monitoring, capacity planning

### 2. MongoDB WiredTiger Storage Engine

Focus: Storage engine internals and performance tuning

**Key Panels:**
- **Cache Usage** - Total, dirty, max cache bytes
- **Cache Utilization %** - Gauge with color thresholds (70%/85%/95%)
- **Dirty Cache %** - Separate dirty cache tracking
- **Cache Pages Read/Written** - I/O at page level
- **Cache Eviction Rate** - Critical for performance (alert at 100 evictions/sec)
- **Transactions** - Committed, rolled back, transactions/sec
- **Checkpoints** - Frequency and duration
- **Concurrent Transactions Tickets** - Read/write ticket availability
- **Ticket Utilization %** - Visual tracking (thresholds at 70%/90%)
- **Block Manager Operations** - Storage I/O
- **Log Operations** - Journal activity

**Use Case:** Performance tuning, cache sizing, identifying storage bottlenecks

### 3. MongoDB Sharding

Focus: Shard balance and router performance

**Key Panels:**
- **Operations Per Second by Shard** - Aggregate ops/sec per shard
- **Operation Types by Shard** - Query/insert/update/delete per shard
- **Write Operations Distribution** - Bar gauge showing shard balance
- **Mongos Operations** - Router-level operations
- **Chunk Distribution per Shard** - Visual chunk imbalance detection
- **Chunk Migrations** - moveChunk operation rate
- **Shard Data Size** - Storage per shard
- **Shard Document Count** - Document distribution
- **Balancer Status** - Real-time balancer state
- **Query Targeting Efficiency** - Scanned vs returned ratio

**Use Case:** Shard balancing, router performance, identifying hot shards

### 4. MongoDB Replication

Focus: Replica set health and replication performance

**Key Panels:**
- **Replication Lag by Instance** - With alerts (thresholds: 10s/30s/60s)
- **Replica Set Member State** - PRIMARY/SECONDARY/ARBITER visualization
- **Replica Set Health** - Up/Down status
- **Oplog Window** - Time window available (alert if < 1 hour)
- **Oplog Size** - Current utilization
- **Oplog GB/hour** - Growth rate
- **Replication Network Operations** - Replication traffic
- **Elections and Failovers** - Election tracking
- **Heartbeat Latency** - Inter-member ping times
- **Vote Status** - Voting member identification

**Use Case:** Replication health, failover readiness, oplog sizing

### 5. System Overview

Focus: Hardware and OS-level metrics with microspike detection

**Key Panels:**
- **Cluster-Wide CPU Usage** - Aggregated + per-host breakdown
- **Disk I/O Latency (Microspikes Detection)** - 1-minute rate for spike detection
  - Read/write latency per device
  - Alert at >50ms
  - Point visualization for anomalies
- **Disk Latency 99th Percentile** - p99/p95 tail latency
- **Disk I/O Operations** - IOPS per device
- **Disk Throughput** - Read/write bytes/sec
- **Disk Queue Depth** - I/O queue monitoring
- **Memory Usage %** - With thresholds
- **Network Traffic** - RX/TX per interface
- **Filesystem Usage** - Disk space per mount
- **Context Switches** - CPU scheduling pressure
- **Open File Descriptors** - FD exhaustion prevention

**Use Case:** Hardware capacity planning, disk latency troubleshooting, CPU analysis

## Monitoring Commands

### Status Check

```bash
mup monitoring status my-cluster
```

Output example:
```
======================================================================
Monitoring Status: my-cluster
======================================================================

Victoria Metrics
----------------------------------------------------------------------
  Status: ✓ running (PID: 12345, uptime: 2h 15m)
  URL:    http://localhost:8428

Grafana
----------------------------------------------------------------------
  Status: ✓ running (PID: 12346, uptime: 2h 15m)
  URL:    http://localhost:3000

Metric Exporters
----------------------------------------------------------------------
  node_exporter        localhost:9100  ✓ running (PID: 12347)
  mongodb_exporter     localhost:9216  ✓ running (PID: 12348)
  mongodb_exporter     localhost:9217  ✓ running (PID: 12349)
======================================================================
```

### View URLs

```bash
mup monitoring urls my-cluster
```

### Open Dashboards

```bash
mup monitoring dashboard my-cluster
```

This will:
1. Display Grafana credentials
2. Open Grafana in your default browser
3. Show available dashboards

### View Logs

```bash
# View all exporter logs
mup monitoring logs my-cluster

# View specific component logs
mup monitoring logs my-cluster node-exporter
mup monitoring logs my-cluster mongodb-exporter
mup monitoring logs my-cluster victoria-metrics
mup monitoring logs my-cluster grafana

# Follow logs in real-time
mup monitoring logs my-cluster --follow

# Show last 50 lines
mup monitoring logs my-cluster --lines 50
```

## Architecture

### Components

```
┌─────────────────────────────────────────────────────────────┐
│                    Monitoring Stack                          │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────────┐    ┌──────────────┐                      │
│  │   Grafana    │───▶│   Victoria   │                      │
│  │  (Docker)    │    │   Metrics    │                      │
│  │  Port: 3000  │    │   (Docker)   │                      │
│  └──────────────┘    │  Port: 8428  │                      │
│                      └──────────────┘                      │
│                             ▲                               │
│                             │ (scrapes)                     │
│         ┌───────────────────┴───────────────────┐          │
│         │                                        │          │
│  ┌─────────────┐                        ┌──────────────┐   │
│  │node_exporter│                        │mongodb       │   │
│  │(native bin) │                        │_exporter     │   │
│  │Port: 9100   │                        │(native bin)  │   │
│  └─────────────┘                        │Ports: 9216+  │   │
│         │                                └──────────────┘   │
│         │                                        │          │
└─────────┼────────────────────────────────────────┼──────────┘
          │                                        │
          ▼                                        ▼
    ┌──────────┐                          ┌──────────────┐
    │  System  │                          │   MongoDB    │
    │  Metrics │                          │   Instances  │
    └──────────┘                          └──────────────┘
```

### Process Management

All monitoring components are managed by supervisord:
- `monitoring-victoria-metrics` - Victoria Metrics container
- `monitoring-grafana` - Grafana container
- `node_exporter-9100` - System metrics exporter
- `mongodb_exporter-9216` - MongoDB metrics (one per instance)
- `mongodb_exporter-9217` - MongoDB metrics (additional instances)

### Data Flow

1. **Exporters** collect metrics:
   - `node_exporter` scrapes OS/hardware metrics
   - `mongodb_exporter` connects to MongoDB and collects metrics

2. **Victoria Metrics** scrapes exporters every 15 seconds:
   - Stores time-series data
   - Provides Prometheus-compatible API

3. **Grafana** queries Victoria Metrics:
   - Renders dashboards
   - Supports alerting (future)

### Storage Locations

```
~/.mup/
└── storage/
    ├── packages/                      # Cached MongoDB binaries
    └── clusters/
        └── <cluster-name>/
            ├── meta.yaml              # Cluster metadata
            ├── supervisor.ini         # Main supervisord config (MongoDB + monitoring)
            ├── monitoring-supervisor.ini  # Monitoring-specific programs
            └── monitoring/
                ├── victoria-metrics/  # VM data
                ├── grafana/           # Grafana data
                ├── exporters/         # Exporter binaries cache
                └── logs/              # Component logs
                    ├── victoria-metrics.log
                    ├── grafana.log
                    └── exporters/
                        ├── node_exporter-9100.log
                        └── mongodb_exporter-9216.log
```

**Note**: Monitoring infrastructure is managed by the cluster's supervisord instance. All monitoring components live within the cluster directory for proper lifecycle management.

## Troubleshooting

### Check Component Status

```bash
# Overall status
mup monitoring status my-cluster

# Check logs for errors
mup monitoring logs my-cluster victoria-metrics --lines 50
mup monitoring logs my-cluster grafana --lines 50
mup monitoring logs my-cluster node-exporter
mup monitoring logs my-cluster mongodb-exporter
```

### Victoria Metrics Not Starting

```bash
# Check if port 8428 is already in use
lsof -i :8428

# Check Docker
docker ps | grep victoria-metrics
docker logs mup-victoria-metrics
```

### Grafana Not Starting

```bash
# Check if port 3000 is already in use
lsof -i :3000

# Check Docker
docker ps | grep grafana
docker logs mup-grafana
```

### Exporters Not Running

```bash
# Check logs
mup monitoring logs my-cluster mongodb-exporter

# Check if MongoDB ports are accessible
telnet localhost 27017

# Verify exporter ports
lsof -i :9100  # node_exporter
lsof -i :9216  # mongodb_exporter
```

### Missing Dashboards

Dashboards are automatically provisioned. If missing:

```bash
# Check Grafana provisioning directory (replace <cluster-name> with your cluster)
ls -la ~/.mup/storage/clusters/<cluster-name>/monitoring/grafana/provisioning/dashboards/

# Check dashboard files
ls -la ~/.mup/storage/clusters/<cluster-name>/monitoring/grafana/dashboards/
```

Expected files:
- mongodb-overview.json
- mongodb-wiredtiger.json
- mongodb-sharding.json
- mongodb-replication.json
- system-overview.json

### High Resource Usage

Victoria Metrics and Grafana run as Docker containers. To limit resources:

Edit `~/.mup/storage/clusters/<cluster-name>/monitoring-supervisor.ini` and add resource limits:

```ini
[program:monitoring-victoria-metrics]
command = docker run --rm \
  --memory="512m" \
  --cpus="0.5" \
  ...
```

## Best Practices

### Retention

Default retention is 30 days. To change:

1. Stop monitoring: `mup cluster stop my-cluster`
2. Edit `~/.mup/storage/clusters/<cluster-name>/monitoring-supervisor.ini`
3. Change `-retentionPeriod=30d` to desired value (e.g., `90d`)
4. Start monitoring: `mup cluster start my-cluster`

### Scrape Interval

Default is 15 seconds. For high-frequency monitoring:

1. Edit Victoria Metrics scrape config: `~/.mup/storage/clusters/<cluster-name>/monitoring/victoria-metrics/promscrape.yaml`
2. Change `scrape_interval: 15s` to desired value
3. Restart Victoria Metrics: `mup cluster stop <cluster-name> && mup cluster start <cluster-name>`

### Alerting

Dashboards include thresholds but not active alerting. For production:

1. Configure Grafana alerting
2. Add alert rules to dashboards
3. Configure notification channels (email, Slack, PagerDuty)

### Grafana Customization

All dashboards allow UI updates (`allowUiUpdates: true`). Changes are persisted in Grafana's database.

To export customized dashboards:
1. Open dashboard in Grafana
2. Click share icon → Export
3. Save JSON to version control

## Metrics Reference

### MongoDB Exporter Metrics

- `mongodb_up` - Instance reachability
- `mongodb_op_counters_total` - Operation counters
- `mongodb_ss_connections` - Connection stats
- `mongodb_ss_mem_resident` - Memory usage
- `mongodb_ss_wt_cache_*` - WiredTiger cache metrics
- `mongodb_mongod_replset_*` - Replication metrics
- `mongodb_mongos_sharding_*` - Sharding metrics

### Node Exporter Metrics

- `node_cpu_seconds_total` - CPU time
- `node_memory_*` - Memory stats
- `node_disk_*` - Disk I/O metrics
- `node_network_*` - Network stats
- `node_filesystem_*` - Filesystem usage

## Next Steps

- Review [MongoDB Overview Dashboard](#1-mongodb-overview) for daily monitoring
- Set up [WiredTiger Dashboard](#2-mongodb-wiredtiger-storage-engine) for performance tuning
- Configure Grafana alerting for production environments
- Export and customize dashboards for your needs
