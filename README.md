# Mup - MongoDB Cluster Management Tool

Mup (MongoDB Utility Platform) is a cluster management tool for MongoDB, inspired by TiDB's TiUP. It simplifies deployment, configuration, and lifecycle management of MongoDB clusters (standalone, replica sets, and sharded clusters) across distributed infrastructure.

## Features

- **Playground Mode**: Quick local MongoDB cluster for development and testing
- **Rolling Operations**: Zero-downtime migrations with replica set awareness
- **Local Management**: Clusters run on the local machine under supervisord
- **Version Flexibility**: Support multiple MongoDB versions (3.6 through 8.0)
- **State Management**: Centralized state tracking in YAML metadata

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

The `mup playground connect` command automatically reads the cluster info and uses an available MongoDB shell with the correct connection string.

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

If a deployment fails, resume it. Operations that already completed are skipped:

```bash
# Fix the issue (e.g., free up a port)

# Resume the most recent failed or paused apply
mup cluster resume my-cluster

# Output:
# Resuming apply state-xyz789 for cluster 'my-cluster' from phase 'deploy'...
#
# Phase 2/4: Deploy
#   ✓ Started mongod processes [5s]
# ...continues from where it left off
```

If an interrupted run left a lock behind that mup cannot clear on its own:

```bash
mup cluster unlock my-cluster
```

> Implemented today: `mup cluster deploy --plan-only`, `mup cluster resume`, `mup cluster unlock`, `mup plan list`, `mup plan show`, and `mup lock list|show|cleanup|force-unlock`. The other `mup plan ...` subcommands (`validate`, `apply`) and the `mup state ...` commands shown above are not implemented yet.

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

## Configuration

### Storage location

mup keeps clusters, cached MongoDB packages and helper binaries under `~/.mup/storage`. Set `MUP_STORAGE_DIR` to an absolute path to use a different location.

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

For managed cluster deployments, shell acquisition follows server wire-protocol compatibility:

| MongoDB server | Shell used by Mup | Source |
|---|---|---|
| 3.6 | `mongo` | MongoDB server archive |
| 4.0 | `mongosh` 2.5.1 | Separate pinned download |
| 4.2+ | `mongosh` 2.5.9 | Separate pinned download |

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
│   ├── cluster/          # Production cluster lifecycle (planned)
│   ├── config/           # Configuration management (planned)
│   ├── deploy/           # Deployment orchestration (planned)
│   ├── executor/         # Local execution abstraction
│   └── supervisor/       # Supervisord integration
└── docs/                 # Design and specification documents
```

## Roadmap

### Phase 1: Playground ✅
- [x] Local MongoDB cluster management
- [x] Start/stop/status/connect commands
- [x] State persistence
- [x] Automatic mongosh connection

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
| **State Management** | YAML | JSON API | No |
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
