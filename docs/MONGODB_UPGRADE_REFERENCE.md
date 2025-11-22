# MongoDB Upgrade Reference Guide

## Overview
This document provides comprehensive reference information for upgrading MongoDB sharded clusters and replica sets, compiled from MongoDB official documentation and best practices.

## Table of Contents
1. [Upgrade Prerequisites](#upgrade-prerequisites)
2. [Sharded Cluster Upgrade Procedure](#sharded-cluster-upgrade-procedure)
3. [Replica Set Upgrade Procedure](#replica-set-upgrade-procedure)
4. [Feature Compatibility Version (FCV)](#feature-compatibility-version-fcv)
5. [Safety Checks and Health Verification](#safety-checks-and-health-verification)
6. [Rollback Considerations](#rollback-considerations)
7. [Common Issues and Troubleshooting](#common-issues-and-troubleshooting)
8. [Version-Specific Considerations](#version-specific-considerations)

---

## Upgrade Prerequisites

### General Prerequisites
Before starting any MongoDB upgrade:

1. **Version Consistency**
   - All cluster members must be running the same current version
   - Mixed version states (except during upgrade) are not supported

2. **Feature Compatibility Version (FCV)**
   - FCV must be set to the current version before upgrading binaries
   - Example: FCV must be "6.0" before upgrading to 7.0
   - Check FCV: `db.adminCommand({getParameter: 1, featureCompatibilityVersion: 1})`

3. **Replica Set Health**
   - No members in ROLLBACK or RECOVERING state
   - All members have `health: 1` in rs.status()
   - Replication lag is minimal (<30 seconds recommended)
   - Each replica set has exactly one PRIMARY member

4. **Backup**
   - **CRITICAL**: Back up config database before upgrading sharded clusters
   - Recommended: Back up all data before major version upgrades
   - Verify backups are restorable

5. **Disk Space**
   - Sufficient disk space for new binaries (typically 200-500 MB per version)
   - Additional space for logs during upgrade process

6. **Network Connectivity**
   - All cluster members can communicate
   - No network partitions or connectivity issues

### Sharded Cluster Additional Prerequisites

7. **Balancer State**
   - Stop the balancer before upgrading: `sh.stopBalancer()`
   - Verify stopped: `sh.getBalancerState()` returns false
   - Wait for active migrations to complete

8. **No Ongoing Operations**
   - No active chunk migrations
   - No index builds in progress (for major upgrades)
   - No ongoing resharding operations

---

## Sharded Cluster Upgrade Procedure

### Overview
Upgrade order for sharded clusters (MUST follow this sequence):
1. Disable balancer
2. Upgrade config server replica set
3. Upgrade each shard replica set
4. Upgrade mongos instances
5. Enable Feature Compatibility Version (optional, recommended to wait)
6. Re-enable balancer

### Detailed Steps

#### Step 1: Disable the Balancer
```javascript
// Connect to any mongos
sh.stopBalancer()

// Verify the balancer has stopped
sh.getBalancerState()  // Should return false

// Wait for any active migrations to complete (max 5 minutes)
use config
while (db.locks.findOne({_id: "balancer", state: 0}) == null) {
    print("Waiting for balancer to stop...");
    sleep(1000);
}
```

#### Step 2: Upgrade Config Server Replica Set
Follow the [Replica Set Upgrade Procedure](#replica-set-upgrade-procedure) for the config server replica set.

**Important**:
- Config servers must be upgraded first
- All config servers must be upgraded before proceeding to shards
- Verify config server health before continuing

#### Step 3: Upgrade Shard Replica Sets
For each shard replica set, follow the [Replica Set Upgrade Procedure](#replica-set-upgrade-procedure).

**Parallelization Options**:
- **Conservative**: Upgrade shards sequentially (one at a time)
- **Aggressive**: Upgrade shards in parallel (higher risk, faster completion)
- **Recommended**: Upgrade one or two shards in parallel, monitor closely

**Between Each Shard**:
- Verify shard health before moving to next shard
- Check replication lag across the cluster
- Review logs for errors

#### Step 4: Upgrade Mongos Instances
Mongos instances can be upgraded in any order and in parallel.

For each mongos:
1. Stop the mongos process gracefully
2. Replace the mongos binary with the new version
3. Start the mongos process
4. Verify connectivity: `db.adminCommand({ping: 1})`

**Note**: Mongos instances can be upgraded with zero downtime by upgrading one at a time while others handle traffic.

#### Step 5: Enable Feature Compatibility Version (FCV)
**IMPORTANT**: This step is optional and should be delayed unless you need new features.

```javascript
// Connect to mongos
db.adminCommand({
  setFeatureCompatibilityVersion: "7.0",
  confirm: true  // Required for MongoDB 7.0+
})

// Verify FCV
db.adminCommand({getParameter: 1, featureCompatibilityVersion: 1})
```

**FCV Upgrade Warnings**:
- Cannot easily downgrade after enabling FCV for new version
- Recommended to wait days/weeks with new binaries before enabling FCV
- "Burn-in period" allows binary rollback if issues are discovered
- Once FCV is upgraded, binary rollback requires MongoDB support assistance

#### Step 6: Re-enable the Balancer
```javascript
// Connect to mongos
sh.startBalancer()

// Verify the balancer is running
sh.getBalancerState()  // Should return true
sh.isBalancerRunning() // Should return true
```

---

## Replica Set Upgrade Procedure

### Upgrade Order Within a Replica Set
1. Upgrade secondary members first (one at a time)
2. Step down the primary
3. Upgrade the former primary (now a secondary)

### Detailed Steps

#### Step 1: Upgrade Secondary Members
For each secondary member (one at a time):

1. **Verify Member is Secondary**
   ```javascript
   rs.status()
   // Check that member shows stateStr: "SECONDARY"
   ```

2. **Stop the mongod Process**
   ```bash
   # Use graceful shutdown (SIGINT)
   kill -2 <mongod-pid>
   # OR use supervisor/systemd
   supervisorctl stop mongod-27017
   ```

3. **Replace the Binary**
   ```bash
   # Backup old binary
   mv /path/to/mongod /path/to/mongod.backup
   # Install new binary
   cp /new/version/mongod /path/to/mongod
   ```

4. **Start the mongod Process**
   ```bash
   supervisorctl start mongod-27017
   ```

5. **Wait for Member to Recover**
   ```javascript
   // Monitor rs.status() until member reaches SECONDARY state
   rs.status().members.find(m => m.name === "host:port").stateStr
   // Should return "SECONDARY"
   ```

6. **Verify Replication Lag**
   ```javascript
   rs.printSecondaryReplicationInfo()
   // Lag should be minimal (<30 seconds)
   ```

7. **Repeat for All Secondary Members**
   - Upgrade only one secondary at a time
   - Wait for each to reach SECONDARY state before upgrading next
   - Maintain replica set majority at all times

#### Step 2: Step Down the Primary
```javascript
// Connect to the primary
rs.stepDown(60)  // Step down for 60 seconds

// Alternative: Controlled stepdown with waiting
rs.stepDown(300, 60)  // Wait up to 60s for secondaries to catch up, then step down for 300s
```

**Wait for New Primary Election**:
```javascript
// Monitor rs.status() until a new primary is elected
while (rs.status().members.filter(m => m.stateStr === "PRIMARY").length === 0) {
    print("Waiting for primary election...");
    sleep(1000);
}
```

**Important**:
- Primary stepdown is required before upgrading the primary member
- System automatically elects a new primary from upgraded secondaries
- Step-down timeout prevents primary from re-election during upgrade

#### Step 3: Upgrade the Former Primary
The former primary is now a secondary member:

1. Follow the same steps as upgrading secondary members (Step 1)
2. Verify the former primary reaches SECONDARY state
3. Check that replica set has a PRIMARY member

#### Step 4: Verify Replica Set Health
```javascript
rs.status()

// Verify:
// - All members have stateStr: "PRIMARY" or "SECONDARY" (or "ARBITER")
// - All members have health: 1
// - Exactly one member has stateStr: "PRIMARY"
// - Replication lag is minimal
```

---

## Feature Compatibility Version (FCV)

### What is FCV?
Feature Compatibility Version controls which MongoDB features are enabled:
- Allows running new binary version with old feature set
- Enables backward compatibility during upgrade period
- Acts as a safety gate before enabling new features

### FCV Upgrade Strategy

#### Conservative Approach (Recommended)
1. Upgrade binaries to new version (e.g., 7.0)
2. Leave FCV at old version (e.g., "6.0")
3. Run in production for "burn-in period" (days to weeks)
4. Monitor for issues, performance regressions
5. If stable, upgrade FCV to new version
6. If issues found, rollback binaries (still possible with old FCV)

#### Aggressive Approach (Higher Risk)
1. Upgrade binaries to new version
2. Immediately upgrade FCV to new version
3. **Consequence**: Cannot easily rollback binaries if issues arise

### FCV Commands

**Check Current FCV**:
```javascript
db.adminCommand({getParameter: 1, featureCompatibilityVersion: 1})
```

**Upgrade FCV** (MongoDB 7.0+):
```javascript
db.adminCommand({
  setFeatureCompatibilityVersion: "7.0",
  confirm: true  // Required in MongoDB 7.0+
})
```

**Upgrade FCV** (MongoDB < 7.0):
```javascript
db.adminCommand({setFeatureCompatibilityVersion: "6.0"})
```

**Downgrade FCV** (if needed):
```javascript
db.adminCommand({setFeatureCompatibilityVersion: "6.0"})
```

### FCV Upgrade Failures

If FCV upgrade fails or is interrupted:
- May leave cluster in intermediate state
- Metadata remains from failed upgrade
- Blocks future FCV upgrade attempts
- **Resolution**: Contact MongoDB support or manually clean metadata (advanced)

**Prevention**:
- Ensure cluster is healthy before FCV upgrade
- No ongoing operations during FCV change
- Monitor FCV upgrade progress closely

---

## Safety Checks and Health Verification

### Replica Set Health Checks

#### rs.status() - Primary Health Check
```javascript
var status = rs.status()

// Check all members
status.members.forEach(function(member) {
    print(member.name + ": " + member.stateStr + " (health: " + member.health + ")")

    // Verify:
    // - stateStr is "PRIMARY", "SECONDARY", or "ARBITER"
    // - health is 1
    // - No "ROLLBACK", "RECOVERING", "DOWN", "UNKNOWN" states
})

// Check for primary
var primaries = status.members.filter(m => m.stateStr === "PRIMARY")
if (primaries.length !== 1) {
    print("ERROR: Expected exactly 1 primary, found " + primaries.length)
}
```

#### Replication Lag Check
```javascript
rs.printSecondaryReplicationInfo()

// Look for:
// - "behind the primary" time should be < 30 seconds
// - Large lag indicates synchronization issues
```

**Alternative Programmatic Check**:
```javascript
var status = rs.status()
var primary = status.members.find(m => m.stateStr === "PRIMARY")
var primaryOptime = primary.optimeDate

status.members.filter(m => m.stateStr === "SECONDARY").forEach(function(secondary) {
    var lag = (primaryOptime - secondary.optimeDate) / 1000  // Lag in seconds
    print(secondary.name + " lag: " + lag + " seconds")

    if (lag > 30) {
        print("WARNING: High replication lag on " + secondary.name)
    }
})
```

### Sharded Cluster Health Checks

#### Balancer Status
```javascript
// Check balancer state
sh.getBalancerState()  // Should return false during upgrade, true normally
sh.isBalancerRunning() // More accurate real-time check

// Check for active migrations
use config
db.locks.find({_id: "balancer"})
```

#### Shard Health
```javascript
// Connect to mongos
sh.status()

// Verify:
// - All shards are listed
// - All shards show "state: 1"
// - Chunks are evenly distributed (if balancer was running)
```

#### Config Server Health
```javascript
// Connect to mongos
use config
db.shards.find()

// Verify all shards are present and active
```

### Pre-Upgrade Safety Checklist
- [ ] All members running same current version
- [ ] FCV set to current version
- [ ] No members in ROLLBACK/RECOVERING state
- [ ] All members have health: 1
- [ ] Replication lag < 30 seconds
- [ ] Exactly one PRIMARY per replica set
- [ ] Balancer stopped (sharded clusters)
- [ ] No active migrations (sharded clusters)
- [ ] Config database backed up
- [ ] Sufficient disk space for new binaries
- [ ] New version binaries available and tested

### Post-Upgrade Safety Checklist
- [ ] All members running new version
- [ ] All members in PRIMARY/SECONDARY/ARBITER state
- [ ] All members have health: 1
- [ ] Replication lag < 30 seconds
- [ ] Connectivity tests pass
- [ ] Application connectivity verified
- [ ] Logs reviewed for errors
- [ ] Performance metrics normal

---

## Rollback Considerations

### When Rollback is Possible
- **Binary Rollback**: Possible if FCV has NOT been upgraded to new version
- **Example**: Running 7.0 binaries with FCV "6.0" → can rollback to 6.0 binaries

### When Rollback is Difficult/Impossible
- **After FCV Upgrade**: Once FCV is set to new version (e.g., "7.0"), rolling back binaries requires MongoDB support assistance
- **Metadata Changes**: FCV upgrade may create metadata incompatible with older binaries
- **Failed FCV Downgrade**: Leaves partial metadata blocking future operations

### Safe Rollback Procedure (Before FCV Upgrade)

1. **Stop All Processes**
   - Reverse order: mongos → shards → config servers
   - Graceful shutdown (SIGINT)

2. **Restore Old Binaries**
   - Replace new binaries with old versions
   - Or use symlink switching to previous version

3. **Start Processes**
   - Same order as upgrade: config → shards → mongos
   - Verify each starts successfully

4. **Verify Health**
   - Check rs.status() for all replica sets
   - Verify FCV is still old version
   - Test application connectivity

### Rollback Strategy Recommendations
1. **Burn-in Period**: Run new binaries with old FCV for at least 1 week in production
2. **Monitoring**: Watch for errors, performance issues, stability problems
3. **Delayed FCV Upgrade**: Only upgrade FCV after burn-in period passes
4. **Downgrade Testing**: Test rollback procedure in staging environment first

---

## Common Issues and Troubleshooting

### Issue: Member Stuck in STARTUP/STARTUP2
**Symptoms**: Member not reaching SECONDARY state after upgrade
**Causes**:
- Initial sync in progress
- Index builds in progress
- Corrupted data files
- Configuration issues

**Resolution**:
1. Check logs: `/path/to/logs/mongod.log`
2. Look for errors about:
   - Index builds: Wait for completion or restart with `--noIndexBuildRetry`
   - Sync: Monitor `rs.status()` for sync progress
3. If stuck > 10 minutes, review logs and consider member restart

### Issue: Primary Election Fails After Stepdown
**Symptoms**: No PRIMARY after rs.stepDown()
**Causes**:
- Insufficient voting members available
- Network partition
- Priority settings prevent election
- Replication lag too high

**Resolution**:
1. Check member voting status: `rs.conf().members[N].votes`
2. Verify network connectivity between members
3. Check member priorities: `rs.conf().members[N].priority`
4. Manually force election if safe: `rs.stepUp()` from desired member

### Issue: High Replication Lag After Upgrade
**Symptoms**: Secondaries falling behind primary by minutes/hours
**Causes**:
- Heavy write load during upgrade
- Secondary performing initial sync
- Slow disk I/O on secondary
- Network bandwidth limitations

**Resolution**:
1. Reduce write load if possible
2. Monitor with: `rs.printSecondaryReplicationInfo()`
3. Check disk I/O: `iostat -x 1`
4. Consider temporarily stopping upgrades until lag recovers
5. For persistent lag, investigate secondary hardware/network

### Issue: Balancer Won't Stop
**Symptoms**: `sh.getBalancerState()` stays true after `sh.stopBalancer()`
**Causes**:
- Active migrations taking longer than expected
- Balancer process hung
- Network issues

**Resolution**:
1. Wait for active migrations: Query `config.locks` collection
2. Increase timeout (default 5 minutes)
3. If hung, restart mongos (balancer runs in mongos)
4. Force stop (dangerous): Update `config.settings` directly (not recommended)

### Issue: FCV Upgrade Fails
**Symptoms**: `setFeatureCompatibilityVersion` command fails or hangs
**Causes**:
- Not all nodes upgraded to new binary version
- Ongoing operations blocking FCV change
- Previous failed FCV upgrade left metadata

**Resolution**:
1. Verify all nodes: `db.adminCommand({buildInfo: 1}).version`
2. Check for ongoing operations: `db.currentOp()`
3. Review logs on all nodes for errors
4. If repeated failures, contact MongoDB support
5. Clean up failed metadata (advanced, see MongoDB docs)

### Issue: Process Fails to Start After Binary Replacement
**Symptoms**: mongod/mongos won't start after upgrade
**Causes**:
- Configuration file incompatible with new version
- Corrupted binary or installation
- File permissions changed
- Incorrect binary path

**Resolution**:
1. Check mongod.log for startup errors
2. Verify binary: `mongod --version`
3. Validate config file: `/path/to/mongod.conf`
4. Check file permissions: Binary and data directories
5. Try starting with minimal config to isolate issue
6. Rollback to previous binary if critical

---

## Version-Specific Considerations

### MongoDB 7.0
- **FCV Command Change**: Requires `confirm: true` parameter
  ```javascript
  db.adminCommand({setFeatureCompatibilityVersion: "7.0", confirm: true})
  ```
- **Time Series Collections**: New optimizations and query capabilities
- **Queryable Encryption**: Generally available
- **Cluster-to-Cluster Replication**: New feature

### MongoDB 6.0
- **Encrypted Fields**: New encryption capabilities
- **Change Streams**: Performance improvements
- **New Operators**: Additional aggregation operators

### MongoDB 5.0
- **Time Series Collections**: Introduced
- **Versioned API**: Stable API versioning
- **Native Time Series**: Optimized storage

### MongoDB 4.4
- **Hedged Reads**: Improved read performance
- **Mirrored Reads**: Testing read distribution
- **Union Operator**: New aggregation stage

### MongoDB 4.2
- **Distributed Transactions**: Sharded cluster transactions
- **Wildcard Indexes**: Flexible indexing
- **Retryable Writes**: Default enabled

### MongoDB 4.0
- **Multi-Document ACID Transactions**: Introduced (replica sets only)
- **Non-Blocking Secondary Reads**: Improved read performance
- **Free Monitoring**: Built-in monitoring service

---

## Additional Resources

### Official MongoDB Documentation
- **Upgrade Guides**: https://docs.mongodb.com/manual/release-notes/
- **Feature Compatibility**: https://docs.mongodb.com/manual/reference/command/setFeatureCompatibilityVersion/
- **Production Notes**: https://docs.mongodb.com/manual/administration/production-notes/

### Reference Materials
- **dashapp-docs MongoDB Collection**: https://github.com/zph/dashapp-docs/tree/main/mongodb
- **MongoDB 7.0 Upgrade Sharded Cluster**: https://www.mongodb.com/docs/manual/release-notes/7.0-upgrade-sharded-cluster/
- **MongoDB 7.0 Upgrade Replica Set**: https://www.mongodb.com/docs/manual/release-notes/7.0-upgrade-replica-set/

### Best Practices
1. **Always backup before upgrading** (especially config database)
2. **Test upgrades in staging first** before production
3. **Use burn-in period** between binary and FCV upgrade
4. **Monitor closely** during and after upgrade
5. **Upgrade during low-traffic periods** when possible
6. **Have rollback plan ready** and tested
7. **Document your upgrade** for future reference and auditing
8. **Coordinate with application teams** for compatibility testing

---

## Glossary

- **FCV**: Feature Compatibility Version - Controls enabled feature set
- **Balancer**: Process that migrates chunks between shards for even distribution
- **Config Server**: Metadata storage for sharded cluster topology
- **Mongos**: Query router for sharded clusters
- **Replica Set**: Group of MongoDB instances maintaining same data set
- **PRIMARY**: Replica set member accepting writes
- **SECONDARY**: Replica set member replicating from primary
- **Oplog**: Operation log used for replication
- **Optime**: Operation timestamp for replication tracking
- **Chunk**: Unit of data distribution in sharded clusters
- **Shard**: Individual replica set storing subset of sharded data
