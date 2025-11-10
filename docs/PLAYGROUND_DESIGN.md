# Playground Design

## Overview

The playground is a simplified interface for quickly spinning up local MongoDB test clusters. It wraps the unified cluster deployment system with sensible defaults for development and testing workflows.

## Architecture

### Design Principle

The playground uses the same underlying deployment infrastructure as `mup cluster deploy`, eliminating code duplication and ensuring consistency. It acts as a convenience wrapper that:

1. Uses a fixed cluster name: `"playground"`
2. Provides an embedded 3-node replica set topology
3. Automatically allocates ports starting from 30000
4. Uses the unified executor, template, and metadata systems

### Implementation Pattern

```
playground command → cluster deployment system
                  ↓
         unified infrastructure
         (executor, templates, metadata)
```

The playground is **not** a separate implementation - it's a specialized use case of the general cluster deployment system with opinionated defaults.

## Topology Configuration

The playground uses an embedded YAML topology:

```yaml
# Playground topology - 3-node replica set
global:
  user: mongodb
  deploy_dir: ~/.mup/playground
  data_dir: ~/.mup/playground/data
  log_dir: ~/.mup/playground/logs
  config_dir: ~/.mup/playground/conf

mongod_servers:
  - host: localhost
    port: 0  # Auto-allocated
    replica_set: rs0

  - host: localhost
    port: 0
    replica_set: rs0

  - host: localhost
    port: 0
    replica_set: rs0
```

**Key Features:**
- 3-node replica set named "rs0"
- All ports auto-allocated (port: 0)
- All nodes on localhost
- Data stored in `~/.mup/playground/`

## Commands

### `mup playground start`

**Purpose:** Create or start the playground cluster

**Behavior:**
1. Checks if playground cluster exists in metadata
2. If exists: calls `cluster.Manager.Start()` to restart existing cluster
3. If not exists:
   - Creates temporary topology file
   - Calls `deploy.NewDeployer()` with `SkipConfirm: true`
   - Deploys new cluster using standard 5-phase deployment
4. Displays connection information

**Flags:**
- `-v, --version`: MongoDB version to use (default: "7.0")
- `-t, --timeout`: Operation timeout (default: 5m)

**Example:**
```bash
mup playground start --version 7.0
```

### `mup playground stop`

**Purpose:** Stop the running playground cluster

**Behavior:**
1. Prompts for confirmation (unless `--yes` flag provided)
2. Calls `cluster.Manager.Stop()` for "playground" cluster
3. Uses stored PIDs from metadata for graceful SIGINT shutdown
4. Updates cluster status to "stopped"

**Safety:** Requires confirmation or `--yes` flag to prevent accidental shutdowns

**Flags:**
- `-y, --yes`: Skip confirmation prompt
- `-t, --timeout`: Operation timeout (default: 5m)

**Example:**
```bash
mup playground stop           # Prompts for confirmation
mup playground stop --yes     # Skips confirmation
```

### `mup playground status`

**Purpose:** Display cluster information and node status

**Behavior:**
1. Calls `cluster.Manager.Display()` for "playground" cluster
2. Shows cluster metadata, node status, and connection URI
3. If no cluster exists, shows friendly message to run start command

**Example:**
```bash
mup playground status
```

**Output:**
```
============================================================
Cluster: playground
============================================================
Status:         running
MongoDB:        7.0
Deploy mode:    local
Created:        2025-11-09T23:16:43-08:00
Topology:       replica_set

Nodes:
------------------------------------------------------------
Mongod Servers:
  - localhost:30000 (rs0) [running]
  - localhost:30001 (rs0) [running]
  - localhost:30002 (rs0) [running]

Connection:
------------------------------------------------------------
URI: mongodb://localhost:30000,localhost:30001,localhost:30002/?replicaSet=rs0
```

### `mup playground destroy`

**Purpose:** Completely remove playground cluster and all data

**Behavior:**
1. Prompts for confirmation (unless `--yes` flag provided)
2. Calls `cluster.Manager.Destroy()` for "playground" cluster
3. Stops all processes if running
4. Removes all data directories
5. Deletes cluster metadata

**Safety:** Requires confirmation or `--yes` flag. Operation cannot be undone.

**Flags:**
- `-y, --yes`: Skip confirmation prompt
- `-t, --timeout`: Operation timeout (default: 5m)

**Example:**
```bash
mup playground destroy           # Prompts for confirmation
mup playground destroy --yes     # Skips confirmation
```

### `mup playground connect`

**Purpose:** Connect to playground cluster using MongoDB shell

**Behavior:**
1. Loads cluster metadata
2. Verifies cluster is running
3. Builds connection string from metadata
4. Attempts to launch `mongosh`, falls back to `mongo` if not found
5. Passes through exit codes from shell

**Example:**
```bash
mup playground connect
```

## Storage Layout

All playground data is stored in `~/.mup/storage/clusters/playground/`:

```
~/.mup/storage/clusters/playground/
├── meta.yaml                    # Cluster metadata with PIDs
├── conf/
│   ├── localhost-30000/
│   │   └── mongod.conf         # Generated from templates
│   ├── localhost-30001/
│   │   └── mongod.conf
│   └── localhost-30002/
│       └── mongod.conf
├── data/
│   ├── localhost-30000/         # MongoDB data directory
│   ├── localhost-30001/
│   └── localhost-30002/
└── logs/
    ├── localhost-30000/
    │   └── mongod.log
    ├── localhost-30001/
    │   └── mongod.log
    └── localhost-30002/
        └── mongod.log
```

## Technical Details

### Port Allocation

- Starts from port 30000
- Uses `topology.AllocatePortsForTopology()` with availability checking
- Scans up to 1000 ports sequentially for available ports
- Port checker uses connection attempt (not bind) to avoid race conditions

### Template System

Playground configs use version-aware templates supporting MongoDB 3.6-7.0+:

- **3.6-4.0**: SSL naming (`ssl.PEMKeyFile`)
- **4.2-4.4**: TLS naming (`tls.certificateKeyFile`)
- **5.0-6.0**: Enhanced options
- **7.0+**: Modern defaults

Templates are selected automatically based on `--version` flag using `hashicorp/go-version` constraints.

### Process Management

- PIDs stored in `meta.yaml` during deployment
- Graceful shutdown via `syscall.SIGINT`
- No lsof dependency - all PID tracking via metadata
- Process status checked during display/stop operations

### Replica Set Initialization

1. All mongod processes started
2. Wait for all nodes to be ready (TCP connection check)
3. Initiate replica set on first node
4. Wait for primary election (up to 60 attempts, 2s intervals)
5. Verify all members are healthy before completing

### Safety Confirmations

Both `stop` and `destroy` commands require user confirmation to prevent accidental operations:

**Stop Command:**
```
Are you sure you want to stop the playground cluster? [y/N]:
```

**Destroy Command:**
```
Are you sure you want to destroy the playground cluster and remove all data? [y/N]:
```

- Accepts: "y", "Y", "yes" to proceed
- Any other input cancels the operation
- Confirmation can be bypassed with `--yes` or `-y` flag

## Comparison with General Cluster Deploy

| Feature | Playground | Cluster Deploy |
|---------|-----------|----------------|
| Cluster Name | Fixed: "playground" | User-specified |
| Topology | Embedded 3-node RS | YAML file required |
| Port Allocation | Automatic (port: 0) | User-defined or auto |
| Confirmation | Built-in for stop/destroy | Uses --yes flag |
| Target Audience | Quick dev/test | Production workflows |
| Implementation | Wrapper around deploy | Core implementation |

## Design Benefits

1. **No Code Duplication**: Playground uses existing cluster infrastructure
2. **Feature Parity**: Benefits from all cluster features (templates, PID tracking, etc.)
3. **Consistency**: Same behavior and guarantees as general deployment
4. **Maintainability**: Single implementation to maintain and test
5. **Future-Proof**: Automatically inherits new cluster features

## Future Considerations

The playground can be extended with additional convenience features without modifying core deployment logic:

- Custom replica set sizes via flag (e.g., `--nodes 5`)
- Sharded cluster playground variant
- Preset configurations (memory-limited, high-performance, etc.)
- Integration with testing frameworks

All extensions would remain thin wrappers over the unified deployment system.
