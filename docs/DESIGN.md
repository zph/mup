# Mup - MongoDB Cluster Management Tool

## Overview

Mup (MongoDB Utility Platform) is a cluster management tool for MongoDB, inspired by TiDB's TiUP. It simplifies deployment, configuration, and lifecycle management of MongoDB clusters (standalone, replica sets, and sharded clusters) across distributed infrastructure.

### Supported MongoDB Versions
- MongoDB 3.6 through 8.0
- Architecture support: x86_64, ARM64

---

## Design Philosophy

### Core Principles
1. **Declarative Configuration**: Define cluster topology in YAML, let mup handle the rest
2. **SSH-based Remote Management**: Agentless architecture using SSH for all remote operations
3. **Version Flexibility**: Support multiple MongoDB versions side-by-side
4. **State Management**: Centralized state tracking in YAML metadata
5. **Idempotent Operations**: Safe to re-run commands without side effects
6. **Zero Downtime**: Rolling operations for configuration changes and upgrades

---

## Architecture

### Component Structure

```
mup/
├── cmd/
│   └── mup/               # Main CLI entry point
├── pkg/
│   ├── cluster/           # Cluster lifecycle management
│   ├── config/            # Configuration parsing and generation
│   ├── deploy/            # Deployment orchestration
│   ├── meta/              # Metadata and state management
│   ├── mongo/             # MongoDB-specific operations
│   ├── repository/        # MongoDB binary repository/mirror
│   ├── ssh/               # SSH connection and execution
│   ├── template/          # Configuration templates
│   └── topology/          # Topology validation and processing
├── components/
│   ├── mongod/            # MongoDB server management
│   ├── mongos/            # MongoDB router management
│   └── config-server/     # Config server management
└── templates/
    ├── mongod.conf.tmpl
    ├── mongos.conf.tmpl
    └── replica-set.yaml.tmpl
```

### Key Components

#### 1. **CLI Commands**
```
mup cluster deploy     # Deploy new cluster
mup cluster start      # Start cluster
mup cluster stop       # Stop cluster
mup cluster restart    # Restart cluster
mup cluster destroy    # Destroy cluster
mup cluster scale-out  # Add nodes
mup cluster scale-in   # Remove nodes
mup cluster upgrade    # Upgrade MongoDB version
mup cluster reload     # Reload configuration
mup cluster display    # Show cluster status
mup cluster edit-config # Edit cluster configuration
mup cluster exec       # Execute command on nodes
mup list               # List managed clusters
mup install            # Install MongoDB binaries locally
mup playground         # Quick local test cluster
```

#### 2. **State Management**
All cluster state stored in `~/.mup/storage/clusters/<cluster-name>/meta.yaml`

**Metadata Structure**:
```yaml
# meta.yaml
cluster_name: prod-cluster
version: "7.0.5"
user: mongodb
ssh_key: /home/user/.ssh/id_rsa
deploy_timestamp: 2025-01-15T10:30:00Z
last_modified: 2025-01-15T14:20:00Z

topology:
  global:
    data_dir: /data/mongodb
    log_dir: /var/log/mongodb
    port_base: 27017
    replica_set: rs0

  mongod_servers:
    - host: 192.168.1.10
      port: 27017
      ssh_port: 22
      data_dir: /data/mongodb
      log_dir: /var/log/mongodb
      config_file: /etc/mongodb/mongod.conf
      status: running
      version: "7.0.5"
      replica_set: rs0
      replica_priority: 1
      votes: 1
      deployed_at: 2025-01-15T10:31:00Z

    - host: 192.168.1.11
      port: 27017
      ssh_port: 22
      data_dir: /data/mongodb
      log_dir: /var/log/mongodb
      config_file: /etc/mongodb/mongod.conf
      status: running
      version: "7.0.5"
      replica_set: rs0
      replica_priority: 1
      votes: 1
      deployed_at: 2025-01-15T10:32:00Z

    - host: 192.168.1.12
      port: 27017
      ssh_port: 22
      data_dir: /data/mongodb
      log_dir: /var/log/mongodb
      config_file: /etc/mongodb/mongod.conf
      status: running
      version: "7.0.5"
      replica_set: rs0
      replica_priority: 1
      votes: 1
      deployed_at: 2025-01-15T10:33:00Z

  mongos_servers: []
  config_servers: []

runtime_config:
  storage:
    engine: wiredTiger
    wiredTiger:
      engineConfig:
        cacheSizeGB: 4
  replication:
    replSetName: rs0
  net:
    bindIp: 0.0.0.0
    port: 27017
```

#### 3. **SSH Management**

**Connection Pooling**:
- Maintain persistent SSH connections per host
- Reuse connections across operations
- Automatic reconnection on failure
- Configurable timeouts and retry logic

**Authentication**:
- SSH key-based (default: `~/.ssh/id_rsa`)
- SSH agent support
- Password authentication (optional)
- Custom identity file per deployment

**Remote Operations**:
```go
// pkg/ssh/executor.go
type Executor interface {
    Execute(host, command string) (output string, err error)
    UploadFile(host, local, remote string) error
    DownloadFile(host, remote, local string) error
    CreateDirectory(host, path string, mode os.FileMode) error
    CheckConnectivity(host string) error
}
```

#### 4. **Configuration Management**

**Template System**:
- Go templates for MongoDB configuration files
- Version-aware templates (3.6 vs 4.0+ syntax differences)
- Support for all MongoDB configuration options
- Validation against MongoDB schema

**Configuration Deployment Flow**:
```
1. Parse topology YAML
2. Generate per-node configuration from templates
3. Validate configuration syntax
4. Upload via SSH to target nodes
5. Backup existing config (if exists)
6. Deploy new configuration
7. Update metadata with deployment status
```

**Configuration Reload**:
```
mup cluster reload <cluster-name> [--node <host>] [--rolling]

Options:
  --node      Reload specific node only
  --rolling   Reload one node at a time (zero downtime)
  --force     Skip validation checks
```

**Reload Process**:
1. Generate new configuration files
2. Validate against current MongoDB version
3. For each node (sequentially if --rolling):
   - Upload new configuration
   - If replica set member:
     - Step down if primary
     - Wait for new primary election
   - Send SIGHUP to mongod (or use db.adminCommand({setParameter: ...}))
   - Verify configuration applied
   - Monitor for errors
4. Update meta.yaml with new configuration state

---

## Deployment Workflow

### Initial Deployment

```bash
# 1. Create topology file
cat > topology.yaml <<EOF
global:
  user: mongodb
  ssh_port: 22
  data_dir: /data/mongodb

mongod_servers:
  - host: 192.168.1.10
  - host: 192.168.1.11
  - host: 192.168.1.12

replica_set:
  name: rs0
EOF

# 2. Deploy cluster
mup cluster deploy prod-cluster 7.0.5 topology.yaml \
  --user admin \
  --identity-file ~/.ssh/prod_key

# Behind the scenes:
# - Parse topology.yaml
# - Validate SSH connectivity to all hosts
# - Check MongoDB version availability
# - Create deployment plan
# - Prompt for confirmation
# - Execute deployment
```

### Deployment Steps (Internal)

1. **Pre-flight Checks**:
   - SSH connectivity verification
   - Check disk space on data directories
   - Verify ports are available
   - Check user/group existence or create
   - Validate OS compatibility

2. **Binary Distribution**:
   - Download MongoDB tarball if not cached (~/.mup/storage/packages/)
   - Extract and prepare binaries
   - Upload to each node (/opt/mup/mongodb/<version>/)
   - Set proper permissions and ownership

3. **Directory Structure Creation**:
   ```
   /data/mongodb/           # Data directory
   /var/log/mongodb/        # Log directory
   /etc/mongodb/            # Configuration files
   /opt/mup/                # Mup-managed files
     └── mongodb/
         └── 7.0.5/         # Version-specific binaries
             ├── bin/
             └── lib/
   ```

4. **Configuration Generation**:
   - Render templates with node-specific values
   - Generate systemd service files (or init scripts)
   - Upload configuration files
   - Upload service definitions

5. **Service Initialization**:
   - Start mongod processes
   - Wait for processes to be ready
   - Initialize replica set (if applicable)
   - Configure authentication (if specified)

6. **Metadata Storage**:
   - Save complete cluster state to meta.yaml
   - Store SSH credentials securely
   - Record deployment timestamps

---

## Configuration File Deployment

### SSH-based File Transfer

```go
// pkg/deploy/config.go
type ConfigDeployer struct {
    sshExecutor ssh.Executor
    templates   template.Manager
}

func (d *ConfigDeployer) DeployConfig(node *topology.Node, config *config.MongoDB) error {
    // 1. Generate configuration from template
    rendered, err := d.templates.Render(node.Version, config)

    // 2. Validate configuration
    if err := d.validateConfig(rendered); err != nil {
        return err
    }

    // 3. Create backup of existing config
    backupPath := fmt.Sprintf("%s.backup.%d", node.ConfigFile, time.Now().Unix())
    d.sshExecutor.Execute(node.Host, fmt.Sprintf("cp %s %s", node.ConfigFile, backupPath))

    // 4. Upload new configuration
    tmpFile := "/tmp/mongod.conf.new"
    if err := d.sshExecutor.UploadFile(node.Host, rendered, tmpFile); err != nil {
        return err
    }

    // 5. Move to final location
    if err := d.sshExecutor.Execute(node.Host,
        fmt.Sprintf("sudo mv %s %s", tmpFile, node.ConfigFile)); err != nil {
        return err
    }

    // 6. Set ownership and permissions
    d.sshExecutor.Execute(node.Host,
        fmt.Sprintf("sudo chown mongodb:mongodb %s", node.ConfigFile))
    d.sshExecutor.Execute(node.Host,
        fmt.Sprintf("sudo chmod 644 %s", node.ConfigFile))

    return nil
}
```

### Configuration Template Example

```yaml
# templates/mongod.conf.tmpl (MongoDB 5.0+)
storage:
  dbPath: {{ .DataDir }}
  journal:
    enabled: true
  engine: {{ .StorageEngine }}
  {{- if eq .StorageEngine "wiredTiger" }}
  wiredTiger:
    engineConfig:
      cacheSizeGB: {{ .CacheSize }}
      journalCompressor: snappy
    collectionConfig:
      blockCompressor: snappy
  {{- end }}

systemLog:
  destination: file
  path: {{ .LogDir }}/mongod.log
  logAppend: true
  logRotate: reopen

net:
  port: {{ .Port }}
  bindIp: {{ .BindIP }}
  {{- if .TLS.Enabled }}
  tls:
    mode: {{ .TLS.Mode }}
    certificateKeyFile: {{ .TLS.CertFile }}
    CAFile: {{ .TLS.CAFile }}
  {{- end }}

{{- if .ReplicaSet }}
replication:
  replSetName: {{ .ReplicaSet }}
{{- end }}

{{- if .ShardingRole }}
sharding:
  clusterRole: {{ .ShardingRole }}
{{- end }}

processManagement:
  fork: false  # Managed by systemd
  pidFilePath: {{ .DataDir }}/mongod.pid

{{- if .Security.Enabled }}
security:
  authorization: enabled
  {{- if .Security.KeyFile }}
  keyFile: {{ .Security.KeyFile }}
  {{- end }}
{{- end }}
```

---

## Configuration Reload Mechanism

### Dynamic Reload (No Restart)

MongoDB supports runtime parameter changes via `setParameter`:

```go
// pkg/mongo/admin.go
func (m *MongoAdmin) ReloadParameter(param string, value interface{}) error {
    cmd := bson.D{
        {Key: "setParameter", Value: 1},
        {Key: param, Value: value},
    }

    return m.client.Database("admin").RunCommand(context.Background(), cmd).Err()
}

// Examples of runtime-changeable parameters:
// - logLevel
// - slowOpThresholdMs
// - maxConns (with caution)
// - ttlMonitorEnabled
```

### Full Reload (Requires Restart)

For configuration changes requiring restart (storage engine, ports, etc.):

```go
// pkg/cluster/reload.go
func (c *Cluster) ReloadWithRestart(node *topology.Node, rolling bool) error {
    if rolling && node.IsReplicaSetMember {
        // 1. Check if node is primary
        if isPrimary, _ := c.IsPrimary(node); isPrimary {
            // Step down primary
            c.StepDownPrimary(node, 60)
            // Wait for new primary election
            time.Sleep(10 * time.Second)
        }

        // 2. Ensure replica set has majority before proceeding
        if !c.HasMajority(node.ReplicaSet) {
            return errors.New("cannot proceed: replica set lacks majority")
        }
    }

    // 3. Deploy new configuration
    if err := c.deployer.DeployConfig(node, node.Config); err != nil {
        return err
    }

    // 4. Restart mongod
    if err := c.RestartNode(node); err != nil {
        // Rollback configuration
        c.deployer.RollbackConfig(node)
        return err
    }

    // 5. Verify node rejoined replica set
    if node.IsReplicaSetMember {
        return c.WaitForNodeHealthy(node, 120*time.Second)
    }

    return nil
}
```

### Rolling Reload Strategy

```bash
mup cluster reload prod-cluster --rolling

# Execution order for replica set:
# 1. Reload all secondaries (can be parallel)
# 2. Step down primary
# 3. Wait for new primary election
# 4. Reload old primary (now secondary)

# For sharded cluster:
# 1. Reload all mongos (parallel)
# 2. Reload config servers (rolling)
# 3. Reload each shard replica set (rolling within shard)
```

---

## Central State Management

### State File Location
```
~/.mup/
├── storage/
│   ├── clusters/
│   │   ├── prod-cluster/
│   │   │   ├── meta.yaml          # Primary state file
│   │   │   ├── topology.yaml      # Original topology
│   │   │   ├── ssh/
│   │   │   │   └── id_rsa         # Cluster-specific SSH key
│   │   │   └── backups/
│   │   │       └── meta.yaml.2025-01-15  # State backups
│   │   └── dev-cluster/
│   │       └── meta.yaml
│   └── packages/
│       ├── mongodb-7.0.5-linux-x86_64.tgz
│       └── mongodb-8.0.0-linux-arm64.tgz
└── config.yaml                     # Global mup configuration
```

### State Operations

```go
// pkg/meta/manager.go
type MetaManager interface {
    Load(clusterName string) (*ClusterMeta, error)
    Save(clusterName string, meta *ClusterMeta) error
    Update(clusterName string, updateFn func(*ClusterMeta) error) error
    Backup(clusterName string) error
    List() ([]string, error)
    Delete(clusterName string) error
}

type ClusterMeta struct {
    ClusterName      string                `yaml:"cluster_name"`
    Version          string                `yaml:"version"`
    User             string                `yaml:"user"`
    SSHKey           string                `yaml:"ssh_key"`
    DeployTimestamp  time.Time             `yaml:"deploy_timestamp"`
    LastModified     time.Time             `yaml:"last_modified"`
    Topology         *Topology             `yaml:"topology"`
    RuntimeConfig    map[string]interface{}`yaml:"runtime_config"`
}

type Topology struct {
    Global        GlobalConfig   `yaml:"global"`
    MongodServers []MongoNode    `yaml:"mongod_servers"`
    MongosServers []MongoNode    `yaml:"mongos_servers"`
    ConfigServers []MongoNode    `yaml:"config_servers"`
}

type MongoNode struct {
    Host            string    `yaml:"host"`
    Port            int       `yaml:"port"`
    SSHPort         int       `yaml:"ssh_port"`
    DataDir         string    `yaml:"data_dir"`
    LogDir          string    `yaml:"log_dir"`
    ConfigFile      string    `yaml:"config_file"`
    Status          string    `yaml:"status"`
    Version         string    `yaml:"version"`
    ReplicaSet      string    `yaml:"replica_set,omitempty"`
    ReplicaPriority int       `yaml:"replica_priority,omitempty"`
    Votes           int       `yaml:"votes,omitempty"`
    DeployedAt      time.Time `yaml:"deployed_at"`
}
```

### State Consistency

**Atomic Updates**:
```go
func (m *FileMetaManager) Update(clusterName string, updateFn func(*ClusterMeta) error) error {
    // 1. Acquire lock
    lock := m.acquireLock(clusterName)
    defer lock.Release()

    // 2. Load current state
    meta, err := m.Load(clusterName)
    if err != nil {
        return err
    }

    // 3. Backup current state
    if err := m.Backup(clusterName); err != nil {
        return err
    }

    // 4. Apply update function
    if err := updateFn(meta); err != nil {
        return err
    }

    // 5. Update timestamp
    meta.LastModified = time.Now()

    // 6. Write to temporary file
    tmpFile := m.metaPath(clusterName) + ".tmp"
    if err := m.writeYAML(tmpFile, meta); err != nil {
        return err
    }

    // 7. Atomic rename
    return os.Rename(tmpFile, m.metaPath(clusterName))
}
```

**State Reconciliation**:
```bash
# Reconcile actual cluster state with stored state
mup cluster reconcile prod-cluster

# What it does:
# - Connect to all nodes
# - Query actual MongoDB status
# - Compare with meta.yaml
# - Report discrepancies
# - Optionally fix issues
```

---

## Advanced Features

### 1. Multi-Version Support

```go
// pkg/mongo/version.go
type VersionManager struct {
    supportedVersions map[string]*VersionInfo
}

type VersionInfo struct {
    Version          string
    MinOSVersion     string
    ConfigTemplate   string
    BinaryURL        string
    FeatureSet       []string
    DeprecatedParams []string
}

func (vm *VersionManager) GetConfigTemplate(version string) (string, error) {
    // Return appropriate template based on version
    // 3.6-4.0: Use legacy YAML format
    // 4.2+: Support transactions
    // 5.0+: New time-series collections
    // 6.0+: Queryable encryption
}
```

### 2. Health Monitoring

```go
// pkg/cluster/health.go
type HealthChecker struct {
    cluster *Cluster
    checks  []HealthCheck
}

type HealthCheck interface {
    Name() string
    Check(node *MongoNode) error
}

// Built-in checks:
// - ProcessRunning
// - PortListening
// - ReplicaSetStatus
// - ReplicationLag
// - DiskSpace
// - MemoryUsage
// - ConnectionCount
```

### 3. Backup Integration

```go
// Future: Integration with mongodump, percona-backup-mongodb
mup cluster backup prod-cluster --type full
mup cluster restore prod-cluster --from <backup-id>
```

### 4. Monitoring Integration

```yaml
# topology.yaml
monitoring:
  prometheus:
    enabled: true
    port: 9216
    exporter_version: "0.40.0"

  grafana:
    enabled: true
    dashboards: ["mongodb-overview", "replication-lag"]
```

---

## Security Considerations

### SSH Key Management
- Support for SSH agent forwarding
- Per-cluster SSH keys
- Encrypted key storage option
- Key rotation support

### MongoDB Authentication
```yaml
# topology.yaml
security:
  auth_enabled: true
  admin_user: admin
  admin_password_file: /secure/admin.pwd  # Read from file, not in YAML
  keyfile: /secure/mongodb-keyfile        # For replica set internal auth
  tls:
    enabled: true
    ca_file: /secure/ca.pem
    cert_file: /secure/mongodb.pem
```

### Audit Logging
- Track all mup operations
- Store in `~/.mup/audit.log`
- Include timestamps, user, command, affected nodes

---

## Error Handling

### Graceful Failure Recovery

```go
// pkg/deploy/rollback.go
type Rollback struct {
    steps []RollbackStep
}

type RollbackStep interface {
    Undo() error
    Description() string
}

// On deployment failure:
// 1. Stop any started processes
// 2. Remove uploaded files
// 3. Restore previous configuration
// 4. Clean up created directories
// 5. Update meta.yaml to reflect rollback
```

### Validation Gates

- Pre-deployment validation (connectivity, resources)
- Pre-upgrade validation (compatibility check)
- Pre-reload validation (config syntax)
- Post-operation validation (health checks)

---

## Example Workflows

### Deploy 3-Node Replica Set

```bash
# topology.yaml
global:
  user: mongodb
  data_dir: /data/mongodb

mongod_servers:
  - host: mongo1.example.com
  - host: mongo2.example.com
  - host: mongo3.example.com

replica_set:
  name: rs0

mup cluster deploy prod-rs 7.0.5 topology.yaml
mup cluster start prod-rs
mup cluster display prod-rs
```

### Deploy Sharded Cluster

```bash
# sharded-topology.yaml
global:
  user: mongodb

config_servers:
  - host: cfg1.example.com
  - host: cfg2.example.com
  - host: cfg3.example.com

mongos_servers:
  - host: mongos1.example.com
  - host: mongos2.example.com

shard1:
  replica_set: shard1rs
  mongod_servers:
    - host: shard1-1.example.com
    - host: shard1-2.example.com
    - host: shard1-3.example.com

shard2:
  replica_set: shard2rs
  mongod_servers:
    - host: shard2-1.example.com
    - host: shard2-2.example.com
    - host: shard2-3.example.com

mup cluster deploy prod-sharded 7.0.5 sharded-topology.yaml
```

### Upgrade Cluster

```bash
# Upgrade from 6.0 to 7.0
mup cluster upgrade prod-rs 7.0.5 --rolling

# Process:
# 1. Backup current metadata
# 2. Download 7.0.5 binaries to all nodes
# 3. Upgrade secondaries first
# 4. Step down primary
# 5. Upgrade old primary
# 6. Update feature compatibility version
```

### Reload Configuration

```bash
# Edit configuration
mup cluster edit-config prod-rs

# Apply changes with rolling restart
mup cluster reload prod-rs --rolling

# Or reload specific parameter without restart
mup cluster reload prod-rs --parameter slowOpThresholdMs=200
```

---

## Implementation Roadmap

### Phase 1: Core Foundation
- [ ] CLI framework and command structure
- [ ] SSH executor with connection pooling
- [ ] Meta manager with YAML state storage
- [ ] Basic topology parser
- [ ] MongoDB binary downloader/cache

### Phase 2: Basic Deployment
- [ ] Deploy standalone MongoDB instance
- [ ] Deploy 3-node replica set
- [ ] Configuration template system
- [ ] Service management (systemd)
- [ ] Basic health checks

### Phase 3: Advanced Operations
- [ ] Configuration reload (runtime + restart-based)
- [ ] Rolling operations
- [ ] Scale out/in
- [ ] Version upgrades
- [ ] Sharded cluster support

### Phase 4: Production Readiness
- [ ] Comprehensive error handling and rollback
- [ ] Audit logging
- [ ] TLS/SSL support
- [ ] Authentication management
- [ ] Backup integration
- [ ] Monitoring integration

### Phase 5: Enhanced Features
- [ ] Multi-datacenter deployment
- [ ] Disaster recovery tools
- [ ] Performance tuning advisor
- [ ] Cost optimization recommendations

---

## Comparison with TiUP

| Feature | TiUP | Mup |
|---------|------|------|
| **State Storage** | Meta directory with YAML | `~/.mup/storage/clusters/*/meta.yaml` |
| **SSH Management** | Built-in SSH with key-based auth | Built-in SSH with key-based auth |
| **Config Deployment** | Template-based, version-aware | Template-based, MongoDB 3.6-8.0 aware |
| **Configuration Reload** | `tiup cluster reload` with rolling | `mup cluster reload` with rolling + runtime changes |
| **Topology Format** | YAML with node specifications | YAML with MongoDB-specific options |
| **Binary Distribution** | Mirror system with local cache | MongoDB download with local cache |
| **Cluster Types** | TiDB, TiKV, PD, TiFlash | mongod, mongos, config servers |
| **Version Support** | TiDB versions | MongoDB 3.6 - 8.0 |

---

## Technical Decisions

### Project structure and inspiration
- Should come from tiup by Pingcap for TiDB
- Consult https://github.com/pingcap/tiup for code implementations

### Why YAML for State?
- Human-readable and editable
- Git-friendly for version control
- Standard in infrastructure tooling
- Easy to parse and validate

### Why SSH-based?
- Agentless architecture (no daemon on nodes)
- Leverage existing infrastructure (SSH keys)
- Secure by default
- Simple troubleshooting

### Why Go?
- Excellent SSH libraries
- Fast compilation and deployment
- Cross-platform binary distribution
- Strong concurrency support
- Similar to TiUP for reference

### Why No Custom Protocol?
- Avoid complexity of custom agents
- SSH is ubiquitous and well-understood
- Easier to debug and audit
- Lower barrier to adoption

---

## Conclusion

Mup provides a TiUP-like experience for MongoDB cluster management. It emphasizes:
- **Simplicity**: Single binary, declarative configuration
- **Reliability**: Atomic operations, rollback support, state consistency
- **Flexibility**: Support for all MongoDB deployment patterns and versions
- **Safety**: Rolling operations, validation gates, audit logging

The design prioritizes operational excellence and production readiness, learning from TiUP's proven patterns while adapting to MongoDB's specific requirements.
