//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zph/mup/test/e2e/testutil"
)

// TestDeployPlanOnly verifies deploy in plan-only mode (no actual deployment)
func TestDeployPlanOnly(t *testing.T) {
	// Create temp directory for test
	tmpDir := testutil.TempDir(t)

	ports, err := testutil.FindAvailablePorts(planTestBasePort, 3)
	if err != nil {
		t.Fatalf("Failed to find available ports: %v", err)
	}

	// Create a simple topology file
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

	// Run deploy with --plan-only flag, isolated from the real storage directory
	result := testutil.RunCommandWithEnv(t, isolatedStorageEnv(tmpDir),
		"cluster", "deploy",
		"test-e2e-cluster",
		topologyFile,
		"--version", "7.0.0",
		"--plan-only",
	)

	// Plan-only should succeed
	testutil.AssertSuccess(t, result)

	// Output should show plan generation (not actual deployment)
	testutil.AssertContains(t, result, "Generating deployment plan")
	testutil.AssertContains(t, result, "test-e2e-cluster")
	testutil.AssertContains(t, result, "Plan saved")

	// Should mention operations that would be executed
	testutil.AssertContains(t, result, "operations")
}

// TestDeployValidation verifies deploy command validation
func TestDeployValidation(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		shouldFail    bool
		errorContains string
	}{
		{
			name:          "missing topology file",
			args:          []string{"cluster", "deploy", "test-cluster", "--version", "7.0.0"},
			shouldFail:    true,
			errorContains: "topology",
		},
		{
			name:          "missing version",
			args:          []string{"cluster", "deploy", "test-cluster", "/nonexistent/topology.yaml"},
			shouldFail:    true,
			errorContains: "version",
		},
		{
			name:          "missing cluster name",
			args:          []string{"cluster", "deploy", "--version", "7.0.0"},
			shouldFail:    true,
			errorContains: "", // General usage error
		},
		{
			name:          "invalid topology file path",
			args:          []string{"cluster", "deploy", "test-cluster", "/nonexistent/file.yaml", "--version", "7.0.0", "--plan-only"},
			shouldFail:    true,
			errorContains: "no such file or directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testutil.RunCommand(t, tt.args...)

			if tt.shouldFail {
				testutil.AssertFailure(t, result)
				if tt.errorContains != "" {
					combined := result.Stdout + result.Stderr
					if !strings.Contains(combined, tt.errorContains) {
						t.Errorf("Expected error to contain %q, got:\nStdout: %s\nStderr: %s",
							tt.errorContains, result.Stdout, result.Stderr)
					}
				}
			} else {
				testutil.AssertSuccess(t, result)
			}
		})
	}
}

// TestPlanGeneration verifies plan is generated and can be viewed
func TestPlanGeneration(t *testing.T) {
	// Create temp directory for test
	tmpDir := testutil.TempDir(t)

	ports, err := testutil.FindAvailablePorts(planTestBasePort, 1)
	if err != nil {
		t.Fatalf("Failed to find available port: %v", err)
	}

	// Create topology file
	topologyContent := fmt.Sprintf(`global:
  user: testuser
  deploy_dir: %s/deploy
  data_dir: %s/data

mongod_servers:
  - host: localhost
    port: %d
`, tmpDir, tmpDir, ports[0])
	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", topologyContent)

	// Set custom storage directory
	env := map[string]string{
		"MUP_STORAGE_DIR": tmpDir + "/storage",
	}

	// Run deploy with plan-only to generate a plan
	deployResult := testutil.RunCommandWithEnv(t, env,
		"cluster", "deploy",
		"test-plan-gen",
		topologyFile,
		"--version", "7.0.0",
		"--plan-only",
	)
	testutil.AssertSuccess(t, deployResult)

	// List plans
	listResult := testutil.RunCommandWithEnv(t, env, "plan", "list")
	testutil.AssertSuccess(t, listResult)

	// Should show at least one plan
	if len(listResult.Lines()) == 0 {
		t.Error("Expected to see plans listed, got empty output")
	}
}

// TestLockCommands verifies lock management commands
func TestLockCommands(t *testing.T) {
	tmpDir := testutil.TempDir(t)
	env := map[string]string{
		"MUP_STORAGE_DIR": tmpDir + "/storage",
	}

	// List locks (should be empty initially)
	result := testutil.RunCommandWithEnv(t, env, "lock", "list")
	testutil.AssertSuccess(t, result)

	// Cleanup command should succeed even with no locks
	result = testutil.RunCommandWithEnv(t, env, "lock", "cleanup")
	testutil.AssertSuccess(t, result)
}

// TestFixtureTopologies verifies we can use fixture topology files
func TestFixtureTopologies(t *testing.T) {
	tests := []struct {
		name        string
		fixture     string
		clusterName string
	}{
		{
			name:        "simple replica set",
			fixture:     "simple-replica-set.yaml",
			clusterName: "test-rs",
		},
		{
			name:        "standalone",
			fixture:     "standalone.yaml",
			clusterName: "test-standalone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Get fixture path
			fixturePath := filepath.Join("fixtures", tt.fixture)

			// Run in plan-only mode, isolated from the real storage directory
			result := testutil.RunCommandWithEnv(t, isolatedStorageEnv(testutil.TempDir(t)),
				"cluster", "deploy",
				tt.clusterName,
				fixturePath,
				"--version", "7.0.0",
				"--plan-only",
			)

			// Should succeed
			testutil.AssertSuccess(t, result)

			// Should mention cluster name
			testutil.AssertContains(t, result, tt.clusterName)
		})
	}
}

// planTestBasePort is where plan-only tests start looking for free ports; it
// is well away from MongoDB's default 27017, which is often already in use.
const planTestBasePort = 43000

// isolatedStorageEnv points mup at a storage directory under dir.
func isolatedStorageEnv(dir string) map[string]string {
	return map[string]string{"MUP_STORAGE_DIR": filepath.Join(dir, "storage")}
}

// TestPlanValidateAndState covers the read-only plan and state commands:
// a generated plan validates, an edited plan is refused, and a cluster with
// no applies reports that plainly.
func TestPlanValidateAndState(t *testing.T) {
	tmpDir := testutil.TempDir(t)
	env := isolatedStorageEnv(tmpDir)
	const clusterName = "test-plan-validate"

	ports, err := testutil.FindAvailablePorts(planTestBasePort, 1)
	if err != nil {
		t.Fatalf("Failed to find available port: %v", err)
	}
	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", fmt.Sprintf(`mongod_servers:
  - host: localhost
    port: %d
`, ports[0]))

	deployResult := testutil.RunCommandWithEnv(t, env,
		"cluster", "deploy", clusterName, topologyFile, "--version", "7.0.0", "--plan-only")
	testutil.AssertSuccess(t, deployResult)

	validateResult := testutil.RunCommandWithEnv(t, env, "plan", "validate", clusterName)
	testutil.AssertSuccess(t, validateResult)
	testutil.AssertContains(t, validateResult, "Plan is valid")

	showResult := testutil.RunCommandWithEnv(t, env, "plan", "show", clusterName)
	testutil.AssertSuccess(t, showResult)

	// No apply has run yet.
	stateResult := testutil.RunCommandWithEnv(t, env, "state", "show", clusterName)
	testutil.AssertFailure(t, stateResult)

	// An edited plan no longer matches its checksum and must be refused.
	planFiles, err := filepath.Glob(filepath.Join(tmpDir, "storage", "clusters", clusterName, "plans", "*.json"))
	if err != nil || len(planFiles) != 1 {
		t.Fatalf("expected exactly one plan file, got %v (err: %v)", planFiles, err)
	}
	original := testutil.ReadFile(t, planFiles[0])
	testutil.WriteFile(t, filepath.Dir(planFiles[0]), filepath.Base(planFiles[0]), original+"\n")

	tamperedResult := testutil.RunCommandWithEnv(t, env, "plan", "validate", clusterName)
	testutil.AssertFailure(t, tamperedResult)
	testutil.AssertStderrContains(t, tamperedResult, "checksum")

	applyResult := testutil.RunCommandWithEnv(t, env, "plan", "apply", clusterName, "--auto-approve")
	testutil.AssertFailure(t, applyResult)
	testutil.AssertStderrContains(t, applyResult, "checksum")
}
