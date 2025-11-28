# Path Management System Requirements

## Overview
This document specifies the functional requirements for the mup Path Management System. The system provides centralized, reusable abstractions for folder structure management across deploy, upgrade, and import operations.

**System Name:** Path Management System
**Version:** 1.0
**Last Updated:** 2024-11-24
**Reference:** TiUP path conventions - https://docs.pingcap.com/tidb/stable/tiup-cluster-topology-reference/

## Requirements

### Path Resolution

**REQ-PM-001:** Ubiquitous

**Requirement:**
The Path Management System shall provide a PathResolver interface with methods for resolving data directories, log directories, config directories, and binary directories.

**Rationale:**
A common interface enables consistent path resolution across all operations (deploy, upgrade, import) and supports multiple deployment modes (local, SSH).

**Verification:**
Code review confirms PathResolver interface exists with methods: DataDir(), LogDir(), ConfigDir(), BinDir().

---

**REQ-PM-002:** Optional Feature

**Requirement:**
Where local deployment mode is active, the Path Management System shall resolve all paths relative to `~/.mup/storage/clusters/<cluster-name>/`.

**Rationale:**
Local deployments (playground mode) use a standardized directory structure under the user's home directory, ignoring topology-specified paths.

**Verification:**
Unit test verifies LocalPathResolver returns paths under ~/.mup/storage/clusters/ regardless of topology configuration.

---

**REQ-PM-003:** Optional Feature

**Requirement:**
Where SSH deployment mode is active, the Path Management System shall resolve paths according to TiUP topology conventions.

**Rationale:**
Remote deployments must follow TiUP path conventions for compatibility with existing MongoDB cluster management practices. This aligns with CLAUDE.md guidance: "When deciding configuration behavior, especially deploy_dir, you SHOULD follow https://docs.pingcap.com/tidb/stable/tiup-cluster-topology-reference/".

**Verification:**
Unit test verifies RemotePathResolver follows TiUP conventions for absolute paths, relative paths, and default cascading.

---

### TiUP Path Conventions

**REQ-PM-004:** Event Driven

**Requirement:**
When a topology specifies an absolute path for deploy_dir at instance level, the Path Management System shall use that exact path.

**Rationale:**
TiUP specification: "If the absolute path of deploy_dir is configured at the instance level, the actual deployment directory is deploy_dir configured for the instance." Absolute paths override all defaults.

**Verification:**
Test with topology containing instance-level absolute path (e.g., /opt/mongodb/mongod-27017) and verify exact path is used.

---

**REQ-PM-005:** State Driven

**Requirement:**
While a topology specifies a relative path for deploy_dir, the Path Management System shall resolve the path as `/home/<user>/<global.deploy_dir>/<instance.deploy_dir>`.

**Rationale:**
TiUP specification: "When relative paths are used, the component is deployed to the /home/<global.user>/<global.deploy_dir>/<instance.deploy_dir> directory." Relative paths nest within the global deploy directory.

**Verification:**
Test with topology containing relative path and verify nesting under /home/<user>/<global.deploy_dir>/.

---

**REQ-PM-006:** State Driven

**Requirement:**
While an instance-level data_dir is not specified, the Path Management System shall use the global data_dir value.

**Rationale:**
TiUP specification: "If no instance-level specification exists, its default value is <global.data_dir>." Global values cascade to instances as defaults.

**Verification:**
Test with topology missing instance data_dir and verify global data_dir is used.

---

**REQ-PM-007:** State Driven

**Requirement:**
While a data_dir is specified as a relative path, the Path Management System shall resolve the path as `<deploy_dir>/<data_dir>`.

**Rationale:**
TiUP specification: "When using relative paths, the component data is placed in <deploy_dir>/<data_dir>." Relative data paths nest within deploy directory.

**Verification:**
Test with relative data_dir and verify nesting within deploy_dir.

---

**REQ-PM-008:** State Driven

**Requirement:**
While a log_dir is specified as a relative path, the Path Management System shall resolve the path as `<deploy_dir>/<log_dir>`.

**Rationale:**
TiUP specification: "Relative log paths follow the same nesting convention: <deploy_dir>/<log_dir>." Consistent nesting for all directory types.

**Verification:**
Test with relative log_dir and verify nesting within deploy_dir.

---

### Version Directory Management

**REQ-PM-009:** Ubiquitous

**Requirement:**
The Path Management System shall name version-specific directories using the pattern `v<version>` where version is the MongoDB semantic version.

**Rationale:**
Consistent version directory naming enables version-specific configurations and binaries while keeping data directories version-independent for seamless upgrades.

**Verification:**
Code review confirms version directory naming uses "v" + semantic version string (e.g., "v7.0.0", "v8.0.0").

---

**REQ-PM-010:** Optional Feature

**Requirement:**
Where local deployment mode is active, the Path Management System shall create per-version subdirectories for bin, mongod-<port>/config, mongod-<port>/log, mongos-<port>/config, mongos-<port>/log, and config-<port>/config.

**Rationale:**
Version-specific directories for executables, configs, and logs enable rolling upgrades where old and new versions run side-by-side temporarily.

**Verification:**
Test confirms directory structure: v<version>/bin/, v<version>/mongod-<port>/config/, v<version>/mongod-<port>/log/.

---

**REQ-PM-011:** Ubiquitous

**Requirement:**
The Path Management System shall store data directories outside version-specific paths using the pattern `data/<host>-<port>`.

**Rationale:**
Data directories must be version-independent because MongoDB handles forward compatibility of data files. Keeping data outside version paths eliminates data copying during upgrades.

**Verification:**
Test confirms data directories are created at <cluster-dir>/data/<host>-<port>/, not within v<version>/ directories.

---

### Symlink Lifecycle Management

**REQ-PM-012:** Event Driven

**Requirement:**
When a new cluster is deployed, the Path Management System shall create a `current` symlink pointing to the `v<version>` directory.

**Rationale:**
The current symlink provides a stable reference to the active version directory, simplifying process management and monitoring.

**Verification:**
Test fresh deploy and verify current symlink exists and points to deployed version directory.

---

**REQ-PM-013:** Event Driven

**Requirement:**
When an upgrade begins, the Path Management System shall create a `next` symlink pointing to the target `v<target-version>` directory.

**Rationale:**
The next symlink identifies the upgrade target version during the rolling upgrade process, enabling the system to manage two versions simultaneously.

**Verification:**
Test upgrade initiation and verify next symlink is created pointing to target version directory.

---

**REQ-PM-014:** Event Driven

**Requirement:**
When an upgrade completes successfully, the Path Management System shall atomically update symlinks by: (1) creating `previous` pointing to old current target, (2) updating `current` to point to next target, (3) removing `next` symlink.

**Rationale:**
Atomic symlink switching ensures clean version transitions. Previous symlink enables rollback to the last working version.

**Verification:**
Test upgrade completion and verify: previous → old version, current → new version, next removed.

---

**REQ-PM-015:** Unwanted Behaviour

**Requirement:**
If an upgrade fails, then the Path Management System shall preserve the `current` and `next` symlinks unchanged.

**Rationale:**
Failed upgrades should not affect the running system. Preserving symlinks maintains system state for diagnostics and retry attempts.

**Verification:**
Test upgrade failure scenario and verify current still points to old version and next still points to target.

---

### Data Directory Strategy

**REQ-PM-016:** State Driven

**Requirement:**
While performing an upgrade, the Path Management System shall reuse existing data directories without copying or moving data files.

**Rationale:**
MongoDB data files are forward-compatible across versions. Reusing data directories eliminates expensive I/O operations and reduces upgrade time from hours to minutes for large datasets.

**Verification:**
Test upgrade and verify no data file copying occurs, only config regeneration in new version directory.

---

**REQ-PM-017:** Event Driven

**Requirement:**
When importing an existing cluster, the Path Management System shall create symlinks from `data/<host>-<port>` to the discovered existing data directory paths.

**Rationale:**
Import must preserve existing data in-place to avoid massive data transfers. Symlinks enable mup's path structure while keeping data at original locations.

**Verification:**
Test import with existing data at /var/lib/mongodb and verify symlink created, no data movement.

---

### Path Construction and Naming

**REQ-PM-018:** Ubiquitous

**Requirement:**
The Path Management System shall name mongod process directories using the pattern `mongod-<port>` where port is the listener port number.

**Rationale:**
Port-based naming enables multiple mongod processes per host with unique directory names, following MongoDB and TiUP conventions.

**Verification:**
Code review confirms mongod directory naming uses "mongod-" + port number.

---

**REQ-PM-019:** Ubiquitous

**Requirement:**
The Path Management System shall name mongos process directories using the pattern `mongos-<port>` where port is the listener port number.

**Rationale:**
Consistent naming convention with mongod processes, distinguishing between process types.

**Verification:**
Code review confirms mongos directory naming uses "mongos-" + port number.

---

**REQ-PM-020:** Ubiquitous

**Requirement:**
The Path Management System shall name config server process directories using the pattern `config-<port>` where port is the listener port number.

**Rationale:**
Config servers require distinct directory naming from mongod processes even though they run mongod binary, enabling clear identification in sharded clusters.

**Verification:**
Code review confirms config server directory naming uses "config-" + port number.

---

### Centralization and Reusability

**REQ-PM-021:** Ubiquitous

**Requirement:**
The Path Management System shall implement all path construction logic in a single pkg/paths package.

**Rationale:**
Centralization eliminates duplication across deploy/upgrade/import operations (currently 30+ scattered filepath.Join calls), ensures consistency, and simplifies maintenance. Single source of truth for all path logic.

**Verification:**
Code review confirms pkg/paths package exists and deploy/upgrade/import packages import and use it exclusively, no local path construction.

---

**REQ-PM-022:** Ubiquitous

**Requirement:**
The Path Management System shall be used by deploy operations, upgrade operations, and import operations.

**Rationale:**
All operations that manipulate cluster filesystem structure must use consistent path abstractions to prevent divergence and ensure interoperability.

**Verification:**
Code review confirms pkg/deploy, pkg/upgrade, and pkg/import all depend on pkg/paths and use PathResolver interface.

---

### Testing and Simulation

**REQ-PM-023:** Ubiquitous

**Requirement:**
The Path Management System shall provide a test simulation harness that validates path logic without filesystem I/O operations.

**Rationale:**
Fast unit testing of path resolution logic is critical for TDD workflow. Simulated filesystem enables testing path logic independently from actual file operations, reducing test runtime from seconds to milliseconds.

**Verification:**
Unit tests execute in <100ms and validate path resolution logic using simulation harness, no temp directories created.

---

**REQ-PM-024:** Ubiquitous

**Requirement:**
The Path Management System shall provide unit tests for LocalPathResolver and RemotePathResolver covering all path types.

**Rationale:**
Each resolver implementation must be tested independently to ensure correct behavior for local and SSH deployment modes.

**Verification:**
Test suite includes resolver_test.go with >90% code coverage of both resolver implementations.

---

**REQ-PM-025:** Ubiquitous

**Requirement:**
The Path Management System shall provide integration tests that simulate complete deploy, upgrade, and import path flows without executing operations.

**Rationale:**
Integration tests validate end-to-end path logic across operation lifecycles, catching issues that unit tests might miss, while avoiding expensive e2e test execution.

**Verification:**
Test suite includes integration_test.go simulating full deploy→upgrade→rollback path flows, completing in <1s.

---

**REQ-PM-026:** Ubiquitous

**Requirement:**
The Path Management System shall include tests validating TiUP path convention compliance.

**Rationale:**
Explicit test coverage ensures RemotePathResolver correctly implements TiUP specifications, preventing compatibility issues with existing cluster management tools.

**Verification:**
Test suite includes tiup_compliance_test.go with test cases for each TiUP path rule (REQ-PM-004 through REQ-PM-008).

---

### Error Handling and Validation

**REQ-PM-027:** Unwanted Behaviour

**Requirement:**
If a path resolution results in an empty string, then the Path Management System shall return an error indicating which path type failed resolution.

**Rationale:**
Empty paths would cause silent failures during deployment. Explicit errors enable early detection of configuration issues.

**Verification:**
Unit test with invalid topology configuration verifies error returned with clear message: "failed to resolve data_dir for node..."

---

**REQ-PM-028:** Unwanted Behaviour

**Requirement:**
If a symlink operation fails, then the Path Management System shall return an error preserving the original error context.

**Rationale:**
Symlink failures during upgrade (disk full, permission issues) must be surfaced clearly to prevent partial upgrades.

**Verification:**
Unit test simulates symlink failure and verifies error is returned with filesystem error details.

---

## Traceability

All code implementing these requirements MUST include EARS reference comments using the pattern:

```go
// REQ-PM-XXX: <brief requirement summary>
```

Example:
```go
// REQ-PM-001: PathResolver interface provides consistent path resolution methods
type PathResolver interface {
    DataDir(node *topology.Node) (string, error)
    LogDir(node *topology.Node) (string, error)
    ConfigDir(node *topology.Node) (string, error)
    BinDir(version string) (string, error)
}
```

## Implementation Notes

- **TiUP Compliance**: Requirements REQ-PM-003 through REQ-PM-008 implement TiUP path conventions as specified in CLAUDE.md and https://docs.pingcap.com/tidb/stable/tiup-cluster-topology-reference/
- **Test-First Development**: All requirements must have corresponding tests written before implementation (TDD approach per global CLAUDE.md)
- **Version Independence**: Requirements REQ-PM-011 and REQ-PM-016 establish version-independent data directory strategy critical for zero-copy upgrades
- **Symlink Strategy**: Requirements REQ-PM-012 through REQ-PM-015 define symlink lifecycle enabling atomic version switches and rollback capability
