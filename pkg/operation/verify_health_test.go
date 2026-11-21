package operation

import (
	"context"
	"net"
	"testing"

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

	_, err := (&VerifyHealthHandler{}).Execute(context.Background(), verifyHealthOp(ports), executor.NewLocalExecutor())

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
