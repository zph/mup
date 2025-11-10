# MongoDB Configuration Template System

This document outlines the design for replacing string interpolation with a template-based configuration generation system that supports version-specific MongoDB configurations.

## Problem Statement

Currently, config generation in `pkg/deploy/deploy.go` uses string interpolation (`fmt.Sprintf`). This approach:
- Becomes unwieldy with complex configs
- Makes it hard to support version-specific options
- Mixes config content with Go code
- Difficult to maintain and validate

## Goals

1. **Template-based generation**: Use Go's `text/template` for clean separation
2. **Version-aware**: Different templates for MongoDB version ranges
3. **Type-safe**: Strongly typed template data structures
4. **Maintainable**: Easy to add new versions or modify configs
5. **Testable**: Templates can be tested independently

## MongoDB Version Ranges

MongoDB configuration options have changed significantly across versions. We'll support these ranges:

### Range 1: MongoDB 3.6 - 4.0
**Key characteristics:**
- YAML config format introduced in 3.6
- Basic replication and sharding
- MMAPv1 still supported (though deprecated)
- Simple TLS options

**Config differences:**
- `net.ssl.*` for TLS (not `net.tls.*`)
- No `setParameter.enableTestCommands`
- Different storage engine defaults

### Range 2: MongoDB 4.2 - 4.4
**Key characteristics:**
- Distributed transactions across shards
- Field-level encryption
- Wildcard indexes
- TLS terminology (replaces SSL)

**Config differences:**
- `net.tls.*` preferred over `net.ssl.*`
- New security options
- `sharding.clusterRole` vs older `sharding.clusterRole`

### Range 3: MongoDB 5.0 - 6.0
**Key characteristics:**
- Time series collections
- Versioned API
- Native time series support
- Enhanced security options

**Config differences:**
- New `setParameter` options
- Enhanced audit logging
- Changed snapshot read concern defaults

### Range 4: MongoDB 7.0+
**Key characteristics:**
- Queryable encryption
- Clustered collections
- Enhanced change streams
- Modern TLS requirements

**Config differences:**
- Stricter TLS defaults
- New queryable encryption params
- Updated audit options

## Architecture

```
pkg/template/
├── manager.go              # Template manager and version selection
├── types.go                # Template data structures
├── mongod/
│   ├── mongod-3.6.conf.tmpl
│   ├── mongod-4.2.conf.tmpl
│   ├── mongod-5.0.conf.tmpl
│   └── mongod-7.0.conf.tmpl
├── mongos/
│   ├── mongos-3.6.conf.tmpl
│   ├── mongos-4.2.conf.tmpl
│   ├── mongos-5.0.conf.tmpl
│   └── mongos-7.0.conf.tmpl
└── config/
    ├── config-3.6.conf.tmpl
    ├── config-4.2.conf.tmpl
    ├── config-5.0.conf.tmpl
    └── config-7.0.conf.tmpl
```

## Implementation

### 1. Version Selection

```go
// pkg/template/manager.go

package template

import (
	"embed"
	"fmt"
	"text/template"
	"github.com/hashicorp/go-version"
)

//go:embed mongod/*.tmpl mongos/*.tmpl config/*.tmpl
var templates embed.FS

type Manager struct {
	templates map[string]*template.Template
}

func NewManager() (*Manager, error) {
	m := &Manager{
		templates: make(map[string]*template.Template),
	}

	// Load all templates at initialization
	if err := m.loadTemplates(); err != nil {
		return nil, err
	}

	return m, nil
}

// GetTemplate returns the appropriate template for a node type and version
func (m *Manager) GetTemplate(nodeType string, mongoVersion string) (*template.Template, error) {
	templateVersion := m.selectTemplateVersion(mongoVersion)
	templateName := fmt.Sprintf("%s-%s.conf.tmpl", nodeType, templateVersion)

	tmpl, ok := m.templates[templateName]
	if !ok {
		return nil, fmt.Errorf("template not found: %s", templateName)
	}

	return tmpl, nil
}

// selectTemplateVersion maps MongoDB version to template version
func (m *Manager) selectTemplateVersion(mongoVersion string) string {
	v, err := version.NewVersion(mongoVersion)
	if err != nil {
		// Default to latest if version parsing fails
		return "7.0"
	}

	// Define version constraints
	constraints := []struct {
		constraint string
		template   string
	}{
		{">= 7.0", "7.0"},
		{">= 5.0, < 7.0", "5.0"},
		{">= 4.2, < 5.0", "4.2"},
		{"< 4.2", "3.6"},
	}

	for _, c := range constraints {
		constraint, err := version.NewConstraint(c.constraint)
		if err != nil {
			continue
		}

		if constraint.Check(v) {
			return c.template
		}
	}

	// Default to oldest supported version
	return "3.6"
}

func (m *Manager) loadTemplates() error {
	dirs := []string{"mongod", "mongos", "config"}

	for _, dir := range dirs {
		entries, err := templates.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("failed to read template dir %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			path := fmt.Sprintf("%s/%s", dir, entry.Name())
			content, err := templates.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read template %s: %w", path, err)
			}

			tmpl, err := template.New(entry.Name()).Parse(string(content))
			if err != nil {
				return fmt.Errorf("failed to parse template %s: %w", path, err)
			}

			m.templates[entry.Name()] = tmpl
		}
	}

	return nil
}
```

### 2. Template Data Structures

```go
// pkg/template/types.go

package template

// MongodConfig represents all possible mongod configuration options
type MongodConfig struct {
	// Network
	Net NetConfig

	// Storage
	Storage StorageConfig

	// SystemLog
	SystemLog SystemLogConfig

	// ProcessManagement
	ProcessManagement ProcessManagementConfig

	// Replication (optional)
	Replication *ReplicationConfig

	// Sharding (optional)
	Sharding *ShardingConfig

	// Security (optional)
	Security *SecurityConfig

	// OperationProfiling (optional)
	OperationProfiling *OperationProfilingConfig

	// SetParameter (optional, version-specific)
	SetParameter map[string]interface{}
}

type NetConfig struct {
	Port            int
	BindIP          string
	MaxIncomingConnections int

	// TLS/SSL (version-dependent naming)
	TLS *TLSConfig
}

type TLSConfig struct {
	Mode              string
	CertificateKeyFile string
	CAFile            string
	AllowInvalidCertificates bool

	// Use "tls" or "ssl" based on version
	UseSSLNaming bool  // true for versions < 4.2
}

type StorageConfig struct {
	DBPath            string
	Journal           JournalConfig
	Engine            string  // "wiredTiger", "inMemory"
	DirectoryPerDB    bool

	// WiredTiger specific
	WiredTiger *WiredTigerConfig
}

type JournalConfig struct {
	Enabled bool
}

type WiredTigerConfig struct {
	EngineConfig          WiredTigerEngineConfig
	CollectionConfig      WiredTigerCollectionConfig
	IndexConfig           WiredTigerIndexConfig
}

type WiredTigerEngineConfig struct {
	CacheSizeGB float64
}

type WiredTigerCollectionConfig struct {
	BlockCompressor string  // "snappy", "zlib", "zstd", "none"
}

type WiredTigerIndexConfig struct {
	PrefixCompression bool
}

type SystemLogConfig struct {
	Destination string  // "file" or "syslog"
	Path        string
	LogAppend   bool
	TimeStampFormat string
}

type ProcessManagementConfig struct {
	Fork       bool
	PIDFilePath string
}

type ReplicationConfig struct {
	ReplSetName      string
	OplogSizeMB      int
	EnableMajorityReadConcern bool
}

type ShardingConfig struct {
	ClusterRole string  // "configsvr" or "shardsvr"
}

type SecurityConfig struct {
	Authorization    string  // "enabled" or "disabled"
	KeyFile          string
	ClusterAuthMode  string  // "keyFile", "x509"

	// LDAP (optional)
	LDAP *LDAPConfig
}

type LDAPConfig struct {
	Servers         string
	Bind            BindConfig
	UserToDNMapping string
}

type BindConfig struct {
	QueryUser     string
	QueryPassword string
}

type OperationProfilingConfig struct {
	Mode              string  // "off", "slowOp", "all"
	SlowOpThresholdMs int
}

// MongosConfig represents mongos (router) configuration
type MongosConfig struct {
	Net               NetConfig
	SystemLog         SystemLogConfig
	ProcessManagement ProcessManagementConfig
	Sharding          MongosShardingConfig
	Security          *SecurityConfig
}

type MongosShardingConfig struct {
	ConfigDB string  // Connection string to config servers
}

// ConfigServerConfig represents config server configuration
type ConfigServerConfig struct {
	// Config servers are mongod instances with clusterRole: configsvr
	MongodConfig
}
```

### 3. Template Files

#### mongod-7.0.conf.tmpl

```yaml
# MongoDB 7.0+ Configuration
# Generated by mup

net:
  port: {{ .Net.Port }}
  bindIp: {{ .Net.BindIP }}
{{- if .Net.MaxIncomingConnections }}
  maxIncomingConnections: {{ .Net.MaxIncomingConnections }}
{{- end }}
{{- if .Net.TLS }}
  tls:
    mode: {{ .Net.TLS.Mode }}
{{- if .Net.TLS.CertificateKeyFile }}
    certificateKeyFile: {{ .Net.TLS.CertificateKeyFile }}
{{- end }}
{{- if .Net.TLS.CAFile }}
    CAFile: {{ .Net.TLS.CAFile }}
{{- end }}
{{- if .Net.TLS.AllowInvalidCertificates }}
    allowInvalidCertificates: {{ .Net.TLS.AllowInvalidCertificates }}
{{- end }}
{{- end }}

storage:
  dbPath: {{ .Storage.DBPath }}
  journal:
    enabled: {{ .Storage.Journal.Enabled }}
{{- if .Storage.Engine }}
  engine: {{ .Storage.Engine }}
{{- end }}
{{- if .Storage.DirectoryPerDB }}
  directoryPerDB: {{ .Storage.DirectoryPerDB }}
{{- end }}
{{- if .Storage.WiredTiger }}
  wiredTiger:
    engineConfig:
      cacheSizeGB: {{ .Storage.WiredTiger.EngineConfig.CacheSizeGB }}
{{- if .Storage.WiredTiger.CollectionConfig.BlockCompressor }}
    collectionConfig:
      blockCompressor: {{ .Storage.WiredTiger.CollectionConfig.BlockCompressor }}
{{- end }}
{{- if .Storage.WiredTiger.IndexConfig.PrefixCompression }}
    indexConfig:
      prefixCompression: {{ .Storage.WiredTiger.IndexConfig.PrefixCompression }}
{{- end }}
{{- end }}

systemLog:
  destination: {{ .SystemLog.Destination }}
{{- if .SystemLog.Path }}
  path: {{ .SystemLog.Path }}
{{- end }}
  logAppend: {{ .SystemLog.LogAppend }}
{{- if .SystemLog.TimeStampFormat }}
  timeStampFormat: {{ .SystemLog.TimeStampFormat }}
{{- end }}

processManagement:
{{- if .ProcessManagement.Fork }}
  fork: {{ .ProcessManagement.Fork }}
{{- end }}
{{- if .ProcessManagement.PIDFilePath }}
  pidFilePath: {{ .ProcessManagement.PIDFilePath }}
{{- end }}

{{- if .Replication }}
replication:
  replSetName: {{ .Replication.ReplSetName }}
{{- if .Replication.OplogSizeMB }}
  oplogSizeMB: {{ .Replication.OplogSizeMB }}
{{- end }}
  enableMajorityReadConcern: {{ .Replication.EnableMajorityReadConcern }}
{{- end }}

{{- if .Sharding }}
sharding:
  clusterRole: {{ .Sharding.ClusterRole }}
{{- end }}

{{- if .Security }}
security:
  authorization: {{ .Security.Authorization }}
{{- if .Security.KeyFile }}
  keyFile: {{ .Security.KeyFile }}
{{- end }}
{{- if .Security.ClusterAuthMode }}
  clusterAuthMode: {{ .Security.ClusterAuthMode }}
{{- end }}
{{- if .Security.LDAP }}
  ldap:
    servers: {{ .Security.LDAP.Servers }}
    bind:
      queryUser: {{ .Security.LDAP.Bind.QueryUser }}
      queryPassword: {{ .Security.LDAP.Bind.QueryPassword }}
    userToDNMapping: {{ .Security.LDAP.UserToDNMapping }}
{{- end }}
{{- end }}

{{- if .OperationProfiling }}
operationProfiling:
  mode: {{ .OperationProfiling.Mode }}
{{- if .OperationProfiling.SlowOpThresholdMs }}
  slowOpThresholdMs: {{ .OperationProfiling.SlowOpThresholdMs }}
{{- end }}
{{- end }}

{{- if .SetParameter }}
setParameter:
{{- range $key, $value := .SetParameter }}
  {{ $key }}: {{ $value }}
{{- end }}
{{- end }}
```

#### mongod-4.2.conf.tmpl

```yaml
# MongoDB 4.2-4.4 Configuration
# Generated by mup

net:
  port: {{ .Net.Port }}
  bindIp: {{ .Net.BindIP }}
{{- if .Net.MaxIncomingConnections }}
  maxIncomingConnections: {{ .Net.MaxIncomingConnections }}
{{- end }}
{{- if .Net.TLS }}
  # MongoDB 4.2+ uses 'tls' naming (ssl still works but deprecated)
  tls:
    mode: {{ .Net.TLS.Mode }}
{{- if .Net.TLS.CertificateKeyFile }}
    certificateKeyFile: {{ .Net.TLS.CertificateKeyFile }}
{{- end }}
{{- if .Net.TLS.CAFile }}
    CAFile: {{ .Net.TLS.CAFile }}
{{- end }}
{{- end }}

storage:
  dbPath: {{ .Storage.DBPath }}
  journal:
    enabled: {{ .Storage.Journal.Enabled }}
{{- if .Storage.Engine }}
  engine: {{ .Storage.Engine }}
{{- end }}
{{- if .Storage.WiredTiger }}
  wiredTiger:
    engineConfig:
      cacheSizeGB: {{ .Storage.WiredTiger.EngineConfig.CacheSizeGB }}
{{- if .Storage.WiredTiger.CollectionConfig.BlockCompressor }}
    collectionConfig:
      blockCompressor: {{ .Storage.WiredTiger.CollectionConfig.BlockCompressor }}
{{- end }}
{{- end }}

systemLog:
  destination: {{ .SystemLog.Destination }}
{{- if .SystemLog.Path }}
  path: {{ .SystemLog.Path }}
{{- end }}
  logAppend: {{ .SystemLog.LogAppend }}

processManagement:
{{- if .ProcessManagement.Fork }}
  fork: {{ .ProcessManagement.Fork }}
{{- end }}
{{- if .ProcessManagement.PIDFilePath }}
  pidFilePath: {{ .ProcessManagement.PIDFilePath }}
{{- end }}

{{- if .Replication }}
replication:
  replSetName: {{ .Replication.ReplSetName }}
{{- if .Replication.OplogSizeMB }}
  oplogSizeMB: {{ .Replication.OplogSizeMB }}
{{- end }}
{{- end }}

{{- if .Sharding }}
sharding:
  clusterRole: {{ .Sharding.ClusterRole }}
{{- end }}

{{- if .Security }}
security:
  authorization: {{ .Security.Authorization }}
{{- if .Security.KeyFile }}
  keyFile: {{ .Security.KeyFile }}
{{- end }}
{{- end }}

{{- if .SetParameter }}
setParameter:
{{- range $key, $value := .SetParameter }}
  {{ $key }}: {{ $value }}
{{- end }}
{{- end }}
```

#### mongod-3.6.conf.tmpl

```yaml
# MongoDB 3.6-4.0 Configuration
# Generated by mup

net:
  port: {{ .Net.Port }}
  bindIp: {{ .Net.BindIP }}
{{- if .Net.TLS }}
  # MongoDB 3.6-4.0 uses 'ssl' naming
  ssl:
    mode: {{ .Net.TLS.Mode }}
{{- if .Net.TLS.CertificateKeyFile }}
    PEMKeyFile: {{ .Net.TLS.CertificateKeyFile }}
{{- end }}
{{- if .Net.TLS.CAFile }}
    CAFile: {{ .Net.TLS.CAFile }}
{{- end }}
{{- end }}

storage:
  dbPath: {{ .Storage.DBPath }}
  journal:
    enabled: {{ .Storage.Journal.Enabled }}
{{- if .Storage.Engine }}
  engine: {{ .Storage.Engine }}
{{- end }}
{{- if .Storage.WiredTiger }}
  wiredTiger:
    engineConfig:
      cacheSizeGB: {{ .Storage.WiredTiger.EngineConfig.CacheSizeGB }}
{{- end }}

systemLog:
  destination: {{ .SystemLog.Destination }}
{{- if .SystemLog.Path }}
  path: {{ .SystemLog.Path }}
{{- end }}
  logAppend: {{ .SystemLog.LogAppend }}

processManagement:
{{- if .ProcessManagement.Fork }}
  fork: {{ .ProcessManagement.Fork }}
{{- end }}
{{- if .ProcessManagement.PIDFilePath }}
  pidFilePath: {{ .ProcessManagement.PIDFilePath }}
{{- end }}

{{- if .Replication }}
replication:
  replSetName: {{ .Replication.ReplSetName }}
{{- if .Replication.OplogSizeMB }}
  oplogSizeMB: {{ .Replication.OplogSizeMB }}
{{- end }}
{{- end }}

{{- if .Sharding }}
sharding:
  clusterRole: {{ .Sharding.ClusterRole }}
{{- end }}

{{- if .Security }}
security:
  authorization: {{ .Security.Authorization }}
{{- if .Security.KeyFile }}
  keyFile: {{ .Security.KeyFile }}
{{- end }}
{{- end }}
```

#### mongos-7.0.conf.tmpl

```yaml
# MongoDB 7.0+ Mongos Configuration
# Generated by mup

net:
  port: {{ .Net.Port }}
  bindIp: {{ .Net.BindIP }}
{{- if .Net.TLS }}
  tls:
    mode: {{ .Net.TLS.Mode }}
{{- if .Net.TLS.CertificateKeyFile }}
    certificateKeyFile: {{ .Net.TLS.CertificateKeyFile }}
{{- end }}
{{- if .Net.TLS.CAFile }}
    CAFile: {{ .Net.TLS.CAFile }}
{{- end }}
{{- end }}

systemLog:
  destination: {{ .SystemLog.Destination }}
{{- if .SystemLog.Path }}
  path: {{ .SystemLog.Path }}
{{- end }}
  logAppend: {{ .SystemLog.LogAppend }}

processManagement:
{{- if .ProcessManagement.Fork }}
  fork: {{ .ProcessManagement.Fork }}
{{- end }}
{{- if .ProcessManagement.PIDFilePath }}
  pidFilePath: {{ .ProcessManagement.PIDFilePath }}
{{- end }}

sharding:
  configDB: {{ .Sharding.ConfigDB }}

{{- if .Security }}
security:
  authorization: {{ .Security.Authorization }}
{{- if .Security.KeyFile }}
  keyFile: {{ .Security.KeyFile }}
{{- end }}
{{- if .Security.ClusterAuthMode }}
  clusterAuthMode: {{ .Security.ClusterAuthMode }}
{{- end }}
{{- end }}
```

### 4. Usage in Deployer

```go
// pkg/deploy/deploy.go

import (
	"bytes"
	"fmt"
	"path/filepath"

	"github.com/zph/mup/pkg/template"
	"github.com/zph/mup/pkg/topology"
)

type Deployer struct {
	// ... existing fields ...
	templateMgr *template.Manager
}

func NewDeployer(cfg DeployConfig) (*Deployer, error) {
	// ... existing code ...

	// Initialize template manager
	templateMgr, err := template.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize template manager: %w", err)
	}

	return &Deployer{
		// ... existing fields ...
		templateMgr: templateMgr,
	}, nil
}

// generateMongodConfig generates configuration using templates
func (d *Deployer) generateMongodConfig(node topology.MongodNode) error {
	exec := d.executors[node.Host]

	configPath := filepath.Join(
		d.getNodeConfigDir(node.Host, node.Port, node.ConfigDir),
		"mongod.conf",
	)

	// Build template data
	data := template.MongodConfig{
		Net: template.NetConfig{
			Port:   node.Port,
			BindIP: "127.0.0.1",
		},
		Storage: template.StorageConfig{
			DBPath: d.getNodeDataDir(node.Host, node.Port, node.DataDir),
			Journal: template.JournalConfig{
				Enabled: true,
			},
			Engine: "wiredTiger",
			WiredTiger: &template.WiredTigerConfig{
				EngineConfig: template.WiredTigerEngineConfig{
					CacheSizeGB: 1.0,
				},
			},
		},
		SystemLog: template.SystemLogConfig{
			Destination: "file",
			Path:        filepath.Join(d.getNodeLogDir(node.Host, node.Port, node.LogDir), "mongod.log"),
			LogAppend:   true,
		},
		ProcessManagement: template.ProcessManagementConfig{
			Fork:        false,
			PIDFilePath: filepath.Join(d.getNodeLogDir(node.Host, node.Port, node.LogDir), "mongod.pid"),
		},
	}

	// Add replication config if this is part of a replica set
	if node.ReplicaSet != "" {
		data.Replication = &template.ReplicationConfig{
			ReplSetName:               node.ReplicaSet,
			EnableMajorityReadConcern: true,
		}
	}

	// Get appropriate template for MongoDB version
	tmpl, err := d.templateMgr.GetTemplate("mongod", d.version)
	if err != nil {
		return fmt.Errorf("failed to get template: %w", err)
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Upload config file
	if err := exec.UploadContent(buf.Bytes(), configPath); err != nil {
		return fmt.Errorf("failed to upload config: %w", err)
	}

	fmt.Printf("  Generated config: %s\n", configPath)
	return nil
}

// Similar for generateMongosConfig and generateConfigServerConfig...
```

## Testing Strategy

### Unit Tests

```go
// pkg/template/manager_test.go

func TestVersionSelection(t *testing.T) {
	tests := []struct {
		version  string
		expected string
	}{
		{"7.0.0", "7.0"},
		{"7.0.5", "7.0"},
		{"6.0.12", "5.0"},
		{"5.0.0", "5.0"},
		{"4.4.28", "4.2"},
		{"4.2.0", "4.2"},
		{"4.0.28", "3.6"},
		{"3.6.23", "3.6"},
	}

	mgr, err := NewManager()
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			result := mgr.selectTemplateVersion(tt.version)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTemplateExecution(t *testing.T) {
	mgr, err := NewManager()
	require.NoError(t, err)

	data := MongodConfig{
		Net: NetConfig{
			Port:   27017,
			BindIP: "127.0.0.1",
		},
		Storage: StorageConfig{
			DBPath: "/data/db",
			Journal: JournalConfig{
				Enabled: true,
			},
		},
		// ... more fields ...
	}

	tmpl, err := mgr.GetTemplate("mongod", "7.0")
	require.NoError(t, err)

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	require.NoError(t, err)

	config := buf.String()
	assert.Contains(t, config, "port: 27017")
	assert.Contains(t, config, "bindIp: 127.0.0.1")
	assert.Contains(t, config, "dbPath: /data/db")
}
```

## Migration Path

1. **Create template package** with all templates
2. **Test template generation** independently
3. **Update deployer** to use templates instead of string interpolation
4. **Remove old string interpolation code**
5. **Update tests** to verify template output
6. **Document** template customization for advanced users

## Benefits

1. **Maintainability**: Templates are easier to read and modify
2. **Version Support**: Easy to add new MongoDB versions
3. **Type Safety**: Compile-time checking of template data
4. **Testability**: Templates can be tested in isolation
5. **Flexibility**: Users can customize templates if needed
6. **Professional**: Industry-standard approach to config generation

## Future Enhancements

1. **Template Overrides**: Allow users to provide custom templates
2. **Config Validation**: Parse generated config to validate YAML syntax
3. **Template Includes**: Share common sections between templates
4. **Hot Reload**: Reload templates without restart (dev mode)
5. **Config Diff**: Show what changes between versions

## Dependencies

Add to `go.mod`:
```
github.com/hashicorp/go-version v1.6.0
```

This provides robust version parsing and constraint checking.

## MongoDB 5.0 Template

For completeness, here's the MongoDB 5.0 template which includes time series and versioned API support:

#### mongod-5.0.conf.tmpl

```yaml
# MongoDB 5.0-6.0 Configuration
# Generated by mup

net:
  port: {{ .Net.Port }}
  bindIp: {{ .Net.BindIP }}
{{- if .Net.MaxIncomingConnections }}
  maxIncomingConnections: {{ .Net.MaxIncomingConnections }}
{{- end }}
{{- if .Net.TLS }}
  tls:
    mode: {{ .Net.TLS.Mode }}
{{- if .Net.TLS.CertificateKeyFile }}
    certificateKeyFile: {{ .Net.TLS.CertificateKeyFile }}
{{- end }}
{{- if .Net.TLS.CAFile }}
    CAFile: {{ .Net.TLS.CAFile }}
{{- end }}
{{- end }}

storage:
  dbPath: {{ .Storage.DBPath }}
  journal:
    enabled: {{ .Storage.Journal.Enabled }}
{{- if .Storage.Engine }}
  engine: {{ .Storage.Engine }}
{{- end }}
{{- if .Storage.DirectoryPerDB }}
  directoryPerDB: {{ .Storage.DirectoryPerDB }}
{{- end }}
{{- if .Storage.WiredTiger }}
  wiredTiger:
    engineConfig:
      cacheSizeGB: {{ .Storage.WiredTiger.EngineConfig.CacheSizeGB }}
{{- if .Storage.WiredTiger.CollectionConfig.BlockCompressor }}
    collectionConfig:
      blockCompressor: {{ .Storage.WiredTiger.CollectionConfig.BlockCompressor }}
{{- end }}
{{- if .Storage.WiredTiger.IndexConfig.PrefixCompression }}
    indexConfig:
      prefixCompression: {{ .Storage.WiredTiger.IndexConfig.PrefixCompression }}
{{- end }}
{{- end }}

systemLog:
  destination: {{ .SystemLog.Destination }}
{{- if .SystemLog.Path }}
  path: {{ .SystemLog.Path }}
{{- end }}
  logAppend: {{ .SystemLog.LogAppend }}
{{- if .SystemLog.TimeStampFormat }}
  timeStampFormat: {{ .SystemLog.TimeStampFormat }}
{{- end }}

processManagement:
{{- if .ProcessManagement.Fork }}
  fork: {{ .ProcessManagement.Fork }}
{{- end }}
{{- if .ProcessManagement.PIDFilePath }}
  pidFilePath: {{ .ProcessManagement.PIDFilePath }}
{{- end }}

{{- if .Replication }}
replication:
  replSetName: {{ .Replication.ReplSetName }}
{{- if .Replication.OplogSizeMB }}
  oplogSizeMB: {{ .Replication.OplogSizeMB }}
{{- end }}
  enableMajorityReadConcern: {{ .Replication.EnableMajorityReadConcern }}
{{- end }}

{{- if .Sharding }}
sharding:
  clusterRole: {{ .Sharding.ClusterRole }}
{{- end }}

{{- if .Security }}
security:
  authorization: {{ .Security.Authorization }}
{{- if .Security.KeyFile }}
  keyFile: {{ .Security.KeyFile }}
{{- end }}
{{- if .Security.ClusterAuthMode }}
  clusterAuthMode: {{ .Security.ClusterAuthMode }}
{{- end }}
{{- end }}

{{- if .OperationProfiling }}
operationProfiling:
  mode: {{ .OperationProfiling.Mode }}
{{- if .OperationProfiling.SlowOpThresholdMs }}
  slowOpThresholdMs: {{ .OperationProfiling.SlowOpThresholdMs }}
{{- end }}
{{- end }}

{{- if .SetParameter }}
setParameter:
{{- range $key, $value := .SetParameter }}
  {{ $key }}: {{ $value }}
{{- end }}
{{- end }}
```

#### mongos-4.2.conf.tmpl

```yaml
# MongoDB 4.2-4.4 Mongos Configuration
# Generated by mup

net:
  port: {{ .Net.Port }}
  bindIp: {{ .Net.BindIP }}
{{- if .Net.TLS }}
  tls:
    mode: {{ .Net.TLS.Mode }}
{{- if .Net.TLS.CertificateKeyFile }}
    certificateKeyFile: {{ .Net.TLS.CertificateKeyFile }}
{{- end }}
{{- if .Net.TLS.CAFile }}
    CAFile: {{ .Net.TLS.CAFile }}
{{- end }}
{{- end }}

systemLog:
  destination: {{ .SystemLog.Destination }}
{{- if .SystemLog.Path }}
  path: {{ .SystemLog.Path }}
{{- end }}
  logAppend: {{ .SystemLog.LogAppend }}

processManagement:
{{- if .ProcessManagement.Fork }}
  fork: {{ .ProcessManagement.Fork }}
{{- end }}
{{- if .ProcessManagement.PIDFilePath }}
  pidFilePath: {{ .ProcessManagement.PIDFilePath }}
{{- end }}

sharding:
  configDB: {{ .Sharding.ConfigDB }}

{{- if .Security }}
security:
  authorization: {{ .Security.Authorization }}
{{- if .Security.KeyFile }}
  keyFile: {{ .Security.KeyFile }}
{{- end }}
{{- end }}
```

#### mongos-5.0.conf.tmpl

```yaml
# MongoDB 5.0-6.0 Mongos Configuration
# Generated by mup

net:
  port: {{ .Net.Port }}
  bindIp: {{ .Net.BindIP }}
{{- if .Net.TLS }}
  tls:
    mode: {{ .Net.TLS.Mode }}
{{- if .Net.TLS.CertificateKeyFile }}
    certificateKeyFile: {{ .Net.TLS.CertificateKeyFile }}
{{- end }}
{{- if .Net.TLS.CAFile }}
    CAFile: {{ .Net.TLS.CAFile }}
{{- end }}
{{- end }}

systemLog:
  destination: {{ .SystemLog.Destination }}
{{- if .SystemLog.Path }}
  path: {{ .SystemLog.Path }}
{{- end }}
  logAppend: {{ .SystemLog.LogAppend }}
{{- if .SystemLog.TimeStampFormat }}
  timeStampFormat: {{ .SystemLog.TimeStampFormat }}
{{- end }}

processManagement:
{{- if .ProcessManagement.Fork }}
  fork: {{ .ProcessManagement.Fork }}
{{- end }}
{{- if .ProcessManagement.PIDFilePath }}
  pidFilePath: {{ .ProcessManagement.PIDFilePath }}
{{- end }}

sharding:
  configDB: {{ .Sharding.ConfigDB }}

{{- if .Security }}
security:
  authorization: {{ .Security.Authorization }}
{{- if .Security.KeyFile }}
  keyFile: {{ .Security.KeyFile }}
{{- end }}
{{- if .Security.ClusterAuthMode }}
  clusterAuthMode: {{ .Security.ClusterAuthMode }}
{{- end }}
{{- end }}
```

#### mongos-3.6.conf.tmpl

```yaml
# MongoDB 3.6-4.0 Mongos Configuration
# Generated by mup

net:
  port: {{ .Net.Port }}
  bindIp: {{ .Net.BindIP }}
{{- if .Net.TLS }}
  # MongoDB 3.6-4.0 uses 'ssl' naming
  ssl:
    mode: {{ .Net.TLS.Mode }}
{{- if .Net.TLS.CertificateKeyFile }}
    PEMKeyFile: {{ .Net.TLS.CertificateKeyFile }}
{{- end }}
{{- if .Net.TLS.CAFile }}
    CAFile: {{ .Net.TLS.CAFile }}
{{- end }}
{{- end }}

systemLog:
  destination: {{ .SystemLog.Destination }}
{{- if .SystemLog.Path }}
  path: {{ .SystemLog.Path }}
{{- end }}
  logAppend: {{ .SystemLog.LogAppend }}

processManagement:
{{- if .ProcessManagement.Fork }}
  fork: {{ .ProcessManagement.Fork }}
{{- end }}
{{- if .ProcessManagement.PIDFilePath }}
  pidFilePath: {{ .ProcessManagement.PIDFilePath }}
{{- end }}

sharding:
  configDB: {{ .Sharding.ConfigDB }}

{{- if .Security }}
security:
  authorization: {{ .Security.Authorization }}
{{- if .Security.KeyFile }}
  keyFile: {{ .Security.KeyFile }}
{{- end }}
{{- end }}
```

## Implementation Checklist

### Phase 1: Template Package Structure
- [ ] Create `pkg/template/` directory
- [ ] Create `pkg/template/mongod/` directory
- [ ] Create `pkg/template/mongos/` directory
- [ ] Create `pkg/template/config/` directory
- [ ] Implement `manager.go` with version selection
- [ ] Implement `types.go` with all config structures

### Phase 2: Template Files (All Versions)
**Mongod Templates:**
- [ ] Create `mongod-3.6.conf.tmpl` (complete in plan)
- [ ] Create `mongod-4.2.conf.tmpl` (complete in plan)
- [ ] Create `mongod-5.0.conf.tmpl` (complete above)
- [ ] Create `mongod-7.0.conf.tmpl` (complete in plan)

**Mongos Templates:**
- [ ] Create `mongos-3.6.conf.tmpl` (complete above)
- [ ] Create `mongos-4.2.conf.tmpl` (complete above)
- [ ] Create `mongos-5.0.conf.tmpl` (complete above)
- [ ] Create `mongos-7.0.conf.tmpl` (complete in plan)

**Config Server Templates:**
- [ ] Create `config-3.6.conf.tmpl` (same as mongod-3.6 with clusterRole: configsvr)
- [ ] Create `config-4.2.conf.tmpl` (same as mongod-4.2 with clusterRole: configsvr)
- [ ] Create `config-5.0.conf.tmpl` (same as mongod-5.0 with clusterRole: configsvr)
- [ ] Create `config-7.0.conf.tmpl` (same as mongod-7.0 with clusterRole: configsvr)

### Phase 3: Integration
- [ ] Add `github.com/hashicorp/go-version` dependency to `go.mod`
- [ ] Update `pkg/deploy/deployer.go` to initialize template manager
- [ ] Update `generateMongodConfig()` to use templates
- [ ] Update `generateMongosConfig()` to use templates
- [ ] Update `generateConfigServerConfig()` to use templates
- [ ] Remove old string interpolation code from `deploy.go`

### Phase 4: Testing
- [ ] Test version selection for all ranges (3.6, 4.2, 5.0, 7.0)
- [ ] Test template execution for mongod (all versions)
- [ ] Test template execution for mongos (all versions)
- [ ] Test template execution for config servers (all versions)
- [ ] Test SSL vs TLS naming for old vs new versions
- [ ] Integration test: deploy with MongoDB 3.6
- [ ] Integration test: deploy with MongoDB 4.2
- [ ] Integration test: deploy with MongoDB 5.0
- [ ] Integration test: deploy with MongoDB 7.0

### Phase 5: Validation
- [ ] Parse generated configs with YAML parser to validate syntax
- [ ] Test with actual MongoDB binaries (if available)
- [ ] Verify config compatibility with each version
- [ ] Add config validation to CI/CD

### Phase 6: Documentation
- [ ] Update CLAUDE.md with template system details
- [ ] Update README with supported versions
- [ ] Add examples of custom templates
- [ ] Document version-specific differences
- [ ] Create template customization guide

## Key Version Differences Summary

| Feature | 3.6-4.0 | 4.2-4.4 | 5.0-6.0 | 7.0+ |
|---------|---------|---------|---------|------|
| TLS naming | `ssl.*` | `tls.*` preferred | `tls.*` | `tls.*` |
| SSL key field | `PEMKeyFile` | `certificateKeyFile` | `certificateKeyFile` | `certificateKeyFile` |
| Time series | ❌ | ❌ | ✅ | ✅ |
| Versioned API | ❌ | ❌ | ✅ | ✅ |
| Enhanced security | Basic | Enhanced | Enhanced | Strict |
| Read concern | Basic | Enhanced | Enhanced | Enhanced |

**Note:** Config servers are essentially mongod instances with `clusterRole: configsvr`, so they can reuse mongod templates with that specific role set.
