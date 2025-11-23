# Implementation Details

## Folder Structure

### Per-Version Directory Layout

Mup uses a per-version directory structure to support MongoDB version upgrades with easy rollback capability. This design keeps data directories version-independent while isolating version-specific configuration, binaries, and logs.

#### Structure Overview

```
~/.mup/storage/clusters/<cluster-name>/
├── data/
│   ├── localhost-27017/          # Data dir (version-independent)
│   ├── localhost-27018/
│   └── localhost-27019/
├── v3.6.23/                       # Old version directory
│   ├── bin/
│   │   ├── mongod               # MongoDB binaries for this version
│   │   ├── mongos
│   │   └── mongosh
│   ├── conf/
│   │   ├── localhost-27017/
│   │   │   └── mongod.conf      # Version-specific config
│   │   ├── localhost-27018/
│   │   │   └── mongod.conf
│   │   └── localhost-27019/
│   │       └── mongod.conf
│   ├── logs/
│   │   ├── mongod-27017.log
│   │   ├── mongod-27018.log
│   │   └── mongod-27019.log
│   ├── supervisor.ini             # Supervisor config for this version
│   ├── supervisor.log
│   └── supervisor.pid
├── v4.0.28/                       # New version directory after upgrade
│   ├── bin/
│   │   ├── mongod
│   │   ├── mongos
│   │   └── mongosh
│   ├── conf/
│   │   ├── localhost-27017/
│   │   │   └── mongod.conf
│   │   ├── localhost-27018/
│   │   │   └── mongod.conf
│   │   └── localhost-27019/
│   │       └── mongod.conf
│   ├── logs/
│   │   ├── mongod-27017.log
│   │   ├── mongod-27018.log
│   │   └── mongod-27019.log
│   ├── supervisor.ini
│   ├── supervisor.log
│   └── supervisor.pid
├── current -> v4.0.28             # Symlink to active version
├── previous -> v3.6.23            # Symlink to previous version
└── meta.yaml                      # Cluster metadata
```

#### Design Rationale

1. **Data Directory (version-independent)**
   - Location: `<cluster-dir>/data/<host-port>/`
   - Shared across all versions
   - MongoDB handles data format compatibility within supported upgrade paths
   - Never modified during upgrade (except by MongoDB itself)

2. **Version-Specific Directories** (`v{version}/`)
   - Contains everything specific to that MongoDB version
   - Allows multiple versions to coexist
   - Enables instant rollback by switching supervisor configs

3. **Binaries** (`v{version}/bin/`)
   - MongoDB binaries copied into version directory
   - Not symlinked - actual copies for isolation
   - Includes: mongod, mongos, mongosh/mongo

4. **Configuration** (`v{version}/conf/`)
   - Version-specific mongod.conf/mongos.conf files
   - May contain version-specific settings
   - Template rendering is version-aware

5. **Logs** (`v{version}/logs/`)
   - Each version has its own log files
   - Helps debug version-specific issues
   - Supervisor logs also kept per-version

6. **Supervisor** (`v{version}/supervisor.ini`)
   - Each version has its own supervisord configuration
   - Points to version-specific binaries, configs, and logs
   - Separate supervisor.pid and supervisor.log per version

7. **Symlinks** (`current` and `previous`)
   - `current` -> points to active version directory (e.g., `v4.0.28`)
   - `previous` -> points to last version directory (e.g., `v3.6.23`)
   - Allows version-agnostic references in code and scripts
   - Simplifies rollback: swap symlinks and restart supervisor

### Upgrade Process with Version Directories

The upgrade workflow handles the version directory transition:

1. **Pre-Upgrade State**
   - Cluster running with `v3.6.23/supervisor.ini`
   - Data in shared `data/` directories

2. **Upgrade Steps**
   - Create new version directory: `v4.0.28/`
   - Download and copy MongoDB 4.0.28 binaries to `v4.0.28/bin/`
   - Generate new configs in `v4.0.28/conf/` (pointing to shared data dirs)
   - Generate new `v4.0.28/supervisor.ini` with new paths
   - Stop old supervisord (v3.6.23)
   - Update `previous` symlink to point to `v3.6.23`
   - Update `current` symlink to point to `v4.0.28`
   - Start new supervisord with `v4.0.28/supervisor.ini`
   - MongoDB processes now run with 4.0.28 binaries using shared data
   - Update meta.yaml with new version

3. **Post-Upgrade State**
   - Cluster running with `v4.0.28/supervisor.ini`
   - Data still in shared `data/` directories
   - Old `v3.6.23/` directory preserved for rollback

4. **Rollback (if needed)**
   - Stop current supervisord (v4.0.28)
   - Start old supervisord with `v3.6.23/supervisor.ini`
   - Cluster reverted to old version (data unchanged)

### Metadata Tracking

The `meta.yaml` file tracks the current active version:

```yaml
name: my-cluster
version: "4.0.28"               # Updated after upgrade
variant: mongo
bin_path: /path/to/v4.0.28/bin  # Points to active version
supervisor_config_path: /path/to/v4.0.28/supervisor.ini
supervisor_pid_file: /path/to/v4.0.28/supervisor.pid
```

### Benefits

1. **Easy Rollback**: Switch supervisor configs to revert
2. **Version Isolation**: No conflicts between versions
3. **Data Safety**: Data directory never moved or modified
4. **Debugging**: Version-specific logs help troubleshoot
5. **Audit Trail**: All versions preserved for history

### Implementation Files

- `pkg/supervisor/config.go` - Supervisor config generation with version paths
- `pkg/upgrade/local.go` - Upgrade orchestration
- `pkg/meta/meta.go` - Metadata management
- `pkg/deploy/binary_manager.go` - Binary downloads and caching
- `pkg/deploy/deploy.go` - Deployment with per-version directories

## Future: Import Command

**Planned Feature**: `mup cluster import` command to bring existing MongoDB clusters into mup's management with the per-version folder layout.

### Import Process

1. **Discovery**: Detect running MongoDB processes and their configurations
2. **Version Detection**: Identify MongoDB version of running processes
3. **Data Migration**:
   - Keep data directories in place (or symlink to existing location)
   - Create version-specific directories for current version
4. **Configuration Import**:
   - Parse existing mongod.conf files
   - Generate mup-compatible configs in `v{version}/conf/`
5. **Supervisor Setup**:
   - Generate supervisord configuration
   - Stop MongoDB processes if needed
   - Start under supervisord management
6. **Metadata Creation**: Generate `meta.yaml` with cluster information
7. **Symlink Setup**: Create `current` and `previous` symlinks

### Import Command Syntax
```bash
mup cluster import <cluster-name> --data-dir /path/to/data --port 27017
mup cluster import <cluster-name> --auto-detect  # Scan for running MongoDB
```

This allows users to adopt mup for existing clusters without downtime or data migration.
