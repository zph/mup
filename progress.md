# Sharded Cluster E2E Test Progress

## Current Status
Working on fixing sharded cluster deployment bugs and creating e2e tests.

## Bugs Found and Fixed

### 1. Config Server Process Naming Mismatch
**Problem**: Config servers were being started with wrong supervisor process names
- Supervisor registered them as: `config-30000`, `config-30001`, `config-30002`
- Deploy code tried to start: `mongod-30000`, `mongod-30001`, `mongod-30002`

**Files Fixed**:
- `pkg/deploy/deploy.go:380` - Changed `mongod-%d` to `config-%d`
- `pkg/deploy/planner.go:542` - Changed `mongod-%d` to `config-%d`

### 2. Config Server Config File Name Mismatch
**Problem**: Config servers use different config filename than mongod
- Actual config file: `config.conf`
- Supervisor pointed to: `mongod.conf`

**Files Fixed**:
- `pkg/supervisor/config.go:235-240` - Added logic to use `config.conf` for config servers, `mongod.conf` for mongod

### 3. Mongos Naming
**Status**: ✅ Already correct
- Uses `mongos-%d` consistently everywhere
- No bugs found

## Completed: Path Consolidation ✅

### Problem
Program names, config file names, and paths were scattered across multiple files:
- `pkg/deploy/deploy.go` - Process starting logic
- `pkg/deploy/planner.go` - Plan generation logic
- `pkg/deploy/initialize.go` - Mongos starting logic
- `pkg/supervisor/config.go` - Supervisor config generation

This duplication led to the bugs above.

### Solution Implemented
Created a centralized naming package `pkg/naming` with helper functions:

```go
// pkg/naming/names.go
package naming

// GetProgramName returns the supervisor program name for a node
// Examples: "config-30000", "mongod-30100", "mongos-30300"
func GetProgramName(nodeType string, port int) string

// GetConfigFileName returns the config filename for a node type
// Examples: "config.conf" for config servers, "mongod.conf" for mongod, "mongos.conf" for mongos
func GetConfigFileName(nodeType string) string

// GetProcessDir returns the process directory name
// Examples: "config-30000", "mongod-30100", "mongos-30300"
func GetProcessDir(nodeType string, port int) string
```

### Files Refactored ✅
1. ✅ `pkg/deploy/deploy.go:381` - Now uses `naming.GetProgramName("config", port)`
2. ✅ `pkg/deploy/planner.go:543` - Now uses `naming.GetProgramName("config", port)`
3. ✅ `pkg/deploy/planner.go:596` - Now uses `naming.GetProgramName("mongod", port)`
4. ✅ `pkg/deploy/planner.go:653` - Now uses `naming.GetProgramName("mongos", port)`
5. ✅ `pkg/deploy/initialize.go:648` - Now uses `naming.GetProgramName("mongos", port)`
6. ✅ `pkg/supervisor/config.go:220-221` - Now uses `naming.GetProgramName()` and `naming.GetProcessDir()`
7. ✅ `pkg/supervisor/config.go:237` - Now uses `naming.GetConfigFileName(nodeType)`

### Testing ✅
- Created comprehensive unit tests in `pkg/naming/names_test.go`
- All tests pass (18 test cases covering all three functions)
- Project builds successfully with `make build`

### Additional Bug Fixes ✅
**Bug: Config server directories using wrong node type**
- **Problem**: Config server log/config directories were created with "mongod" type instead of "config" type
- **Fix 1**: `pkg/deploy/deploy.go:79-80` - Changed directory creation to use `getNodeLogDirWithType(..., "config")` and `getNodeConfigDirWithType(..., "config")`
- **Fix 2**: `pkg/deploy/deploy.go:256-257, 280` - Changed config generation to use correct type and file names (`config.conf`, `config.log`)
- **Impact**: Config servers now correctly create `config-XXXX` directories and use `config.conf` and `config.log` files

**Bug: Mongos supervisor config not using naming package**
- **Problem**: `generateMongosNodeSupervisorConf` function still used hardcoded naming
- **Fix**: `pkg/supervisor/config.go:266-267, 282` - Updated to use naming package functions
- **Impact**: Mongos supervisor configs now consistently use centralized naming

**Bug: Log file names included host and port**
- **Problem**: Operation handlers generated log names like `mongod-localhost-30300.log` instead of simple `mongod.log`
- **Fix**: `pkg/operation/handlers.go:740, 818` - Changed to use simple log names: `mongod.log`, `mongos.log`
- **Impact**: Consistent with deploy.go which uses simple names; easier to find logs

**Bug: Missing mongos_host parameter in addShard operation**
- **Problem**: AddShardHandler requires `mongos_host` parameter but planner wasn't providing it
- **Error**: `handler validation failed: [missing required parameter: mongos_host]`
- **Fix**: `pkg/deploy/planner.go:792-793, 809` - Added mongos_host parameter using first mongos node from topology
- **Impact**: Sharded cluster addShard operations now have required parameter for connecting to mongos

**Bug: Mongos config includes unsupported processManagement option**
- **Problem**: Mongos config always included empty `processManagement:` section, but mongos 3.6 doesn't support this option
- **Error**: `Unrecognized option: processManagement`
- **Fix ATTEMPTED**: `pkg/template/types.go:135` - Added `omitempty` tag to ProcessManagement field in MongosConfig
- **Fix FAILED**: `omitempty` doesn't work for struct types in Go YAML, only works for pointers/nil values
- **Fix CORRECTED**: `pkg/template/types.go:135` - Changed ProcessManagement from struct to pointer (*ProcessManagementConfig)
- **Additional Changes Needed**: Remove ProcessManagement initialization in:
  - `pkg/deploy/deploy.go:220-223` - Mongos config generation
  - `pkg/operation/handlers.go:832-834` - Operation handler mongos config
- **Impact**: Mongos configs will now properly omit processManagement section when nil (for mongos 3.6 compatibility)

**Bug: Config server log and PID files hardcoded to "mongod" names**
- **Problem**: Config server configs used "mongod.log" and "mongod.pid" instead of "config.log" and "config.pid"
- **Error**: `Failed to open "/path/to/mongod-30000/log/mongod.log"` (directory doesn't exist)
- **Fix**: `pkg/operation/handlers.go:740-741, 768, 819` - Simplified to use "process.log" and "process.pid" for all node types
- **Impact**: All nodes (mongod, config, mongos) use consistent "process.log" and "process.pid" names; folder context provides node type

## Test Status

### Configuration Created
- `/tmp/e2e-sharded-cluster.yaml` - Sharded cluster config with:
  - 3 config servers (ports 30000-30002)
  - 2 shards with 3 nodes each (ports 30100-30102, 30200-30202)
  - 2 mongos routers (ports 30300-30301)

### E2E Test File
- `test/e2e/sharded_cluster_test.go` - Already exists with comprehensive tests
  - `TestShardedClusterDeployment` - Full sharded cluster test
  - `TestMinimalShardedCluster` - Minimal sharded cluster test

### Current Deploy Status
After fixing the bugs, deployment gets further but may still have issues. Need to:
1. Complete the path consolidation refactoring
2. Clean up any stuck processes
3. Test full deployment end-to-end
4. Run the e2e tests

## Commands to Continue

```bash
# 1. Create the naming package
# See solution above

# 2. Refactor all files to use the naming package

# 3. Rebuild and test
make build

# 4. Clean up and deploy
pkill mongod; pkill mongos; sleep 2
rm -rf ~/.mup/storage/clusters/e2e-manual-test
./bin/mup cluster deploy e2e-manual-test /tmp/e2e-sharded-cluster.yaml --version 7.0.0 --auto-approve

# 5. Run e2e tests
go test -v -tags=e2e ./test/e2e -run TestShardedCluster
```

## Key Learnings

1. **Centralize naming logic** - Having program names scattered across files leads to bugs
2. **Node types matter** - Config servers, mongod, and mongos have different naming conventions
3. **Supervisor process names must match** - The name in the supervisor config must match what's used to start it
4. **Config file names vary by node type** - Config servers use `config.conf`, others use `mongod.conf` or `mongos.conf`

## New Bugs Found and Fixed (Session 2)

### 7. Config Server Log Directory Path Bug
**Problem**: Config servers were generating MongoDB config files with wrong log directory paths
- Config file had: `/path/to/mongod-42000/log/process.log`
- Should be: `/path/to/config-42000/log/process.log`
- This caused MongoDB to fail on startup with "FileNotOpen" errors

**Root Cause**:
- `pkg/deploy/planner.go:359` - Called `getNodeLogDir()` which hardcodes "mongod" as nodeType
- `pkg/deploy/planner.go:1002` - `getNodeLogDir()` always returns "mongod-XXXX" paths

**Files Fixed**:
- `pkg/deploy/planner.go:359` - Changed from `getNodeLogDir()` to `getNodeLogDirWithType(..., "config")`

**Impact**: Config servers now correctly use `config-XXXX/log/process.log` paths and start successfully

### 8. Mongos ProcessManagement Empty Struct Bug
**Problem**: Mongos config included empty `processManagement:` section causing mongos 3.6 to fail
- Generated config had: `processManagement:` (empty section)
- Mongos 3.6 error: `Unrecognized option: processManagement`
- This prevented mongos from starting on MongoDB 3.6

**Root Cause**:
- `pkg/template/types.go:135` - ProcessManagement was a struct type with `omitempty` tag
- In Go YAML, `omitempty` doesn't work for empty structs, only for nil pointers
- Even with `omitempty`, an empty struct `ProcessManagementConfig{}` is serialized as `processManagement:`

**Files Fixed**:
- `pkg/template/types.go:135` - Changed `ProcessManagement ProcessManagementConfig` to `ProcessManagement *ProcessManagementConfig`
- **TODO**: Remove ProcessManagement initialization in mongos config generation (leave as nil)

**Impact**: Mongos configs will properly omit processManagement section, fixing mongos 3.6 compatibility

## Todo Checklist

- [x] Identify config server naming bug
- [x] Fix in deploy.go
- [x] Fix in planner.go
- [x] Fix config file name bug in supervisor/config.go
- [x] Create centralized naming package
- [x] Refactor all files to use naming package
- [x] Build project and verify compilation
- [x] Find and fix config server log directory bug in planner.go
- [x] Add e2e test for config server replica sets
- [x] Verify config servers start successfully
- [ ] Test sharded cluster deployment end-to-end
- [ ] Run e2e tests
- [ ] Commit changes
