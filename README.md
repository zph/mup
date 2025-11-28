# Mup - MongoDB Cluster Management Tool

Mup (MongoDB Utility Platform) is a cluster management tool for MongoDB, inspired by TiDB's TiUP. It simplifies deployment, configuration, and lifecycle management of MongoDB clusters (standalone, replica sets, and sharded clusters) across distributed infrastructure.

## Features

- **Playground Mode**: Quick local MongoDB cluster for development and testing
- **Cluster Import**: Import existing MongoDB clusters with zero data movement
- **Rolling Operations**: Zero-downtime migrations with replica set awareness
- **SSH-based Remote Management**: Agentless architecture using SSH
- **Version Flexibility**: Support multiple MongoDB versions (3.6 through 8.0)
- **State Management**: Centralized state tracking in YAML metadata
- **Auto-Detection**: Discover and import systemd-managed MongoDB clusters

## Installation

### From Source

```bash
git clone https://github.com/zph/mup.git
cd mup
go build -o mup ./cmd/mup
sudo mv mup /usr/local/bin/
```

### Binary Release

Download the latest binary from the [releases page](https://github.com/zph/mup/releases).

## Quick Start - Playground Mode

The fastest way to get started with Mup is using the playground feature, which creates a local MongoDB cluster for development and testing.

### Start a Playground Cluster

```bash
# Start a local MongoDB 7.0 cluster
mup playground start

# Start with a specific version
mup playground start --version 8.0
```

Output:
```
Starting playground cluster (MongoDB 7.0)...

✓ Playground cluster started successfully!

Connection URI: mongodb://localhost:27017
Data directory: /Users/you/.mup/playground/data

To connect: mongosh "mongodb://localhost:27017"
```

### Check Playground Status

```bash
mup playground status
```

Output:
```
Playground Cluster Status
========================
Name:           playground
Status:         running
MongoDB:        7.0
Started:        2025-01-15T10:30:00Z
Connection URI: mongodb://localhost:27017
Data directory: /Users/you/.mup/playground/data
Uptime:         2h15m30s
```

### Connect to Playground

```bash
# Using mup playground connect (recommended - uses the correct mongo/mongosh binary)
mup playground connect

# Or manually (check cluster status for the connection URI)
mongosh "mongodb://localhost:27017"  # For newer MongoDB versions
mongo "mongodb://localhost:27017"    # For older MongoDB versions

# Using mongo driver in your application
mongodb://localhost:27017
```

The `mup playground connect` command automatically reads the cluster info and uses the appropriate MongoDB shell binary (legacy `mongo` for older versions, `mongosh` for newer versions) with the correct connection string.

### Stop Playground

```bash
mup playground stop
```

### Destroy Playground

Remove the playground cluster and all data:

```bash
mup playground destroy
```

## Playground Use Cases

The playground feature is perfect for:

- **Local Development**: Test MongoDB features without affecting production
- **CI/CD Pipelines**: Spin up temporary databases for integration tests
- **Learning**: Experiment with MongoDB sharding and replica sets
- **Prototyping**: Quick database for proof-of-concept projects

## Complete Playground Workflow Example

```bash
# Start a playground cluster
make build
./bin/mup playground start --version 7.0

# Check the status
./bin/mup playground status

# Connect to the cluster
./bin/mup playground connect

# When done, stop the cluster
./bin/mup playground stop

# Or completely remove it
./bin/mup playground destroy
```

## Production Cluster Management

Mup uses a Terraform-inspired **plan/apply workflow** for all cluster operations. This ensures you can preview changes before execution, with automatic checkpoint recovery on failures.

### Deploy a New Cluster

#### Basic Workflow

```bash
# 1. Generate and review a deployment plan
mup cluster deploy my-cluster topology.yaml --version 7.0 --plan-only

# 2. View plan details
mup plan show my-cluster

# 3. Apply the plan (with confirmation prompt)
mup cluster deploy my-cluster topology.yaml --version 7.0

# Or auto-approve to skip confirmation
mup cluster deploy my-cluster topology.yaml --version 7.0 --auto-approve
```

#### What Happens During Deploy

The deploy operation runs through 4 phases:

1. **Prepare** - Download binaries, create directories, validate prerequisites
2. **Deploy** - Generate configs, start MongoDB processes
3. **Initialize** - Initialize replica sets, configure sharding
4. **Finalize** - Verify health, save cluster metadata

Each phase creates a checkpoint for recovery if something fails.

#### Example: Deploy a Replica Set

Create a topology file (`replica-set.yaml`):

```yaml
global:
  user: mongodb
  deploy_dir: ~/.mup/storage/clusters/prod-rs

mongod_servers:
  - host: localhost
    port: 27017
    replica_set: rs0
  - host: localhost
    port: 27018
    replica_set: rs0
  - host: localhost
    port: 27019
    replica_set: rs0
```

Deploy with plan/apply:

```bash
# Generate plan
mup cluster deploy prod-rs topology.yaml --version 7.0 --plan-only

# Output:
# Planning deployment for cluster: prod-rs
# MongoDB version: 7.0.5
# Topology: Replica set (3 nodes)
#
# Running pre-flight checks...
# ✓ Connectivity to all hosts (1/1)
# ✓ Disk space available (50GB required, 500GB available)
# ✓ All ports available (3 ports)
#
# Plan generated: plan-abc123
# Resources:
#   Hosts: 1
#   Processes: 3 mongod
#   Ports: 27017-27019
#   Disk: 10GB
#
# Phases:
#   1. Prepare (2m)
#      • Download MongoDB 7.0.5 binary
#      • Create directories
#      • Upload configuration files
#
#   2. Deploy (1m)
#      • Start 3 mongod processes
#      • Wait for processes ready
#
#   3. Initialize (3m)
#      • Initialize replica set rs0
#
#   4. Finalize (30s)
#      • Verify cluster health
#      • Save cluster metadata
#
# Total operations: 12
# Estimated duration: 6.5 minutes

# Apply the plan
mup cluster deploy prod-rs topology.yaml --version 7.0

# Output:
# Apply plan-abc123 to cluster prod-rs?
# This will:
#   • Create 3 MongoDB processes on localhost
#   • Use ports 27017-27019
#   • Download 120MB of binaries
#
# Do you want to continue? (yes/no): yes
#
# Applying plan...
# Phase 1/4: Prepare
#   ✓ Downloaded MongoDB 7.0.5 (120MB) [1m 23s]
#   ✓ Created directories [2s]
# Checkpoint saved: phase1-complete
#
# Phase 2/4: Deploy
#   ✓ Started 3 mongod processes [8s]
# Checkpoint saved: phase2-complete
#
# Phase 3/4: Initialize
#   ✓ Initialized replica set rs0 [30s]
# Checkpoint saved: phase3-complete
#
# Phase 4/4: Finalize
#   ✓ Verified cluster health [2s]
#
# Deployment completed successfully! [2m 5s]
# Connection: mongosh 'mongodb://localhost:27017'
```

### Plan Management

View and manage deployment plans:

```bash
# List all plans
mup plan list

# Show plan details
mup plan show my-cluster
mup plan show plan-abc123

# Validate a saved plan
mup plan validate plan.json

# Apply a saved plan
mup plan apply my-cluster
```

### Monitoring Progress

Track deployment progress in real-time:

```bash
# Show current state
mup state show my-cluster

# View execution logs
mup state logs my-cluster

# List checkpoints
mup state checkpoints my-cluster
```

### Recovery from Failures

If a deployment fails, resume from the last checkpoint:

```bash
# Fix the issue (e.g., free up a port)

# Resume from last checkpoint
mup plan resume my-cluster

# Output:
# Resuming apply state-xyz789 from checkpoint phase1-complete
#
# Phase 2/4: Deploy
#   ✓ Started mongod processes [5s]
# ...continues from where it left off
```

### Future Operations (Coming Soon)

```bash
# Scale out cluster
mup cluster scale-out prod-rs --node 192.168.1.20

# Upgrade MongoDB version
mup cluster upgrade prod-rs 8.0.0 --rolling

# Reload configuration
mup cluster reload prod-rs --rolling
```

See [docs/specs/PLAN_APPLY_SYSTEM.md](docs/specs/PLAN_APPLY_SYSTEM.md) for complete technical details.

## Import Existing Clusters

Mup can import existing MongoDB clusters (especially systemd-managed remote deployments) into its management structure with minimal downtime. The import process uses symlinks for data directories (zero data movement) and performs rolling restarts with replica set awareness.

### What Import Does

The import command:
1. **Discovers** MongoDB instances (auto-detect or manual specification)
2. **Creates** mup's per-version directory structure
3. **Symlinks** existing data directories (no data movement)
4. **Imports** MongoDB configurations, preserving custom settings
5. **Generates** topology.yaml for declarative cluster management
6. **Migrates** from systemd to supervisord management
7. **Performs** rolling restart (SECONDARY → PRIMARY pattern)

### Prerequisites

- Existing MongoDB cluster (standalone, replica set, or sharded)
- SSH access (for remote imports)
- Systemd-managed services (for auto-detection)
- Sufficient disk space for binaries and logs

### Auto-Detect Mode (Recommended)

Import a local systemd-managed cluster by auto-detecting MongoDB processes:

```bash
# Local cluster auto-detection
mup cluster import my-cluster --auto-detect

# Remote cluster via SSH
mup cluster import prod-rs --auto-detect --ssh-host user@mongodb-server.example.com
```

Output:
```
Phase 1: Discovering MongoDB instances...
  Found 3 MongoDB instance(s), version 7.0.5 (replica set)
  Detected systemd services: mongod-27017, mongod-27018, mongod-27019

Phase 2: Creating directory structure...
  Created: ~/.mup/storage/clusters/my-cluster/v7.0.5/
  Symlinked data directories (no data movement)
  Directory structure created

Phase 3: Importing configurations...
  Imported configs for 3 instances
  Preserved custom settings: security, TLS, setParameter
  Configurations imported

Phase 3.5: Generating topology.yaml...
  Topology file generated

Phase 4: Managing systemd services...
  Rolling restart: SECONDARY → SECONDARY → PRIMARY
  Disabling systemd service: mongod-27017
  Disabling systemd service: mongod-27018
  Stepping down PRIMARY: localhost:27019
  Disabling systemd service: mongod-27019
  Disabled 3 systemd service(s)

✓ Import successful!
  Cluster: my-cluster
  Version: 7.0.5 (mongo)
  Nodes imported: 3
  Services disabled: mongod-27017, mongod-27018, mongod-27019

Your cluster is now managed by mup. Use 'mup cluster status my-cluster' to check status.
```

### Manual Mode

Import with explicit configuration when auto-detection isn't available:

```bash
# Local import with manual specification
mup cluster import my-cluster \
  --config /etc/mongod.conf \
  --data-dir /var/lib/mongodb \
  --port 27017

# Remote import with manual specification
mup cluster import prod-standalone \
  --config /etc/mongod.conf \
  --data-dir /var/lib/mongodb \
  --port 27017 \
  --ssh-host user@mongodb-server.example.com
```

### Dry Run Mode

Preview import actions without making changes:

```bash
mup cluster import my-cluster --auto-detect --dry-run
```

This shows what would be done without actually:
- Creating directories
- Disabling systemd services
- Restarting MongoDB processes

### Import Options

```bash
mup cluster import <cluster-name> [flags]

Flags:
  --auto-detect              Auto-detect MongoDB processes
  --config string            Path to mongod.conf (manual mode)
  --data-dir string          MongoDB data directory (manual mode)
  --port int                 MongoDB port (manual mode)
  --host string              MongoDB host (default: localhost)
  --ssh-host string          Remote host via SSH (user@host)
  --dry-run                  Preview without making changes
  --skip-restart             Import structure only, don't restart processes
  --keep-systemd-files       Don't remove systemd unit files
```

### Rolling Restart Pattern

For replica sets, import uses a SECONDARY-first pattern to minimize downtime:

1. **Identify members**: Query replica set status to find PRIMARY and SECONDARYs
2. **Migrate SECONDARYs**:
   - Stop systemd service for SECONDARY
   - Start under supervisord
   - Verify health and replication lag < 30s
   - Repeat for all SECONDARYs
3. **Step down PRIMARY**: Use `rs.stepDown()` to elect new PRIMARY
4. **Migrate former PRIMARY**: Now a SECONDARY, safe to restart

### Data Safety

Import uses **symlinks** instead of copying data:

```
~/.mup/storage/clusters/my-cluster/
├── data/
│   ├── localhost-27017 -> /var/lib/mongodb-27017  # Symlink to existing data
│   ├── localhost-27018 -> /var/lib/mongodb-27018
│   └── localhost-27019 -> /var/lib/mongodb-27019
└── v7.0.5/
    ├── bin/          # MongoDB binaries
    ├── conf/         # Mup-managed configs
    └── logs/         # Process logs
```

**Benefits**:
- Zero data movement (no disk I/O)
- Original data location preserved
- Instant rollback capability

### Rollback

If import fails, systemd services are automatically re-enabled. For manual rollback:

```bash
# Re-enable systemd services
sudo systemctl enable mongod-27017
sudo systemctl enable mongod-27018
sudo systemctl enable mongod-27019

# Start services
sudo systemctl start mongod-27017
sudo systemctl start mongod-27018
sudo systemctl start mongod-27019
```

### After Import

Once imported, manage your cluster with mup commands:

```bash
# Check cluster status
mup cluster status my-cluster

# View the generated topology
cat ~/.mup/storage/clusters/my-cluster/topology.yaml

# View logs
tail -f ~/.mup/storage/clusters/my-cluster/current/logs/mongod-27017.log

# Upgrade to new version
mup cluster upgrade my-cluster 8.0.0 --rolling

# Stop cluster
mup cluster stop my-cluster

# Start cluster
mup cluster start my-cluster
```

The generated `topology.yaml` provides a declarative description of your cluster, enabling future operations like scale-out, reconfiguration, and disaster recovery.

### Supported Import Sources

- ✅ Systemd-managed MongoDB (auto-detect)
- ✅ Manual configuration specification
- ✅ Local and remote (SSH) clusters
- ✅ Standalone, replica sets, and sharded clusters
- ✅ MongoDB 3.6 through 8.0
- ✅ Percona Server for MongoDB

## Configuration

Mup stores its state and configuration in:

```
~/.mup/
├── playground/
│   ├── cluster-info.json  # Cluster connection details
│   └── state.json         # Playground state
└── storage/
    └── clusters/          # Production cluster metadata (coming soon)
```

Note: The actual cluster data is stored in temporary directories managed by mongo-scaffold.

## Architecture

Mup follows these design principles:

1. **Declarative Configuration**: Define what you want, Mup handles how
2. **Idempotent Operations**: Safe to re-run commands
3. **State Management**: Central source of truth for cluster state
4. **Zero Downtime**: Rolling operations for production changes

See [DESIGN.md](DESIGN.md) for detailed architecture documentation.

## Development

### Prerequisites

- Go 1.21 or later
- MongoDB binaries (automatically downloaded by mongo-scaffold)

### Build

```bash
go build -o mup ./cmd/mup
```

### Run Tests

```bash
go test ./...
```

### Project Structure

```
mup/
├── cmd/
│   └── mup/              # CLI commands
├── pkg/
│   ├── playground/       # Playground cluster management
│   ├── import/           # Cluster import operations
│   ├── cluster/          # Production cluster lifecycle (planned)
│   ├── config/           # Configuration management (planned)
│   ├── deploy/           # Deployment orchestration (planned)
│   ├── executor/         # Local and SSH execution abstraction
│   └── supervisor/       # Supervisord integration
└── docs/                 # Design and specification documents
```

## Roadmap

### Phase 1: Playground ✅
- [x] Local MongoDB cluster management
- [x] Start/stop/status/connect commands
- [x] State persistence
- [x] Automatic mongosh connection

### Phase 1.5: Cluster Import ✅
- [x] Auto-detect existing MongoDB clusters
- [x] Import systemd-managed deployments
- [x] Zero-downtime rolling migration
- [x] SSH-based remote import
- [x] Replica set awareness (SECONDARY→PRIMARY pattern)
- [x] Automatic rollback on failure

### Phase 2: Basic Deployment (Next)
- [ ] Deploy standalone MongoDB instance
- [ ] Deploy 3-node replica set
- [ ] Configuration templates
- [ ] Plan/apply workflow

### Phase 3: Advanced Operations
- [ ] Configuration reload
- [ ] Scale out/in
- [ ] Version upgrades
- [ ] Sharded cluster support

See [DESIGN.md](DESIGN.md) for the complete roadmap.

## Comparison with Similar Tools

| Feature | Mup | mongo-orchestration | mtools |
|---------|-----|---------------------|--------|
| **Playground** | ✓ | ✓ | ✓ |
| **Import Existing Clusters** | ✓ | No | No |
| **Production Clusters** | Import | Limited | No |
| **State Management** | YAML | JSON API | No |
| **Remote Management** | SSH | No | No |
| **Rolling Operations** | ✓ | No | No |

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.

## Acknowledgments

- Inspired by [TiUP](https://github.com/pingcap/tiup) - TiDB cluster manager
- Uses [mongo-scaffold](https://github.com/zph/mongo-scaffold) for local cluster management
- Built with [Cobra](https://github.com/spf13/cobra) CLI framework

## Support

- GitHub Issues: [github.com/zph/mup/issues](https://github.com/zph/mup/issues)
- Documentation: [DESIGN.md](DESIGN.md)
