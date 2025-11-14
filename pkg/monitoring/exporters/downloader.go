package exporters

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

const (
	NodeExporterVersion    = "1.7.0"
	MongoDBExporterVersion = "0.40.0"
)

// Downloader handles downloading and caching exporter binaries
type Downloader struct {
	cacheDir string
}

// NewDownloader creates a new exporter downloader
func NewDownloader(cacheDir string) (*Downloader, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &Downloader{
		cacheDir: cacheDir,
	}, nil
}

// DownloadNodeExporter downloads node_exporter binary
func (d *Downloader) DownloadNodeExporter(ctx context.Context, version string) (string, error) {
	exporterDir := filepath.Join(d.cacheDir, "node_exporter", "versions", version)
	binaryPath := filepath.Join(exporterDir, "node_exporter")

	// Check if already downloaded
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	// Create directory
	if err := os.MkdirAll(exporterDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create exporter directory: %w", err)
	}

	// Download and extract
	osName := runtime.GOOS
	arch := runtime.GOARCH

	// Map Go arch to node_exporter naming
	if arch == "amd64" {
		arch = "amd64"
	} else if arch == "arm64" {
		arch = "arm64"
	}

	url := fmt.Sprintf(
		"https://github.com/prometheus/node_exporter/releases/download/v%s/node_exporter-%s.%s-%s.tar.gz",
		version, version, osName, arch,
	)

	if err := d.downloadAndExtract(ctx, url, exporterDir, fmt.Sprintf("node_exporter-%s.%s-%s/node_exporter", version, osName, arch)); err != nil {
		return "", fmt.Errorf("failed to download node_exporter: %w", err)
	}

	// Make binary executable
	if err := os.Chmod(binaryPath, 0755); err != nil {
		return "", fmt.Errorf("failed to make binary executable: %w", err)
	}

	return binaryPath, nil
}

// DownloadMongoDBExporter downloads mongodb_exporter binary
func (d *Downloader) DownloadMongoDBExporter(ctx context.Context, version string) (string, error) {
	exporterDir := filepath.Join(d.cacheDir, "mongodb_exporter", "versions", version)
	binaryPath := filepath.Join(exporterDir, "mongodb_exporter")

	// Check if already downloaded
	if _, err := os.Stat(binaryPath); err == nil {
		return binaryPath, nil
	}

	// Create directory
	if err := os.MkdirAll(exporterDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create exporter directory: %w", err)
	}

	// Download and extract
	osName := runtime.GOOS
	arch := runtime.GOARCH

	// Map Go arch to mongodb_exporter naming
	if arch == "amd64" {
		arch = "amd64"
	} else if arch == "arm64" {
		arch = "arm64"
	}

	url := fmt.Sprintf(
		"https://github.com/percona/mongodb_exporter/releases/download/v%s/mongodb_exporter-%s.%s-%s.tar.gz",
		version, version, osName, arch,
	)

	if err := d.downloadAndExtract(ctx, url, exporterDir, fmt.Sprintf("mongodb_exporter-%s.%s-%s/mongodb_exporter", version, osName, arch)); err != nil {
		return "", fmt.Errorf("failed to download mongodb_exporter: %w", err)
	}

	// Make binary executable
	if err := os.Chmod(binaryPath, 0755); err != nil {
		return "", fmt.Errorf("failed to make binary executable: %w", err)
	}

	return binaryPath, nil
}

// downloadAndExtract downloads a tar.gz file and extracts a specific binary
func (d *Downloader) downloadAndExtract(ctx context.Context, url, destDir, binaryPathInArchive string) error {
	// Download file
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	// Extract tar.gz
	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	// Find and extract the binary
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		if header.Name == binaryPathInArchive {
			// Extract this file
			binaryName := filepath.Base(binaryPathInArchive)
			destPath := filepath.Join(destDir, binaryName)

			outFile, err := os.Create(destPath)
			if err != nil {
				return fmt.Errorf("failed to create output file: %w", err)
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, tarReader); err != nil {
				return fmt.Errorf("failed to write binary: %w", err)
			}

			return nil
		}
	}

	return fmt.Errorf("binary not found in archive: %s", binaryPathInArchive)
}

// GetNodeExporterPath returns the path to node_exporter binary
func (d *Downloader) GetNodeExporterPath(version string) string {
	return filepath.Join(d.cacheDir, "node_exporter", "versions", version, "node_exporter")
}

// GetMongoDBExporterPath returns the path to mongodb_exporter binary
func (d *Downloader) GetMongoDBExporterPath(version string) string {
	return filepath.Join(d.cacheDir, "mongodb_exporter", "versions", version, "mongodb_exporter")
}
