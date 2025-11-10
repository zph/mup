# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Mup (MongoDB Utility Platform) is a cluster management tool for MongoDB, inspired by TiDB's TiUP. The project is currently in Phase 2-3, providing both playground and production cluster management capabilities. Phase 1 (Playground) is complete, and Phase 2-3 (Core Foundation and Basic Deployment) are largely implemented for local deployments. Remote SSH-based deployment is planned for future phases.

**Key Dependencies:**
- `github.com/zph/mongo-scaffold` - Core library for MongoDB cluster orchestration
- `github.com/spf13/cobra` - CLI framework

## Build and Development Commands

### Building
```bash
make build              # Build binary to ./bin/mup
make clean             # Remove build artifacts
make install           # Install to $GOPATH/bin
```

### Testing
```bash
make test              # Run all tests
make test-coverage     # Generate coverage report to coverage.html
go test -v ./pkg/playground  # Run tests for specific package
```

### Code Quality
```bash
make fmt               # Format code
make vet               # Run go vet
make lint              # Run golangci-lint (if installed)
```

### Running
```bash
make run               # Build and run playground start
./bin/mup playground start --version 7.0  # Start local cluster
./bin/mup playground status               # Check cluster status
./bin/mup playground connect             # Connect to cluster

# Production cluster management
./bin/mup cluster deploy my-cluster examples/replica-set.yaml --version 7.0
./bin/mup cluster list                   # List all clusters
./bin/mup cluster display my-cluster     # Show cluster details
./bin/mup cluster connect my-cluster     # Connect to cluster
./bin/mup cluster start my-cluster       # Start cluster
./bin/mup cluster stop my-cluster         # Stop cluster
./bin/mup cluster destroy my-cluster     # Destroy cluster
```

## Architecture

### Directory Structure
```
cmd/mup/           - CLI entry point and command definitions
  main.go          - Root command setup
  playground.go    - Playground subcommands (start/stop/status/connect/destroy)
  cluster.go       - Cluster subcommands (deploy/start/stop/display/destroy/list/connect)
pkg/playground/    - Playground cluster management logic
  playground.go    - Manager, state persistence, cluster lifecycle
pkg/cluster/       - Production cluster management logic
  manager.go       - Cluster lifecycle operations (start/stop/destroy)
  display.go       - Cluster status display
  list.go          - Cluster listing
  destroy.go       - Cluster destruction
pkg/deploy/        - Deployment orchestration
  deployer.go      - Main deployer interface
  deploy.go        - Phase 3: Deploy (start processes)
  prepare.go       - Phase 2: Prepare (download binaries, create dirs)
  initialize.go   - Phase 4: Initialize (replica sets, sharding)
  finalize.go      - Phase 5: Finalize (save metadata, display info)
  binary_manager.go - Binary download and management
pkg/meta/          - Cluster metadata management
  meta.go          - Metadata storage and retrieval
pkg/topology/      - Topology parsing and validation
  topology.go      - Topology structure and parsing
  port_allocator.go - Port allocation for local deployments
pkg/template/      - Configuration template management
  manager.go       - Template loading and rendering
pkg/executor/      - Unified execution interface
  executor.go      - Executor interface definition
  local.go         - Local executor implementation
bin/               - Compiled binaries (gitignored)
docs/              - Design documentation
examples/          - Example topology files
```

### State Management

**Playground state**: `~/.mup/playground/`
- `state.json` - Mup's internal state (status, version, timestamps)
- `cluster-info.json` - mongo-scaffold cluster details (connection info, ports, topology)

**Production cluster state**: `~/.mup/storage/clusters/<name>/`
- `meta.yaml` - Cluster metadata (name, version, topology, nodes, connection command)
- Binary cache: `~/.mup/storage/packages/` - Cached MongoDB binaries

The playground uses mongo-scaffold's defaults: 2 shards with 3 replica nodes each, 1 config server, 1 mongos router.

Production clusters use custom deployment logic with template-based configuration generation.

### Key Implementation Patterns

#### 1. Playground Manager Pattern
`pkg/playground/playground.go` implements the Manager pattern:
- `NewManager()` - Initializes state directory at `~/.mup/playground/`
- `Start()` - Delegates to `mongocluster.Start()` with default config
- `Stop()` - Uses `mongocluster.Stop()` with cluster info file
- `GetClusterInfo()` - Reads cluster connection details from JSON
- State persisted as JSON, not the YAML described in DESIGN.md (future feature)

#### 2. Connection Command Handling
The `connect` command uses `connection_command` from cluster JSON rather than hardcoded `mongosh`. This ensures compatibility with older MongoDB versions (uses legacy `mongo` shell) and newer versions (`mongosh`). Execute via `sh -c` to properly handle quoted connection strings.

#### 3. Cobra Command Structure
Commands follow cobra's pattern with:
- Command definition in `cmd/mup/playground.go`
- Business logic in `pkg/playground/`
- Error wrapping with context (e.g., `fmt.Errorf("failed to X: %w", err)`)

### Working with mongo-scaffold

The project delegates cluster orchestration to `github.com/zph/mongo-scaffold/pkg/mongocluster`:
- Use `mongocluster.Start()` not `cluster.NewCluster()` (lower-level API)
- Always run in background mode (`Background: true`)
- Save cluster info to file for later operations (`OutputFile`, `FileOverwrite: true`)
- Use defaults: `DefaultShards`, `DefaultReplicaNodes`, `DefaultConfigServers`, `DefaultMongosCount`
- Read connection details via `mongocluster.ReadClusterInfoFromFile()`

**Common issues:**
- Port allocation failures: Library needs sequential available ports (8 for default config)
- Previous clusters not cleaned up: Use `killall mongod` or restart
- Wrong API: `pkg/cluster` is low-level; use `pkg/mongocluster` for high-level operations

## Implementation Guidelines

### Adding New Commands
1. Define cobra command in `cmd/mup/playground.go`
2. Implement business logic in `pkg/playground/playground.go`
3. Add to `init()` function via `playgroundCmd.AddCommand()`
4. Update help text and README.md

### State Updates
When modifying cluster state:
1. Load current state via `LoadState()`
2. Perform operation
3. Update state struct
4. Call `SaveState()` immediately
5. State is JSON (uses Go json tags), not YAML

### Templating and Interpolation
1. Any interpolation of a string that's more than one line MUST use golang templates

### Binary Output Location
All compiled binaries go to `./bin/` (gitignored). Never output to project root or `cmd/` directory.

## Documentation

- **docs/DESIGN.md** - Comprehensive architectural design and technical decisions
- **docs/TODO.md** - Detailed implementation roadmap with all planned commands and features organized by phase
- **README.md** - User-facing documentation and quick start guide
- New markdown files MUST go in docs/

## Current Implementation Status

### Phase 1: Playground ✅ (Complete)
- Local MongoDB cluster management using mongo-scaffold
- Playground start/stop/status/connect/destroy commands
- State persistence (JSON)

### Phase 2: Core Foundation ✅ (Mostly Complete)
- ✅ Meta Manager (`pkg/meta/`) - YAML-based state storage
- ✅ Topology Parser (`pkg/topology/`) - YAML parsing and validation
- ✅ Binary Manager (`pkg/deploy/binary_manager.go`) - Download and cache MongoDB binaries
- ✅ Template Manager (`pkg/template/`) - Version-aware configuration templates
- ✅ Executor Interface (`pkg/executor/`) - Unified local/remote execution (local ✅, SSH ⏳)
- ⏳ SSH Executor - Remote deployment support (planned)

### Phase 3: Basic Deployment ✅ (Mostly Complete)
- ✅ `mup cluster deploy` - Full deployment workflow (5 phases)
- ✅ `mup cluster start` - Start stopped clusters
- ✅ `mup cluster stop` - Stop running clusters
- ✅ `mup cluster display` - Show cluster status and information
- ✅ `mup cluster destroy` - Remove clusters
- ✅ `mup cluster list` - List all managed clusters
- ✅ `mup cluster connect` - Connect to clusters using mongosh/mongo
- ⏳ `mup cluster restart` - Restart clusters (planned)

### Key Features Implemented
- Version-specific template handling (e.g., `storage.journal.enabled` removed in 6.1+)
- Process death detection with automatic log reading
- Automatic mongosh/mongo download to BinPath
- Connection command generation (mongosh for >= 4.0, mongo for < 4.0)
- Replica set initialization using MongoDB Go driver
- Sharded cluster configuration using MongoDB Go driver
- Template-based configuration generation
- Local port allocation for local deployments

## Future Architecture

Phase 4+ will add advanced features (see docs/TODO.md for complete roadmap):
- SSH-based agentless deployment to remote hosts
- Rolling operations for zero-downtime updates
- Configuration management and reload
- Scale out/in operations
- Version upgrades
- Security features (TLS, authentication)

When implementing new features:
1. Check docs/TODO.md for detailed implementation tasks
2. Update DESIGN.md with architectural decisions
3. Update README.md with user-facing documentation
4. Mark completed items in docs/TODO.md
