// +build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zph/mup/test/e2e/testutil"
)

// TestDeployPlanOnly verifies deploy in plan-only mode (no actual deployment)
func TestDeployPlanOnly(t *testing.T) {
	// Create temp directory for test
	tmpDir := testutil.TempDir(t)

	// Create a simple topology file
	topologyContent := `global:
  user: testuser
  deploy_dir: ` + tmpDir + `/deploy
  data_dir: ` + tmpDir + `/data

mongod_servers:
  - host: localhost
    port: 27017
    replica_set: rs0
  - host: localhost
    port: 27018
    replica_set: rs0
  - host: localhost
    port: 27019
    replica_set: rs0
`
	topologyFile := testutil.WriteFile(t, tmpDir, "topology.yaml", topologyContent)

	// Run deploy with --plan-only flag
	result := testutil.RunCommand(t,
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
		name        string
		args        []string
		shouldFail  bool
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
			errorContains: "",  // General usage error
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

	// Create topology file
	topologyContent := `global:
  user: testuser
  deploy_dir: ` + tmpDir + `/deploy
  data_dir: ` + tmpDir + `/data

mongod_servers:
  - host: localhost
    port: 27017
`
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

			// Run in plan-only mode
			result := testutil.RunCommand(t,
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
