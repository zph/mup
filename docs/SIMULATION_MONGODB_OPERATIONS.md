# MongoDB Operations in Simulation Mode

## Overview

This document explains how MongoDB-specific operations (replica set initialization, shard configuration) are handled in simulation mode.

## Operation Flow

```
CLI Command (--simulate)
    ↓
SimulationExecutor replaces LocalExecutor/SSHExecutor
    ↓
Deployment Plan Generated with MongoDB operations:
  - OpInitReplicaSet (initialize replica sets)
  - OpAddShard (add shards to cluster)
    ↓
OperationExecutor dispatches to handlers:
  - InitReplicaSetHandler
  - AddShardHandler
    ↓
Handlers receive SimulationExecutor
    ↓
Handlers would execute: exec.Execute("mongosh --eval 'rs.initiate()'")
    ↓
SimulationExecutor records operation without actual MongoDB connection
    ↓
Simulation report shows all MongoDB operations
```

## MongoDB Operations Supported

### 1. Replica Set Operations

**OpInitReplicaSet** - Initialize a replica set

**Handler**: `InitReplicaSetHandler` (`pkg/operation/handlers.go:569`)

**What it does:**
- Takes replica set name and member list
- Would execute: `mongosh --eval 'rs.initiate({...})'`
- In simulation: Records the operation, returns success

**Example simulation:**
```bash
mup cluster deploy rs-cluster replica-set.yaml --version 7.0 --simulate

# Simulation output includes:
# [SIMULATION] init_replica_set
# [SIMULATION]   Target: rs0
# [SIMULATION]   Details: 3 members (localhost:27017, localhost:27018, localhost:27019)
```

**Scenario configuration:**
```yaml
simulation:
  responses:
    "mongosh --port 27017 --eval 'rs.initiate()'": |
      {"ok": 1, "operationTime": Timestamp(1700000000, 1)}

    "mongosh --port 27017 --eval 'rs.status()'": |
      {
        "set": "rs0",
        "members": [
          {"_id": 0, "name": "localhost:27017", "stateStr": "PRIMARY"}
        ],
        "ok": 1
      }
```

### 2. Sharding Operations

**OpAddShard** - Add a shard to sharded cluster

**Handler**: `AddShardHandler` (`pkg/operation/handlers.go:638`)

**What it does:**
- Takes shard name and connection string
- Would execute: `mongosh --eval 'sh.addShard("shard1/localhost:27017")'`
- In simulation: Records the operation, returns success

**Example simulation:**
```bash
mup cluster deploy sharded-cluster sharded.yaml --version 7.0 --simulate

# Simulation output includes:
# [SIMULATION] add_shard
# [SIMULATION]   Target: shard1
# [SIMULATION]   Details: shard1/localhost:27017
# [SIMULATION] add_shard
# [SIMULATION]   Target: shard2
# [SIMULATION]   Details: shard2/localhost:27018
```

**Scenario configuration:**
```yaml
simulation:
  responses:
    "mongosh --port 27016 --eval 'sh.addShard(\"shard1/localhost:27017\")'": |
      {"shardAdded": "shard1", "ok": 1}

    "mongosh --port 27016 --eval 'sh.status()'": |
      {
        "shards": [
          {"_id": "shard1", "host": "shard1/localhost:27017", "state": 1}
        ],
        "ok": 1
      }
```

## Complete Sharded Cluster Example

### Scenario File: `test-scenarios/sharded-cluster-init.yaml`

This scenario simulates a complete sharded cluster deployment:

```yaml
simulation:
  responses:
    # Config server replica set init
    "mongosh --port 27019 --eval 'rs.initiate()'": '{"ok": 1}'

    # Shard1 replica set init
    "mongosh --port 27017 --eval 'rs.initiate()'": '{"ok": 1}'

    # Shard2 replica set init
    "mongosh --port 27018 --eval 'rs.initiate()'": '{"ok": 1}'

    # Add shards via mongos
    "mongosh --port 27016 --eval 'sh.addShard(\"shard1/localhost:27017\")'": '{"shardAdded": "shard1", "ok": 1}'
    "mongosh --port 27016 --eval 'sh.addShard(\"shard2/localhost:27018\")'": '{"shardAdded": "shard2", "ok": 1}'

    # Status checks
    "mongosh --port 27016 --eval 'sh.status()'": '{"shards": [...], "ok": 1}'

  processes:
    running:
      - command: mongod
        port: 27019  # config server
      - command: mongod
        port: 27017  # shard1
      - command: mongod
        port: 27018  # shard2
      - command: mongos
        port: 27016  # router
```

### Usage:

```bash
mup cluster deploy sharded-test sharded-cluster.yaml \
  --version 7.0 \
  --simulate \
  --simulate-scenario test-scenarios/sharded-cluster-init.yaml \
  --auto-approve
```

### Expected Simulation Output:

```
[SIMULATION] Phase 1: Prepare
[SIMULATION]   ✓ create_directory: /data/config-27019
[SIMULATION]   ✓ create_directory: /data/shard1-27017
[SIMULATION]   ✓ create_directory: /data/shard2-27018
[SIMULATION]   ✓ upload_file: mongod binary

[SIMULATION] Phase 2: Deploy
[SIMULATION]   ✓ start_process: mongod --port 27019 (config)
[SIMULATION]   ✓ start_process: mongod --port 27017 (shard1)
[SIMULATION]   ✓ start_process: mongod --port 27018 (shard2)
[SIMULATION]   ✓ start_process: mongos --port 27016

[SIMULATION] Phase 3: Initialize
[SIMULATION]   ✓ init_replica_set: configReplSet (1 member)
[SIMULATION]   ✓ init_replica_set: shard1 (1 member)
[SIMULATION]   ✓ init_replica_set: shard2 (1 member)
[SIMULATION]   ✓ add_shard: shard1/localhost:27017
[SIMULATION]   ✓ add_shard: shard2/localhost:27018

[SIMULATION] Summary:
[SIMULATION]   Total operations: 15
[SIMULATION]   Replica sets initialized: 3
[SIMULATION]   Shards added: 2
```

## Operation Details

### Replica Set Initialization Flow

1. **Generate Plan**: `OpInitReplicaSet` operation created with parameters:
   ```json
   {
     "replica_set": "rs0",
     "members": ["localhost:27017", "localhost:27018", "localhost:27019"]
   }
   ```

2. **Execute** (via `InitReplicaSetHandler`):
   ```go
   // Real execution would do:
   mongoshCmd := fmt.Sprintf(
       "mongosh --port %d --eval 'rs.initiate({...})'",
       primaryPort
   )
   output, err := exec.Execute(mongoshCmd)

   // With SimulationExecutor:
   // - mongoshCmd is recorded in operations list
   // - Configured response returned (or default)
   // - No actual MongoDB connection made
   ```

3. **Simulated**: Operation recorded as:
   ```json
   {
     "type": "init_replica_set",
     "target": "rs0",
     "details": "3 members",
     "result": "success"
   }
   ```

### Shard Addition Flow

1. **Generate Plan**: `OpAddShard` operation created with parameters:
   ```json
   {
     "shard_name": "shard1",
     "connection_string": "shard1/localhost:27017"
   }
   ```

2. **Execute** (via `AddShardHandler`):
   ```go
   // Real execution would do:
   mongoshCmd := fmt.Sprintf(
       "mongosh --port %d --eval 'sh.addShard(\"%s\")'",
       mongosPort, connectionString
   )
   output, err := exec.Execute(mongoshCmd)

   // With SimulationExecutor:
   // - mongoshCmd is recorded
   // - Returns success with configured response
   ```

3. **Simulated**: Operation recorded as:
   ```json
   {
     "type": "add_shard",
     "target": "shard1",
     "details": "shard1/localhost:27017",
     "result": "success"
   }
   ```

## Default Responses

The simulation framework includes default responses for common MongoDB commands (see `pkg/simulation/config.go:61-70`):

```go
// MongoDB replica set commands
c.Responses["mongosh --eval rs.initiate()"] = `{"ok": 1}`
c.Responses["mongosh --eval rs.status()"] = `{"set": "rs0", "members": [...], "ok": 1}`
c.Responses["mongosh --eval rs.add()"] = `{"ok": 1}`

// MongoDB sharding commands
c.Responses["mongosh --eval sh.addShard()"] = `{"shardAdded": "shard0", "ok": 1}`
c.Responses["mongosh --eval sh.status()"] = `{"shards": [...], "ok": 1}`
```

## Testing Failure Scenarios

### Replica Set Initialization Failure

```yaml
simulation:
  failures:
    - operation: execute
      target: "mongosh --port 27017 --eval 'rs.initiate()'"
      error: "MongoServerError: replSetInitiate quorum check failed"

  responses:
    "mongosh --port 27017 --eval 'rs.status()'": |
      {"ok": 0, "errmsg": "no replset config has been received"}
```

### Shard Addition Failure

```yaml
simulation:
  failures:
    - operation: execute
      target: "mongosh --port 27016 --eval 'sh.addShard(\"shard1/localhost:27017\")'"
      error: "MongoServerError: couldn't connect to shard1/localhost:27017"
```

## Implementation Notes

### Current State

- ✅ Operation types defined (`OpInitReplicaSet`, `OpAddShard`)
- ✅ Handlers implemented (`InitReplicaSetHandler`, `AddShardHandler`)
- ✅ Handlers integrated with operation executor
- ✅ SimulationExecutor intercepts all execute calls
- ✅ Default responses configured for common MongoDB commands
- ⚠️  Handlers currently use TODO placeholders for actual MongoDB driver calls

### Future Enhancements

1. **Real MongoDB Driver Integration**: Replace TODO placeholders with actual MongoDB driver calls for non-simulation mode
2. **More Granular Operations**: Break down `OpInitReplicaSet` into:
   - `OpReplicaSetInitiate` - Initial rs.initiate()
   - `OpReplicaSetAddMember` - Add individual member
   - `OpReplicaSetReconfig` - Reconfigure replica set
3. **Additional Sharding Operations**:
   - `OpEnableSharding` - Enable sharding on database
   - `OpShardCollection` - Shard a collection
   - `OpRemoveShard` - Remove shard from cluster

## EARS Requirements Traced

- **REQ-SIM-001**: Simulation mode executes MongoDB operations without side effects
- **REQ-SIM-002**: All MongoDB operations tracked in simulation state
- **REQ-SIM-013**: MongoDB command responses configurable per scenario
- **REQ-SIM-041**: MongoDB operations testable via scenario files
- **REQ-SIM-042**: Custom responses for MongoDB commands in scenarios
- **REQ-SIM-043**: Default responses for common MongoDB commands

## See Also

- `pkg/operation/handlers.go` - Handler implementations
- `pkg/operation/executor.go` - Operation dispatching
- `pkg/plan/plan.go` - Operation type definitions
- `test-scenarios/sharded-cluster-init.yaml` - Complete example scenario
