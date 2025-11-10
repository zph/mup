# Mup Implementation TODO

This document tracks the implementation roadmap for Mup commands and features, based on the design in DESIGN.md.

## Current Status

### ✅ Phase 1: Playground (Completed)
- [x] Local MongoDB cluster management
- [x] Playground start/stop/status/connect/destroy commands
- [x] State persistence (JSON)
- [x] Automatic mongo/mongosh connection
- [x] Integration with mongo-scaffold library

### ✅ Phase 2: Core Foundation (Mostly Complete)
- [x] Meta Manager - YAML-based state storage with Load/Save/List/Delete operations
- [x] Topology Parser - YAML parsing, validation, and port allocation
- [x] Binary Manager - Download, cache, and manage MongoDB binaries (including mongosh/mongo)
- [x] Template Manager - Version-aware configuration templates
- [x] Executor Interface - Unified local/remote execution (local ✅, SSH ⏳)
- [ ] SSH Executor - Remote deployment support (planned)

### ✅ Phase 3: Basic Deployment (Mostly Complete)
- [x] `mup cluster deploy` - Full 5-phase deployment workflow
- [x] `mup cluster start` - Start stopped clusters
- [x] `mup cluster stop` - Stop running clusters
- [x] `mup cluster display` - Show cluster status and information
- [x] `mup cluster destroy` - Remove clusters
- [x] `mup cluster list` - List all managed clusters
- [x] `mup cluster connect` - Connect to clusters using mongosh/mongo
- [ ] `mup cluster restart` - Restart clusters (planned)

**Note:** All Phase 3 commands work for local deployments. Remote SSH-based deployment is planned for future phases.

---

## Phase 2: Core Foundation

### Infrastructure Components

#### Executor Interface (`pkg/executor/`) ✅
- [x] Unified executor interface for local/remote execution
- [x] Local executor implementation (`pkg/executor/local.go`)
  - [x] File operations (create, upload, download, remove)
  - [x] Command execution (execute, background)
  - [x] Process management (is running, kill, stop)
  - [x] System information (OS info, disk space, port checking)
- [ ] SSH executor implementation (`pkg/ssh/`)
  - [ ] SSH connection manager with connection pooling
  - [ ] Key-based authentication (default: `~/.ssh/id_rsa`)
  - [ ] SSH agent support
  - [ ] Custom identity file per deployment
  - [ ] Execute remote commands with output capture
  - [ ] File upload/download (SCP/SFTP)
  - [ ] Directory creation with permissions
  - [ ] Connectivity checking
  - [ ] Timeout and retry logic
  - [ ] Parallel execution across hosts

#### Meta Manager (`pkg/meta/`) ✅
- [x] YAML-based state storage at `~/.mup/storage/clusters/<name>/meta.yaml`
- [x] Load/Save/Update operations
- [x] List all clusters
- [x] Delete cluster metadata
- [x] Connection command storage
- [ ] Atomic updates with file locking
- [ ] State backup on updates
- [ ] State reconciliation (actual vs stored state)
- [ ] Cluster state validation

#### Topology Parser (`pkg/topology/`) ✅
- [x] Parse topology YAML files
- [x] Validate topology structure
- [x] Support global configuration inheritance
- [x] Validate node specifications (host, port, paths)
- [x] Detect topology type (standalone, replica set, sharded)
- [x] Port conflict detection (for local deployments)
- [x] Port allocation for local deployments
- [ ] Resource requirement calculation

#### Binary Manager (`pkg/deploy/binary_manager.go`) ✅
- [x] Download MongoDB tarballs from official sources
- [x] Local cache at `~/.mup/storage/packages/`
- [x] Version-specific binary extraction
- [x] Support for x86_64 and ARM64
- [x] MongoDB version 3.6 through 8.0 support
- [x] Automatic mongosh download (version 2.5.9)
- [x] Automatic mongo (legacy shell) support for < 4.0
- [x] Multi-platform support (darwin, linux)
- [ ] Checksum verification

---

## Phase 3: Basic Deployment

### `mup cluster deploy` ✅
Deploy a new MongoDB cluster from topology file.

**Command:**
```bash
mup cluster deploy <cluster-name> <topology-file> [flags]
  --version string           MongoDB version (default: 7.0)
  --user string              SSH user (default from topology)
  --identity-file string     SSH private key path
  --yes                      Skip confirmation prompts
  --timeout duration         Deployment timeout (default: 30m)
```

**Implementation Tasks:**
- [x] Command structure and flags
- [x] Pre-flight checks (for local deployments)
  - [x] Port availability checks
  - [ ] SSH connectivity to all hosts (remote deployments)
  - [ ] Disk space verification
  - [ ] User/group existence or creation
  - [ ] OS compatibility validation
- [x] Binary distribution
  - [x] Download/cache MongoDB binaries
  - [x] Extract and prepare binaries
  - [x] Automatic mongosh/mongo download
  - [ ] Upload to each node at `/opt/mup/mongodb/<version>/` (remote)
  - [ ] Set permissions and ownership (remote)
- [x] Directory structure creation
  - [x] Create data directories (local: `~/.mup/storage/clusters/<name>/data/`)
  - [x] Create log directories (local: `~/.mup/storage/clusters/<name>/logs/`)
  - [x] Create config directories (local: `~/.mup/storage/clusters/<name>/config/`)
- [x] Configuration generation
  - [x] Render mongod.conf from templates
  - [x] Render mongos.conf from templates
  - [x] Render config server configs from templates
  - [x] Version-aware template selection
  - [ ] Generate systemd service files (remote)
  - [x] Upload configurations (local)
- [x] Service initialization
  - [x] Start mongod processes
  - [x] Start mongos processes (for sharded clusters)
  - [x] Start config servers (for sharded clusters)
  - [x] Wait for processes to be ready
  - [x] Process death detection with log reading
  - [x] Initialize replica set (using MongoDB Go driver)
  - [x] Configure sharding (using MongoDB Go driver)
  - [ ] Configure authentication (if specified)
- [x] Metadata storage
  - [x] Save complete cluster state to meta.yaml
  - [x] Connection command generation

### `mup cluster start` ✅
Start a stopped cluster.

**Command:**
```bash
mup cluster start <cluster-name> [flags]
  --node string    Start specific node only (host:port)
```

**Implementation Tasks:**
- [x] Command structure
- [x] Load cluster metadata
- [x] Start MongoDB processes on each node (or specified node)
- [x] Update PID in metadata
- [x] Update metadata with running status
- [ ] Wait for MongoDB processes to be ready
- [ ] Verify replica set status
- [ ] Start systemd services on each node (remote)

### `mup cluster stop` ✅
Stop a running cluster.

**Command:**
```bash
mup cluster stop <cluster-name> [flags]
  --node string    Stop specific node only (host:port)
  --yes            Skip confirmation prompt
```

**Implementation Tasks:**
- [x] Command structure
- [x] Load cluster metadata
- [x] Graceful shutdown of MongoDB processes (SIGINT)
- [x] Clear PID from metadata
- [x] Update metadata with stopped status
- [x] Confirmation prompt (unless --yes)
- [ ] Stop systemd services (remote)
- [ ] Verify processes stopped

### `mup cluster restart`
Restart cluster nodes.

**Command:**
```bash
mup cluster restart <cluster-name> [flags]
  --node string     Restart specific node only
  --rolling         Restart one node at a time (zero downtime)
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Rolling restart implementation
  - [ ] Step down primary (if replica set)
  - [ ] Restart secondaries first
  - [ ] Wait for sync before proceeding
  - [ ] Restart old primary last
- [ ] Non-rolling restart (all nodes)
- [ ] Health verification after restart

### `mup cluster display` ✅
Show cluster status and information.

**Command:**
```bash
mup cluster display <cluster-name> [flags]
  --format string   Output format: text, json, yaml (default: text)
```

**Implementation Tasks:**
- [x] Command structure
- [x] Load cluster metadata
- [x] Display formatted output (text format)
  - [x] Cluster overview
  - [x] Node details (version, status, type)
  - [x] Topology visualization
  - [x] Connection information
- [x] Port-based status checking
- [ ] Query actual cluster state
  - [ ] Connect to each node
  - [ ] Get MongoDB server status
  - [ ] Get replica set status
  - [ ] Get replication lag
- [ ] JSON/YAML output options (structure exists, needs implementation)

### `mup cluster destroy` ✅
Completely remove a cluster.

**Command:**
```bash
mup cluster destroy <cluster-name> [flags]
  --yes           Skip confirmation
  --keep-data     Stop services but keep data
```

**Implementation Tasks:**
- [x] Command structure
- [x] Confirmation prompt (unless --yes)
- [x] Stop all MongoDB processes
- [x] Remove data directories (unless --keep-data, local only)
- [x] Remove log directories (local only)
- [x] Remove configuration directories (local only)
- [x] Delete metadata
- [ ] Remove systemd services (remote)
- [ ] Remove MongoDB binaries (optional)

### `mup cluster list` ✅
List all managed clusters.

**Command:**
```bash
mup cluster list [flags]
  --format string   Output format: text, json, yaml (default: text)
```

**Implementation Tasks:**
- [x] Command structure
- [x] Scan `~/.mup/storage/clusters/` for metadata
- [x] Display cluster summary (name, version, status, topology, node count)
- [x] Support multiple output formats (text, json, yaml)

---

### `mup cluster connect` ✅
Connect to a MongoDB cluster using mongosh/mongo.

**Command:**
```bash
mup cluster connect <cluster-name>
```

**Implementation Tasks:**
- [x] Command structure
- [x] Load cluster metadata
- [x] Read connection command from metadata
- [x] Execute connection command via shell
- [x] Version-aware shell selection (mongosh for >= 4.0, mongo for < 4.0)

---

## Phase 4: Configuration Management

### Template System (`pkg/template/`) ✅
- [x] MongoDB configuration templates
  - [x] mongod.conf template for 3.6-4.0
  - [x] mongod.conf template for 4.2+
  - [x] mongod.conf template for 5.0+
  - [x] mongod.conf template for 7.0+
  - [x] mongos.conf template for 4.2+
  - [x] mongos.conf template for 5.0+
  - [x] config server templates
  - [x] Version-aware template selection
- [x] Go template rendering
- [x] Version-specific template functions (e.g., `supportsJournalEnabled`)
- [ ] Configuration validation against MongoDB schema
- [ ] Support for all MongoDB configuration options

### `mup cluster edit-config`
Edit cluster configuration interactively.

**Command:**
```bash
mup cluster edit-config <cluster-name> [flags]
  --editor string   Text editor to use (default: $EDITOR)
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Load current runtime_config from metadata
- [ ] Open in editor (YAML format)
- [ ] Validate edited configuration
- [ ] Save to metadata
- [ ] Prompt to apply changes (reload cluster)

### `mup cluster reload`
Reload cluster configuration.

**Command:**
```bash
mup cluster reload <cluster-name> [flags]
  --node string              Reload specific node only
  --rolling                  Reload one node at a time
  --parameter string=value   Set runtime parameter without restart
  --force                    Skip validation checks
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Runtime parameter changes (via setParameter)
  - [ ] Support parameters that don't require restart
  - [ ] logLevel, slowOpThresholdMs, maxConns, etc.
- [ ] Configuration file reload (requires restart)
  - [ ] Generate new configuration files
  - [ ] Validate against MongoDB version
  - [ ] Rolling reload implementation
    - [ ] Step down primary if needed
    - [ ] Restart with new config
    - [ ] Wait for node to rejoin
- [ ] Update metadata with new configuration

---

## Phase 5: Advanced Operations

### `mup cluster scale-out`
Add nodes to existing cluster.

**Command:**
```bash
mup cluster scale-out <cluster-name> <topology-file> [flags]
  --yes   Skip confirmation
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Parse new node specifications
- [ ] Validate new nodes don't conflict with existing
- [ ] Deploy MongoDB to new nodes (same as deploy)
- [ ] Add to replica set
  - [ ] rs.add() for new members
  - [ ] Wait for initial sync
- [ ] Add to shard (if sharded cluster)
- [ ] Update metadata

### `mup cluster scale-in`
Remove nodes from cluster.

**Command:**
```bash
mup cluster scale-in <cluster-name> --node <host> [flags]
  --yes   Skip confirmation
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Validate node can be safely removed
- [ ] Remove from replica set
  - [ ] Ensure not primary
  - [ ] rs.remove()
- [ ] Stop MongoDB process
- [ ] Optionally remove data
- [ ] Update metadata

### `mup cluster upgrade`
Upgrade MongoDB version.

**Command:**
```bash
mup cluster upgrade <cluster-name> <version> [flags]
  --rolling   Upgrade one node at a time
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Backup current metadata
- [ ] Download new version binaries
- [ ] Rolling upgrade implementation
  - [ ] Upgrade secondaries first
  - [ ] Step down primary
  - [ ] Upgrade old primary
- [ ] Update feature compatibility version
  - [ ] db.adminCommand({setFeatureCompatibilityVersion: "X.Y"})
- [ ] Update metadata with new version

### `mup cluster exec`
Execute commands on cluster nodes.

**Command:**
```bash
mup cluster exec <cluster-name> -- <command> [flags]
  --node string   Execute on specific node only
  --role string   Execute on nodes with specific role (primary, secondary)
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Node filtering (by host, role, etc.)
- [ ] Parallel command execution
- [ ] Output aggregation
- [ ] Error handling

---

## Phase 6: Production Readiness

### Security Features
- [ ] TLS/SSL support
  - [ ] Certificate management
  - [ ] TLS configuration in templates
  - [ ] Certificate deployment
- [ ] Authentication management
  - [ ] Create admin users
  - [ ] Internal authentication (keyFile)
  - [ ] SCRAM-SHA-256 support
  - [ ] x.509 certificate authentication
- [ ] Authorization
  - [ ] Role-based access control setup
  - [ ] User management commands

### Monitoring and Observability
- [ ] Health checks
  - [ ] Process running check
  - [ ] Port listening check
  - [ ] Replica set status check
  - [ ] Replication lag check
  - [ ] Disk space monitoring
  - [ ] Memory usage monitoring
- [ ] Monitoring integration
  - [ ] Prometheus exporter deployment
  - [ ] Grafana dashboard templates
  - [ ] Alert rule templates

### Backup and Recovery
- [ ] Backup integration
  - [ ] mongodump integration
  - [ ] Percona Backup for MongoDB integration
  - [ ] Backup scheduling
  - [ ] Backup to S3/Azure/GCS
- [ ] Restore operations
  - [ ] mongorestore integration
  - [ ] Point-in-time recovery

### Error Handling and Rollback
- [ ] Rollback mechanism
  - [ ] Track rollback steps during operations
  - [ ] Automatic rollback on failure
  - [ ] Manual rollback command
- [ ] Validation gates
  - [ ] Pre-deployment validation
  - [ ] Pre-upgrade validation
  - [ ] Pre-reload validation
  - [ ] Post-operation validation

### Audit and Logging
- [ ] Audit logging
  - [ ] Track all mup operations
  - [ ] Store in `~/.mup/audit.log`
  - [ ] Include timestamps, user, command, affected nodes
- [ ] Operation history
  - [ ] Store operation history in metadata
  - [ ] View operation history command

---

## Phase 7: Enhanced Features

### Multi-Datacenter Support
- [ ] Geographic topology awareness
- [ ] Cross-datacenter replication configuration
- [ ] Read preference configuration
- [ ] Write concern configuration

### Disaster Recovery
- [ ] Automated failover procedures
- [ ] DR site management
- [ ] Cluster migration tools
- [ ] Data center switchover

### Performance and Optimization
- [ ] Performance tuning advisor
- [ ] Configuration recommendations based on workload
- [ ] Index recommendation integration
- [ ] Query optimization suggestions
- [ ] Cost optimization recommendations

---

## Testing Strategy

### Unit Tests
- [ ] Test all packages in isolation
- [ ] Mock external dependencies (SSH, MongoDB connections)
- [ ] Test state management operations
- [ ] Test configuration generation

### Integration Tests
- [ ] Test with actual MongoDB instances
- [ ] Test replica set deployment
- [ ] Test sharded cluster deployment
- [ ] Test upgrade scenarios
- [ ] Test failure scenarios

### End-to-End Tests
- [ ] Full deployment workflow
- [ ] Configuration reload workflow
- [ ] Scale out/in workflow
- [ ] Upgrade workflow
- [ ] Disaster recovery workflow

---

## Documentation Tasks

- [ ] Complete API documentation (godoc)
- [ ] User guide for each command
- [ ] Topology file reference
- [ ] Configuration template reference
- [ ] Troubleshooting guide
- [ ] Best practices guide
- [ ] Migration guide from other tools

---

## Notes

- **State Management:** Playground uses JSON for state; production clusters use YAML for consistency with topology files
- **Deployment Modes:**
  - Local deployments are fully functional (all Phase 3 commands work)
  - Remote SSH-based deployments are planned for future phases
- **Binary Management:** MongoDB binaries and shells (mongosh/mongo) are automatically downloaded and cached
- **Template System:** Version-aware templates handle MongoDB version differences (e.g., `storage.journal.enabled` removed in 6.1+)
- **Process Management:** Process death detection automatically reads and displays log files when processes die during startup
- **Connection Commands:** Automatically generated based on MongoDB version (mongosh for >= 4.0, mongo for < 4.0)
- **Replica Sets & Sharding:** Initialization uses MongoDB Go driver for robust, programmatic configuration
- **Future Work:** SSH executor, remote deployments, rolling operations, and advanced features are planned
