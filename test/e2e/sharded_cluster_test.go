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

// TestShardedClusterDeployment tests deploying a complete sharded cluster
func TestShardedClusterDeployment(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping actual deployment test in short mode")
	}

	// Find available ports for:
	// - 3 config servers
	// - 6 shard servers (2 shards x 3 nodes each)
	// - 2 mongos routers
	// Total: 11 ports
	ports, err := testutil.FindAvailablePorts(40000, 11)
	if err != nil {
		t.Fatalf("Failed to find available ports: %v", err)
	}

	clusterName := fmt.Sprintf("e2e-sharded-%d", ports[0])

	// Ensure cleanup on test completion
	defer testutil.CleanupCluster(t, clusterName)

	// Create temp directory for test
	tmpDir := testutil.TempDir(t)
	storageDir := filepath.Join(tmpDir, "storage")

	// Create topology for sharded cluster
	// Config servers: ports[0:3]
	// Shard 1: ports[3:6]
	// Shard 2: ports[6:9]
	// Mongos: ports[9:11]
	topologyContent := fmt.Sprintf(`global:
  user: testuser
  deploy_dir: %s/deploy
  data_dir: %s/data

# Config servers (must be a replica set)
config_servers:
  - host: localhost
    port: %d
    replica_set: configRS
  - host: localhost
    port: %d
    replica_set: configRS
  - host: localhost
    port: %d
    replica_set: configRS

# Shard 1: 3-node replica set
mongod_servers:
  - host: localhost
    port: %d
    replica_set: shard1
  - host: localhost
    port: %d
    replica_set: shard1
  - host: localhost
    port: %d
    replica_set: shard1

  # Shard 2: 3-node replica set
  - host: localhost
    port: %d
    replica_set: shard2
  - host: localhost
    port: %d
    replica_set: shard2
  - host: localhost
    port: %d
    replica_set: shard2

# Mongos routers (query routers)
mongos_servers:
  - host: localhost
    port: %d
  - host: localhost
    port: %d
`, tmpDir, tmpDir,
		ports[0], ports[1], ports[2], // config servers
		ports[3], ports[4], ports[5], // shard1
		ports[6], ports[7], ports[8], // shard2
		ports[9], ports[10]) // mongos routers

	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", topologyContent)

	// Set custom storage directory
	env := map[string]string{
		"MUP_STORAGE_DIR": storageDir,
	}

	// Deploy the sharded cluster
	t.Logf("Deploying sharded cluster with ports %v", ports)
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

	// Wait for all ports to be available
	t.Log("Waiting for all cluster nodes to start...")
	for i, port := range ports {
		nodeType := "unknown"
		if i < 3 {
			nodeType = "config server"
		} else if i < 9 {
			nodeType = fmt.Sprintf("shard server (shard%d)", ((i-3)/3)+1)
		} else {
			nodeType = "mongos"
		}

		t.Logf("Waiting for %s on port %d", nodeType, port)
		if err := testutil.WaitForPort(t, "localhost", port, 45*time.Second); err != nil {
			t.Fatalf("Port %d (%s) not available: %v", port, nodeType, err)
		}
	}

	// Get mongosh path from cluster's local bin directory
	mongoshPath := filepath.Join(storageDir, "clusters", clusterName, "v7.0.0", "bin", "mongosh")

	// Connect via mongos to verify cluster is working
	mongosPort := ports[9]
	connectionString := fmt.Sprintf("mongodb://localhost:%d", mongosPort)

	t.Log("Waiting for mongos to be ready")
	if err := testutil.WaitForMongoReady(t, mongoshPath, connectionString, 30*time.Second); err != nil {
		t.Fatalf("Mongos not ready: %v", err)
	}

	// Verify sharding is enabled
	t.Log("Verifying sharded cluster status")
	output, err := testutil.QueryMongo(t, mongoshPath, connectionString, "sh.status()")
	if err != nil {
		t.Fatalf("Failed to query shard status: %v", err)
	}

	// Check that both shards are present
	if !strings.Contains(output, "shard1") {
		t.Error("Shard1 not found in cluster status")
	}
	if !strings.Contains(output, "shard2") {
		t.Error("Shard2 not found in cluster status")
	}

	// Test basic sharding operations
	t.Log("Testing sharding operations")

	// Enable sharding on test database
	enableCmd := `sh.enableSharding("testdb")`
	_, err = testutil.QueryMongo(t, mongoshPath, connectionString, enableCmd)
	if err != nil {
		t.Fatalf("Failed to enable sharding: %v", err)
	}

	// Create a sharded collection
	shardCmd := `sh.shardCollection("testdb.testcoll", {_id: "hashed"})`
	_, err = testutil.QueryMongo(t, mongoshPath, connectionString, shardCmd)
	if err != nil {
		t.Fatalf("Failed to shard collection: %v", err)
	}

	// Insert test data
	t.Log("Inserting test data into sharded collection")
	insertCmd := `for(let i = 0; i < 100; i++) { db.getSiblingDB("testdb").testcoll.insertOne({_id: i, data: "test" + i}); }`
	_, err = testutil.QueryMongo(t, mongoshPath, connectionString, insertCmd)
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	// Verify data was distributed across shards
	countCmd := `db.getSiblingDB("testdb").testcoll.count()`
	countOutput, err := testutil.QueryMongo(t, mongoshPath, connectionString, countCmd)
	if err != nil {
		t.Fatalf("Failed to count documents: %v", err)
	}

	if !strings.Contains(countOutput, "100") {
		t.Errorf("Expected 100 documents, got: %s", countOutput)
	}

	// Verify cluster display shows all components
	t.Log("Verifying cluster display")
	displayResult := testutil.RunCommandWithEnv(t, env, "cluster", "display", clusterName)
	testutil.AssertSuccess(t, displayResult)

	// Check for config servers
	for i := 0; i < 3; i++ {
		portStr := fmt.Sprintf("%d", ports[i])
		if !strings.Contains(displayResult.Stdout, portStr) {
			t.Errorf("Cluster display missing config server port %d", ports[i])
		}
	}

	// Check for mongos routers
	for i := 9; i < 11; i++ {
		portStr := fmt.Sprintf("%d", ports[i])
		if !strings.Contains(displayResult.Stdout, portStr) {
			t.Errorf("Cluster display missing mongos port %d", ports[i])
		}
	}

	// Test stop and start
	t.Log("Testing cluster stop/start")
	stopResult := testutil.RunCommandWithEnv(t, env, "cluster", "stop", clusterName, "--yes")
	testutil.AssertSuccess(t, stopResult)

	// Verify all ports are released
	time.Sleep(5 * time.Second)
	for i, port := range ports {
		if !testutil.PortAvailable(port) {
			t.Errorf("Port %d (index %d) still in use after cluster stop", port, i)
		}
	}

	// Start cluster again
	t.Log("Starting cluster")
	startResult := testutil.RunCommandWithEnv(t, env, "cluster", "start", clusterName, "--yes")
	testutil.AssertSuccess(t, startResult)

	// Wait for mongos to be available again
	if err := testutil.WaitForPort(t, "localhost", mongosPort, 45*time.Second); err != nil {
		t.Fatalf("Mongos port not available after restart: %v", err)
	}

	// Wait for mongos to be ready
	if err := testutil.WaitForMongoReady(t, mongoshPath, connectionString, 30*time.Second); err != nil {
		t.Fatalf("Mongos not ready after restart: %v", err)
	}

	// Verify data persisted
	t.Log("Verifying data persisted after restart")
	countOutput2, err := testutil.QueryMongo(t, mongoshPath, connectionString, countCmd)
	if err != nil {
		t.Fatalf("Failed to count documents after restart: %v", err)
	}

	if !strings.Contains(countOutput2, "100") {
		t.Errorf("Expected 100 documents after restart, got: %s", countOutput2)
	}

	// Cleanup
	t.Log("Destroying sharded cluster")
	destroyResult := testutil.RunCommandWithEnv(t, env, "cluster", "destroy", clusterName, "--yes")
	testutil.AssertSuccess(t, destroyResult)
}

// TestMinimalShardedCluster tests deploying a minimal sharded cluster (1 config server, 1 shard, 1 mongos)
func TestMinimalShardedCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping actual deployment test in short mode")
	}

	// Find 3 available ports (1 config, 1 shard, 1 mongos)
	ports, err := testutil.FindAvailablePorts(41000, 3)
	if err != nil {
		t.Fatalf("Failed to find available ports: %v", err)
	}

	clusterName := fmt.Sprintf("e2e-minimal-shard-%d", ports[0])

	// Ensure cleanup
	defer testutil.CleanupCluster(t, clusterName)

	// Create temp directory
	tmpDir := testutil.TempDir(t)
	storageDir := filepath.Join(tmpDir, "storage")

	// Minimal sharded cluster topology
	topologyContent := fmt.Sprintf(`global:
  user: testuser
  deploy_dir: %s/deploy
  data_dir: %s/data

config_servers:
  - host: localhost
    port: %d
    replica_set: configRS

mongod_servers:
  - host: localhost
    port: %d
    replica_set: shard1

mongos_servers:
  - host: localhost
    port: %d
`, tmpDir, tmpDir, ports[0], ports[1], ports[2])

	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", topologyContent)

	env := map[string]string{
		"MUP_STORAGE_DIR": storageDir,
	}

	// Deploy
	t.Logf("Deploying minimal sharded cluster on ports %v", ports)
	deployResult := testutil.RunCommandWithEnv(t, env,
		"cluster", "deploy",
		clusterName,
		topologyFile,
		"--version", "7.0.0",
		"--auto-approve",
	)

	testutil.AssertSuccess(t, deployResult)

	// Wait for all ports
	for _, port := range ports {
		if err := testutil.WaitForPort(t, "localhost", port, 30*time.Second); err != nil {
			t.Fatalf("Port %d not available: %v", port, err)
		}
	}

	// Verify via mongos
	mongoshPath := filepath.Join(storageDir, "clusters", clusterName, "v7.0.0", "bin", "mongosh")
	connectionString := fmt.Sprintf("mongodb://localhost:%d", ports[2])

	if err := testutil.WaitForMongoReady(t, mongoshPath, connectionString, 30*time.Second); err != nil {
		t.Fatalf("Mongos not ready: %v", err)
	}

	// Verify shard is present
	output, err := testutil.QueryMongo(t, mongoshPath, connectionString, "sh.status()")
	if err != nil {
		t.Fatalf("Failed to query status: %v", err)
	}

	if !strings.Contains(output, "shard1") {
		t.Error("Shard1 not found in cluster status")
	}

	// Cleanup
	testutil.CleanupCluster(t, clusterName)
}

// TestConfigServerReplicaSet tests deploying just a config server replica set
// This validates that config servers work independently without mongos/shards
func TestConfigServerReplicaSet(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping actual deployment test in short mode")
	}

	// Find 3 available ports for config servers
	ports, err := testutil.FindAvailablePorts(42000, 3)
	if err != nil {
		t.Fatalf("Failed to find available ports: %v", err)
	}

	clusterName := fmt.Sprintf("e2e-config-rs-%d", ports[0])

	// Ensure cleanup
	defer testutil.CleanupCluster(t, clusterName)

	// Create temp directory
	tmpDir := testutil.TempDir(t)
	storageDir := filepath.Join(tmpDir, "storage")

	// Config server replica set topology (no mongos, no shards)
	topologyContent := fmt.Sprintf(`global:
  user: testuser
  deploy_dir: %s/deploy
  data_dir: %s/data

config_servers:
  - host: localhost
    port: %d
    replica_set: configRS
  - host: localhost
    port: %d
    replica_set: configRS
  - host: localhost
    port: %d
    replica_set: configRS
`, tmpDir, tmpDir, ports[0], ports[1], ports[2])

	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", topologyContent)

	env := map[string]string{
		"MUP_STORAGE_DIR": storageDir,
	}

	// Deploy config server replica set
	t.Logf("Deploying config server replica set on ports %v", ports)
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

	// Wait for all config server ports to be available
	t.Log("Waiting for config servers to start...")
	for i, port := range ports {
		t.Logf("Waiting for config server %d on port %d", i+1, port)
		if err := testutil.WaitForPort(t, "localhost", port, 45*time.Second); err != nil {
			t.Fatalf("Port %d not available: %v", port, err)
		}
	}

	// Get mongosh path from cluster's local bin directory
	mongoshPath := filepath.Join(storageDir, "clusters", clusterName, "v7.0.0", "bin", "mongosh")

	// Connect to first config server
	connectionString := fmt.Sprintf("mongodb://localhost:%d", ports[0])

	t.Log("Waiting for config server to be ready")
	if err := testutil.WaitForMongoReady(t, mongoshPath, connectionString, 30*time.Second); err != nil {
		t.Fatalf("Config server not ready: %v", err)
	}

	// Verify replica set is initialized and healthy
	t.Log("Verifying replica set status")
	if err := testutil.VerifyReplicaSetStatus(t, mongoshPath, connectionString); err != nil {
		t.Fatalf("Replica set status check failed: %v", err)
	}

	// Get replica set status details
	rsStatusOutput, err := testutil.QueryMongo(t, mongoshPath, connectionString, "rs.status()")
	if err != nil {
		t.Fatalf("Failed to query replica set status: %v", err)
	}

	// Verify we have 3 members
	memberCount := strings.Count(rsStatusOutput, `"name"`)
	if memberCount != 3 {
		t.Errorf("Expected 3 replica set members, got output suggesting %d: %s", memberCount, rsStatusOutput)
	}

	// Verify replica set name is configRS
	if !strings.Contains(rsStatusOutput, "configRS") {
		t.Error("Replica set name 'configRS' not found in status")
	}

	// Test basic database operations on config server
	t.Log("Testing basic operations on config server")

	// Create test data
	insertCmd := `db.getSiblingDB("testdb").testcoll.insertOne({_id: 1, test: "config-server-data"})`
	_, err = testutil.QueryMongo(t, mongoshPath, connectionString, insertCmd)
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	// Query test data
	queryCmd := `db.getSiblingDB("testdb").testcoll.findOne({_id: 1})`
	queryOutput, err := testutil.QueryMongo(t, mongoshPath, connectionString, queryCmd)
	if err != nil {
		t.Fatalf("Failed to query data: %v", err)
	}

	if !strings.Contains(queryOutput, "config-server-data") {
		t.Errorf("Expected to find inserted data, got: %s", queryOutput)
	}

	// Verify cluster display shows all config servers
	t.Log("Verifying cluster display")
	displayResult := testutil.RunCommandWithEnv(t, env, "cluster", "display", clusterName)
	testutil.AssertSuccess(t, displayResult)

	// Check for all config server ports
	for i, port := range ports {
		portStr := fmt.Sprintf("%d", port)
		if !strings.Contains(displayResult.Stdout, portStr) {
			t.Errorf("Cluster display missing config server %d port %d", i+1, port)
		}
	}

	// Test stop and start
	t.Log("Testing cluster stop/start")
	stopResult := testutil.RunCommandWithEnv(t, env, "cluster", "stop", clusterName, "--yes")
	testutil.AssertSuccess(t, stopResult)

	// Verify all ports are released
	time.Sleep(5 * time.Second)
	for i, port := range ports {
		if !testutil.PortAvailable(port) {
			t.Errorf("Port %d (config server %d) still in use after cluster stop", port, i+1)
		}
	}

	// Start cluster again
	t.Log("Starting cluster")
	startResult := testutil.RunCommandWithEnv(t, env, "cluster", "start", clusterName, "--yes")
	testutil.AssertSuccess(t, startResult)

	// Wait for first config server to be available again
	if err := testutil.WaitForPort(t, "localhost", ports[0], 45*time.Second); err != nil {
		t.Fatalf("Config server port not available after restart: %v", err)
	}

	// Wait for config server to be ready
	if err := testutil.WaitForMongoReady(t, mongoshPath, connectionString, 30*time.Second); err != nil {
		t.Fatalf("Config server not ready after restart: %v", err)
	}

	// Verify data persisted after restart
	t.Log("Verifying data persisted after restart")
	queryOutput2, err := testutil.QueryMongo(t, mongoshPath, connectionString, queryCmd)
	if err != nil {
		t.Fatalf("Failed to query data after restart: %v", err)
	}

	if !strings.Contains(queryOutput2, "config-server-data") {
		t.Errorf("Expected to find data after restart, got: %s", queryOutput2)
	}

	// Cleanup
	t.Log("Destroying config server replica set")
	destroyResult := testutil.RunCommandWithEnv(t, env, "cluster", "destroy", clusterName, "--yes")
	testutil.AssertSuccess(t, destroyResult)
}
