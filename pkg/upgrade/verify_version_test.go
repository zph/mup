package upgrade

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UPGSAFE-007
func TestPortFromNodeID(t *testing.T) {
	port, err := portFromNodeID("mongod-30000")
	require.NoError(t, err)
	assert.Equal(t, 30000, port)

	port, err = portFromNodeID("config-27019")
	require.NoError(t, err)
	assert.Equal(t, 27019, port)

	_, err = portFromNodeID("mongod")
	require.Error(t, err)
	_, err = portFromNodeID("mongod-abc")
	require.Error(t, err)
}

// UPGSAFE-007: the node is polled until it answers, then its version is checked.
func TestWaitForServerVersion(t *testing.T) {
	const pollInterval = time.Millisecond

	t.Run("retries until the server answers", func(t *testing.T) {
		attempts := 0
		lookup := func(context.Context) (string, error) {
			attempts++
			if attempts < 3 {
				return "", errors.New("connection refused")
			}
			return "7.0.14", nil
		}

		err := waitForServerVersion(context.Background(), lookup, "7.0", time.Minute, pollInterval)

		require.NoError(t, err)
		assert.Equal(t, 3, attempts)
	})

	t.Run("fails at once on the wrong version", func(t *testing.T) {
		attempts := 0
		lookup := func(context.Context) (string, error) {
			attempts++
			return "6.0.15", nil
		}

		err := waitForServerVersion(context.Background(), lookup, "7.0", time.Minute, pollInterval)

		require.Error(t, err)
		assert.Equal(t, 1, attempts)
		assert.Contains(t, err.Error(), "6.0.15")
		assert.Contains(t, err.Error(), "7.0")
	})

	t.Run("gives up when the server never answers", func(t *testing.T) {
		lookup := func(context.Context) (string, error) { return "", errors.New("connection refused") }

		err := waitForServerVersion(context.Background(), lookup, "7.0", time.Millisecond, pollInterval)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("stops when the context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		lookup := func(context.Context) (string, error) { return "", errors.New("connection refused") }

		err := waitForServerVersion(ctx, lookup, "7.0", time.Minute, pollInterval)

		require.ErrorIs(t, err, context.Canceled)
	})
}
