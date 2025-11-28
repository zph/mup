package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IMP-006: Parse systemd unit file
func TestParseSystemdUnit(t *testing.T) {
	t.Run("parse mongod service unit", func(t *testing.T) {
		unitContent := `[Unit]
Description=MongoDB Database Server
Documentation=https://docs.mongodb.org/manual
After=network-online.target
Wants=network-online.target

[Service]
User=mongodb
Group=mongodb
Environment="OPTIONS=-f /etc/mongod.conf"
EnvironmentFile=-/etc/default/mongod
ExecStart=/usr/bin/mongod --config /etc/mongod.conf
ExecStartPre=/usr/bin/mkdir -p /var/run/mongodb
ExecStartPre=/usr/bin/chown mongodb:mongodb /var/run/mongodb
ExecStartPre=/usr/bin/chmod 0755 /var/run/mongodb
PermissionsStartOnly=true
PIDFile=/var/run/mongodb/mongod.pid
Type=forking
# file size
LimitFSIZE=infinity
# cpu time
LimitCPU=infinity
# virtual memory size
LimitAS=infinity
# open files
LimitNOFILE=64000
# processes/threads
LimitNPROC=64000
# locked memory
LimitMEMLOCK=infinity
# total threads (user+kernel)
TasksMax=infinity
TasksAccounting=false
# Recommended limits for mongod as specified in
# https://docs.mongodb.com/manual/reference/ulimit/#recommended-ulimit-settings

[Install]
WantedBy=multi-user.target
`

		parser := &SystemdParser{}
		unit, err := parser.ParseUnit(unitContent)
		require.NoError(t, err)

		// IMP-007: Extract config path from ExecStart
		assert.Equal(t, "/usr/bin/mongod --config /etc/mongod.conf", unit.ExecStart)
		assert.Equal(t, "/etc/mongod.conf", unit.ConfigPath)
		assert.Equal(t, "mongodb", unit.User)
		assert.Equal(t, "mongodb", unit.Group)
		assert.NotEmpty(t, unit.Environment)
	})

	t.Run("parse mongos service unit", func(t *testing.T) {
		unitContent := `[Unit]
Description=MongoDB Sharding Router
After=network-online.target
Wants=network-online.target

[Service]
User=mongod
ExecStart=/usr/local/bin/mongos --configdb configRS/config1:27019,config2:27019,config3:27019 --port 27017
Restart=always

[Install]
WantedBy=multi-user.target
`

		parser := &SystemdParser{}
		unit, err := parser.ParseUnit(unitContent)
		require.NoError(t, err)

		assert.Contains(t, unit.ExecStart, "mongos")
		assert.Contains(t, unit.ExecStart, "--configdb")
		assert.Equal(t, "mongod", unit.User)
	})

	t.Run("extract config path from various ExecStart formats", func(t *testing.T) {
		parser := &SystemdParser{}

		testCases := []struct {
			execStart  string
			configPath string
		}{
			{
				execStart:  "/usr/bin/mongod --config /etc/mongod.conf",
				configPath: "/etc/mongod.conf",
			},
			{
				execStart:  "/usr/bin/mongod -f /etc/mongod.conf",
				configPath: "/etc/mongod.conf",
			},
			{
				execStart:  "/usr/bin/mongod --config=/etc/mongod.conf",
				configPath: "/etc/mongod.conf",
			},
			{
				execStart:  "/usr/bin/mongod -f=/etc/mongod.conf",
				configPath: "/etc/mongod.conf",
			},
			{
				execStart:  "/usr/bin/mongod --port 27017 --dbpath /data/db",
				configPath: "", // No config file
			},
		}

		for _, tc := range testCases {
			unit := &SystemdUnit{ExecStart: tc.execStart}
			parser.extractConfigPath(unit)
			assert.Equal(t, tc.configPath, unit.ConfigPath, "For ExecStart: %s", tc.execStart)
		}
	})
}

// IMP-007: Extract parameters from ExecStart
func TestExtractExecStartParams(t *testing.T) {
	parser := &SystemdParser{}

	t.Run("extract multiple parameters", func(t *testing.T) {
		execStart := "/usr/bin/mongod --config /etc/mongod.conf --port 27017 --dbpath /var/lib/mongodb --logpath /var/log/mongodb/mongod.log"

		unit := &SystemdUnit{ExecStart: execStart}
		parser.extractConfigPath(unit)
		params := parser.extractExecStartParams(execStart)

		assert.Equal(t, "/etc/mongod.conf", unit.ConfigPath)
		assert.Equal(t, "27017", params["port"])
		assert.Equal(t, "/var/lib/mongodb", params["dbpath"])
		assert.Equal(t, "/var/log/mongodb/mongod.log", params["logpath"])
	})

	t.Run("handle parameters with equals sign", func(t *testing.T) {
		execStart := "/usr/bin/mongod --config=/etc/mongod.conf --port=27017"

		params := parser.extractExecStartParams(execStart)
		assert.Equal(t, "/etc/mongod.conf", params["config"])
		assert.Equal(t, "27017", params["port"])
	})
}

// Test parsing environment variables
func TestParseEnvironment(t *testing.T) {
	parser := &SystemdParser{}

	t.Run("parse single environment variable", func(t *testing.T) {
		envLine := `Environment="OPTIONS=-f /etc/mongod.conf"`

		unit := &SystemdUnit{}
		parser.parseEnvironmentLine(envLine, unit)

		assert.Contains(t, unit.Environment, "OPTIONS=-f /etc/mongod.conf")
	})

	t.Run("parse multiple environment variables", func(t *testing.T) {
		unit := &SystemdUnit{}

		parser.parseEnvironmentLine(`Environment="FOO=bar"`, unit)
		parser.parseEnvironmentLine(`Environment="BAZ=qux"`, unit)

		assert.Len(t, unit.Environment, 2)
		assert.Contains(t, unit.Environment, "FOO=bar")
		assert.Contains(t, unit.Environment, "BAZ=qux")
	})
}
