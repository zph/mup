package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const (
	supervisordVersion = "v1.0.0"
	supervisordBaseURL = "https://github.com/zph/supervisord/releases/download"
)

// GetSupervisordBinary downloads and caches the supervisord binary from zph/supervisord releases
// Returns the path to the binary
func GetSupervisordBinary(cacheDir string) (string, error) {
	platform := runtime.GOOS
	arch := runtime.GOARCH

	// Map Go platform/arch to release names
	var releaseArch string
	switch {
	case platform == "darwin" && arch == "arm64":
		releaseArch = "Darwin_arm64"
	case platform == "darwin" && arch == "amd64":
		releaseArch = "Darwin_x86_64"
	case platform == "linux" && arch == "amd64":
		releaseArch = "Linux_x86_64"
	case platform == "linux" && arch == "arm64":
		releaseArch = "Linux_arm64"
	default:
		return "", fmt.Errorf("unsupported platform/architecture: %s/%s", platform, arch)
	}

	// Create cache directory
	binDir := filepath.Join(cacheDir, "supervisor", supervisordVersion, fmt.Sprintf("%s-%s", platform, arch))
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	binaryPath := filepath.Join(binDir, "supervisord")

	// Check if already cached
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	// Download from zph/supervisord releases
	// Format: supervisord_1.0.0-SNAPSHOT-908c0d1_Darwin_arm64.tar.gz
	filename := fmt.Sprintf("supervisord_1.0.0-SNAPSHOT-908c0d1_%s.tar.gz", releaseArch)
	url := fmt.Sprintf("%s/%s/%s", supervisordBaseURL, supervisordVersion, filename)

	fmt.Printf("Downloading supervisord %s for %s/%s...\n", supervisordVersion, platform, arch)

	// Download the archive
	tmpFile, err := os.CreateTemp("", "supervisord-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	cmd := exec.Command("curl", "-fsSL", url, "-o", tmpFile.Name())
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to download supervisord from %s: %w", url, err)
	}

	// Extract the binary (with --strip-components to remove directory prefix)
	extractCmd := exec.Command("tar", "-xzf", tmpFile.Name(), "-C", binDir, "--strip-components=1")
	if err := extractCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to extract supervisord: %w", err)
	}

	// Verify binary was extracted
	if _, err := os.Stat(binaryPath); err != nil {
		return "", fmt.Errorf("supervisord binary not found after extraction: %w", err)
	}

	// Make executable
	if err := os.Chmod(binaryPath, 0755); err != nil {
		return "", fmt.Errorf("failed to make binary executable: %w", err)
	}

	fmt.Printf("  ✓ supervisord cached at %s\n", binaryPath)
	return binaryPath, nil
}
