// Package testutil provides utilities for end-to-end testing of the mup binary.
package testutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	// binaryPath holds the path to the compiled mup binary
	binaryPath string
	// buildOnce ensures we only build the binary once
	buildOnce sync.Once
	// buildErr stores any error from building the binary
	buildErr error
)

// CommandResult holds the result of running a command
type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Combined string
	Duration time.Duration
	Args     []string
}

// Lines returns stdout split into lines (trimmed)
func (r *CommandResult) Lines() []string {
	lines := strings.Split(strings.TrimSpace(r.Stdout), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// StderrLines returns stderr split into lines (trimmed)
func (r *CommandResult) StderrLines() []string {
	lines := strings.Split(strings.TrimSpace(r.Stderr), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// Success returns true if the command exited with code 0
func (r *CommandResult) Success() bool {
	return r.ExitCode == 0
}

// BuildBinary builds the mup binary for testing.
// This is called automatically by RunCommand but can be called explicitly if needed.
// The binary is built only once and reused for all tests in the package.
func BuildBinary(t *testing.T) string {
	t.Helper()

	buildOnce.Do(func() {
		// Get path to this file
		_, filename, _, ok := runtime.Caller(0)
		if !ok {
			buildErr = fmt.Errorf("failed to get caller info")
			return
		}

		// Navigate to project root (3 levels up from test/e2e/testutil/binary.go)
		projectRoot := filepath.Join(filepath.Dir(filename), "..", "..", "..")
		var err error
		projectRoot, err = filepath.Abs(projectRoot)
		if err != nil {
			buildErr = fmt.Errorf("failed to get project root: %w", err)
			return
		}

		// Build binary to test/bin/mup
		binDir := filepath.Join(projectRoot, "test", "bin")
		if err := os.MkdirAll(binDir, 0755); err != nil {
			buildErr = fmt.Errorf("failed to create bin directory: %w", err)
			return
		}

		binaryPath = filepath.Join(binDir, "mup")

		// Build command
		cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/mup")
		cmd.Dir = projectRoot

		// Capture output for debugging
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			buildErr = fmt.Errorf("failed to build binary: %w\nStdout: %s\nStderr: %s",
				err, stdout.String(), stderr.String())
			return
		}

		// Verify binary exists
		if _, err := os.Stat(binaryPath); err != nil {
			buildErr = fmt.Errorf("binary not found after build: %w", err)
			return
		}
	})

	if buildErr != nil {
		t.Fatalf("Failed to build mup binary: %v", buildErr)
	}

	return binaryPath
}

// RunCommand executes the mup binary with the given arguments.
// It automatically builds the binary if needed.
func RunCommand(t *testing.T, args ...string) *CommandResult {
	t.Helper()
	return RunCommandWithEnv(t, nil, args...)
}

// RunCommandWithEnv executes the mup binary with custom environment variables.
func RunCommandWithEnv(t *testing.T, env map[string]string, args ...string) *CommandResult {
	t.Helper()

	binary := BuildBinary(t)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Prepare command
	cmd := exec.CommandContext(ctx, binary, args...)

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Capture output
	var stdout, stderr, combined bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Also write to combined buffer in real-time order (approximately)
	cmd.Stdout = &struct {
		*bytes.Buffer
		combined *bytes.Buffer
	}{&stdout, &combined}
	cmd.Stderr = &struct {
		*bytes.Buffer
		combined *bytes.Buffer
	}{&stderr, &combined}

	// Run command and measure duration
	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	// Determine exit code
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Command failed to start or context timeout
			t.Fatalf("Failed to run command: %v\nArgs: %v\nStdout: %s\nStderr: %s",
				err, args, stdout.String(), stderr.String())
		}
	}

	return &CommandResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Combined: combined.String(),
		Duration: duration,
		Args:     args,
	}
}

// RunCommandWithInput executes the mup binary with the given arguments and stdin input.
func RunCommandWithInput(t *testing.T, input string, args ...string) *CommandResult {
	t.Helper()
	return RunCommandWithEnvAndInput(t, nil, input, args...)
}

// RunCommandWithEnvAndInput executes the mup binary with custom environment variables and stdin input.
func RunCommandWithEnvAndInput(t *testing.T, env map[string]string, input string, args ...string) *CommandResult {
	t.Helper()

	binary := BuildBinary(t)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Prepare command
	cmd := exec.CommandContext(ctx, binary, args...)

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Set stdin
	cmd.Stdin = strings.NewReader(input)

	// Capture output
	var stdout, stderr, combined bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Also write to combined buffer in real-time order (approximately)
	cmd.Stdout = &struct {
		*bytes.Buffer
		combined *bytes.Buffer
	}{&stdout, &combined}
	cmd.Stderr = &struct {
		*bytes.Buffer
		combined *bytes.Buffer
	}{&stderr, &combined}

	// Run command and measure duration
	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	// Determine exit code
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Command failed to start or context timeout
			t.Fatalf("Failed to run command: %v\nArgs: %v\nStdout: %s\nStderr: %s",
				err, args, stdout.String(), stderr.String())
		}
	}

	return &CommandResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Combined: combined.String(),
		Duration: duration,
		Args:     args,
	}
}

// AssertSuccess fails the test if the command did not exit successfully
func AssertSuccess(t *testing.T, result *CommandResult) {
	t.Helper()
	if !result.Success() {
		t.Fatalf("Command failed with exit code %d\nArgs: %v\nStdout:\n%s\nStderr:\n%s",
			result.ExitCode, result.Args, result.Stdout, result.Stderr)
	}
}

// AssertFailure fails the test if the command exited successfully
func AssertFailure(t *testing.T, result *CommandResult) {
	t.Helper()
	if result.Success() {
		t.Fatalf("Command succeeded unexpectedly\nArgs: %v\nStdout:\n%s\nStderr:\n%s",
			result.Args, result.Stdout, result.Stderr)
	}
}

// AssertExitCode fails the test if the command did not exit with the expected code
func AssertExitCode(t *testing.T, result *CommandResult, expectedCode int) {
	t.Helper()
	if result.ExitCode != expectedCode {
		t.Fatalf("Expected exit code %d, got %d\nArgs: %v\nStdout:\n%s\nStderr:\n%s",
			expectedCode, result.ExitCode, result.Args, result.Stdout, result.Stderr)
	}
}

// AssertContains fails the test if the stdout does not contain the expected string
func AssertContains(t *testing.T, result *CommandResult, expected string) {
	t.Helper()
	if !strings.Contains(result.Stdout, expected) {
		t.Fatalf("Stdout does not contain %q\nArgs: %v\nStdout:\n%s",
			expected, result.Args, result.Stdout)
	}
}

// AssertNotContains fails the test if the stdout contains the unexpected string
func AssertNotContains(t *testing.T, result *CommandResult, unexpected string) {
	t.Helper()
	if strings.Contains(result.Stdout, unexpected) {
		t.Fatalf("Stdout unexpectedly contains %q\nArgs: %v\nStdout:\n%s",
			unexpected, result.Args, result.Stdout)
	}
}

// AssertStderrContains fails the test if the stderr does not contain the expected string
func AssertStderrContains(t *testing.T, result *CommandResult, expected string) {
	t.Helper()
	if !strings.Contains(result.Stderr, expected) {
		t.Fatalf("Stderr does not contain %q\nArgs: %v\nStderr:\n%s",
			expected, result.Args, result.Stderr)
	}
}
