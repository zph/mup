package testutil

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// WaitForPort waits for a port to be available for connection
func WaitForPort(t *testing.T, host string, port int, timeout time.Duration) error {
	t.Helper()

	deadline := time.Now().Add(timeout)
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 1*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for port %d on %s", port, host)
}

// MongoReadyTimeout is how long tests wait for a deployed node to answer
// mongosh. It is a ceiling on waiting for an event, not an expected duration.
const MongoReadyTimeout = 2 * time.Minute

// mongoQueryTimeout bounds one mongosh command run by a test. It is a
// ceiling for a stuck command, not an expected duration: the first writes
// through mongos to a newly sharded collection can take well over ten seconds
// on a busy machine.
const mongoQueryTimeout = 2 * time.Minute

// mongoshAttemptTimeout bounds one mongosh invocation. The first launch of a
// freshly copied mongosh binary can take several seconds on a busy machine.
const mongoshAttemptTimeout = 20 * time.Second

// WaitForMongoReady waits for MongoDB to be ready to accept connections
func WaitForMongoReady(t *testing.T, mongoshPath, connectionString string, timeout time.Duration) error {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), mongoshAttemptTimeout)
		cmd := exec.CommandContext(ctx, mongoshPath,
			connectionString,
			"--quiet",
			"--eval", "db.adminCommand({ping: 1})")

		output, err := cmd.CombinedOutput()
		cancel()

		if err == nil && strings.Contains(string(output), "ok") {
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("timeout waiting for MongoDB to be ready at %s", connectionString)
}

// QueryMongo executes a MongoDB command and returns the output
func QueryMongo(t *testing.T, mongoshPath, connectionString, command string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), mongoQueryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, mongoshPath,
		connectionString,
		"--quiet",
		"--eval", command)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("mongo command failed: %w\nOutput: %s", err, string(output))
	}

	return string(output), nil
}

// VerifyReplicaSetStatus verifies replica set is initialized and healthy
func VerifyReplicaSetStatus(t *testing.T, mongoshPath, connectionString string) error {
	t.Helper()

	output, err := QueryMongo(t, mongoshPath, connectionString, "rs.status()")
	if err != nil {
		return fmt.Errorf("failed to get replica set status: %w", err)
	}

	// Check for basic replica set indicators
	if !strings.Contains(output, "members") {
		return fmt.Errorf("replica set status missing 'members' field")
	}

	return nil
}

// GetClusterPorts finds which ports are being used by a cluster
func GetClusterPorts(t *testing.T, topologyContent string) []int {
	t.Helper()

	var ports []int
	lines := strings.Split(topologyContent, "\n")

	for _, line := range lines {
		if strings.Contains(line, "port:") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "port:" && i+1 < len(fields) {
					var port int
					_, _ = fmt.Sscanf(fields[i+1], "%d", &port)
					if port > 0 {
						ports = append(ports, port)
					}
				}
			}
		}
	}

	return ports
}

// CleanupCluster ensures a cluster is stopped and removed
func CleanupCluster(t *testing.T, clusterName string) {
	t.Helper()

	// Try to stop the cluster (ignore errors if already stopped)
	env := ClusterEnv(clusterName)
	stopResult := RunCommandWithEnv(t, env, "cluster", "stop", clusterName, "--yes")
	t.Logf("Stop cluster result (exit %d): %s", stopResult.ExitCode, stopResult.Stdout)

	// Try to destroy the cluster (ignore errors if doesn't exist)
	destroyResult := RunCommandWithEnv(t, env, "cluster", "destroy", clusterName, "--yes")
	t.Logf("Destroy cluster result (exit %d): %s", destroyResult.ExitCode, destroyResult.Stdout)
}

// PortAvailable checks if a port is available (not in use)
func PortAvailable(port int) bool {
	address := fmt.Sprintf("localhost:%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// FindAvailablePorts finds N consecutive available ports starting from a base port
func FindAvailablePorts(basePort, count int) ([]int, error) {
	ports := make([]int, 0, count)

	for port := basePort; port < basePort+1000 && len(ports) < count; port++ {
		if PortAvailable(port) {
			ports = append(ports, port)
		}
	}

	if len(ports) < count {
		return nil, fmt.Errorf("could not find %d available ports starting from %d", count, basePort)
	}

	return ports, nil
}
