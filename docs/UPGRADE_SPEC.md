# MongoDB Cluster Upgrade Specification (EARS)

## Overview
This specification defines the requirements for upgrading MongoDB clusters (both local and remote) managed by mup, supporting both MongoDB and Percona Server for MongoDB variants with safety checks, version isolation, and rollback capabilities.

## Scope
- **In Scope**: Local MongoDB cluster upgrades, variant support (mongo/percona), version management, rollback mechanisms, safety checks
- **Out of Scope**: Remote SSH-based upgrades (future phase), cross-variant migrations, downgrade operations

## Terminology
- **Variant**: The MongoDB distribution type (mongo = official MongoDB, percona = Percona Server for MongoDB)
- **Version**: The semantic version of the MongoDB distribution (e.g., 6.0.15, 7.0.0)
- **Full Version**: Combined variant and version identifier (e.g., mongo-6.0.15, percona-7.0.5-4)
- **FCV**: Feature Compatibility Version - MongoDB's internal compatibility level
- **Rollback**: Reverting to the previous MongoDB version by switching symlinks and restarting processes

---

## Requirements

### UPG-001: Version-Isolated Directory Structure
**EARS-ID**: UPG-001
**Type**: Ubiquitous
**Requirement**: The system **shall** maintain separate directory structures for each full version containing binaries, configuration files, and logs.

**Rationale**: Enables instant rollback via symlink switching without reinstalling binaries.

**Directory Structure**:
```
~/.mup/storage/clusters/<cluster-name>/
├── current -> versions/mongo-7.0.0/     # Symlink to active version
├── previous -> versions/mongo-6.0.15/   # Symlink to previous version
└── versions/
    ├── mongo-6.0.15/
    │   ├── bin/                         # mongod, mongos binaries
    │   ├── conf/                        # Configuration files per node
    │   └── logs/                        # Log files per node
    └── mongo-7.0.0/
        ├── bin/
        ├── conf/
        └── logs/
```

**Acceptance Criteria**:
- Each version directory contains isolated bin/, conf/, logs/ subdirectories
- The `current` symlink points to the active version
- The `previous` symlink points to the last successful version
- Configuration files reference version-specific paths

**EARS Tags**: [UPG-001]

---

### UPG-002: Variant Support
**EARS-ID**: UPG-002
**Type**: Ubiquitous
**Requirement**: The system **shall** support multiple MongoDB variants, with "mongo" as the default variant.

**Supported Variants**:
- `mongo`: Official MongoDB Community/Enterprise Server (default)
- `percona`: Percona Server for MongoDB

**Version Naming Convention**:
- Format: `<variant>-<version>`
- Examples: `mongo-6.0.15`, `percona-7.0.5-4`

**Acceptance Criteria**:
- Cluster metadata includes a `Variant` field (default: "mongo")
- Binary download logic handles variant-specific download URLs
- CLI accepts `--variant` flag with "mongo" as default
- Version parsing extracts both variant and version components

**EARS Tags**: [UPG-002]

---

### UPG-003: Pre-Upgrade Validation
**EARS-ID**: UPG-003
**Type**: Ubiquitous
**Requirement**: The system **shall** perform comprehensive pre-upgrade validation checks before initiating any upgrade operation.

**Pre-Upgrade Checks**:
1. All cluster nodes are running the same current version
2. Feature Compatibility Version (FCV) matches current version
3. No replica set members in ROLLBACK or RECOVERING state
4. All replica sets have a PRIMARY member
5. Replication lag is within acceptable threshold (<30 seconds recommended)
6. Sufficient disk space for new binaries (minimum 2x binary size)
7. Target version binaries are available or downloadable
8. Balancer is stopped (for sharded clusters)
9. No active chunk migrations (for sharded clusters)

**Acceptance Criteria**:
- Pre-upgrade validation returns pass/fail status with detailed messages
- Upgrade is blocked if any critical check fails
- User receives clear guidance on remediation steps
- Validation results are logged

**EARS Tags**: [UPG-003]

---

### UPG-004: Phased Upgrade Workflow
**EARS-ID**: UPG-004
**Type**: Ubiquitous
**Requirement**: The system **shall** execute upgrades in a phased approach following MongoDB best practices.

**Upgrade Phases** (for sharded clusters):

**Phase 0: Pre-Flight**
- Execute pre-upgrade validation [UPG-003]
- Download and verify target version binaries
- Generate upgrade plan
- Prompt user for confirmation (unless `--yes` flag)

**Phase 1: Upgrade Config Servers**
- Disable balancer and wait for migrations to complete
- Upgrade config server replica set members [UPG-005]
- Verify config server replica set health [UPG-006]

**Phase 2: Upgrade Shards**
- For each shard replica set (sequential or parallel based on `--parallel-shards` flag):
  - Upgrade shard replica set members [UPG-005]
  - Verify shard replica set health [UPG-006]

**Phase 3: Upgrade Mongos**
- For each mongos instance (can be parallel):
  - Stop mongos process
  - Replace binary [UPG-007]
  - Start mongos process
  - Verify mongos connectivity

**Phase 4: Post-Upgrade**
- Verify all nodes are running target version
- Optionally upgrade FCV (if `--upgrade-fcv` flag) [UPG-008]
- Re-enable balancer (for sharded clusters)
- Update cluster metadata
- Display upgrade summary

**For replica sets** (non-sharded):
- Skip balancer operations
- Execute Phase 2 (upgrade replica set members)
- Execute Phase 4 (post-upgrade)

**Acceptance Criteria**:
- Phases execute in documented order
- Each phase completion is verified before proceeding
- Phase progress is saved to metadata for resumability
- Failures halt the upgrade with clear error messages

**EARS Tags**: [UPG-004]

---

### UPG-005: Replica Set Member Upgrade
**EARS-ID**: UPG-005
**Type**: Ubiquitous
**Requirement**: Within a replica set, the system **shall** upgrade secondary members first, then step down the primary, then upgrade the former primary.

**Replica Set Upgrade Procedure**:
1. Identify all SECONDARY members
2. For each secondary (sequential, one at a time):
   - Execute custom pre-node safety checks [UPG-009]
   - Stop mongod process gracefully (SIGINT)
   - Replace binary [UPG-007]
   - Start mongod process with new binary
   - Wait for member to reach SECONDARY state
   - Verify replication lag is acceptable
   - Execute custom post-node safety checks [UPG-009]
3. Step down PRIMARY member (rs.stepDown())
4. Wait for new primary election
5. Upgrade former primary (now secondary):
   - Execute custom pre-node safety checks [UPG-009]
   - Stop mongod process gracefully
   - Replace binary [UPG-007]
   - Start mongod process with new binary
   - Wait for member to reach SECONDARY state
   - Execute custom post-node safety checks [UPG-009]

**Concurrency Constraint**: Maximum one member upgrade at a time within a replica set.

**Acceptance Criteria**:
- Secondaries are upgraded before primary
- Primary is stepped down before upgrade
- Each member reaches healthy state before proceeding to next
- Replica set maintains majority during entire upgrade
- No more than one member is down at any time

**EARS Tags**: [UPG-005]

---

### UPG-006: Health Verification Gates
**EARS-ID**: UPG-006
**Type**: Ubiquitous
**Requirement**: The system **shall** verify replica set health after each member upgrade and block progression if health checks fail.

**Health Checks**:
1. **Member State**: All members in PRIMARY, SECONDARY, or ARBITER state
2. **Primary Exists**: Exactly one PRIMARY member exists
3. **Member Health**: All members have `health: 1`
4. **Replication Lag**: All secondaries within acceptable lag threshold (<30s recommended)
5. **Member Count**: Expected number of members present and voting

**Health Check Timing**:
- After each secondary member upgrade
- After primary stepdown and election
- After former primary upgrade
- After each replica set completes
- After entire upgrade completes

**Acceptance Criteria**:
- Health checks query MongoDB via rs.status()
- Failed health checks block upgrade progression
- Health check failures display detailed diagnostic information
- Configurable timeout for health check stabilization (default: 2 minutes)
- User can override health check failures with `--force` flag (with warnings)

**EARS Tags**: [UPG-006]

---

### UPG-007: Binary Replacement
**EARS-ID**: UPG-007
**Type**: Ubiquitous
**Requirement**: The system **shall** replace MongoDB binaries by updating the `current` symlink to point to the target version directory.

**Binary Replacement Procedure**:
1. Verify target version directory exists with binaries
2. Stop the MongoDB process gracefully
3. Update `previous` symlink to point to old `current` target
4. Update `current` symlink to point to target version directory
5. Start MongoDB process using `current/bin/mongod` (or mongos)

**Rollback Procedure**:
1. Stop MongoDB process
2. Swap `current` symlink to point to `previous` symlink target
3. Start MongoDB process
4. Verify process starts successfully

**Acceptance Criteria**:
- Symlink operations are atomic
- Old binaries remain accessible via `previous` symlink
- Configuration files use version-specific paths
- Process restarts use symlink-resolved binary paths
- Rollback completes in under 30 seconds per node

**EARS Tags**: [UPG-007]

---

### UPG-008: Feature Compatibility Version (FCV) Management
**EARS-ID**: UPG-008
**Type**: Event-driven
**Requirement**: When the user specifies `--upgrade-fcv`, the system **shall** upgrade the Feature Compatibility Version after all binaries are successfully upgraded.

**FCV Upgrade Procedure**:
1. Verify all cluster nodes are running target version
2. Connect to primary of first replica set (or mongos for sharded)
3. Execute: `db.adminCommand({setFeatureCompatibilityVersion: "<target-version>", confirm: true})`
4. Monitor FCV upgrade progress
5. Verify FCV is set to target version across all nodes

**Safety Warnings**:
- Warn user that FCV upgrade is difficult to reverse
- Recommend "burn-in period" with new binaries before FCV upgrade
- Require explicit `--upgrade-fcv` flag (never automatic)
- Display confirmation prompt with implications

**Acceptance Criteria**:
- FCV upgrade only occurs if `--upgrade-fcv` flag is set
- FCV upgrade only proceeds if all binaries are upgraded
- Uses `confirm: true` parameter for MongoDB 7.0+
- Failed FCV upgrade is reported with remediation steps
- Metadata tracks current FCV version

**EARS Tags**: [UPG-008]

---

### UPG-009: Custom Safety Check Hooks
**EARS-ID**: UPG-009
**Type**: Event-driven
**Requirement**: When custom safety check hooks are configured, the system **shall** execute them at designated checkpoints and validate exit codes.

**Hook Execution Points**:
- `pre-upgrade`: Before any upgrade operations begin
- `pre-phase`: Before each upgrade phase (config/shard/mongos)
- `pre-node`: Before upgrading each individual node
- `post-node`: After upgrading each individual node
- `post-phase`: After completing each upgrade phase
- `post-upgrade`: After all upgrade operations complete

**Hook Configuration** (in cluster metadata):
```yaml
safety_hooks:
  pre-upgrade:
    script: /path/to/check-backup.sh
    args: ["--cluster", "{{cluster_name}}", "--version", "{{target_version}}"]
    timeout: 60
  pre-node:
    script: /usr/local/bin/verify-node-health
    args: ["--node", "{{node_host}}:{{node_port}}", "--json"]
    timeout: 30
```

**Argument Encoding**:
- Arguments are JSON-encoded before passing to script/binary
- Template variables are expanded: `{{cluster_name}}`, `{{target_version}}`, `{{node_host}}`, `{{node_port}}`, `{{current_version}}`, `{{phase}}`

**Exit Code Handling**:
- Exit code 0: Check passed, continue upgrade
- Exit code 1-255: Check failed, halt upgrade
- Timeout: Treat as failure, halt upgrade

**Acceptance Criteria**:
- Hooks execute at designated checkpoints
- Template variables are correctly expanded
- Arguments are JSON-encoded
- Exit codes control upgrade flow
- Hook failures display stdout/stderr
- Hooks respect timeout configuration
- Missing hook scripts cause upgrade failure with clear error

**EARS Tags**: [UPG-009]

---

### UPG-010: Upgrade Plan Generation
**EARS-ID**: UPG-010
**Type**: Ubiquitous
**Requirement**: The system **shall** generate and display a detailed upgrade plan before execution, requiring user confirmation unless `--yes` flag is provided.

**Upgrade Plan Contents**:
- Current cluster state (version, topology, node count)
- Target version and variant
- Upgrade phases with node-by-node sequence
- Estimated downtime per component
- Safety checks that will be executed
- Custom hooks that will be invoked
- Rollback procedure summary

**Acceptance Criteria**:
- Plan displays before any operations execute
- Plan shows exact upgrade sequence
- User must confirm plan (unless `--yes`)
- Plan is saved to metadata for audit trail
- `--dry-run` flag shows plan without execution

**EARS Tags**: [UPG-010]

---

### UPG-011: Upgrade Progress Tracking
**EARS-ID**: UPG-011
**Type**: Ubiquitous
**Requirement**: The system **shall** persist upgrade state after each phase completion to enable resume capability after failures.

**Progress State**:
- Current phase (pre-flight, config, shard-N, mongos, post-upgrade)
- Nodes upgraded (list of host:port)
- Nodes pending (list of host:port)
- Previous version
- Target version
- Timestamp of last phase completion

**Resume Capability**:
- Detect incomplete upgrade on cluster start
- Offer to resume or rollback
- Skip already-completed phases
- Re-validate health before resuming

**Acceptance Criteria**:
- Upgrade state is saved in cluster metadata
- Failed upgrades can be resumed from last successful phase
- User can choose to rollback instead of resume
- Completed upgrades clear progress state

**EARS Tags**: [UPG-011]

---

### UPG-012: Balancer Management for Sharded Clusters
**EARS-ID**: UPG-012
**Type**: Event-driven
**Requirement**: When upgrading a sharded cluster, the system **shall** disable the balancer before starting upgrades and re-enable it after completion.

**Balancer Disable Procedure**:
1. Connect to mongos
2. Execute: `sh.stopBalancer()`
3. Wait for active migrations to complete (configurable timeout, default 5 minutes)
4. Verify balancer is stopped: `sh.getBalancerState()` returns false

**Balancer Enable Procedure**:
1. Connect to mongos
2. Execute: `sh.startBalancer()`
3. Verify balancer is running: `sh.getBalancerState()` returns true

**Acceptance Criteria**:
- Balancer is disabled before config server upgrades
- System waits for active migrations to complete
- Balancer state is verified
- Balancer is re-enabled after successful upgrade
- Balancer remains disabled if upgrade fails (manual intervention required)
- Timeout is configurable via `--balancer-timeout` flag

**EARS Tags**: [UPG-012]

---

### UPG-013: Upgrade Command Interface
**EARS-ID**: UPG-013
**Type**: Ubiquitous
**Requirement**: The system **shall** provide a `mup cluster upgrade` command with the following interface:

```bash
mup cluster upgrade <cluster-name> --to-version <version> [flags]
```

**Required Flags**:
- `--to-version <version>`: Target version (e.g., "7.0.0")

**Optional Flags**:
- `--variant <variant>`: Target variant (default: "mongo", options: "mongo", "percona")
- `--upgrade-fcv`: Upgrade Feature Compatibility Version after binary upgrade (default: false)
- `--parallel-shards`: Upgrade shards in parallel (default: false, sequential)
- `--yes`: Skip confirmation prompts
- `--dry-run`: Show upgrade plan without executing
- `--balancer-timeout <duration>`: Timeout for balancer to stop (default: 5m)
- `--force`: Override health check failures (dangerous, requires confirmation)

**Auto-Detection**:
- `--from-version`: Auto-detected from cluster metadata (optional override)
- `--variant`: Defaults to current cluster variant if already set, otherwise "mongo"

**Acceptance Criteria**:
- Command validates required flags
- Version format is validated (semantic version)
- Variant is validated (mongo or percona)
- Help text documents all flags
- Error messages guide user on missing/invalid flags

**EARS Tags**: [UPG-013]

---

### UPG-014: Config Database Backup and Restore
**EARS-ID**: UPG-014
**Type**: Ubiquitous (for sharded clusters)
**Requirement**: For sharded clusters, the system **shall** back up the config database before upgrades and support restoring from backup during catastrophic rollback scenarios.

**Config Database Backup**:
1. Connect to config server replica set PRIMARY
2. Use mongodump to backup config database:
   ```bash
   mongodump --host <config-primary> --port <port> \
     --db config --out <backup-dir>/config-backup-<timestamp>
   ```
3. Store backup path in upgrade state metadata
4. Verify backup integrity (check files exist, non-zero size)

**Config Database Restore** (Catastrophic Rollback):
When binary rollback is insufficient (e.g., metadata corruption, incompatible schema changes):

1. Stop all mongos instances
2. Stop all shard mongod instances
3. Restore config database to config server replica set:
   ```bash
   mongorestore --host <config-primary> --port <port> \
     --db config --drop <backup-dir>/config-backup-<timestamp>/config
   ```
4. Restart config servers with previous binary version
5. Verify config server replica set health
6. Restart shards with previous binary version
7. Restart mongos instances with previous binary version
8. Verify cluster health and connectivity

**When to Use Config DB Restore**:
- FCV upgrade failed with metadata corruption
- Incompatible schema changes between versions
- Config server upgrade caused cluster-wide issues
- Standard binary rollback fails due to metadata issues

**Backup Retention**:
- Keep config backups for last 5 upgrades
- Automatic cleanup of older backups
- User can specify `--keep-backups <count>` to override

**Acceptance Criteria**:
- Config database backup created before config server upgrade
- Backup path stored in upgrade state
- Backup integrity verified after creation
- Restore command available: `mup cluster restore-config --from-backup <path>`
- Restore validates backup exists and is readable
- Restore requires explicit confirmation (dangerous operation)
- Documentation warns about data loss risks
- Restore logs all operations for audit trail

**EARS Tags**: [UPG-014]

---

### UPG-015: Rollback Command Interface
**EARS-ID**: UPG-015
**Type**: Event-driven
**Requirement**: When an upgrade fails or user requests rollback, the system **shall** provide a `mup cluster rollback` command to revert to the previous version.

```bash
mup cluster rollback <cluster-name> [flags]
```

**Optional Flags**:
- `--yes`: Skip confirmation prompts

**Rollback Procedure**:
1. Verify `previous` symlink exists and points to valid version
2. For each node (in reverse of upgrade order):
   - Stop process
   - Update `current` symlink to `previous` target
   - Start process with previous binary
   - Verify process health
3. Update cluster metadata to previous version
4. Clear upgrade progress state

**Limitations**:
- Rollback only works before FCV upgrade
- Rollback not supported after `--upgrade-fcv` (display error with warning)
- Rollback uses same phased approach (mongos → shards → config)
- For catastrophic failures, use config database restore [UPG-014]

**Acceptance Criteria**:
- Rollback command validates rollback is possible
- Blocks rollback if FCV was upgraded
- Executes in reverse order of upgrade
- Verifies each node after rollback
- Updates metadata to reflect rollback
- Displays clear warning about FCV limitation
- Suggests config DB restore if binary rollback fails

**EARS Tags**: [UPG-015]

---

## Traceability Matrix

| EARS ID | Component | Implementation Location | Test Location |
|---------|-----------|------------------------|---------------|
| UPG-001 | Version isolation | pkg/upgrade/version_manager.go | pkg/upgrade/version_manager_test.go |
| UPG-002 | Variant support | pkg/meta/meta.go, pkg/deploy/binary_manager.go | pkg/meta/meta_test.go, pkg/deploy/binary_manager_test.go |
| UPG-003 | Pre-upgrade validation | pkg/upgrade/validate.go | pkg/upgrade/validate_test.go |
| UPG-004 | Phased workflow | pkg/upgrade/upgrade.go | pkg/upgrade/upgrade_test.go |
| UPG-005 | Replica set upgrade | pkg/upgrade/replica_set.go | pkg/upgrade/replica_set_test.go |
| UPG-006 | Health verification | pkg/health/health.go | pkg/health/health_test.go |
| UPG-007 | Binary replacement | pkg/upgrade/binary.go | pkg/upgrade/binary_test.go |
| UPG-008 | FCV management | pkg/upgrade/fcv.go | pkg/upgrade/fcv_test.go |
| UPG-009 | Safety check hooks | pkg/upgrade/hooks.go | pkg/upgrade/hooks_test.go |
| UPG-010 | Upgrade plan | pkg/upgrade/plan.go | pkg/upgrade/plan_test.go |
| UPG-011 | Progress tracking | pkg/upgrade/state.go | pkg/upgrade/state_test.go |
| UPG-012 | Balancer management | pkg/health/balancer.go | pkg/health/balancer_test.go |
| UPG-013 | Upgrade command | cmd/mup/cluster.go | N/A (integration tests) |
| UPG-014 | Config DB backup/restore | pkg/upgrade/backup.go | pkg/upgrade/backup_test.go |
| UPG-015 | Rollback command | cmd/mup/cluster.go, pkg/upgrade/rollback.go | pkg/upgrade/rollback_test.go |

---

## Implementation Notes

### TDD Approach
All requirements must be implemented using Test-Driven Development:
1. Write failing tests for the requirement
2. Implement minimum code to pass tests
3. Refactor for clarity and performance
4. Tag tests and implementation with EARS IDs in comments

### EARS ID Tagging
All code implementing a requirement must include the EARS ID in a comment:
```go
// [UPG-007] Binary replacement using symlink switching
func (u *Upgrader) replaceBinary(node *Node, targetVersion string) error {
    // Implementation
}
```

### Documentation Updates
After implementing each requirement, update `docs/IMPLEMENTATION.md` with:
- EARS ID
- Implementation status
- Code location
- Test coverage
- Known limitations

---

## Future Enhancements
- UPG-016: Remote SSH-based upgrades (Phase 4+)
- UPG-017: Rolling upgrades with zero downtime guarantees
- UPG-018: Upgrade simulation mode with validation only
- UPG-019: Cross-variant migrations (mongo → percona)
- UPG-020: Partial upgrades (specific shards only)
- UPG-021: Automated full cluster backup before upgrade
- UPG-022: Performance benchmarking during upgrade
- UPG-023: Upgrade cost estimation (time and resources)
