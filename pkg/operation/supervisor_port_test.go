package operation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/executor"
)

// LOCAL-026: a supervisor control port already in use is reported up front.
func TestEnsureSupervisorPortFree(t *testing.T) {
	exec := executor.NewLocalExecutor()

	require.NoError(t, ensureSupervisorPortFree(exec, closedPort(t), "c1"))

	busy := listenOnFreePort(t)
	err := ensureSupervisorPortFree(exec, busy, "c1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "c1")
	assert.Contains(t, err.Error(), "already in use")
}
