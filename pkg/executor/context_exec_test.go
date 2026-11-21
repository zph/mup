package executor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LOCAL-042
func TestLocalExecutor_ExecuteContext(t *testing.T) {
	exec := NewLocalExecutor()

	t.Run("returns output", func(t *testing.T) {
		output, err := exec.ExecuteContext(context.Background(), "echo hello")

		require.NoError(t, err)
		assert.Equal(t, "hello\n", output)
	})

	t.Run("reports a failing command", func(t *testing.T) {
		_, err := exec.ExecuteContext(context.Background(), "echo oops >&2; exit 3")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "oops")
	})

	t.Run("a command that outlives its deadline is stopped", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		// The command would run for an hour; returning at all proves it was stopped.
		_, err := exec.ExecuteContext(ctx, "sleep 3600")

		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("a cancelled context does not run the command", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := exec.ExecuteContext(ctx, "echo should-not-run")

		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}
