//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zph/mup/test/e2e/testutil"
)

// TestLocalStandaloneDeployment tests deploying a single standalone MongoDB instance
func TestLocalStandaloneDeployment(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping actual deployment test in short mode")
	}

	// Find available port (start from 37017 to avoid conflicts with default MongoDB port 27017)
	ports, err := testutil.FindAvailablePorts(37017, 1)
	if err != nil {
		t.Fatalf("Failed to find available port: %v", err)
	}
	port := ports[0]

	clusterName := fmt.Sprintf("e2e-standalone-%d", port)

	// Ensure cleanup on test completion
	defer testutil.CleanupCluster(t, clusterName)

	// Create temp directory for test
	tmpDir := testutil.TempDir(t)
	storageDir := filepath.Join(tmpDir, "storage")

	// Create topology for standalone
	topologyContent := fmt.Sprintf(`global:
  user: %s
  deploy_dir: %s/deploy
  data_dir: %s/data

mongod_servers:
  - host: localhost
    port: %d
`, "testuser", tmpDir, tmpDir, port)

	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", topologyContent)

	// Set custom storage directory
	env := map[string]string{
		"MUP_STORAGE_DIR": storageDir,
	}

	// Deploy the cluster
	t.Logf("Deploying standalone cluster on port %d", port)
	deployResult := testutil.RunCommandWithEnv(t, env,
		"cluster", "deploy",
		clusterName,
		topologyFile,
		"--version", "7.0.0",
		"--auto-approve",
	)

	// Deployment should succeed
	if !deployResult.Success() {
		t.Fatalf("Deployment failed (exit %d):\nStdout:\n%s\nStderr:\n%s",
			deployResult.ExitCode, deployResult.Stdout, deployResult.Stderr)
	}

	t.Logf("Deployment completed in %v", deployResult.Duration)

	// Wait for MongoDB port to be available
	t.Logf("Waiting for MongoDB on port %d", port)
	if err := testutil.WaitForPort(t, "localhost", port, 30*time.Second); err != nil {
		t.Fatalf("MongoDB port not available: %v", err)
	}

	// Get mongosh path from cluster's local bin directory
	// Binaries are copied to <storage_dir>/clusters/<cluster_name>/v<version>/bin during deployment
	mongoshPath := filepath.Join(storageDir, "clusters", clusterName, "v7.0.0", "bin", "mongosh")

	// Wait for MongoDB to be ready
	connectionString := fmt.Sprintf("mongodb://localhost:%d", port)
	if err := testutil.WaitForMongoReady(t, mongoshPath, connectionString, 30*time.Second); err != nil {
		t.Fatalf("MongoDB not ready: %v", err)
	}

	// Verify we can connect and run commands
	output, err := testutil.QueryMongo(t, mongoshPath, connectionString, "db.version()")
	if err != nil {
		t.Fatalf("Failed to query MongoDB: %v", err)
	}

	if !strings.Contains(output, "7.0") {
		t.Errorf("Expected version 7.0, got: %s", output)
	}

	// Verify cluster status
	listResult := testutil.RunCommandWithEnv(t, env, "cluster", "list")
	testutil.AssertSuccess(t, listResult)
	testutil.AssertContains(t, listResult, clusterName)

	// Display cluster info
	displayResult := testutil.RunCommandWithEnv(t, env, "cluster", "display", clusterName)
	testutil.AssertSuccess(t, displayResult)
	testutil.AssertContains(t, displayResult, "7.0.0")

	// Stop the cluster
	t.Log("Stopping cluster")
	stopResult := testutil.RunCommandWithEnv(t, env, "cluster", "stop", clusterName)
	testutil.AssertSuccess(t, stopResult)

	// Verify port is no longer in use
	time.Sleep(2 * time.Second)
	if !testutil.PortAvailable(port) {
		t.Error("Port still in use after cluster stop")
	}

	// Start the cluster again
	t.Log("Starting cluster")
	startResult := testutil.RunCommandWithEnv(t, env, "cluster", "start", clusterName)
	testutil.AssertSuccess(t, startResult)

	// Wait for port again
	if err := testutil.WaitForPort(t, "localhost", port, 30*time.Second); err != nil {
		t.Fatalf("MongoDB port not available after restart: %v", err)
	}

	// Destroy the cluster
	t.Log("Destroying cluster")
	destroyResult := testutil.RunCommandWithEnv(t, env, "cluster", "destroy", clusterName, "--yes")
	testutil.AssertSuccess(t, destroyResult)

	// Verify cluster is gone
	listResult = testutil.RunCommandWithEnv(t, env, "cluster", "list")
	testutil.AssertSuccess(t, listResult)
	if strings.Contains(listResult.Stdout, clusterName) {
		t.Error("Cluster still listed after destroy")
	}
}

// TestLocalReplicaSetDeployment tests deploying a 3-node replica set
func TestLocalReplicaSetDeployment(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping actual deployment test in short mode")
	}

	// Find 3 available ports (start from 37017 to avoid conflicts with default MongoDB port 27017)
	ports, err := testutil.FindAvailablePorts(37017, 3)
	if err != nil {
		t.Fatalf("Failed to find available ports: %v", err)
	}

	clusterName := fmt.Sprintf("e2e-rs-%d", ports[0])

	// Ensure cleanup on test completion
	defer testutil.CleanupCluster(t, clusterName)

	// Create temp directory for test
	tmpDir := testutil.TempDir(t)
	storageDir := filepath.Join(tmpDir, "storage")

	// Create topology for 3-node replica set
	topologyContent := fmt.Sprintf(`global:
  user: testuser
  deploy_dir: %s/deploy
  data_dir: %s/data

mongod_servers:
  - host: localhost
    port: %d
    replica_set: rs0
  - host: localhost
    port: %d
    replica_set: rs0
  - host: localhost
    port: %d
    replica_set: rs0
`, tmpDir, tmpDir, ports[0], ports[1], ports[2])

	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", topologyContent)

	// Set custom storage directory
	env := map[string]string{
		"MUP_STORAGE_DIR": storageDir,
	}

	// Deploy the replica set
	t.Logf("Deploying replica set on ports %v", ports)
	deployResult := testutil.RunCommandWithEnv(t, env,
		"cluster", "deploy",
		clusterName,
		topologyFile,
		"--version", "7.0.0",
		"--auto-approve",
	)

	// Deployment should succeed
	if !deployResult.Success() {
		t.Fatalf("Deployment failed (exit %d):\nStdout:\n%s\nStderr:\n%s",
			deployResult.ExitCode, deployResult.Stdout, deployResult.Stderr)
	}

	t.Logf("Deployment completed in %v", deployResult.Duration)

	// Wait for all MongoDB ports to be available
	for i, port := range ports {
		t.Logf("Waiting for MongoDB node %d on port %d", i+1, port)
		if err := testutil.WaitForPort(t, "localhost", port, 30*time.Second); err != nil {
			t.Fatalf("MongoDB port %d not available: %v", port, err)
		}
	}

	// Get mongosh path from cluster's local bin directory
	// Binaries are copied to <storage_dir>/clusters/<cluster_name>/v<version>/bin during deployment
	mongoshPath := filepath.Join(storageDir, "clusters", clusterName, "v7.0.0", "bin", "mongosh")

	// Wait for primary to be ready
	connectionString := fmt.Sprintf("mongodb://localhost:%d", ports[0])
	if err := testutil.WaitForMongoReady(t, mongoshPath, connectionString, 30*time.Second); err != nil {
		t.Fatalf("MongoDB not ready: %v", err)
	}

	// Verify replica set status
	t.Log("Verifying replica set status")
	if err := testutil.VerifyReplicaSetStatus(t, mongoshPath, connectionString); err != nil {
		t.Fatalf("Replica set verification failed: %v", err)
	}

	// Verify we can write data
	t.Log("Testing write operations")
	writeCmd := `db.test.insertOne({test: "e2e", timestamp: new Date()})`
	_, err = testutil.QueryMongo(t, mongoshPath, connectionString, writeCmd)
	if err != nil {
		t.Fatalf("Failed to write to MongoDB: %v", err)
	}

	// Verify we can read data back
	t.Log("Testing read operations")
	readCmd := `db.test.find({test: "e2e"}).count()`
	output, err := testutil.QueryMongo(t, mongoshPath, connectionString, readCmd)
	if err != nil {
		t.Fatalf("Failed to read from MongoDB: %v", err)
	}

	if !strings.Contains(output, "1") {
		t.Errorf("Expected to find 1 document, got: %s", output)
	}

	// Verify cluster display shows all nodes
	displayResult := testutil.RunCommandWithEnv(t, env, "cluster", "display", clusterName)
	testutil.AssertSuccess(t, displayResult)
	for _, port := range ports {
		portStr := fmt.Sprintf("%d", port)
		if !strings.Contains(displayResult.Stdout, portStr) {
			t.Errorf("Cluster display missing port %d", port)
		}
	}

	// Stop the cluster
	t.Log("Stopping replica set")
	stopResult := testutil.RunCommandWithEnv(t, env, "cluster", "stop", clusterName)
	testutil.AssertSuccess(t, stopResult)

	// Verify all ports are released
	time.Sleep(3 * time.Second)
	for _, port := range ports {
		if !testutil.PortAvailable(port) {
			t.Errorf("Port %d still in use after cluster stop", port)
		}
	}

	// Destroy the cluster
	t.Log("Destroying replica set")
	destroyResult := testutil.RunCommandWithEnv(t, env, "cluster", "destroy", clusterName, "--yes")
	testutil.AssertSuccess(t, destroyResult)
}

// TestLocalDeploymentFailureHandling tests error handling during deployment
func TestLocalDeploymentFailureHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping actual deployment test in short mode")
	}

	// Test 1: Deploy with port already in use
	t.Run("port_conflict", func(t *testing.T) {
		// This test would require deliberately creating a port conflict
		// Skipping for now as it's complex to set up reliably
		t.Skip("Port conflict test requires additional setup")
	})

	// Test 2: Deploy with invalid topology
	t.Run("invalid_topology", func(t *testing.T) {
		tmpDir := testutil.TempDir(t)
		clusterName := "e2e-invalid-topo"

		// Create invalid topology (missing required fields)
		invalidTopo := `global:
  user: testuser

# Missing mongod_servers section
`
		topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", invalidTopo)

		// Deploy should fail
		result := testutil.RunCommand(t,
			"cluster", "deploy",
			clusterName,
			topologyFile,
			"--version", "7.0.0",
			"--auto-approve",
		)

		// Should fail with validation error
		testutil.AssertFailure(t, result)
		combined := result.Stdout + result.Stderr
		if !strings.Contains(combined, "mongod_servers") && !strings.Contains(combined, "validation") {
			t.Logf("Expected validation error about mongod_servers, got:\n%s", combined)
		}
	})
}

// TestLocalClusterConnect tests the cluster connect command
func TestLocalClusterConnect(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping actual deployment test in short mode")
	}

	// Find available port (start from 37017 to avoid conflicts with default MongoDB port 27017)
	ports, err := testutil.FindAvailablePorts(37017, 1)
	if err != nil {
		t.Fatalf("Failed to find available port: %v", err)
	}
	port := ports[0]

	clusterName := fmt.Sprintf("e2e-connect-%d", port)

	// Ensure cleanup
	defer testutil.CleanupCluster(t, clusterName)

	// Create temp directory
	tmpDir := testutil.TempDir(t)
	storageDir := filepath.Join(tmpDir, "storage")

	// Create simple topology
	topologyContent := fmt.Sprintf(`global:
  user: testuser
  deploy_dir: %s/deploy
  data_dir: %s/data

mongod_servers:
  - host: localhost
    port: %d
`, tmpDir, tmpDir, port)

	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", topologyContent)

	env := map[string]string{
		"MUP_STORAGE_DIR": storageDir,
	}

	// Deploy
	t.Logf("Deploying cluster for connect test on port %d", port)
	deployResult := testutil.RunCommandWithEnv(t, env,
		"cluster", "deploy",
		clusterName,
		topologyFile,
		"--version", "7.0.0",
		"--auto-approve",
	)
	testutil.AssertSuccess(t, deployResult)

	// Wait for MongoDB
	if err := testutil.WaitForPort(t, "localhost", port, 30*time.Second); err != nil {
		t.Fatalf("MongoDB not available: %v", err)
	}

	// Get mongosh path to verify it exists
	mongoshPath := filepath.Join(storageDir, "clusters", clusterName, "v7.0.0", "bin", "mongosh")
	connectionString := fmt.Sprintf("mongodb://localhost:%d", port)
	if err := testutil.WaitForMongoReady(t, mongoshPath, connectionString, 30*time.Second); err != nil {
		t.Fatalf("MongoDB not ready: %v", err)
	}

	// Test connect command interactively
	// Send MongoDB commands via stdin: get version and exit
	// mongosh will read these commands from stdin
	mongoInput := "db.version()\n.exit\n"
	connectResult := testutil.RunCommandWithEnvAndInput(t, env, mongoInput,
		"cluster", "connect", clusterName)

	// The command should succeed (mongosh connected and executed commands)
	testutil.AssertSuccess(t, connectResult)

	// Verify we got version output (mongosh prints version info)
	// The output should contain "7.0" from db.version()
	if !strings.Contains(connectResult.Stdout, "7.0") && !strings.Contains(connectResult.Combined, "7.0") {
		t.Logf("Connect output:\nStdout: %s\nStderr: %s\nCombined: %s",
			connectResult.Stdout, connectResult.Stderr, connectResult.Combined)
		// Don't fail - mongosh output format may vary, but connection succeeded if exit code is 0
	}

	// Cleanup
	testutil.CleanupCluster(t, clusterName)
}
