// +build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/zph/mup/test/e2e/testutil"
)

// TestHelpCommand verifies that the help command works correctly
func TestHelpCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains []string
	}{
		{
			name: "root help",
			args: []string{"--help"},
			contains: []string{
				"mup",
				"Usage:",
				"Available Commands:",
				"cluster",
				"plan",
				"lock",
			},
		},
		{
			name: "short help flag",
			args: []string{"-h"},
			contains: []string{
				"mup",
				"Usage:",
			},
		},
		{
			name: "cluster help",
			args: []string{"cluster", "--help"},
			contains: []string{
				"cluster",
				"deploy",
				"list",
				"stop",
				"status",
			},
		},
		{
			name: "deploy help",
			args: []string{"cluster", "deploy", "--help"},
			contains: []string{
				"deploy",
				"--version",
				"<cluster-name>",
				"--auto-approve",
			},
		},
		{
			name: "plan help",
			args: []string{"plan", "--help"},
			contains: []string{
				"plan",
				"list",
				"show",
				"apply",
			},
		},
		{
			name: "lock help",
			args: []string{"lock", "--help"},
			contains: []string{
				"lock",
				"list",
				"show",
				"release",
				"force-unlock",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testutil.RunCommand(t, tt.args...)

			// Help commands should exit with code 0
			testutil.AssertSuccess(t, result)

			// Verify expected content is present
			for _, expected := range tt.contains {
				if !strings.Contains(result.Stdout, expected) {
					t.Errorf("Help output missing %q\nOutput:\n%s", expected, result.Stdout)
				}
			}

			// Help output should not be empty
			if len(result.Stdout) < 100 {
				t.Errorf("Help output suspiciously short (%d chars): %s",
					len(result.Stdout), result.Stdout)
			}
		})
	}
}

// TestVersionCommand verifies version flag behavior
func TestVersionCommand(t *testing.T) {
	result := testutil.RunCommand(t, "--version")

	// --version flag is not currently supported, should fail
	testutil.AssertFailure(t, result)

	// Should show error about unknown flag
	if !strings.Contains(result.Stderr, "unknown flag") {
		t.Errorf("Expected unknown flag error, got: %s", result.Stderr)
	}
}

// TestInvalidCommand verifies error handling for invalid commands
func TestInvalidCommand(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		shouldFail   bool
		contains     string
	}{
		{
			name:       "invalid root command",
			args:       []string{"invalid-command"},
			shouldFail: true,
			contains:   "unknown command",
		},
		{
			name:       "invalid cluster subcommand shows help",
			args:       []string{"cluster", "invalid-subcommand"},
			shouldFail: false, // Cobra shows help with exit 0
			contains:   "Available Commands",
		},
		{
			name:       "invalid plan subcommand shows help",
			args:       []string{"plan", "invalid-subcommand"},
			shouldFail: false, // Cobra shows help with exit 0
			contains:   "Available Commands",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testutil.RunCommand(t, tt.args...)

			if tt.shouldFail {
				testutil.AssertFailure(t, result)
			} else {
				testutil.AssertSuccess(t, result)
			}

			// Verify expected content is present
			combined := result.Stdout + result.Stderr
			if !strings.Contains(combined, tt.contains) {
				t.Errorf("Output missing %q\nStdout:\n%s\nStderr:\n%s",
					tt.contains, result.Stdout, result.Stderr)
			}
		})
	}
}
