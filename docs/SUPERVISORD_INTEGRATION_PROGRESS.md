# Supervisord Integration Progress

This document tracks the integration of supervisord into mup's cluster management.

## Phase 1: Core Infrastructure ✅ COMPLETE

- [x] Add supervisord dependency to go.mod
- [x] Create pkg/supervisor/ package
  - [x] manager.go - Core Manager interface for supervisord operations
  - [x] config.go - Configuration file generation
  - [x] manager_test.go - Unit tests
- [x] All tests passing

## Phase 2: Deployment Integration ✅ COMPLETE

### Step 1: Update Metadata Structure ✅
- [x] Add supervisor fields to meta.ClusterMetadata
  - [x] SupervisorConfigPath - Path to supervisor.ini
  - [x] SupervisorPIDFile - Path to supervisor.pid
  - [x] SupervisorRunning - Boolean flag
- [x] Update meta.NodeMetadata
  - [x] Deprecate PID field (supervisord manages PIDs now)
  - [x] Add SupervisorProgramName field
  - [x] Add SupervisorConfigFile field
- [x] Update finalize.go metadata types to match

### Step 2: Modify Deployer ✅
- [x] Add supervisor.Manager field to Deployer struct
- [x] Initialize supervisor.Manager in NewDeployer
- [x] Add supervisor config generation to deployment workflow
  - [x] Call supervisor.ConfigGenerator in deploy phase
  - [x] Generate main supervisor.ini
  - [x] Generate per-node supervisor configs

### Step 3: Replace Process Management ✅
- [x] Replace direct process starting with supervisor operations
  - [x] Removed old startMongod(), startMongos(), startConfigServer() functions
  - [x] Use supervisor.StartProcess() for all node types
  - [x] Start supervisord daemon during deployment
- [x] Update mongos starting in initialize.go
  - [x] Use supervisor.StartProcess() for mongos nodes
- [x] Remove nodePIDs field from Deployer (breaking change)
- [x] Update metadata collection to use supervisor fields

### Step 4: Testing ✅
- [x] Test local deployment with supervisord
- [x] Verify processes start correctly
- [ ] Verify process lifecycle (start/stop/restart)
- [ ] Verify process monitoring and auto-restart
- [ ] Test cluster destroy cleanup

## Phase 3: Cluster Operations (PENDING)

- [ ] Update `mup cluster start` to use supervisor
- [ ] Update `mup cluster stop` to use supervisor
- [ ] Update `mup cluster restart` to use supervisor
- [ ] Update `mup cluster display` to show supervisor status
- [ ] Add `mup cluster reload` command for config reloads

## Phase 4: Migration and Documentation (PENDING)

- [ ] Create migration command for existing clusters
- [ ] Update user documentation
- [ ] Add troubleshooting guide for supervisor issues

## Implementation Notes

### Breaking Changes Made
1. **Removed `nodePIDs` field from Deployer** - Supervisor manages PIDs now
2. **Deprecated `PID` field in NodeMetadata** - Still present for backward compat but not set
3. **Removed manual process start functions** - All process lifecycle managed via supervisor

### Files Modified
- `pkg/supervisor/manager.go` - Core supervisor manager
- `pkg/supervisor/config.go` - Config file generation
- `pkg/supervisor/manager_test.go` - Unit tests
- `pkg/meta/meta.go` - Added supervisor fields to metadata
- `pkg/deploy/deployer.go` - Added supervisor.Manager field
- `pkg/deploy/deploy.go` - Replaced manual starting with supervisor, removed old functions
- `pkg/deploy/initialize.go` - Use supervisor for mongos
- `pkg/deploy/finalize.go` - Updated metadata to include supervisor fields

### How It Works
1. During deployment, a unified supervisor.ini is generated with all program definitions
2. Config includes [supervisord] section, [inet_http_server], and all [program:*] sections
3. Supervisor daemon starts during Phase 3 (Deploy) via `manager.Start()`
4. Individual MongoDB processes started via `manager.StartProcess(programName)`
5. Metadata tracks supervisor program names and config paths

### Important Discovery: Config Loading
The supervisord library does NOT properly expand `[include]` glob patterns when using `config.Load()`. Therefore, we generate a single unified config file with all program definitions written directly, rather than using includes. This ensures all programs are loaded correctly.

## Testing Results

**✅ Deployment Test**: Successfully deployed a 3-node replica set cluster (cluster-test)
- All 3 mongod processes started correctly via supervisor
- Unified config file generated with all program definitions
- Process status correctly shows "running"
- Cluster is functional

**Test Command**:
```bash
./bin/mup cluster deploy cluster-test examples/replica-set.yaml --version 3.6
```

**Results**:
- ✅ Supervisor daemon started
- ✅ All programs loaded (verified with logging)
- ✅ MongoDB processes running (verified with `ps aux`)
- ✅ Cluster display shows all nodes as running

## Next Steps

1. Update cluster operations (start/stop) to use supervisor
2. Test process lifecycle (restart, stop/start)
3. Add CLI commands under `mup cluster supervisor` for supervisor management:
   - `mup cluster supervisor status <cluster>` - Show supervisor status
   - `mup cluster supervisor reload <cluster>` - Reload supervisor config
   - `mup cluster supervisor logs <cluster> [program]` - View logs
4. Test auto-restart behavior
5. Test cluster destroy cleanup
