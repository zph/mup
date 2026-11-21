package operation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zph/mup/pkg/executor"
)

// LOCAL-042
func TestStartOutcome(t *testing.T) {
	startErr := errors.New("command stopped: context deadline exceeded")

	t.Run("a clean start succeeds", func(t *testing.T) {
		require.NoError(t, startOutcome("mongod-1", nil, func() bool { return false }))
	})

	t.Run("a start that errored but left the process running succeeds", func(t *testing.T) {
		require.NoError(t, startOutcome("mongod-1", startErr, func() bool { return true }))
	})

	t.Run("a start that errored and left nothing running fails with the cause", func(t *testing.T) {
		err := startOutcome("mongod-1", startErr, func() bool { return false })

		require.Error(t, err)
		assert.ErrorIs(t, err, startErr)
		assert.Contains(t, err.Error(), "mongod-1")
	})
}

// LOCAL-042: a command is bounded even when the caller's context has no deadline.
func TestExecuteBounded_StopsAStuckCommand(t *testing.T) {
	_, err := executeBounded(context.Background(), localExecutorForTest(), "sleep 3600", 50*time.Millisecond)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecuteBounded_ReturnsOutput(t *testing.T) {
	output, err := executeBounded(context.Background(), localExecutorForTest(), "echo ok", time.Minute)

	require.NoError(t, err)
	assert.Equal(t, "ok\n", output)
}

func localExecutorForTest() executor.Executor {
	return executor.NewLocalExecutor()
}
