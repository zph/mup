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
	Port                   int
	BindIP                 string
	MaxIncomingConnections int

	// TLS/SSL (version-dependent naming)
	TLS *TLSConfig
}

type TLSConfig struct {
	Mode                     string
	CertificateKeyFile       string
	CAFile                   string
	AllowInvalidCertificates bool

	// Use "tls" or "ssl" based on version
	UseSSLNaming bool // true for versions < 4.2
}

type StorageConfig struct {
	DBPath         string
	Journal        JournalConfig
	Engine         string // "wiredTiger", "inMemory"
	DirectoryPerDB bool

	// WiredTiger specific
	WiredTiger *WiredTigerConfig
}

type JournalConfig struct {
	Enabled bool
}

type WiredTigerConfig struct {
	EngineConfig     WiredTigerEngineConfig
	CollectionConfig WiredTigerCollectionConfig
	IndexConfig      WiredTigerIndexConfig
}

type WiredTigerEngineConfig struct {
	CacheSizeGB float64
}

type WiredTigerCollectionConfig struct {
	BlockCompressor string // "snappy", "zlib", "zstd", "none"
}

type WiredTigerIndexConfig struct {
	PrefixCompression bool
}

type SystemLogConfig struct {
	Destination     string // "file" or "syslog"
	Path            string
	LogAppend       bool
	TimeStampFormat string
}

type ProcessManagementConfig struct {
	Fork        bool
	PIDFilePath string
}

type ReplicationConfig struct {
	ReplSetName               string
	OplogSizeMB               int
	EnableMajorityReadConcern bool
}

type ShardingConfig struct {
	ClusterRole string // "configsvr" or "shardsvr"
}

type SecurityConfig struct {
	Authorization   string // "enabled" or "disabled"
	KeyFile         string
	ClusterAuthMode string // "keyFile", "x509"

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
	Mode              string // "off", "slowOp", "all"
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
	ConfigDB string // Connection string to config servers
}

// ConfigServerConfig represents config server configuration
// Config servers are mongod instances with clusterRole: configsvr
type ConfigServerConfig = MongodConfig
