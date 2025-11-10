# Mup - MongoDB Cluster Management Tool

Mup (MongoDB Utility Platform) is a cluster management tool for MongoDB, inspired by TiDB's TiUP. It simplifies deployment, configuration, and lifecycle management of MongoDB clusters (standalone, replica sets, and sharded clusters) across distributed infrastructure.

## Features

- **Playground Mode**: Quick local MongoDB cluster for development and testing
- **Declarative Configuration**: Define cluster topology in YAML
- **SSH-based Remote Management**: Agentless architecture using SSH
- **Version Flexibility**: Support multiple MongoDB versions (3.6 through 8.0)
- **Rolling Operations**: Zero-downtime configuration changes and upgrades
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

## Production Cluster Management (Coming Soon)

Mup will support full production cluster management with features like:

```bash
# Deploy a replica set
mup cluster deploy prod-rs 7.0.5 topology.yaml

# Scale out cluster
mup cluster scale-out prod-rs --node 192.168.1.20

# Upgrade MongoDB version
mup cluster upgrade prod-rs 8.0.0 --rolling

# Reload configuration
mup cluster reload prod-rs --rolling
```

See [DESIGN.md](DESIGN.md) for the complete architecture and roadmap.

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
│   ├── cluster/          # Production cluster lifecycle (planned)
│   ├── config/           # Configuration management (planned)
│   ├── deploy/           # Deployment orchestration (planned)
│   └── ssh/              # SSH operations (planned)
└── DESIGN.md             # Detailed design document
```

## Roadmap

### Phase 1: Playground (Current)
- [x] Local MongoDB cluster management
- [x] Start/stop/status/connect commands
- [x] State persistence
- [x] Automatic mongosh connection

### Phase 2: Basic Deployment (Next)
- [ ] Deploy standalone MongoDB instance
- [ ] Deploy 3-node replica set
- [ ] SSH-based remote management
- [ ] Configuration templates

### Phase 3: Advanced Operations
- [ ] Configuration reload
- [ ] Rolling operations
- [ ] Scale out/in
- [ ] Version upgrades
- [ ] Sharded cluster support

See [DESIGN.md](DESIGN.md) for the complete roadmap.

## Comparison with Similar Tools

| Feature | Mup | mongo-orchestration | mtools |
|---------|-----|---------------------|--------|
| **Playground** | ✓ | ✓ | ✓ |
| **Production Clusters** | Planned | Limited | No |
| **State Management** | YAML | JSON API | No |
| **Remote Management** | SSH | No | No |
| **Rolling Operations** | Planned | No | No |

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
