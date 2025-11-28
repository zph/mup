package template

// MongodConfig represents all possible mongod configuration options
type MongodConfig struct {
	// Network
	Net NetConfig `yaml:"net"`

	// Storage
	Storage StorageConfig `yaml:"storage"`

	// SystemLog
	SystemLog SystemLogConfig `yaml:"systemLog"`

	// ProcessManagement
	ProcessManagement ProcessManagementConfig `yaml:"processManagement"`

	// Replication (optional)
	Replication *ReplicationConfig `yaml:"replication,omitempty"`

	// Sharding (optional)
	Sharding *ShardingConfig `yaml:"sharding,omitempty"`

	// Security (optional)
	Security *SecurityConfig `yaml:"security,omitempty"`

	// OperationProfiling (optional)
	OperationProfiling *OperationProfilingConfig `yaml:"operationProfiling,omitempty"`

	// SetParameter (optional, version-specific)
	SetParameter map[string]interface{} `yaml:"setParameter,omitempty"`
}

type NetConfig struct {
	Port                   int    `yaml:"port,omitempty"`
	BindIP                 string `yaml:"bindIp,omitempty"`
	MaxIncomingConnections int    `yaml:"maxIncomingConnections,omitempty"`

	// TLS/SSL (version-dependent naming)
	TLS *TLSConfig `yaml:"tls,omitempty"`
}

type TLSConfig struct {
	Mode                     string `yaml:"mode,omitempty"`
	CertificateKeyFile       string `yaml:"certificateKeyFile,omitempty"`
	CAFile                   string `yaml:"CAFile,omitempty"`
	AllowInvalidCertificates bool   `yaml:"allowInvalidCertificates,omitempty"`

	// Use "tls" or "ssl" based on version
	UseSSLNaming bool // true for versions < 4.2
}

type StorageConfig struct {
	DBPath         string `yaml:"dbPath,omitempty"`
	Journal        JournalConfig `yaml:"journal,omitempty"`
	Engine         string `yaml:"engine,omitempty"` // "wiredTiger", "inMemory"
	DirectoryPerDB bool   `yaml:"directoryPerDB,omitempty"`

	// WiredTiger specific
	WiredTiger *WiredTigerConfig `yaml:"wiredTiger,omitempty"`
}

type JournalConfig struct {
	Enabled bool `yaml:"enabled,omitempty"`
}

type WiredTigerConfig struct {
	EngineConfig     WiredTigerEngineConfig     `yaml:"engineConfig,omitempty"`
	CollectionConfig WiredTigerCollectionConfig `yaml:"collectionConfig,omitempty"`
	IndexConfig      WiredTigerIndexConfig      `yaml:"indexConfig,omitempty"`
}

type WiredTigerEngineConfig struct {
	CacheSizeGB float64 `yaml:"cacheSizeGB,omitempty"`
}

type WiredTigerCollectionConfig struct {
	BlockCompressor string `yaml:"blockCompressor,omitempty"` // "snappy", "zlib", "zstd", "none"
}

type WiredTigerIndexConfig struct {
	PrefixCompression bool `yaml:"prefixCompression,omitempty"`
}

type SystemLogConfig struct {
	Destination     string `yaml:"destination,omitempty"` // "file" or "syslog"
	Path            string `yaml:"path,omitempty"`
	LogAppend       bool   `yaml:"logAppend,omitempty"`
	TimeStampFormat string `yaml:"timeStampFormat,omitempty"`
}

type ProcessManagementConfig struct {
	Fork        bool   `yaml:"fork,omitempty"`
	PIDFilePath string `yaml:"pidFilePath,omitempty"`
}

type ReplicationConfig struct {
	ReplSetName               string `yaml:"replSetName,omitempty"`
	OplogSizeMB               int    `yaml:"oplogSizeMB,omitempty"`
	EnableMajorityReadConcern bool   `yaml:"enableMajorityReadConcern,omitempty"`
}

type ShardingConfig struct {
	ClusterRole string `yaml:"clusterRole,omitempty"` // "configsvr" or "shardsvr"
}

type SecurityConfig struct {
	Authorization   string `yaml:"authorization,omitempty"` // "enabled" or "disabled"
	KeyFile         string `yaml:"keyFile,omitempty"`
	ClusterAuthMode string `yaml:"clusterAuthMode,omitempty"` // "keyFile", "x509"

	// LDAP (optional)
	LDAP *LDAPConfig `yaml:"ldap,omitempty"`
}

type LDAPConfig struct {
	Servers         string     `yaml:"servers,omitempty"`
	Bind            BindConfig `yaml:"bind,omitempty"`
	UserToDNMapping string     `yaml:"userToDNMapping,omitempty"`
}

type BindConfig struct {
	QueryUser     string `yaml:"queryUser,omitempty"`
	QueryPassword string `yaml:"queryPassword,omitempty"`
}

type OperationProfilingConfig struct {
	Mode              string `yaml:"mode,omitempty"` // "off", "slowOp", "all"
	SlowOpThresholdMs int    `yaml:"slowOpThresholdMs,omitempty"`
}

// MongosConfig represents mongos (router) configuration
type MongosConfig struct {
	Net               NetConfig                `yaml:"net"`
	SystemLog         SystemLogConfig          `yaml:"systemLog"`
	ProcessManagement ProcessManagementConfig  `yaml:"processManagement"`
	Sharding          MongosShardingConfig     `yaml:"sharding"`
	Security          *SecurityConfig          `yaml:"security,omitempty"`
}

type MongosShardingConfig struct {
	ConfigDB string `yaml:"configDB,omitempty"` // Connection string to config servers
}

// ConfigServerConfig represents config server configuration
// Config servers are mongod instances with clusterRole: configsvr
type ConfigServerConfig = MongodConfig
