# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Mup (MongoDB Utility Platform) is a cluster management tool for MongoDB, inspired by TiDB's TiUP. It's currently in Phase 1 (Playground), providing local MongoDB cluster management for development and testing. Future phases will add production cluster deployment capabilities with SSH-based remote management.

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
```

## Architecture

### Directory Structure
```
cmd/mup/           - CLI entry point and command definitions
  main.go          - Root command setup
  playground.go    - Playground subcommands (start/stop/status/connect/destroy)
pkg/playground/    - Playground cluster management logic
  playground.go    - Manager, state persistence, cluster lifecycle
bin/               - Compiled binaries (gitignored)
docs/              - Design documentation
```

### State Management

**Playground state**: `~/.mup/playground/`
- `state.json` - Mup's internal state (status, version, timestamps)
- `cluster-info.json` - mongo-scaffold cluster details (connection info, ports, topology)

The playground uses mongo-scaffold's defaults: 2 shards with 3 replica nodes each, 1 config server, 1 mongos router.

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

### Binary Output Location
All compiled binaries go to `./bin/` (gitignored). Never output to project root or `cmd/` directory.

## Documentation

- **docs/DESIGN.md** - Comprehensive architectural design and technical decisions
- **docs/TODO.md** - Detailed implementation roadmap with all planned commands and features organized by phase
- **README.md** - User-facing documentation and quick start guide

## Future Architecture

Phase 2+ will add production cluster management (see docs/TODO.md for complete roadmap):
- SSH-based agentless deployment to remote hosts
- YAML topology files for cluster configuration
- State in `~/.mup/storage/clusters/<name>/meta.yaml`
- Template-based MongoDB configuration generation
- Rolling operations for zero-downtime updates

When implementing new features:
1. Check docs/TODO.md for detailed implementation tasks
2. Update DESIGN.md with architectural decisions
3. Update README.md with user-facing documentation
4. Mark completed items in docs/TODO.md
