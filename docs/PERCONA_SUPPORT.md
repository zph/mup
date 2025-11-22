# Percona Server for MongoDB Support

## Overview
Mup supports downloading and running Percona Server for MongoDB alongside official MongoDB binaries through the variant system.

## Supported Versions

### ✅ Fully Supported
The following Percona Server for MongoDB major versions are supported:

| Major Version | Latest Tested | Download Method | Notes |
|---------------|---------------|-----------------|-------|
| **8.0** | 8.0.12-4 | Tarball | Latest release, uses bookworm-minimal |
| **7.0** | 7.0.24-13 | Tarball | Stable release, uses bookworm-minimal |
| **6.0** | 6.0.25-20 | Tarball | Uses jammy-minimal tarball |
| **5.0** | 5.0.28-24 | Tarball | LTS release, uses jammy-minimal |
| **4.4** | 4.4.29-28 | .deb Packages | Uses Percona apt repository |
| **4.2** | 4.2.25-25 | .deb Packages | Uses Percona apt repository |
| **4.0** | 4.0.28-23 | .deb Packages | Uses Percona apt repository |
| **3.6** | 3.6.23-13 | .deb Packages | Special package structure (percona-server-mongodb-36) |

All major Percona Server for MongoDB versions from 3.6 through 8.0 are fully supported!

## Usage

### Deploy with Percona
```bash
# Deploy Percona 8.0 cluster
mup cluster deploy my-cluster topology.yaml --version 8.0.12-4 --variant percona

# Deploy Percona 7.0 cluster
mup cluster deploy my-cluster topology.yaml --version 7.0.24-13 --variant percona

# Deploy Percona 5.0 cluster
mup cluster deploy my-cluster topology.yaml --version 5.0.28-24 --variant percona
```

### Default Behavior
If no `--variant` flag is specified, mup defaults to the official MongoDB binaries (`variant=mongo`):
```bash
# This uses official MongoDB (default)
mup cluster deploy my-cluster topology.yaml --version 7.0
```

## Version Format
Percona versions follow the format: `major.minor.patch-build`
- Example: `7.0.24-13` (MongoDB 7.0.24, Percona build 13)
- Example: `8.0.12-4` (MongoDB 8.0.12, Percona build 4)

## Download Sources
Mup automatically downloads Percona binaries from:
```
https://downloads.percona.com/downloads/percona-server-mongodb-{major.minor}/percona-server-mongodb-{version}/binary/tarball/
```

The implementation tries multiple Linux distribution variants:
- bookworm (Debian 12) - Primary
- jammy (Ubuntu 22.04 LTS)
- focal (Ubuntu 20.04 LTS)
- noble (Ubuntu 24.04 LTS)
- bullseye (Debian 11)

## Limitations

### Platform Support
- **Linux (x86_64)**: ✅ Fully supported
- **Linux (aarch64/ARM64)**: ⚠️  Limited support (check availability per version)
- **macOS (Intel/ARM)**: ❌ Percona does not provide macOS binaries

### Version Gaps
Version 6.0 is notably absent from Percona's downloadable tarballs. If you need MongoDB 6.0, use the official MongoDB binaries:
```bash
mup cluster deploy my-cluster topology.yaml --version 6.0 --variant mongo
```

## Testing
Run the comprehensive version availability test:
```bash
go test -v ./pkg/deploy -run TestPercona_AvailableVersions
```

This test checks all major versions from 3.6 to 8.0 and reports which are actually downloadable.

## Upgrade Path
When upgrading between Percona versions:
```bash
# Future upgrade command (not yet implemented)
mup cluster upgrade my-cluster --to-version 8.0.12-4 --variant percona
```

## Metadata Storage
Cluster metadata stores the variant information:
```yaml
# ~/.mup/storage/clusters/my-cluster/meta.yaml
name: my-cluster
version: "8.0.12-4"
variant: percona
```

## Binary Caching
Downloaded binaries are cached in:
```
~/.mup/storage/packages/percona-{version}-{platform}-{arch}/
```

Example:
```
~/.mup/storage/packages/percona-8.0.12-4-linux-amd64/
├── bin/
│   ├── mongod
│   ├── mongos
│   └── mongosh
└── version.json
```

## Troubleshooting

### Version Not Available
If you encounter a "no valid Percona download URL found" error:
1. Verify the version exists at [Percona's downloads page](https://www.percona.com/downloads/percona-server-mongodb-8.0/)
2. Check the version format matches `major.minor.patch-build`
3. Consider using an officially supported version (5.0, 7.0, or 8.0)
4. Fall back to official MongoDB: `--variant mongo`

### macOS Users
Percona does not provide macOS binaries. macOS users should use official MongoDB:
```bash
mup cluster deploy my-cluster topology.yaml --version 7.0 --variant mongo
```

## References
- [Percona Server for MongoDB Documentation](https://docs.percona.com/percona-server-for-mongodb/)
- [Percona Downloads](https://www.percona.com/downloads)
- [Percona Server for MongoDB 8.0 Docs](https://docs.percona.com/percona-server-for-mongodb/8.0/)
- [Percona Server for MongoDB 7.0 Docs](https://docs.percona.com/percona-server-for-mongodb/7.0/)
- [Percona Server for MongoDB 5.0 Docs](https://docs.percona.com/percona-server-for-mongodb/5.0/)
