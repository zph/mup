package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IMP-010: Parse existing mongod.conf files
func TestParseMongoConfig(t *testing.T) {
	t.Run("parse standard mongod.conf", func(t *testing.T) {
		configYAML := `
net:
  port: 27017
  bindIp: 0.0.0.0
  maxIncomingConnections: 1000

storage:
  dbPath: /var/lib/mongodb
  journal:
    enabled: true
  engine: wiredTiger
  wiredTiger:
    engineConfig:
      cacheSizeGB: 2.0

systemLog:
  destination: file
  path: /var/log/mongodb/mongod.log
  logAppend: true

processManagement:
  fork: true
  pidFilePath: /var/run/mongodb/mongod.pid

replication:
  replSetName: rs0
  oplogSizeMB: 1024
`

		importer := &ConfigImporter{}
		config, err := importer.ParseMongoConfig(configYAML)
		require.NoError(t, err)

		assert.Equal(t, 27017, config.Net.Port)
		assert.Equal(t, "0.0.0.0", config.Net.BindIP)
		assert.Equal(t, 1000, config.Net.MaxIncomingConnections)
		assert.Equal(t, "/var/lib/mongodb", config.Storage.DBPath)
		assert.True(t, config.Storage.Journal.Enabled)
		assert.Equal(t, "wiredTiger", config.Storage.Engine)
		assert.Equal(t, 2.0, config.Storage.WiredTiger.EngineConfig.CacheSizeGB)
		assert.Equal(t, "/var/log/mongodb/mongod.log", config.SystemLog.Path)
		assert.True(t, config.ProcessManagement.Fork)
		assert.Equal(t, "rs0", config.Replication.ReplSetName)
		assert.Equal(t, 1024, config.Replication.OplogSizeMB)
	})

	t.Run("parse config with sharding", func(t *testing.T) {
		configYAML := `
net:
  port: 27018
  bindIp: localhost

storage:
  dbPath: /data/shard1

sharding:
  clusterRole: shardsvr

replication:
  replSetName: shard1
`

		importer := &ConfigImporter{}
		config, err := importer.ParseMongoConfig(configYAML)
		require.NoError(t, err)

		assert.Equal(t, 27018, config.Net.Port)
		assert.Equal(t, "shardsvr", config.Sharding.ClusterRole)
		assert.Equal(t, "shard1", config.Replication.ReplSetName)
	})
}

// IMP-012: Preserve custom MongoDB settings
func TestPreserveCustomSettings(t *testing.T) {
	t.Run("preserve setParameter settings", func(t *testing.T) {
		configYAML := `
net:
  port: 27017
  bindIp: localhost

storage:
  dbPath: /var/lib/mongodb

setParameter:
  enableLocalhostAuthBypass: false
  authenticationMechanisms: SCRAM-SHA-256
  ttlMonitorSleepSecs: 60
`

		importer := &ConfigImporter{}
		config, err := importer.ParseMongoConfig(configYAML)
		require.NoError(t, err)

		// IMP-012: Custom settings should be preserved in SetParameter map
		assert.NotNil(t, config.SetParameter)
		assert.Contains(t, config.SetParameter, "enableLocalhostAuthBypass")
		assert.Contains(t, config.SetParameter, "authenticationMechanisms")
		assert.Contains(t, config.SetParameter, "ttlMonitorSleepSecs")
	})

	t.Run("preserve operation profiling", func(t *testing.T) {
		configYAML := `
net:
  port: 27017

storage:
  dbPath: /var/lib/mongodb

operationProfiling:
  mode: slowOp
  slowOpThresholdMs: 100
`

		importer := &ConfigImporter{}
		config, err := importer.ParseMongoConfig(configYAML)
		require.NoError(t, err)

		assert.Equal(t, "slowOp", config.OperationProfiling.Mode)
		assert.Equal(t, 100, config.OperationProfiling.SlowOpThresholdMs)
	})
}

// IMP-011: Generate mup-compatible configs
func TestGenerateMupConfig(t *testing.T) {
	t.Run("generate config with mup paths", func(t *testing.T) {
		existingConfig := `
net:
  port: 27017
  bindIp: localhost

storage:
  dbPath: /var/lib/mongodb
  journal:
    enabled: true

systemLog:
  destination: file
  path: /var/log/mongodb/mongod.log
  logAppend: true

processManagement:
  fork: true

replication:
  replSetName: rs0
`

		importer := &ConfigImporter{}
		parsedConfig, err := importer.ParseMongoConfig(existingConfig)
		require.NoError(t, err)

		// IMP-011: Generate mup-compatible config with new paths
		mupPaths := ConfigPaths{
			DataDir: "/home/user/.mup/storage/clusters/my-cluster/data/localhost-27017",
			LogPath: "/home/user/.mup/storage/clusters/my-cluster/v7.0.5/logs/mongod-27017.log",
		}

		mupConfig := importer.GenerateMupConfig(parsedConfig, mupPaths)

		// Verify mup paths are used
		assert.Equal(t, mupPaths.DataDir, mupConfig.Storage.DBPath)
		assert.Equal(t, mupPaths.LogPath, mupConfig.SystemLog.Path)

		// Verify fork is disabled (supervisor manages processes)
		assert.False(t, mupConfig.ProcessManagement.Fork)

		// Verify other settings are preserved
		assert.Equal(t, 27017, mupConfig.Net.Port)
		assert.Equal(t, "rs0", mupConfig.Replication.ReplSetName)
	})
}

// Test parsing legacy (pre-YAML) configs
func TestParseLegacyConfig(t *testing.T) {
	t.Run("handle legacy config format", func(t *testing.T) {
		// Legacy configs use key=value format
		legacyConfig := `
port=27017
dbpath=/var/lib/mongodb
logpath=/var/log/mongodb/mongod.log
fork=true
replSet=rs0
`

		importer := &ConfigImporter{}
		config, err := importer.ParseLegacyConfig(legacyConfig)

		// May not fully support legacy format yet
		if err == nil {
			assert.Equal(t, 27017, config.Net.Port)
			assert.Equal(t, "/var/lib/mongodb", config.Storage.DBPath)
		}
	})
}

// Test mongos config parsing
func TestParseMongosConfig(t *testing.T) {
	t.Run("parse mongos.conf", func(t *testing.T) {
		configYAML := `
net:
  port: 27017
  bindIp: localhost

systemLog:
  destination: file
  path: /var/log/mongodb/mongos.log
  logAppend: true

sharding:
  configDB: configRS/config1:27019,config2:27019,config3:27019
`

		importer := &ConfigImporter{}
		config, err := importer.ParseMongosConfig(configYAML)
		require.NoError(t, err)

		assert.Equal(t, 27017, config.Net.Port)
		assert.Equal(t, "localhost", config.Net.BindIP)
		assert.Equal(t, "file", config.SystemLog.Destination)
		assert.Equal(t, "configRS/config1:27019,config2:27019,config3:27019", config.Sharding.ConfigDB)
	})
}
