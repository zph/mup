package supervisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LOCAL-011: supervisor state is interpreted regardless of casing or colour.
func TestParseStatusLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantName  string
		wantState ProcessState
		wantPID   int
	}{
		{"title case", "mongod-30000  Running  pid 4242, uptime 0:01:23", "mongod-30000", StateRunning, 4242},
		{"upper case", "mongod-30000  RUNNING  pid 4242, uptime 0:01:23", "mongod-30000", StateRunning, 4242},
		{"green ansi", "mongod-30000  \x1b[0;32mRunning\x1b[0m  pid 7, uptime 0:00:01", "mongod-30000", StateRunning, 7},
		{"red ansi fatal", "config-30001  \x1b[0;31mFatal\x1b[0m  Exited too quickly", "config-30001", StateFatal, 0},
		{"stopped", "mongos-30002  Stopped  Not started", "mongos-30002", StateStopped, 0},
		{"starting", "mongos-30002  STARTING", "mongos-30002", StateStarting, 0},
		{"backoff", "mongos-30002  Backoff  Exited too quickly", "mongos-30002", StateBackoff, 0},
		{"exited", "mongos-30002  Exited  Jan 01 10:00 AM", "mongos-30002", StateExited, 0},
		{"unknown", "mongos-30002  Weird", "mongos-30002", StateUnknown, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, err := parseStatusLine(tt.line)

			require.NoError(t, err)
			assert.Equal(t, tt.wantName, status.Name)
			assert.Equal(t, tt.wantState, status.ProcessState())
			assert.Equal(t, tt.wantPID, status.PID)
		})
	}
}

func TestParseStatusLine_RejectsMalformed(t *testing.T) {
	_, err := parseStatusLine("garbage")
	require.Error(t, err)
}

func TestProcessState_String(t *testing.T) {
	assert.Equal(t, "RUNNING", StateRunning.String())
	assert.Equal(t, "FATAL", StateFatal.String())
	assert.Equal(t, "UNKNOWN", StateUnknown.String())
	assert.True(t, StateRunning.IsRunning())
	assert.True(t, StateFatal.IsFailed())
	assert.True(t, StateBackoff.IsFailed())
	assert.True(t, StateExited.IsFailed())
	assert.False(t, StateStarting.IsFailed())
}
