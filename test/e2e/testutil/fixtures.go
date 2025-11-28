package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TempDir creates a temporary directory for testing and ensures cleanup
func TempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "mup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	t.Cleanup(func() {
		os.RemoveAll(dir)
	})

	return dir
}

// WriteFile writes content to a file in the temp directory
func WriteFile(t *testing.T, dir, filename, content string) string {
	t.Helper()

	path := filepath.Join(dir, filename)

	// Create parent directories if needed
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("Failed to create directories for %s: %v", path, err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file %s: %v", path, err)
	}

	return path
}

// ReadFile reads a file and returns its contents
func ReadFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read file %s: %v", path, err)
	}

	return string(content)
}

// FileExists checks if a file exists
func FileExists(t *testing.T, path string) bool {
	t.Helper()

	_, err := os.Stat(path)
	return err == nil
}

// GetFixturePath returns the absolute path to a test fixture file
func GetFixturePath(t *testing.T, name string) string {
	t.Helper()

	// Get path to this file
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Failed to get caller info")
	}

	// Navigate to test/e2e/fixtures
	fixturesDir := filepath.Join(filepath.Dir(filename), "..", "fixtures")
	path := filepath.Join(fixturesDir, name)

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Fixture file not found: %s", path)
	}

	return path
}

// CopyFixture copies a fixture file to the temp directory
func CopyFixture(t *testing.T, fixtureName, destDir string) string {
	t.Helper()

	srcPath := GetFixturePath(t, fixtureName)
	content := ReadFile(t, srcPath)

	return WriteFile(t, destDir, filepath.Base(fixtureName), content)
}
