# MongoDB Upgrade Architecture

## Overview
This document describes the technical architecture for MongoDB cluster upgrades in mup, including version-isolated directory structures, symlink-based rollback, custom safety hooks, and phased upgrade orchestration.

## Table of Contents
1. [Architecture Principles](#architecture-principles)
2. [Directory Structure](#directory-structure)
3. [Version Management](#version-management)
4. [Package Design](#package-design)
5. [Upgrade Workflow](#upgrade-workflow)
6. [Safety Check System](#safety-check-system)
7. [Health Verification](#health-verification)
8. [Rollback Mechanism](#rollback-mechanism)
9. [Integration with Existing Components](#integration-with-existing-components)
10. [Data Structures](#data-structures)

---

## Architecture Principles

### 1. Version Isolation
Each MongoDB version gets its own isolated directory containing:
- Binaries (mongod, mongos, mongosh/mongo)
- Configuration files (per node)
- Log files (per node)

**Benefits**:
- No binary reinstallation needed for rollback
- Multiple versions can coexist
- Clean separation prevents version conflicts
- Audit trail of all installed versions

### 2. Symlink-Based Activation
The "active" version is determined by symlinks:
- `current` → active version directory
- `previous` → previous version directory (for rollback)

**Benefits**:
- Atomic version switching
- Fast rollback (change symlink + restart)
- No file copying or moving
- Clear indication of active version

### 3. TiUP-Inspired Design
Directory structure inspired by TiUP's proven approach:
- Centralized storage directory
- Version-specific subdirectories
- Metadata-driven management
- Component-aware organization

### 4. Phased Execution
Upgrades execute in well-defined phases:
- Pre-flight validation
- Config server upgrade (if sharded)
- Shard upgrades (parallel or sequential)
- Mongos upgrades
- Post-upgrade verification

**Benefits**:
- Clear progress tracking
- Resume capability after failures
- Granular error handling
- Predictable behavior

### 5. Extensible Safety Checks
Custom safety check hooks at multiple execution points:
- Pre-upgrade
- Pre-phase
- Pre-node
- Post-node
- Post-phase
- Post-upgrade

**Benefits**:
- Organization-specific validation
- Integration with external monitoring
- Backup verification
- Custom compliance checks

---

## Directory Structure

### Storage Root
```
~/.mup/storage/
├── packages/                          # Binary cache (shared across clusters)
│   ├── mongo-6.0.15/
│   │   ├── bin/
│   │   │   ├── mongod
│   │   │   ├── mongos
│   │   │   └── mongosh
│   │   └── version.json               # Version metadata
│   ├── mongo-7.0.0/
│   │   ├── bin/
│   │   │   ├── mongod
│   │   │   ├── mongos
│   │   │   └── mongosh
│   │   └── version.json
│   ├── percona-6.0.15/
│   │   ├── bin/
│   │   │   ├── mongod
│   │   │   ├── mongos
│   │   │   └── mongo
│   │   └── version.json
│   └── percona-7.0.5-4/
│       ├── bin/
│       │   ├── mongod
│       │   ├── mongos
│       │   └── mongosh
│       └── version.json
└── clusters/
    └── <cluster-name>/
        ├── current -> versions/mongo-7.0.0/      # Symlink to active version
        ├── previous -> versions/mongo-6.0.15/    # Symlink to previous version
        ├── meta.yaml                             # Cluster metadata
        ├── upgrade.state                         # Upgrade progress state
        └── versions/
            ├── mongo-6.0.15/
            │   ├── bin -> ~/.mup/storage/packages/mongo-6.0.15/bin/
            │   ├── conf/
            │   │   ├── mongod-27017.conf
            │   │   ├── mongod-27018.conf
            │   │   ├── mongod-27019.conf
            │   │   └── mongos-27020.conf
            │   └── logs/
            │       ├── mongod-27017.log
            │       ├── mongod-27018.log
            │       ├── mongod-27019.log
            │       └── mongos-27020.log
            └── mongo-7.0.0/
                ├── bin -> ~/.mup/storage/packages/mongo-7.0.0/bin/
                ├── conf/
                │   ├── mongod-27017.conf
                │   ├── mongod-27018.conf
                │   ├── mongod-27019.conf
                │   └── mongos-27020.conf
                └── logs/
                    ├── mongod-27017.log
                    ├── mongod-27018.log
                    ├── mongod-27019.log
                    └── mongos-27020.log
```

### Key Design Decisions

#### Shared Binary Cache (`packages/`)
- Binaries stored once, shared across all clusters
- Saves disk space when multiple clusters use same version
- Version directories symlinked from cluster directories

#### Per-Cluster Version Directories (`clusters/<name>/versions/`)
- Each version has own conf/ and logs/ directories
- Configuration files are version-specific (may change between versions)
- Logs separated by version for debugging and auditing
- Binary directory is symlink to shared package cache

#### Symlink Strategy
- `current` points to active version (e.g., `versions/mongo-7.0.0/`)
- `previous` points to last successful version (for rollback)
- `bin/` within version directory symlinks to package cache
- Supervisor configuration uses `current/bin/mongod` (follows symlink)

#### Log Rotation
- Each version gets its own log directory
- Prevents log mixing between versions
- Simplifies debugging upgrade issues
- Historical logs preserved per version

---

## Version Management

### Full Version Format
- Format: `<variant>-<version>`
- Examples:
  - `mongo-6.0.15`
  - `mongo-7.0.0`
  - `percona-6.0.15`
  - `percona-7.0.5-4`

### Version Parsing
```go
type FullVersion struct {
    Variant string // "mongo" or "percona"
    Version string // "6.0.15", "7.0.0", etc.
}

func ParseFullVersion(fullVersion string) (*FullVersion, error) {
    parts := strings.SplitN(fullVersion, "-", 2)
    if len(parts) != 2 {
        return nil, fmt.Errorf("invalid full version format: %s", fullVersion)
    }
    variant := parts[0]
    if variant != "mongo" && variant != "percona" {
        return nil, fmt.Errorf("unknown variant: %s", variant)
    }
    return &FullVersion{
        Variant: variant,
        Version: parts[1],
    }, nil
}
```

### Version Directory Management

#### Creating a New Version Directory
```go
func (vm *VersionManager) PrepareVersion(clusterName, fullVersion string) error {
    // 1. Parse full version
    fv, err := ParseFullVersion(fullVersion)

    // 2. Ensure binary package exists (download if needed)
    packageDir := filepath.Join(storage, "packages", fullVersion)
    if !exists(packageDir) {
        err = binaryManager.Download(fv.Variant, fv.Version, packageDir)
    }

    // 3. Create version directory in cluster
    versionDir := filepath.Join(storage, "clusters", clusterName, "versions", fullVersion)
    os.MkdirAll(versionDir, 0755)

    // 4. Symlink bin/ to package cache
    os.Symlink(
        filepath.Join(packageDir, "bin"),
        filepath.Join(versionDir, "bin"),
    )

    // 5. Create conf/ and logs/ directories
    os.MkdirAll(filepath.Join(versionDir, "conf"), 0755)
    os.MkdirAll(filepath.Join(versionDir, "logs"), 0755)

    // 6. Generate configuration files
    err = configGenerator.GenerateConfigs(versionDir, clusterMeta, fv)

    return nil
}
```

#### Activating a Version
```go
func (vm *VersionManager) ActivateVersion(clusterName, fullVersion string) error {
    // 1. Get current active version (for previous symlink)
    currentTarget, err := os.Readlink(filepath.Join(clusterDir, "current"))

    // 2. Update previous symlink
    if currentTarget != "" {
        os.Remove(filepath.Join(clusterDir, "previous"))
        os.Symlink(currentTarget, filepath.Join(clusterDir, "previous"))
    }

    // 3. Update current symlink
    os.Remove(filepath.Join(clusterDir, "current"))
    os.Symlink(
        filepath.Join("versions", fullVersion),
        filepath.Join(clusterDir, "current"),
    )

    return nil
}
```

---

## Package Design

### Package Structure
```
pkg/
├── upgrade/
│   ├── upgrade.go           # Main upgrade orchestrator [UPG-004]
│   ├── plan.go              # Upgrade plan generation [UPG-010]
│   ├── binary.go            # Binary replacement [UPG-007]
│   ├── replica_set.go       # Replica set upgrade logic [UPG-005]
│   ├── fcv.go               # FCV management [UPG-008]
│   ├── hooks.go             # Safety check hooks [UPG-009]
│   ├── state.go             # Progress tracking [UPG-011]
│   ├── validate.go          # Pre-upgrade validation [UPG-003]
│   ├── rollback.go          # Rollback procedures [UPG-014]
│   ├── version_manager.go   # Version directory management [UPG-001]
│   └── types.go             # Shared types and constants
│
├── health/
│   ├── health.go            # Health check coordinator [UPG-006]
│   ├── replica_set.go       # Replica set health checks
│   ├── sharded.go           # Sharded cluster health checks
│   └── balancer.go          # Balancer management [UPG-012]
│
├── deploy/
│   └── binary_manager.go    # Extended for variant support [UPG-002]
│
└── meta/
    └── meta.go              # Extended with Variant field [UPG-002]
```

### Key Interfaces

#### Upgrader Interface
```go
type Upgrader interface {
    // Plan generates an upgrade plan
    Plan(from, to FullVersion) (*UpgradePlan, error)

    // Execute runs the upgrade
    Execute(plan *UpgradePlan, opts UpgradeOptions) error

    // Resume continues a failed upgrade
    Resume() error

    // Rollback reverts to previous version
    Rollback() error
}
```

#### HealthChecker Interface
```go
type HealthChecker interface {
    // CheckReplicaSet verifies replica set health
    CheckReplicaSet(ctx context.Context, nodes []Node) (*ReplicaSetHealth, error)

    // CheckShardedCluster verifies sharded cluster health
    CheckShardedCluster(ctx context.Context, meta *ClusterMetadata) (*ClusterHealth, error)

    // WaitForHealthy blocks until health checks pass
    WaitForHealthy(ctx context.Context, timeout time.Duration) error
}
```

#### SafetyHook Interface
```go
type SafetyHook interface {
    // Execute runs the safety check
    Execute(ctx context.Context, hookConfig HookConfig, vars HookVariables) error
}
```

---

## Upgrade Workflow

### High-Level Flow
```
┌─────────────────────────────────────────────────────────────────┐
│                    User: mup cluster upgrade                    │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│ Phase 0: Pre-Flight                                             │
├─────────────────────────────────────────────────────────────────┤
│ 1. Load cluster metadata                                        │
│ 2. Parse target version                                         │
│ 3. Run pre-upgrade validation [UPG-003]                         │
│ 4. Execute pre-upgrade hooks [UPG-009]                          │
│ 5. Download target binaries (if needed) [UPG-002]               │
│ 6. Prepare version directory [UPG-001]                          │
│ 7. Generate upgrade plan [UPG-010]                              │
│ 8. Display plan and confirm                                     │
│ 9. Save upgrade state [UPG-011]                                 │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ▼
                      ┌─────────────┐
                      │  Sharded?   │
                      └──┬───────┬──┘
                    Yes  │       │ No
         ┌───────────────┘       └──────────────┐
         ▼                                       ▼
┌─────────────────────────┐           ┌──────────────────────┐
│ Phase 1: Config Servers │           │ Skip to Phase 2      │
├─────────────────────────┤           └──────────┬───────────┘
│ 1. Disable balancer     │                      │
│ 2. Wait for migrations  │                      │
│ 3. Execute pre-phase    │                      │
│    hooks                │                      │
│ 4. Upgrade config RS    │                      │
│    [UPG-005]            │                      │
│ 5. Verify health        │                      │
│    [UPG-006]            │                      │
│ 6. Execute post-phase   │                      │
│    hooks                │                      │
│ 7. Update state         │                      │
└────────┬────────────────┘                      │
         │                                       │
         └───────────────┬───────────────────────┘
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Phase 2: Shards / Replica Set                                   │
├─────────────────────────────────────────────────────────────────┤
│ For each shard (or single RS):                                  │
│   1. Execute pre-phase hooks                                    │
│   2. Upgrade replica set [UPG-005]:                             │
│      a. Upgrade secondaries (one by one)                        │
│         - Execute pre-node hooks                                │
│         - Stop process                                          │
│         - Replace binary [UPG-007]                              │
│         - Start process                                         │
│         - Wait for SECONDARY state                              │
│         - Verify health [UPG-006]                               │
│         - Execute post-node hooks                               │
│      b. Step down primary                                       │
│      c. Wait for new primary election                           │
│      d. Upgrade former primary (same steps)                     │
│   3. Verify replica set health [UPG-006]                        │
│   4. Execute post-phase hooks                                   │
│   5. Update state                                               │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ▼
                      ┌─────────────┐
                      │  Sharded?   │
                      └──┬───────┬──┘
                    Yes  │       │ No
         ┌───────────────┘       └──────────────┐
         ▼                                       ▼
┌─────────────────────────┐           ┌──────────────────────┐
│ Phase 3: Mongos         │           │ Skip to Phase 4      │
├─────────────────────────┤           └──────────┬───────────┘
│ For each mongos:        │                      │
│   1. Execute pre-phase  │                      │
│      hooks              │                      │
│   2. For each mongos:   │                      │
│      - Pre-node hooks   │                      │
│      - Stop process     │                      │
│      - Replace binary   │                      │
│      - Start process    │                      │
│      - Verify conn      │                      │
│      - Post-node hooks  │                      │
│   3. Execute post-phase │                      │
│      hooks              │                      │
│   4. Update state       │                      │
└────────┬────────────────┘                      │
         │                                       │
         └───────────────┬───────────────────────┘
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ Phase 4: Post-Upgrade                                           │
├─────────────────────────────────────────────────────────────────┤
│ 1. Verify all nodes on target version                          │
│ 2. Verify cluster health [UPG-006]                              │
│ 3. Upgrade FCV if requested [UPG-008]                           │
│ 4. Re-enable balancer (if sharded) [UPG-012]                    │
│ 5. Activate version [UPG-001]:                                  │
│    - Update previous symlink                                    │
│    - Update current symlink                                     │
│ 6. Update cluster metadata                                      │
│ 7. Execute post-upgrade hooks [UPG-009]                         │
│ 8. Clear upgrade state [UPG-011]                                │
│ 9. Display summary                                              │
└─────────────────────────────────────────────────────────────────┘
```

### Parallel vs Sequential Shard Upgrades

#### Sequential (Default)
```go
for _, shard := range shards {
    err := upgrader.UpgradeReplicaSet(shard)
    if err != nil {
        return err
    }
}
```

**Benefits**:
- Lower risk
- Easier to monitor
- Clear failure isolation

**Drawbacks**:
- Longer total time

#### Parallel (--parallel-shards)
```go
var wg sync.WaitGroup
errChan := make(chan error, len(shards))

for _, shard := range shards {
    wg.Add(1)
    go func(s Shard) {
        defer wg.Done()
        if err := upgrader.UpgradeReplicaSet(s); err != nil {
            errChan <- err
        }
    }(shard)
}

wg.Wait()
close(errChan)

if len(errChan) > 0 {
    return <-errChan
}
```

**Benefits**:
- Faster completion
- Better for many-shard clusters

**Drawbacks**:
- Harder to monitor
- More resource intensive
- Multiple simultaneous failures

---

## Safety Check System

### Hook Configuration Schema

```yaml
# In cluster meta.yaml
safety_hooks:
  pre-upgrade:
    - name: verify-backup
      script: /usr/local/bin/check-backup.sh
      args:
        - "--cluster"
        - "{{cluster_name}}"
        - "--version"
        - "{{target_version}}"
      timeout: 60
      required: true  # Fail upgrade if hook fails

  pre-node:
    - name: node-health-check
      script: /usr/local/bin/verify-node.sh
      args:
        - "--host"
        - "{{node_host}}"
        - "--port"
        - "{{node_port}}"
        - "--json"
      timeout: 30
      required: true

  post-node:
    - name: smoke-test
      script: /usr/local/bin/smoke-test.sh
      args:
        - "--connection"
        - "{{node_host}}:{{node_port}}"
      timeout: 45
      required: false  # Warn but don't fail

  post-upgrade:
    - name: integration-test
      script: /opt/tests/integration.sh
      args:
        - "--cluster"
        - "{{cluster_name}}"
      timeout: 300
      required: false
```

### Template Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `{{cluster_name}}` | Cluster name | "my-cluster" |
| `{{current_version}}` | Current full version | "mongo-6.0.15" |
| `{{target_version}}` | Target full version | "mongo-7.0.0" |
| `{{variant}}` | MongoDB variant | "mongo" or "percona" |
| `{{phase}}` | Current upgrade phase | "config", "shard-0", "mongos" |
| `{{node_host}}` | Node hostname | "localhost" |
| `{{node_port}}` | Node port | "27017" |
| `{{node_type}}` | Node type | "mongod", "mongos", "config" |
| `{{replica_set}}` | Replica set name | "rs0" |

### Hook Execution

```go
type HookExecutor struct {
    logger *log.Logger
}

func (he *HookExecutor) Execute(ctx context.Context, hook HookConfig, vars HookVariables) error {
    // 1. Expand template variables
    args := make([]string, len(hook.Args))
    for i, arg := range hook.Args {
        args[i] = expandTemplateVars(arg, vars)
    }

    // 2. Prepare command
    cmd := exec.CommandContext(ctx, hook.Script, args...)

    // 3. Set timeout
    ctx, cancel := context.WithTimeout(ctx, time.Duration(hook.Timeout)*time.Second)
    defer cancel()

    // 4. Execute
    output, err := cmd.CombinedOutput()

    // 5. Handle result
    if err != nil {
        if hook.Required {
            return fmt.Errorf("required hook %s failed: %w\nOutput: %s", hook.Name, err, output)
        } else {
            he.logger.Printf("WARNING: Optional hook %s failed: %v\nOutput: %s", hook.Name, err, output)
        }
    }

    return nil
}

func expandTemplateVars(tmpl string, vars HookVariables) string {
    result := tmpl
    result = strings.ReplaceAll(result, "{{cluster_name}}", vars.ClusterName)
    result = strings.ReplaceAll(result, "{{current_version}}", vars.CurrentVersion)
    result = strings.ReplaceAll(result, "{{target_version}}", vars.TargetVersion)
    result = strings.ReplaceAll(result, "{{node_host}}", vars.NodeHost)
    result = strings.ReplaceAll(result, "{{node_port}}", vars.NodePort)
    // ... etc
    return result
}
```

### Hook Invocation Points

```go
func (u *Upgrader) Execute(plan *UpgradePlan, opts UpgradeOptions) error {
    // Pre-upgrade hooks
    if err := u.executeHooks("pre-upgrade", baseVars); err != nil {
        return err
    }

    // For each phase
    for _, phase := range plan.Phases {
        // Pre-phase hooks
        phaseVars := baseVars
        phaseVars.Phase = phase.Name
        if err := u.executeHooks("pre-phase", phaseVars); err != nil {
            return err
        }

        // For each node in phase
        for _, node := range phase.Nodes {
            // Pre-node hooks
            nodeVars := phaseVars
            nodeVars.NodeHost = node.Host
            nodeVars.NodePort = node.Port
            if err := u.executeHooks("pre-node", nodeVars); err != nil {
                return err
            }

            // Upgrade node
            if err := u.upgradeNode(node); err != nil {
                return err
            }

            // Post-node hooks
            if err := u.executeHooks("post-node", nodeVars); err != nil {
                return err
            }
        }

        // Post-phase hooks
        if err := u.executeHooks("post-phase", phaseVars); err != nil {
            return err
        }
    }

    // Post-upgrade hooks
    if err := u.executeHooks("post-upgrade", baseVars); err != nil {
        return err
    }

    return nil
}
```

---

## Health Verification

### Replica Set Health Check

```go
type ReplicaSetHealth struct {
    Members       []MemberHealth
    HasPrimary    bool
    PrimaryName   string
    AllHealthy    bool
    MaxLag        time.Duration
}

type MemberHealth struct {
    Name       string
    State      string  // PRIMARY, SECONDARY, ARBITER, etc.
    Health     int     // 0 = down, 1 = up
    Optime     time.Time
    Lag        time.Duration
}

func (hc *HealthChecker) CheckReplicaSet(ctx context.Context, nodes []Node) (*ReplicaSetHealth, error) {
    // 1. Connect to any node
    client, err := mongo.Connect(ctx, options.Client().ApplyURI(nodes[0].URI()))

    // 2. Run replSetGetStatus
    var status struct {
        Members []struct {
            Name       string    `bson:"name"`
            StateStr   string    `bson:"stateStr"`
            Health     int       `bson:"health"`
            OptimeDate time.Time `bson:"optimeDate"`
        } `bson:"members"`
    }
    err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}).Decode(&status)

    // 3. Analyze health
    health := &ReplicaSetHealth{Members: make([]MemberHealth, 0, len(status.Members))}
    var primaryOptime time.Time

    for _, m := range status.Members {
        if m.StateStr == "PRIMARY" {
            health.HasPrimary = true
            health.PrimaryName = m.Name
            primaryOptime = m.OptimeDate
        }

        health.Members = append(health.Members, MemberHealth{
            Name:   m.Name,
            State:  m.StateStr,
            Health: m.Health,
            Optime: m.OptimeDate,
        })
    }

    // 4. Calculate lag
    for i := range health.Members {
        if health.Members[i].State == "SECONDARY" {
            lag := primaryOptime.Sub(health.Members[i].Optime)
            health.Members[i].Lag = lag
            if lag > health.MaxLag {
                health.MaxLag = lag
            }
        }
    }

    // 5. Determine overall health
    health.AllHealthy = health.HasPrimary
    for _, m := range health.Members {
        if m.Health != 1 {
            health.AllHealthy = false
        }
        if m.State != "PRIMARY" && m.State != "SECONDARY" && m.State != "ARBITER" {
            health.AllHealthy = false
        }
    }

    return health, nil
}
```

### Health Check Gates

```go
func (u *Upgrader) upgradeReplicaSet(rs ReplicaSet) error {
    // Upgrade secondaries
    secondaries := rs.GetSecondaries()
    for _, secondary := range secondaries {
        // Pre-node health check
        health, err := u.healthChecker.CheckReplicaSet(ctx, rs.Nodes)
        if !health.AllHealthy {
            return fmt.Errorf("replica set not healthy before upgrading %s", secondary.Name)
        }

        // Upgrade node
        if err := u.upgradeNode(secondary); err != nil {
            return err
        }

        // Wait for node to return to SECONDARY
        if err := u.waitForState(secondary, "SECONDARY", 2*time.Minute); err != nil {
            return err
        }

        // Post-node health check
        health, err = u.healthChecker.CheckReplicaSet(ctx, rs.Nodes)
        if !health.AllHealthy || health.MaxLag > 30*time.Second {
            return fmt.Errorf("replica set unhealthy or high lag after upgrading %s", secondary.Name)
        }
    }

    // Step down primary
    primary := rs.GetPrimary()
    if err := u.stepDownPrimary(primary); err != nil {
        return err
    }

    // Wait for new primary
    if err := u.waitForPrimaryElection(rs, 30*time.Second); err != nil {
        return err
    }

    // Upgrade former primary (now secondary)
    if err := u.upgradeNode(primary); err != nil {
        return err
    }

    // Final health check
    health, err := u.healthChecker.CheckReplicaSet(ctx, rs.Nodes)
    if !health.AllHealthy {
        return fmt.Errorf("replica set not healthy after upgrade")
    }

    return nil
}
```

---

## Rollback Mechanism

### Rollback Requirements
- Only works before FCV upgrade
- Reverses upgrade order (mongos → shards → config)
- Uses symlink switching for fast recovery

### Rollback Process

```go
func (u *Upgrader) Rollback(clusterName string) error {
    // 1. Load metadata
    meta, err := u.metaManager.Load(clusterName)

    // 2. Check if rollback is possible
    if meta.UpgradeState != nil && meta.UpgradeState.FCVUpgraded {
        return fmt.Errorf("cannot rollback after FCV upgrade - requires MongoDB support assistance")
    }

    // 3. Verify previous version exists
    clusterDir := filepath.Join(storage, "clusters", clusterName)
    previousTarget, err := os.Readlink(filepath.Join(clusterDir, "previous"))
    if err != nil || previousTarget == "" {
        return fmt.Errorf("no previous version available for rollback")
    }

    // 4. Confirm with user
    fmt.Printf("Rolling back %s from %s to %s\n", clusterName, meta.Version, previousTarget)
    if !confirmAction() {
        return fmt.Errorf("rollback cancelled by user")
    }

    // 5. Execute rollback in reverse order
    if meta.Topology.Type == "sharded" {
        // 5a. Rollback mongos instances
        for _, mongos := range meta.Topology.Mongos {
            if err := u.rollbackNode(mongos, previousTarget); err != nil {
                return err
            }
        }

        // 5b. Rollback shards (reverse order)
        for i := len(meta.Topology.Shards) - 1; i >= 0; i-- {
            shard := meta.Topology.Shards[i]
            if err := u.rollbackReplicaSet(shard, previousTarget); err != nil {
                return err
            }
        }

        // 5c. Rollback config servers
        if err := u.rollbackReplicaSet(meta.Topology.ConfigServers, previousTarget); err != nil {
            return err
        }
    } else {
        // Rollback single replica set
        if err := u.rollbackReplicaSet(meta.Topology.ReplicaSet, previousTarget); err != nil {
            return err
        }
    }

    // 6. Update current symlink
    if err := u.versionManager.ActivateVersion(clusterName, previousTarget); err != nil {
        return err
    }

    // 7. Update metadata
    meta.Version = extractVersionFromPath(previousTarget)
    meta.UpgradeState = nil
    if err := u.metaManager.Save(meta); err != nil {
        return err
    }

    fmt.Printf("Rollback completed successfully\n")
    return nil
}

func (u *Upgrader) rollbackNode(node Node, targetVersion string) error {
    // 1. Stop process
    if err := u.executor.StopProcess(node.PID); err != nil {
        return err
    }

    // 2. Symlink already points to target via current->previous
    //    (just restart with previous binary)

    // 3. Start process with previous binary
    binPath := filepath.Join(clusterDir, "previous", "bin", node.Type)
    confPath := filepath.Join(clusterDir, "previous", "conf", fmt.Sprintf("%s-%d.conf", node.Type, node.Port))

    pid, err := u.executor.StartProcess(binPath, []string{"-f", confPath})
    if err != nil {
        return err
    }

    // 4. Update PID in metadata
    node.PID = pid

    // 5. Verify process health
    time.Sleep(2 * time.Second)
    if !u.executor.IsProcessRunning(pid) {
        return fmt.Errorf("process failed to start after rollback")
    }

    return nil
}
```

### Symlink Rollback Illustration

**Before Rollback**:
```
current -> versions/mongo-7.0.0/
previous -> versions/mongo-6.0.15/
```

**After Rollback**:
```
current -> versions/mongo-6.0.15/
previous -> versions/mongo-6.0.15/  # Still points to same
```

**Alternative After Rollback** (preserve upgrade for retry):
```
current -> versions/mongo-6.0.15/
previous -> versions/mongo-7.0.0/  # Swap for potential re-upgrade
```

---

## Integration with Existing Components

### Metadata Extension

```go
// In pkg/meta/meta.go
type ClusterMetadata struct {
    // Existing fields
    Name       string
    User       string
    Version    string  // Now stores full version (mongo-7.0.0)
    Topology   TopologyMetadata
    CreatedAt  time.Time

    // New fields for upgrade support
    Variant       string              `yaml:"variant"`        // [UPG-002] "mongo" or "percona"
    UpgradeState  *UpgradeState       `yaml:"upgrade_state,omitempty"`  // [UPG-011]
    SafetyHooks   []HookConfig        `yaml:"safety_hooks,omitempty"`   // [UPG-009]
}

type UpgradeState struct {
    FromVersion    string            `yaml:"from_version"`
    ToVersion      string            `yaml:"to_version"`
    CurrentPhase   string            `yaml:"current_phase"`
    CompletedNodes []string          `yaml:"completed_nodes"`
    PendingNodes   []string          `yaml:"pending_nodes"`
    FCVUpgraded    bool              `yaml:"fcv_upgraded"`
    StartedAt      time.Time         `yaml:"started_at"`
    LastUpdated    time.Time         `yaml:"last_updated"`
}
```

### Binary Manager Extension

```go
// In pkg/deploy/binary_manager.go
type BinaryManager struct {
    logger      *log.Logger
    storageDir  string
    httpClient  *http.Client
}

// [UPG-002] Variant-aware download
func (bm *BinaryManager) Download(variant, version, platform, arch string) (string, error) {
    fullVersion := fmt.Sprintf("%s-%s", variant, version)
    packageDir := filepath.Join(bm.storageDir, "packages", fullVersion)

    // Check if already downloaded
    if exists(filepath.Join(packageDir, "bin", "mongod")) {
        return packageDir, nil
    }

    // Build download URL based on variant
    var downloadURL string
    switch variant {
    case "mongo":
        downloadURL = bm.buildMongoURL(version, platform, arch)
    case "percona":
        downloadURL = bm.buildPerconaURL(version, platform, arch)
    default:
        return "", fmt.Errorf("unknown variant: %s", variant)
    }

    // Download and extract
    if err := bm.downloadAndExtract(downloadURL, packageDir); err != nil {
        return "", err
    }

    // Verify binaries
    if err := bm.verifyBinaries(packageDir); err != nil {
        return "", err
    }

    return packageDir, nil
}

func (bm *BinaryManager) buildPerconaURL(version, platform, arch string) string {
    // Percona download URL format
    // https://downloads.percona.com/downloads/percona-server-mongodb-{major.minor}/percona-server-mongodb-{version}/binary/{platform}/percona-server-mongodb-{version}-{platform}-{arch}.tar.gz

    majorMinor := extractMajorMinor(version)  // "7.0"

    return fmt.Sprintf(
        "https://downloads.percona.com/downloads/percona-server-mongodb-%s/percona-server-mongodb-%s/binary/%s/percona-server-mongodb-%s-%s-%s.tar.gz",
        majorMinor, version, platform, version, platform, arch,
    )
}
```

### Supervisor Integration

```go
// Processes reference current symlink
func (d *Deployer) generateSupervisorConfig(node Node, clusterDir string) string {
    binPath := filepath.Join(clusterDir, "current", "bin", node.Type)  // Follows symlink
    confPath := filepath.Join(clusterDir, "current", "conf", fmt.Sprintf("%s-%d.conf", node.Type, node.Port))
    logPath := filepath.Join(clusterDir, "current", "logs", fmt.Sprintf("%s-%d.log", node.Type, node.Port))

    return fmt.Sprintf(`[program:%s-%d]
command=%s -f %s
autostart=false
autorestart=true
redirect_stderr=true
stdout_logfile=%s
`, node.Type, node.Port, binPath, confPath, logPath)
}

// Symlink switch automatically changes process binaries on restart
```

---

## Data Structures

### Upgrade Plan
```go
type UpgradePlan struct {
    ClusterName    string
    FromVersion    FullVersion
    ToVersion      FullVersion
    Phases         []UpgradePhase
    EstimatedTime  time.Duration
    RollbackPlan   string
}

type UpgradePhase struct {
    Name          string       // "config", "shard-0", "shard-1", "mongos"
    Type          string       // "replica_set", "mongos"
    Nodes         []Node
    Parallel      bool
    EstimatedTime time.Duration
}
```

### Hook Configuration
```go
type HookConfig struct {
    Name     string   `yaml:"name"`
    Script   string   `yaml:"script"`
    Args     []string `yaml:"args"`
    Timeout  int      `yaml:"timeout"`   // seconds
    Required bool     `yaml:"required"`  // fail upgrade if hook fails
}

type HookVariables struct {
    ClusterName    string
    CurrentVersion string
    TargetVersion  string
    Variant        string
    Phase          string
    NodeHost       string
    NodePort       string
    NodeType       string
    ReplicaSet     string
}
```

---

## Future Enhancements

1. **Remote SSH Upgrades**: Extend executor interface for remote operations
2. **Automated Backups**: Integrate backup before upgrade
3. **Canary Upgrades**: Upgrade one shard first, monitor, then proceed
4. **Upgrade Simulation**: Dry-run with validation checks only
5. **Cross-Variant Migration**: Convert mongo cluster to percona
6. **Progressive Rollout**: Percentage-based upgrades
7. **Health Metrics Collection**: Detailed metrics during upgrade
8. **Notification Integration**: Slack/email notifications on completion/failure

---

## Summary

This architecture provides:
- ✅ **Fast Rollback**: Symlink switching + restart (< 1 minute)
- ✅ **Zero Reinstallation**: Binaries cached, configs preserved
- ✅ **Extensible Safety**: Custom hooks at all critical points
- ✅ **Clear Auditability**: Version history in directories and logs
- ✅ **Resumable Operations**: State tracking enables retry
- ✅ **Multi-Variant Support**: MongoDB and Percona with unified interface
- ✅ **TDD Ready**: Clear interfaces and testable components
- ✅ **Production Safe**: Health gates, phased execution, validation

The design balances safety, speed, and flexibility while maintaining mup's philosophy of simplicity and local-first operation.
