# Mup Implementation TODO

This document tracks the implementation roadmap for Mup commands and features, based on the design in DESIGN.md.

## Current Status

### ✅ Phase 1: Playground (Completed)
- [x] Local MongoDB cluster management
- [x] Playground start/stop/status/connect/destroy commands
- [x] State persistence (JSON)
- [x] Automatic mongo/mongosh connection
- [x] Integration with mongo-scaffold library

---

## Phase 2: Core Foundation

### Infrastructure Components

#### SSH Executor (`pkg/ssh/`)
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

#### Meta Manager (`pkg/meta/`)
- [ ] YAML-based state storage at `~/.mup/storage/clusters/<name>/meta.yaml`
- [ ] Load/Save/Update operations
- [ ] Atomic updates with file locking
- [ ] State backup on updates
- [ ] State reconciliation (actual vs stored state)
- [ ] List all clusters
- [ ] Delete cluster metadata
- [ ] Cluster state validation

#### Topology Parser (`pkg/topology/`)
- [ ] Parse topology YAML files
- [ ] Validate topology structure
- [ ] Support global configuration inheritance
- [ ] Validate node specifications (host, port, paths)
- [ ] Detect topology type (standalone, replica set, sharded)
- [ ] Port conflict detection
- [ ] Resource requirement calculation

#### Binary Repository (`pkg/repository/`)
- [ ] Download MongoDB tarballs from official sources
- [ ] Local cache at `~/.mup/storage/packages/`
- [ ] Version-specific binary extraction
- [ ] Checksum verification
- [ ] Support for x86_64 and ARM64
- [ ] MongoDB version 3.6 through 8.0 support

---

## Phase 3: Basic Deployment

### `mup cluster deploy`
Deploy a new MongoDB cluster from topology file.

**Command:**
```bash
mup cluster deploy <cluster-name> <version> <topology-file> [flags]
  --user string              SSH user (default from topology)
  --identity-file string     SSH private key path
  --yes                      Skip confirmation prompts
```

**Implementation Tasks:**
- [ ] Command structure and flags
- [ ] Pre-flight checks
  - [ ] SSH connectivity to all hosts
  - [ ] Disk space verification
  - [ ] Port availability checks
  - [ ] User/group existence or creation
  - [ ] OS compatibility validation
- [ ] Binary distribution
  - [ ] Download/cache MongoDB binaries
  - [ ] Extract and prepare binaries
  - [ ] Upload to each node at `/opt/mup/mongodb/<version>/`
  - [ ] Set permissions and ownership
- [ ] Directory structure creation
  - [ ] Create data directories (`/data/mongodb/`)
  - [ ] Create log directories (`/var/log/mongodb/`)
  - [ ] Create config directories (`/etc/mongodb/`)
- [ ] Configuration generation
  - [ ] Render mongod.conf from templates
  - [ ] Generate systemd service files
  - [ ] Upload configurations
- [ ] Service initialization
  - [ ] Start mongod processes
  - [ ] Wait for processes to be ready
  - [ ] Initialize replica set (if applicable)
  - [ ] Configure authentication (if specified)
- [ ] Metadata storage
  - [ ] Save complete cluster state to meta.yaml

### `mup cluster start`
Start a stopped cluster.

**Command:**
```bash
mup cluster start <cluster-name> [flags]
  --node string    Start specific node only
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Load cluster metadata
- [ ] Start systemd services on each node (or specified node)
- [ ] Wait for MongoDB processes to be ready
- [ ] Verify replica set status
- [ ] Update metadata with running status

### `mup cluster stop`
Stop a running cluster.

**Command:**
```bash
mup cluster stop <cluster-name> [flags]
  --node string    Stop specific node only
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Load cluster metadata
- [ ] Graceful shutdown of MongoDB processes
- [ ] Stop systemd services
- [ ] Verify processes stopped
- [ ] Update metadata with stopped status

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

### `mup cluster display`
Show cluster status and information.

**Command:**
```bash
mup cluster display <cluster-name> [flags]
  --format string   Output format: text, json, yaml (default: text)
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Query actual cluster state
  - [ ] Connect to each node
  - [ ] Get MongoDB server status
  - [ ] Get replica set status
  - [ ] Get replication lag
- [ ] Display formatted output
  - [ ] Cluster overview
  - [ ] Node details (version, status, role)
  - [ ] Topology visualization
  - [ ] Health status
- [ ] JSON/YAML output options

### `mup cluster destroy`
Completely remove a cluster.

**Command:**
```bash
mup cluster destroy <cluster-name> [flags]
  --yes           Skip confirmation
  --keep-data     Stop services but keep data
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Confirmation prompt (unless --yes)
- [ ] Stop all MongoDB processes
- [ ] Remove systemd services
- [ ] Remove MongoDB binaries (optional)
- [ ] Remove data directories (unless --keep-data)
- [ ] Remove log files
- [ ] Remove configuration files
- [ ] Delete metadata

### `mup list`
List all managed clusters.

**Command:**
```bash
mup list [flags]
  --format string   Output format: text, json, yaml (default: text)
```

**Implementation Tasks:**
- [ ] Command structure
- [ ] Scan `~/.mup/storage/clusters/` for metadata
- [ ] Display cluster summary (name, version, status, node count)
- [ ] Support multiple output formats

---

## Phase 4: Configuration Management

### Template System (`pkg/template/`)
- [ ] MongoDB configuration templates
  - [ ] mongod.conf template for 3.6-4.0
  - [ ] mongod.conf template for 4.2+
  - [ ] mongos.conf template
  - [ ] Version-aware template selection
- [ ] Go template rendering
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

- Current implementation (Phase 1) uses JSON for state; future phases use YAML for consistency with topology files
- Playground uses mongo-scaffold library; production cluster management will be custom implementation
- All SSH operations should support connection pooling for performance
- Configuration changes should always be validated before application
- Rolling operations are critical for zero-downtime updates in production
