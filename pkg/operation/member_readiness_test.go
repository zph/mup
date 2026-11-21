package operation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LOCAL-039: a replica set is initiated only once every member answers.
func TestWaitForMembersReachable(t *testing.T) {
	const poll = time.Millisecond
	hosts := []string{"127.0.0.1:1", "127.0.0.1:2", "127.0.0.1:3"}

	t.Run("waits for a slow member", func(t *testing.T) {
		attempts := map[string]int{}
		probe := func(_ context.Context, host string) error {
			attempts[host]++
			if host == "127.0.0.1:3" && attempts[host] < 4 {
				return errors.New("connection refused")
			}
			return nil
		}

		require.NoError(t, waitForMembersReachable(context.Background(), hosts, probe, time.Minute, poll))

		assert.Equal(t, 4, attempts["127.0.0.1:3"])
		assert.Equal(t, 1, attempts["127.0.0.1:1"], "members that already answered are not probed again")
	})

	t.Run("names the member that never answers", func(t *testing.T) {
		probe := func(_ context.Context, host string) error {
			if host == "127.0.0.1:2" {
				return errors.New("connection refused")
			}
			return nil
		}

		err := waitForMembersReachable(context.Background(), hosts, probe, time.Millisecond, poll)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "127.0.0.1:2")
		assert.NotContains(t, err.Error(), "127.0.0.1:1,")
	})

	t.Run("stops when cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		probe := func(context.Context, string) error { return errors.New("connection refused") }

		err := waitForMembersReachable(ctx, hosts, probe, time.Minute, poll)

		require.ErrorIs(t, err, context.Canceled)
	})
}
