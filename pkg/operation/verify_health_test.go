package operation

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/plan"
)

// listenOnFreePort holds a TCP port open for the duration of the test.
func listenOnFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	return listener.Addr().(*net.TCPAddr).Port
}

// closedPort returns a port that was just released and has no listener.
func closedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

func verifyHealthOp(ports interface{}) *plan.PlannedOperation {
	op := &plan.PlannedOperation{Type: plan.OpVerifyHealth, Params: map[string]interface{}{}}
	if ports != nil {
		op.Params[paramPorts] = ports
	}
	return op
}

// LOCAL-010
func TestVerifyHealth_PassesWhenAllPortsListening(t *testing.T) {
	port := listenOnFreePort(t)

	tests := map[string]interface{}{
		"in-memory plan":   []int{port},
		"json-loaded plan": []interface{}{float64(port)},
	}
	for name, ports := range tests {
		t.Run(name, func(t *testing.T) {
			result, err := (&VerifyHealthHandler{}).Execute(context.Background(), verifyHealthOp(ports), executor.NewLocalExecutor())

			require.NoError(t, err)
			assert.True(t, result.Success)
		})
	}
}

// LOCAL-010
func TestVerifyHealth_FailsWhenPortNotListening(t *testing.T) {
	ports := []int{listenOnFreePort(t), closedPort(t)}

	// The port never opens, so a short limit keeps the test quick; the
	// outcome does not depend on how long the wait actually takes.
	handler := &VerifyHealthHandler{Timeout: time.Millisecond}
	_, err := handler.Execute(context.Background(), verifyHealthOp(ports), executor.NewLocalExecutor())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not listening")
}

// LOCAL-010: a health check with nothing to check must not pass.
func TestVerifyHealth_FailsWithoutPorts(t *testing.T) {
	for name, ports := range map[string]interface{}{"missing": nil, "empty": []int{}} {
		t.Run(name, func(t *testing.T) {
			_, err := (&VerifyHealthHandler{}).Execute(context.Background(), verifyHealthOp(ports), executor.NewLocalExecutor())

			require.Error(t, err)
		})
	}
}

// LOCAL-045: health verification waits for slow starters instead of failing
// on the first look.
func TestWaitForPortsListening(t *testing.T) {
	const poll = time.Millisecond

	t.Run("waits for a port that opens late", func(t *testing.T) {
		checks := map[int]int{}
		listening := func(port int) (bool, error) {
			checks[port]++
			return port == 1 || checks[port] >= 4, nil
		}

		notListening, err := waitForPortsListening(context.Background(), []int{1, 2}, listening, time.Minute, poll)

		require.NoError(t, err)
		assert.Empty(t, notListening)
		assert.Equal(t, 4, checks[2])
		assert.Equal(t, 1, checks[1], "a port that is already listening is not checked again")
	})

	t.Run("reports ports that never open", func(t *testing.T) {
		listening := func(port int) (bool, error) { return port != 2, nil }

		notListening, err := waitForPortsListening(context.Background(), []int{1, 2, 3}, listening, time.Millisecond, poll)

		require.NoError(t, err)
		assert.Equal(t, []int{2}, notListening)
	})

	t.Run("a check that errors counts as not listening", func(t *testing.T) {
		listening := func(int) (bool, error) { return false, errors.New("cannot check") }

		notListening, err := waitForPortsListening(context.Background(), []int{7}, listening, time.Millisecond, poll)

		require.NoError(t, err)
		assert.Equal(t, []int{7}, notListening)
	})

	t.Run("stops when cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		listening := func(int) (bool, error) { return false, nil }

		_, err := waitForPortsListening(ctx, []int{7}, listening, time.Minute, poll)

		require.ErrorIs(t, err, context.Canceled)
	})
}
