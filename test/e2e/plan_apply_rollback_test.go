//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zph/mup/test/e2e/testutil"
)

const (
	e2eBaseVersion    = "7.0.0"
	e2eUpgradeVersion = "7.0.48"
	planApplyBasePort = 44000
	rollbackBasePort  = 45000
)

// nodeVersions asks each node directly which server version it runs.
func nodeVersions(t *testing.T, mongoshPath string, ports []int) []string {
	t.Helper()
	versions := make([]string, 0, len(ports))
	for _, port := range ports {
		uri := fmt.Sprintf("mongodb://localhost:%d/?directConnection=true", port)
		if err := testutil.WaitForMongoReady(t, mongoshPath, uri, testutil.MongoReadyTimeout); err != nil {
			t.Fatalf("node on port %d not ready: %v", port, err)
		}
		output, err := testutil.QueryMongo(t, mongoshPath, uri, "db.version()")
		if err != nil {
			t.Fatalf("failed to read version from port %d: %v", port, err)
		}
		versions = append(versions, strings.TrimSpace(output))
	}
	return versions
}

func countVersion(versions []string, want string) int {
	count := 0
	for _, version := range versions {
		if version == want {
			count++
		}
	}
	return count
}

// TestPlanApply generates a plan without applying it, then applies it from
// disk with 'mup plan apply' and checks the cluster it produces.
// LOCAL-032, LOCAL-022
func TestPlanApply(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping actual deployment test in short mode")
	}

	ports, err := testutil.FindAvailablePorts(planApplyBasePort, 3)
	if err != nil {
		t.Fatalf("Failed to find available ports: %v", err)
	}
	clusterName := fmt.Sprintf("e2e-plan-apply-%d", ports[0])
	defer testutil.CleanupCluster(t, clusterName)

	tmpDir := testutil.TempDir(t)
	storageDir := filepath.Join(tmpDir, "storage")
	env := map[string]string{"MUP_STORAGE_DIR": storageDir}

	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", fmt.Sprintf(`mongod_servers:
  - {host: localhost, port: %d, replica_set: rsplan, priority: 2}
  - {host: localhost, port: %d, replica_set: rsplan}
  - {host: localhost, port: %d, replica_set: rsplan, priority: 0, hidden: true, votes: 0}
`, ports[0], ports[1], ports[2]))

	planResult := testutil.RunCommandWithEnv(t, env,
		"cluster", "deploy", clusterName, topologyFile, "--version", e2eBaseVersion, "--plan-only")
	testutil.AssertSuccess(t, planResult)

	// Planning alone starts nothing.
	for _, port := range ports {
		if !testutil.PortAvailable(port) {
			t.Fatalf("port %d is in use after --plan-only", port)
		}
	}

	applyResult := testutil.RunCommandWithEnv(t, env, "plan", "apply", clusterName, "--auto-approve")
	if !applyResult.Success() {
		t.Fatalf("plan apply failed (exit %d):\nStdout:\n%s\nStderr:\n%s", applyResult.ExitCode, applyResult.Stdout, applyResult.Stderr)
	}

	mongoshPath := filepath.Join(storageDir, "clusters", clusterName, "v"+e2eBaseVersion, "bin", "mongosh")
	versions := nodeVersions(t, mongoshPath, ports)
	if got := countVersion(versions, e2eBaseVersion); got != len(ports) {
		t.Fatalf("expected every node on %s, got %v", e2eBaseVersion, versions)
	}

	// Member settings from the topology reached the replica set.
	uri := fmt.Sprintf("mongodb://localhost:%d/?directConnection=true", ports[0])
	memberConfig, err := testutil.QueryMongo(t, mongoshPath, uri,
		"JSON.stringify(rs.conf().members.map(m => [m.priority, m.hidden, m.votes]))")
	if err != nil {
		t.Fatalf("failed to read replica set config: %v", err)
	}
	if got, want := strings.TrimSpace(memberConfig), "[[2,false,1],[1,false,1],[0,true,0]]"; got != want {
		t.Errorf("replica set member settings = %s, want %s", got, want)
	}

	// The apply is recorded, and the same plan cannot be applied twice.
	stateResult := testutil.RunCommandWithEnv(t, env, "state", "show", clusterName)
	testutil.AssertSuccess(t, stateResult)
	testutil.AssertContains(t, stateResult, "completed")

	secondApply := testutil.RunCommandWithEnv(t, env, "plan", "apply", clusterName, "--auto-approve")
	testutil.AssertFailure(t, secondApply)
	testutil.AssertStderrContains(t, secondApply, "already applied")

	destroyResult := testutil.RunCommandWithEnv(t, env, "cluster", "destroy", clusterName, "--yes")
	testutil.AssertSuccess(t, destroyResult)
}

// TestUpgradeRollback pauses a rolling upgrade after its first node and
// rolls it back, then checks every node is on the original version.
// UPGSAFE-014
func TestUpgradeRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping actual deployment test in short mode")
	}

	ports, err := testutil.FindAvailablePorts(rollbackBasePort, 3)
	if err != nil {
		t.Fatalf("Failed to find available ports: %v", err)
	}
	clusterName := fmt.Sprintf("e2e-rollback-%d", ports[0])
	defer testutil.CleanupCluster(t, clusterName)

	tmpDir := testutil.TempDir(t)
	storageDir := filepath.Join(tmpDir, "storage")
	env := map[string]string{"MUP_STORAGE_DIR": storageDir}
	clusterDir := filepath.Join(storageDir, "clusters", clusterName)

	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", fmt.Sprintf(`mongod_servers:
  - {host: localhost, port: %d, replica_set: rsroll}
  - {host: localhost, port: %d, replica_set: rsroll}
  - {host: localhost, port: %d, replica_set: rsroll}
`, ports[0], ports[1], ports[2]))

	deployResult := testutil.RunCommandWithEnv(t, env,
		"cluster", "deploy", clusterName, topologyFile, "--version", e2eBaseVersion, "--auto-approve")
	if !deployResult.Success() {
		t.Fatalf("Deployment failed (exit %d):\nStdout:\n%s\nStderr:\n%s", deployResult.ExitCode, deployResult.Stdout, deployResult.Stderr)
	}
	mongoshPath := filepath.Join(clusterDir, "v"+e2eBaseVersion, "bin", "mongosh")
	if got := countVersion(nodeVersions(t, mongoshPath, ports), e2eBaseVersion); got != len(ports) {
		t.Fatalf("expected every node on %s before the upgrade", e2eBaseVersion)
	}

	// Continue at the first node's prompt, pause at the second.
	t.Log("Upgrading one node, then pausing")
	pausedUpgrade := testutil.RunCommandWithEnvAndInput(t, env, "c\np\n",
		"cluster", "upgrade", clusterName, "--to-version", e2eUpgradeVersion, "--prompt-level", "node")
	testutil.AssertFailure(t, pausedUpgrade)
	testutil.AssertContains(t, pausedUpgrade, "paused")

	mixed := nodeVersions(t, mongoshPath, ports)
	if upgraded := countVersion(mixed, e2eUpgradeVersion); upgraded != 1 {
		t.Fatalf("expected exactly one node on %s after pausing, got %v", e2eUpgradeVersion, mixed)
	}

	t.Log("Rolling back")
	rollbackResult := testutil.RunCommandWithEnv(t, env,
		"cluster", "upgrade", clusterName, "--rollback", "--prompt-level", "none")
	if !rollbackResult.Success() {
		t.Fatalf("rollback failed (exit %d):\nStdout:\n%s\nStderr:\n%s", rollbackResult.ExitCode, rollbackResult.Stdout, rollbackResult.Stderr)
	}

	restored := nodeVersions(t, mongoshPath, ports)
	if got := countVersion(restored, e2eBaseVersion); got != len(ports) {
		t.Fatalf("expected every node back on %s, got %v", e2eBaseVersion, restored)
	}

	// The set is whole again, and the unfinished upgrade is no longer pending.
	uri := fmt.Sprintf("mongodb://localhost:%d/?replicaSet=rsroll", ports[0])
	states, err := testutil.QueryMongo(t, mongoshPath, uri,
		"JSON.stringify(rs.status().members.map(m => m.stateStr).sort())")
	if err != nil {
		t.Fatalf("failed to read replica set status: %v", err)
	}
	if got, want := strings.TrimSpace(states), `["PRIMARY","SECONDARY","SECONDARY"]`; got != want {
		t.Errorf("replica set member states = %s, want %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(clusterDir, "upgrade-state.yaml")); !os.IsNotExist(err) {
		t.Errorf("upgrade state should be archived after rollback (stat error: %v)", err)
	}

	secondRollback := testutil.RunCommandWithEnv(t, env,
		"cluster", "upgrade", clusterName, "--rollback", "--prompt-level", "none")
	testutil.AssertFailure(t, secondRollback)

	destroyResult := testutil.RunCommandWithEnv(t, env, "cluster", "destroy", clusterName, "--yes")
	testutil.AssertSuccess(t, destroyResult)

	// Give processes a moment to release their ports before the next test.
	deadline := time.Now().Add(testutil.MongoReadyTimeout)
	for time.Now().Before(deadline) && !testutil.PortAvailable(ports[0]) {
		time.Sleep(time.Second)
	}
}
