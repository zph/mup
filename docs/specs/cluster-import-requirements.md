# Cluster Import System Requirements

## Overview
This document specifies the functional requirements for the mup cluster import capability. The system enables importing existing MongoDB deployments (especially systemd-managed remote clusters) into mup's management structure, using rolling restarts with replica set awareness to minimize downtime.

**System Name:** mup
**Feature:** Cluster Import
**Version:** 1.0
**Last Updated:** 2024-11-22

## Requirements

### Discovery and Detection

**IMP-001:** Ubiquitous

**Requirement:**
The mup shall support both auto-detection and manual specification modes for importing clusters.

**Rationale:**
Users need flexibility to either let mup discover running MongoDB instances automatically or explicitly specify configuration when auto-detection is insufficient for complex deployments.

**Verification:**
Test both `--auto-detect` and manual config specification modes successfully import clusters.

---

**IMP-002:** Event Driven

**Requirement:**
When the user specifies `--auto-detect`, the mup shall scan for running MongoDB processes and systemd services.

**Rationale:**
Auto-detection reduces manual configuration burden by discovering existing MongoDB deployments automatically.

**Verification:**
Test with running MongoDB processes and verify mup discovers all mongod/mongos instances and their systemd services.

---

**IMP-003:** Event Driven

**Requirement:**
When the user specifies manual mode with `--config`, `--data-dir`, and `--port` options, the mup shall use the provided configuration paths.

**Rationale:**
Manual specification is required when auto-detection fails or for complex deployments requiring explicit configuration.

**Verification:**
Test manual mode by providing explicit paths and verify mup uses specified configuration.

---

**IMP-004:** Event Driven

**Requirement:**
When discovering MongoDB processes, the mup shall detect associated systemd service units.

**Rationale:**
Systemd services must be identified to enable graceful migration from systemd to supervisord management.

**Verification:**
Test with systemd-managed MongoDB and verify mup identifies service unit files.

---

**IMP-005:** Event Driven

**Requirement:**
When discovering MongoDB processes, the mup shall query each instance for version, variant, and topology information.

**Rationale:**
Version and topology information is essential for creating appropriate directory structures and configurations.

**Verification:**
Test discovery against MongoDB instances and verify mup correctly identifies version (e.g., 7.0.5), variant (official/Percona), and topology (standalone/replica set/sharded).

---

### Systemd Service Management

**IMP-006:** Event Driven

**Requirement:**
When a systemd service is discovered, the mup shall parse the service unit file to extract MongoDB configuration.

**Rationale:**
Systemd unit files contain critical information about ExecStart commands, data directories, user/group, and environment variables needed for import.

**Verification:**
Test with various systemd unit files and verify mup correctly extracts config paths, data directories, and runtime parameters.

---

**IMP-007:** Event Driven

**Requirement:**
When parsing systemd unit files, the mup shall extract the MongoDB configuration file path from the ExecStart directive.

**Rationale:**
The config file path in ExecStart (typically via `--config` flag) is the primary source for MongoDB configuration settings.

**Verification:**
Test with unit files containing `ExecStart=/usr/bin/mongod --config /etc/mongod.conf` and verify mup extracts `/etc/mongod.conf`.

---

**IMP-008:** Event Driven

**Requirement:**
When importing a cluster, the mup shall disable systemd services using `systemctl disable`.

**Rationale:**
Systemd services must be disabled to prevent conflicts when mup manages processes via supervisord, while keeping them available for rollback.

**Verification:**
Test import and verify systemd services are disabled but not removed, and do not start on system boot.

---

**IMP-009:** Unwanted Behaviour

**Requirement:**
If the import process fails, then the mup shall re-enable previously disabled systemd services.

**Rationale:**
Rollback to systemd management ensures MongoDB continues running even if import fails.

**Verification:**
Test by forcing import failure and verify systemd services are re-enabled and can be started normally.

---

### Configuration Import and Conversion

**IMP-010:** Event Driven

**Requirement:**
When importing configuration, the mup shall parse existing mongod.conf and mongos.conf files.

**Rationale:**
Existing configuration files contain all MongoDB settings that must be preserved during import.

**Verification:**
Test with various MongoDB config files (YAML and legacy formats) and verify mup successfully parses all settings.

---

**IMP-011:** Event Driven

**Requirement:**
When importing configuration, the mup shall generate mup-compatible configuration files in the version-specific conf/ directory.

**Rationale:**
Mup's template-based system requires configs in standardized locations with version-specific settings.

**Verification:**
Test import and verify configs are created in `~/.mup/storage/clusters/<name>/v<version>/conf/` with correct MongoDB settings.

---

**IMP-012:** State Driven

**Requirement:**
While importing configuration, the mup shall preserve custom MongoDB settings not defined in default templates.

**Rationale:**
Users may have custom MongoDB parameters that must not be lost during import to maintain production behavior.

**Verification:**
Test with config files containing custom parameters (e.g., custom operationProfiling settings) and verify they are preserved in generated configs.

---

### Directory Structure Creation

**IMP-013:** Event Driven

**Requirement:**
When importing a cluster, the mup shall create version-specific directory structure at `~/.mup/storage/clusters/<name>/v<version>/`.

**Rationale:**
Mup's architecture uses per-version directories to support zero-downtime upgrades and rollbacks.

**Verification:**
Test import and verify directory structure includes `bin/`, `conf/`, `logs/`, and data symlinks.

---

**IMP-014:** Event Driven

**Requirement:**
When creating directory structure, the mup shall create symlinks from the data/ directory to existing MongoDB data directories without moving data files.

**Rationale:**
Symlinks avoid lengthy data migration and associated downtime while integrating with mup's structure.

**Verification:**
Test import and verify symlinks exist (e.g., `data/localhost-27017 -> /var/lib/mongodb`), data files remain at original locations, and MongoDB can read/write data.

---

**IMP-015:** Event Driven

**Requirement:**
When creating directory structure, the mup shall create empty conf/, logs/, and bin/ directories in the version-specific directory.

**Rationale:**
These directories are required for mup's operational model of storing configs, logs, and binaries per version.

**Verification:**
Test import and verify `v<version>/conf/`, `v<version>/logs/`, and `v<version>/bin/` directories exist.

---

**IMP-016:** Event Driven

**Requirement:**
When completing directory structure creation, the mup shall create a 'current' symlink pointing to the version-specific directory.

**Rationale:**
The 'current' symlink enables mup to track the active version and simplifies version management operations.

**Verification:**
Test import and verify `current -> v<version>` symlink exists and points to the correct version directory.

---

### Process Migration with Rolling Restart

**IMP-017:** State Driven

**Requirement:**
While importing a replica set, the mup shall identify and order members with SECONDARY members first and PRIMARY member last.

**Rationale:**
SECONDARY-first migration minimizes downtime by maintaining replica set availability throughout the migration process.

**Verification:**
Test with 3-member replica set and verify mup migrates 2 secondaries before stepping down and migrating the primary.

---

**IMP-018:** Event Driven

**Requirement:**
When migrating a SECONDARY member, the mup shall stop the systemd service, then start the process via supervisord.

**Rationale:**
Transitioning from systemd to supervisord management enables mup's process lifecycle management while maintaining MongoDB operation.

**Verification:**
Test SECONDARY migration and verify systemd service stops gracefully (SIGINT), supervisord starts the process, and the member returns to SECONDARY state.

---

**IMP-019:** Event Driven

**Requirement:**
When all SECONDARY members have been migrated, the mup shall step down the PRIMARY using `rs.stepDown()`.

**Rationale:**
Stepping down the primary triggers replica set election, ensuring a newly-migrated secondary becomes primary before migrating the former primary.

**Verification:**
Test replica set import and verify mup executes `rs.stepDown()` after secondaries are migrated, and a new primary is elected.

---

**IMP-020:** Event Driven

**Requirement:**
When migrating the former PRIMARY member (now SECONDARY), the mup shall follow the SECONDARY migration process.

**Rationale:**
Once stepped down, the former primary is a regular secondary and can be migrated safely.

**Verification:**
Test replica set import and verify former primary is migrated after becoming secondary, and replica set remains healthy.

---

**IMP-021:** State Driven

**Requirement:**
While migrating each replica set member, the mup shall verify member health by checking replication lag is less than 30 seconds.

**Rationale:**
Health verification after each member ensures replica set integrity and prevents cascading failures during migration.

**Verification:**
Test migration and verify mup checks replication lag after each member migration and fails if lag exceeds 30 seconds.

---

**IMP-022:** Optional Feature

**Requirement:**
Where the cluster is sharded, the mup shall import config server replica set first, then shard replica sets, then mongos instances.

**Rationale:**
Sharded cluster components have dependencies: config servers must be available for shards, and both must be available for mongos routers.

**Verification:**
Test with sharded cluster and verify mup migrates in correct order: config servers → shards → mongos.

---

### Validation and Safety

**IMP-023:** Event Driven

**Requirement:**
When starting an import operation, the mup shall execute pre-flight checks for user permissions, disk space, and MongoDB accessibility.

**Rationale:**
Pre-flight validation prevents failures mid-import due to insufficient permissions or resources.

**Verification:**
Test import with insufficient disk space and verify mup detects and reports the issue before making changes.

---

**IMP-024:** Event Driven

**Requirement:**
When the user specifies `--dry-run`, the mup shall display the import plan without making any configuration or process changes.

**Rationale:**
Dry-run mode allows users to preview import actions and verify correctness before committing to changes.

**Verification:**
Test `--dry-run` mode and verify mup displays discovery results, planned directory structure, and migration steps without creating files or stopping processes.

---

**IMP-025:** Unwanted Behaviour

**Requirement:**
If a health check fails during migration, then the mup shall halt the migration and rollback by re-enabling systemd services.

**Rationale:**
Automatic rollback on failure prevents leaving the cluster in a partially-migrated, unhealthy state.

**Verification:**
Test by simulating health check failure (e.g., block replica set communication) and verify mup stops migration and restores systemd management.

---

**IMP-026:** Event Driven

**Requirement:**
When the import completes successfully, the mup shall verify replica set status and connection command functionality.

**Rationale:**
Post-import verification ensures the cluster is fully operational under mup management before declaring success.

**Verification:**
Test import completion and verify mup checks `rs.status()`, confirms all members are healthy, and validates the connection command works.

---

### SSH Executor Support

**IMP-027:** Optional Feature

**Requirement:**
Where the user specifies `--ssh-host`, the mup shall execute all import operations via SSH on the remote host.

**Rationale:**
Remote import capability is essential for managing production MongoDB clusters running on remote servers.

**Verification:**
Test with `--ssh-host user@host` and verify mup executes all discovery, configuration, and process management operations on the remote host.

---

**IMP-028:** State Driven

**Requirement:**
While using the SSH executor, the mup shall detect and parse systemd services on the remote host.

**Rationale:**
Remote systemd management requires SSH-based execution of systemctl commands and file operations.

**Verification:**
Test remote import and verify mup discovers systemd services, parses unit files, and manages services on the remote host via SSH.

---

### Metadata and State Management

**IMP-029:** Event Driven

**Requirement:**
When import completes successfully, the mup shall create a meta.yaml file containing cluster name, version, variant, topology, and connection command.

**Rationale:**
Metadata file enables mup to manage the imported cluster using standard cluster management commands.

**Verification:**
Test import and verify `meta.yaml` exists with correct cluster information and can be used by `mup cluster display` and other commands.

---

**IMP-030:** Event Driven

**Requirement:**
When import encounters an error, the mup shall save progress checkpoint data to enable resume of the import operation.

**Rationale:**
Checkpoint data prevents restarting the entire import process after transient failures, especially important for large clusters.

**Verification:**
Test by forcing failure mid-import, then resuming, and verify mup skips already-completed steps.

---

### Supervisor Configuration

**IMP-031:** Event Driven

**Requirement:**
When importing a cluster, the mup shall generate a unified supervisord configuration file with all MongoDB process definitions.

**Rationale:**
Supervisord configuration is required for mup's process lifecycle management using the supervisor daemon.

**Verification:**
Test import and verify `supervisor.ini` exists in the version directory with correct program definitions for all MongoDB processes.

---

**IMP-032:** State Driven

**Requirement:**
While generating supervisor configuration, the mup shall set `fork: false` in MongoDB configs and `autorestart = unexpected` in supervisor programs.

**Rationale:**
Supervisor manages process lifecycle (no forking) and should only auto-restart on unexpected failures, not intentional stops.

**Verification:**
Test import and verify MongoDB configs have `fork: false`, supervisor.ini has `autorestart = unexpected`, and processes behave correctly under supervisord.

---

**IMP-033:** Event Driven

**Requirement:**
When importing a cluster, the mup shall generate a topology.yaml file that represents the discovered cluster topology.

**Rationale:**
A topology.yaml file enables declarative management of the imported cluster, allowing future operations like scale-out, reconfiguration, and re-deployment to understand the cluster structure.

**Verification:**
Test import and verify topology.yaml exists with correct representation of all discovered nodes, replica sets, and configuration settings.

---

**IMP-034:** State Driven

**Requirement:**
While generating topology.yaml, the mup shall correctly represent different topology types: standalone, replica set, and sharded cluster configurations.

**Rationale:**
Different MongoDB topologies require different YAML structures. Standalone uses simple mongod_servers, replica sets include replica_set names, sharded clusters include config servers and shard definitions.

**Verification:**
Test import for each topology type and verify generated topology.yaml matches the expected structure for that topology (standalone, replica set, sharded).

---

**IMP-035:** Event Driven

**Requirement:**
When topology generation is complete, the mup shall save the topology.yaml file to the cluster directory at `<cluster-dir>/topology.yaml`.

**Rationale:**
The topology.yaml must be persisted alongside meta.yaml to provide a complete declarative description of the cluster for future management operations.

**Verification:**
Test import and verify topology.yaml exists at the correct path with proper permissions and valid YAML syntax.

---

## Traceability

All implementation code must include EARS requirement IDs in comments to enable traceability. For example:

```go
// IMP-001: Support both auto-detect and manual modes
func (i *Importer) Import(opts ImportOptions) error {
    if opts.AutoDetect {
        // IMP-002: Scan for MongoDB processes
        return i.autoDetectImport(opts)
    }
    // IMP-003: Use provided config paths
    return i.manualImport(opts)
}
```

## Requirements Summary

- **Total Requirements:** 35
- **Ubiquitous:** 1
- **State Driven:** 6
- **Event Driven:** 24
- **Optional Feature:** 2
- **Unwanted Behaviour:** 2

## Verification Strategy

1. **Unit Tests:** Each requirement with isolated behavior (IMP-006, IMP-010, IMP-014, etc.)
2. **Integration Tests:** Multi-component workflows (IMP-017 through IMP-022 for rolling restart)
3. **System Tests:** End-to-end import scenarios with real MongoDB clusters
4. **SSH Tests:** Remote import validation (IMP-027, IMP-028)
