# Supervisord Integration Plan

## Overview

This document outlines the plan to integrate [github.com/ochinchina/supervisord](https://github.com/ochinchina/supervisord) as a **library** (not binary) for managing local MongoDB cluster processes. This will replace the current manual PID tracking approach with a robust process supervisor that provides automatic restarts, better monitoring, and unified logging.

## Current State vs. Proposed State

### Current Approach
- **Process Management**: Manual `exec.Command()` with background processes
- **PID Tracking**: PIDs stored in `meta.yaml`, manually tracked
- **Process Control**: Direct signal sending (SIGINT, SIGKILL)
- **Limitations**:
  - No automatic restart on crash
  - Manual log file management
  - No process health monitoring
  - Complex process lifecycle tracking
  - No process groups

### Proposed Approach (with supervisord)
- **Process Management**: Supervisord library manages all MongoDB processes
- **PID Tracking**: Supervisord handles internally
- **Process Control**: Unified API (`Start`, `Stop`, `Restart`, `Status`)
- **Benefits**:
  - ✅ Automatic restart on crash (configurable)
  - ✅ Unified log management
  - ✅ Process health monitoring
  - ✅ Process groups (e.g., all mongod, all config servers)
  - ✅ HTTP/XML-RPC API for management
  - ✅ Prometheus metrics endpoint
  - ✅ Event system for process state changes

## Architecture

### Directory Structure

```
~/.mup/storage/clusters/<cluster-name>/
├── meta.yaml                       # Cluster metadata
├── supervisor.ini                  # Main supervisord config
├── supervisor.pid                  # Supervisord daemon PID
├── supervisor.log                  # Supervisord daemon log
├── conf/
│   ├── localhost-30000/
│   │   ├── mongod.conf            # MongoDB config (existing)
│   │   └── supervisor-mongod.ini  # Supervisord program config
│   ├── localhost-30001/
│   │   ├── mongod.conf
│   │   └── supervisor-mongod.ini
│   └── localhost-30002/
│       ├── mongod.conf
│       └── supervisor-mongod.ini
├── data/                           # MongoDB data (existing)
├── logs/
│   ├── supervisor-mongod-30000.log  # Managed by supervisord
│   ├── supervisor-mongod-30001.log
│   └── supervisor-mongod-30002.log
```

### Configuration File Format

#### Main Supervisord Config (`supervisor.ini`)

```ini
[supervisord]
logfile = ~/.mup/storage/clusters/<cluster-name>/supervisor.log
loglevel = info
pidfile = ~/.mup/storage/clusters/<cluster-name>/supervisor.pid
nodaemon = false

[inet_http_server]
port = 127.0.0.1:9001  # For programmatic control

[include]
files = conf/*/supervisor-*.ini
```

#### Per-Node Program Config (`supervisor-mongod.ini`)

```ini
[program:mongod-30000]
command = /path/to/mongodb-7.0/bin/mongod --config /path/to/mongod.conf
directory = ~/.mup/storage/clusters/<cluster-name>/data/localhost-30000
autostart = false                # Don't auto-start (mup controls this)
autorestart = unexpected          # Restart on crash, not on clean shutdown
startsecs = 5                     # Process must stay up 5s to be "started"
startretries = 3                  # Max restart attempts
stdout_logfile = ~/.mup/storage/clusters/<cluster-name>/logs/supervisor-mongod-30000.log
stderr_logfile = ~/.mup/storage/clusters/<cluster-name>/logs/supervisor-mongod-30000.log
stdout_logfile_maxbytes = 50MB
stdout_logfile_backups = 10
stopwaitsecs = 30                 # Wait 30s for graceful shutdown
stopsignal = INT                  # Use SIGINT for graceful shutdown

[group:mongod-servers]
programs = mongod-30000,mongod-30001,mongod-30002

[program:mongos-27017]
command = /path/to/mongodb-7.0/bin/mongos --config /path/to/mongos.conf
# ... similar config

[group:mongos-routers]
programs = mongos-27017

[program:config-server-27019]
command = /path/to/mongodb-7.0/bin/mongod --config /path/to/config-server.conf
# ... similar config

[group:config-servers]
programs = config-server-27019
```

### Key Configuration Parameters

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| `autostart` | `false` | Mup explicitly controls when to start processes |
| `autorestart` | `unexpected` | Auto-restart on crash, not on clean shutdown |
| `startsecs` | `5` | MongoDB needs a few seconds to initialize |
| `startretries` | `3` | Allow multiple restart attempts on failure |
| `stopwaitsecs` | `30` | Give MongoDB time for graceful shutdown |
| `stopsignal` | `INT` | Use SIGINT (not TERM) for MongoDB |

## Implementation Plan

### Phase 1: Core Infrastructure (pkg/supervisor/)

Create a new package to wrap supervisord library functionality.

#### Files to Create:
- `pkg/supervisor/manager.go` - Supervisord wrapper
- `pkg/supervisor/config.go` - Config file generation
- `pkg/supervisor/process.go` - Process control API

#### Manager Interface:

```go
package supervisor

import (
    "context"
    "github.com/ochinchina/supervisord"
    "github.com/ochinchina/supervisord/config"
)

// Manager wraps supervisord library for managing MongoDB processes
type Manager struct {
    supervisor  *supervisord.Supervisor
    configPath  string
    clusterName string
    running     bool
}

// NewManager creates a new supervisord manager for a cluster
func NewManager(clusterDir, clusterName string) (*Manager, error) {
    // Create supervisor.ini in cluster directory
    // Initialize supervisord.Supervisor
}

// Start starts the supervisord daemon
func (m *Manager) Start(ctx context.Context) error {
    // Start supervisord daemon in background
    // Wait for it to be ready
}

// Stop stops the supervisord daemon
func (m *Manager) Stop(ctx context.Context) error {
    // Gracefully shutdown all processes
    // Stop supervisord daemon
}

// AddProcess adds a MongoDB process to supervision
func (m *Manager) AddProcess(name, command, workDir, logFile string) error {
    // Generate supervisor-<name>.ini
    // Reload supervisord to pick up new config
}

// StartProcess starts a specific process
func (m *Manager) StartProcess(name string) error {
    // Use supervisord API to start process
}

// StopProcess stops a specific process
func (m *Manager) StopProcess(name string) error {
    // Use supervisord API to stop process
}

// GetProcessStatus returns status of a process
func (m *Manager) GetProcessStatus(name string) (*ProcessStatus, error) {
    // Query supervisord for process state
}

// StartGroup starts all processes in a group
func (m *Manager) StartGroup(groupName string) error {
    // Start process group (e.g., "mongod-servers")
}

// StopGroup stops all processes in a group
func (m *Manager) StopGroup(groupName string) error {
    // Stop process group
}

// ProcessStatus represents the state of a supervised process
type ProcessStatus struct {
    Name        string
    State       string  // RUNNING, STOPPED, STARTING, FATAL, etc.
    PID         int
    Uptime      int64
    Description string
}
```

### Phase 2: Config Generation (pkg/supervisor/config.go)

```go
package supervisor

import (
    "text/template"
    "github.com/zph/mup/pkg/topology"
    "github.com/zph/mup/pkg/meta"
)

// ConfigGenerator generates supervisord config files
type ConfigGenerator struct {
    clusterDir  string
    clusterName string
    topology    *topology.Topology
    version     string
    binPath     string
}

// GenerateMainConfig creates supervisor.ini
func (g *ConfigGenerator) GenerateMainConfig() error {
    // Create supervisor.ini with:
    // - logfile path
    // - pidfile path
    // - inet_http_server config
    // - include directive for per-node configs
}

// GenerateMongodConfig creates supervisor config for a mongod node
func (g *ConfigGenerator) GenerateMongodConfig(node topology.MongodNode) error {
    // Create supervisor-mongod-<port>.ini with:
    // - command: /path/to/mongod --config /path/to/mongod.conf
    // - directory: data directory
    // - log file paths
    // - process parameters
}

// GenerateMongosConfig creates supervisor config for a mongos router
func (g *ConfigGenerator) GenerateMongosConfig(node topology.MongosNode) error {
    // Similar to GenerateMongodConfig
}

// GenerateConfigServerConfig creates config for config server
func (g *ConfigGenerator) GenerateConfigServerConfig(node topology.ConfigServerNode) error {
    // Similar to GenerateMongodConfig
}

// GenerateAllConfigs creates all supervisor configs for a cluster
func (g *ConfigGenerator) GenerateAllConfigs() error {
    // 1. Generate main supervisor.ini
    // 2. Generate per-node configs
    // 3. Define process groups
}
```

### Phase 3: Integration with Deployment (pkg/deploy/)

Update deployment phases to use supervisord.

#### Changes to `pkg/deploy/deploy.go`:

```go
// OLD: Start processes manually
func (d *Deployer) startMongodProcess(node topology.MongodNode) (int, error) {
    cmd := exec.CommandContext(d.ctx, mongodPath, "--config", configPath)
    cmd.Start()
    return cmd.Process.Pid, nil
}

// NEW: Add to supervisord and start
func (d *Deployer) startMongodProcess(node topology.MongodNode) error {
    // 1. Generate supervisor config for this node
    configGen := supervisor.NewConfigGenerator(...)
    configGen.GenerateMongodConfig(node)

    // 2. Reload supervisord to pick up new config
    d.supervisorMgr.Reload()

    // 3. Start the process
    processName := fmt.Sprintf("mongod-%d", node.Port)
    return d.supervisorMgr.StartProcess(processName)
}
```

#### Changes to `pkg/deploy/prepare.go`:

```go
func (d *Deployer) Prepare(ctx context.Context) error {
    // ... existing preparation ...

    // NEW: Initialize supervisord for this cluster
    d.supervisorMgr, err = supervisor.NewManager(d.clusterDir, d.clusterName)
    if err != nil {
        return fmt.Errorf("failed to create supervisor manager: %w", err)
    }

    // Start supervisord daemon
    if err := d.supervisorMgr.Start(ctx); err != nil {
        return fmt.Errorf("failed to start supervisord: %w", err)
    }

    return nil
}
```

### Phase 4: Update Cluster Operations (pkg/cluster/)

#### Changes to `pkg/cluster/manager.go`:

```go
// Start starts a stopped cluster
func (m *Manager) Start(clusterName string) error {
    // OLD: Find PIDs in metadata, check if running, start if needed
    // NEW:

    // 1. Load supervisor manager
    supervisorMgr, err := supervisor.LoadManager(clusterDir, clusterName)
    if err != nil {
        return fmt.Errorf("failed to load supervisor: %w", err)
    }

    // 2. Start supervisord daemon if not running
    if !supervisorMgr.IsRunning() {
        supervisorMgr.Start(ctx)
    }

    // 3. Start all processes in appropriate order
    // First: config servers
    supervisorMgr.StartGroup("config-servers")

    // Second: mongod servers
    supervisorMgr.StartGroup("mongod-servers")

    // Third: mongos routers
    supervisorMgr.StartGroup("mongos-routers")

    return nil
}

// Stop stops a running cluster
func (m *Manager) Stop(clusterName string) error {
    // NEW:

    // 1. Load supervisor manager
    supervisorMgr, err := supervisor.LoadManager(clusterDir, clusterName)

    // 2. Stop all processes (supervisord handles graceful shutdown)
    supervisorMgr.StopGroup("mongos-routers")
    supervisorMgr.StopGroup("mongod-servers")
    supervisorMgr.StopGroup("config-servers")

    // 3. Stop supervisord daemon
    supervisorMgr.Stop(ctx)

    return nil
}
```

#### Changes to `pkg/cluster/display.go`:

```go
func (m *Manager) Display(clusterName string) error {
    // NEW: Get process status from supervisord

    supervisorMgr, err := supervisor.LoadManager(clusterDir, clusterName)

    for _, process := range supervisorMgr.GetAllProcesses() {
        status := supervisorMgr.GetProcessStatus(process.Name)

        fmt.Printf("  - %s [%s] PID: %d Uptime: %s\n",
            process.Name,
            status.State,
            status.PID,
            formatUptime(status.Uptime))
    }

    return nil
}
```

### Phase 5: Metadata Updates (pkg/meta/)

#### Changes to `pkg/meta/meta.go`:

```go
// OLD metadata structure
type ClusterMeta struct {
    Name    string
    Nodes   []NodeMeta
}

type NodeMeta struct {
    Host string
    Port int
    PID  int  // REMOVE: No longer track PIDs manually
}

// NEW metadata structure
type ClusterMeta struct {
    Name              string
    Nodes             []NodeMeta
    SupervisorEnabled bool   `yaml:"supervisor_enabled"`  // NEW
    SupervisorPort    int    `yaml:"supervisor_port"`     // NEW: HTTP API port
}

type NodeMeta struct {
    Host           string
    Port           int
    SupervisorName string  // NEW: Name in supervisord (e.g., "mongod-30000")
    // PID removed - supervisord tracks this
}
```

### Phase 6: Playground Updates (pkg/playground/)

The playground should also use supervisord for consistency.

```go
func (m *Manager) Start(version string) error {
    // Use same deployment flow as cluster deploy
    // But with fixed "playground" cluster name

    deployer := deploy.NewDeployer(...)
    deployer.Deploy(ctx)  // This now uses supervisord internally
}
```

## Configuration Template

### Template for Mongod Program

```go
const mongodProgramTemplate = `
[program:{{.Name}}]
command = {{.BinPath}}/mongod --config {{.ConfigPath}}
directory = {{.DataDir}}
autostart = false
autorestart = unexpected
startsecs = 5
startretries = 3
stdout_logfile = {{.LogFile}}
stderr_logfile = {{.LogFile}}
stdout_logfile_maxbytes = 50MB
stdout_logfile_backups = 10
stopwaitsecs = 30
stopsignal = INT
environment = HOME="{{.HomeDir}}",USER="{{.User}}"
{{if .ReplicaSet}}
; Replica Set: {{.ReplicaSet}}
{{end}}
`
```

## Migration Strategy

### For Existing Clusters

1. **Detection**: Check if cluster was created with old (PID-based) approach
   - Look for `supervisor_enabled: false` or absence of field in meta.yaml

2. **Migration Command**: `mup cluster migrate <name>`
   ```go
   func (m *Manager) Migrate(clusterName string) error {
       // 1. Stop cluster using old method (send signals to PIDs)
       m.stopOldStyle(clusterName)

       // 2. Generate supervisord configs
       generateSupervisorConfigs(clusterName)

       // 3. Start supervisord
       supervisorMgr.Start(ctx)

       // 4. Start all processes via supervisord
       supervisorMgr.StartGroup("all")

       // 5. Update metadata
       meta.SupervisorEnabled = true
       meta.Save()
   }
   ```

3. **Backward Compatibility**: Keep old PID-based code for reading old clusters
   ```go
   if meta.SupervisorEnabled {
       // Use new supervisord approach
       return m.startWithSupervisor(clusterName)
   } else {
       // Use old PID approach (deprecated)
       return m.startLegacy(clusterName)
   }
   ```

### For New Clusters

All new clusters created after this feature will automatically use supervisord.

## Testing Strategy

### Unit Tests

```go
// pkg/supervisor/manager_test.go
func TestManager_Start(t *testing.T) {
    mgr := NewManager(tempDir, "test-cluster")
    err := mgr.Start(context.Background())
    assert.NoError(t, err)
    assert.True(t, mgr.IsRunning())
}

func TestManager_AddProcess(t *testing.T) {
    mgr := NewManager(tempDir, "test-cluster")
    mgr.Start(context.Background())

    err := mgr.AddProcess("test-proc", "/bin/sleep 100", "/tmp", "/tmp/test.log")
    assert.NoError(t, err)

    err = mgr.StartProcess("test-proc")
    assert.NoError(t, err)

    status, _ := mgr.GetProcessStatus("test-proc")
    assert.Equal(t, "RUNNING", status.State)
}
```

### Integration Tests

```go
// pkg/deploy/deploy_supervisor_test.go
func TestDeploy_WithSupervisor(t *testing.T) {
    // Test full deployment workflow with supervisord
    // 1. Deploy 3-node replica set
    // 2. Verify all processes managed by supervisord
    // 3. Stop one node
    // 4. Verify supervisord auto-restarts it
    // 5. Stop cluster
    // 6. Verify clean shutdown
}
```

### Manual Testing Checklist

- [ ] Deploy new cluster - verify supervisord running
- [ ] Check `supervisor.ini` generated correctly
- [ ] Check per-node configs generated
- [ ] Verify process groups defined
- [ ] Start cluster - all processes come up
- [ ] Stop cluster - clean shutdown
- [ ] Kill a mongod process - verify auto-restart
- [ ] Check logs in supervisor-managed log files
- [ ] Use `mup cluster display` - shows correct status
- [ ] HTTP API accessible at `http://localhost:9001`
- [ ] Migrate old cluster - works correctly

## Benefits

### Operational Benefits

1. **Automatic Recovery**: Crashed processes automatically restart
2. **Unified Logging**: All process logs in one place, rotated automatically
3. **Process Groups**: Start/stop related processes together
4. **Health Monitoring**: Know immediately when a process crashes
5. **Metrics**: Prometheus endpoint for monitoring integration

### Development Benefits

1. **Simpler Code**: Remove manual PID tracking, signal handling
2. **Better Testing**: Mock supervisord for tests
3. **Consistency**: Same process management for playground and production clusters
4. **Extensibility**: Easy to add new process types (mongosync, backup agents, etc.)

### Future Enhancements

1. **Resource Limits**: Set memory/CPU limits per process via supervisord
2. **Priority**: Start processes in specific order
3. **Dependencies**: Express process dependencies
4. **Events**: React to process state changes (notifications, healing)
5. **Remote Management**: XML-RPC API for remote cluster management

## Alternatives Considered

### 1. systemd
- **Pros**: Native to Linux, widely used
- **Cons**: macOS incompatible, requires root, complex setup

### 2. Docker Compose
- **Pros**: Popular, declarative
- **Cons**: Docker dependency, overkill for local dev

### 3. Manual PID Tracking (Current)
- **Pros**: Simple, no dependencies
- **Cons**: No auto-restart, manual log management, fragile

### 4. supervisord (Chosen)
- **Pros**: Cross-platform, embeddable as library, rich features
- **Cons**: Additional dependency, learning curve

## Implementation Timeline

### Phase 1 (Week 1): Core Infrastructure
- [ ] Create `pkg/supervisor/` package
- [ ] Implement `Manager` interface
- [ ] Add supervisord dependency
- [ ] Basic config generation

### Phase 2 (Week 2): Deployment Integration
- [ ] Update `pkg/deploy/` to use supervisord
- [ ] Generate supervisor configs during deployment
- [ ] Test with simple replica set

### Phase 3 (Week 3): Cluster Operations
- [ ] Update `start`, `stop`, `display` commands
- [ ] Update metadata structure
- [ ] Backward compatibility for old clusters

### Phase 4 (Week 4): Testing & Polish
- [ ] Comprehensive tests
- [ ] Migration command
- [ ] Documentation updates
- [ ] Playground integration

## Dependencies

```go
// go.mod
require (
    github.com/ochinchina/supervisord v0.7.3
)
```

## Documentation Updates

### Files to Update:
- `README.md` - Mention supervisord-based process management
- `docs/DESIGN.md` - Architecture section
- `docs/TODO.md` - Mark supervisord tasks complete
- `CLAUDE.md` - Update implementation guidelines

### New Documentation:
- `docs/SUPERVISORD.md` - Detailed supervisord integration guide
- Add troubleshooting section for supervisord issues

## Rollout Plan

1. **Feature Flag**: Introduce `USE_SUPERVISOR` environment variable
   - Default: `false` (use old approach)
   - Set to `true` to enable supervisord

2. **Alpha Testing**: Test with playground only
   - Get comfortable with supervisord behavior
   - Identify edge cases

3. **Beta Testing**: Enable for new production clusters
   - Monitor for issues
   - Gather feedback

4. **Migration**: Provide migration path for existing clusters
   - Document migration steps
   - Automated migration command

5. **Default**: Make supervisord the default
   - Update documentation
   - Deprecate old PID-based approach

6. **Removal**: Eventually remove old code
   - After 2-3 releases
   - After all clusters migrated

## Risk Mitigation

### Risk: Supervisord crashes
- **Mitigation**: Supervisord is stable, but have fallback to manual start

### Risk: Performance overhead
- **Mitigation**: Supervisord is lightweight, minimal overhead expected

### Risk: Breaking changes
- **Mitigation**: Maintain backward compatibility for at least 2 releases

### Risk: Complex debugging
- **Mitigation**: Detailed logging, supervisor status commands

## Success Criteria

1. ✅ All new clusters use supervisord
2. ✅ Existing clusters can be migrated
3. ✅ Auto-restart works reliably
4. ✅ No regression in cluster start/stop time
5. ✅ Logs are easily accessible
6. ✅ `mup cluster display` shows supervisor status
7. ✅ HTTP API accessible for advanced users
8. ✅ All tests passing

## Conclusion

Integrating supervisord as a library provides significant benefits for process management in Mup. By using a battle-tested process supervisor, we gain automatic restarts, unified logging, process grouping, and a foundation for future enhancements like resource limits and remote management.

The phased approach ensures we can adopt this incrementally with minimal risk, maintaining backward compatibility while building towards a more robust process management solution.
