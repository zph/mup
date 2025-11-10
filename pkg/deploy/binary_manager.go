package deploy

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// Platform represents a target platform for MongoDB binaries
type Platform struct {
	OS   string // "linux", "darwin", "windows"
	Arch string // "amd64", "arm64"
}

// platformKey returns a unique key for the platform
func (p Platform) Key() string {
	return fmt.Sprintf("%s-%s", p.OS, p.Arch)
}

// BinaryManager manages MongoDB binaries for multiple platforms
type BinaryManager struct {
	cacheDir    string            // Base cache directory (~/.mup/storage/packages)
	storageDir  string            // Storage directory (~/.mup/storage)
	binPaths    map[string]string // platformKey -> binPath
	mu          sync.Mutex
	versionJSON *MongoDBFullJSON // Cached version data
	versionMu   sync.Mutex
}

// NewBinaryManager creates a new binary manager
func NewBinaryManager() (*BinaryManager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	storageDir := filepath.Join(homeDir, ".mup", "storage")
	cacheDir := filepath.Join(storageDir, "packages")

	return &BinaryManager{
		cacheDir:   cacheDir,
		storageDir: storageDir,
		binPaths:   make(map[string]string),
	}, nil
}

// Close cleans up the binary manager
func (bm *BinaryManager) Close() error {
	// Nothing to clean up currently
	return nil
}

// getMongoDBVersions fetches or loads cached MongoDB versions
// Caches the full.json file for 24 hours in ~/.mup/storage/mongo-versions.json
func (bm *BinaryManager) getMongoDBVersions() (*MongoDBFullJSON, error) {
	bm.versionMu.Lock()
	defer bm.versionMu.Unlock()

	// Return cached in-memory version if available
	if bm.versionJSON != nil {
		return bm.versionJSON, nil
	}

	// Check for cached file
	cacheFile := filepath.Join(bm.storageDir, "mongo-versions.json")
	cacheMaxAge := 24 * time.Hour

	// Try to load from cache if it exists and is fresh
	if info, err := os.Stat(cacheFile); err == nil {
		age := time.Since(info.ModTime())
		if age < cacheMaxAge {
			// Load from cache
			data, err := os.ReadFile(cacheFile)
			if err == nil {
				var fullJSON MongoDBFullJSON
				if err := json.Unmarshal(data, &fullJSON); err == nil {
					bm.versionJSON = &fullJSON
					return &fullJSON, nil
				}
			}
		}
	}

	// Need to fetch fresh data
	resp, err := http.Get("https://downloads.mongodb.org/full.json")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch MongoDB versions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch MongoDB versions: HTTP %d", resp.StatusCode)
	}

	// Read response
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read MongoDB versions: %w", err)
	}

	// Parse JSON
	var fullJSON MongoDBFullJSON
	if err := json.Unmarshal(data, &fullJSON); err != nil {
		return nil, fmt.Errorf("failed to parse MongoDB versions: %w", err)
	}

	// Cache to disk
	if err := os.MkdirAll(bm.storageDir, 0755); err == nil {
		_ = os.WriteFile(cacheFile, data, 0644)
	}

	// Cache in memory
	bm.versionJSON = &fullJSON

	return &fullJSON, nil
}

// GetBinPathForPlatform gets the bin path for a specific platform
// Downloads and caches binaries for any platform/architecture combination
func (bm *BinaryManager) GetBinPathForPlatform(version string, platform Platform) (string, error) {
	platformKey := platform.Key()

	// Check in-memory cache first
	bm.mu.Lock()
	if cached, ok := bm.binPaths[platformKey]; ok {
		bm.mu.Unlock()
		return cached, nil
	}
	bm.mu.Unlock()

	// Resolve version (X.Y -> latest X.Y.Z)
	resolvedVersion, err := bm.resolveVersion(version)
	if err != nil {
		return "", fmt.Errorf("failed to resolve version: %w", err)
	}

	// Download and cache for this platform
	binPath, err := bm.downloadForPlatform(resolvedVersion, platform)
	if err != nil {
		return "", fmt.Errorf("failed to download for platform %s: %w", platformKey, err)
	}

	bm.mu.Lock()
	bm.binPaths[platformKey] = binPath
	bm.mu.Unlock()

	return binPath, nil
}

// downloadForPlatform downloads MongoDB binaries for a specific platform
func (bm *BinaryManager) downloadForPlatform(version string, platform Platform) (string, error) {
	// Cache location: ~/.mup/storage/packages/{version}-{os}-{arch}/bin
	platformKey := platform.Key()
	cacheDir := filepath.Join(bm.cacheDir, fmt.Sprintf("%s-%s", version, platformKey))
	binPath := filepath.Join(cacheDir, "bin")

	// Check if already cached in storage/packages
	if _, err := os.Stat(binPath); err == nil {
		// Verify mongod exists
		mongodPath := filepath.Join(binPath, "mongod")
		if platform.OS == "windows" {
			mongodPath = filepath.Join(binPath, "mongod.exe")
		}
		if _, err := os.Stat(mongodPath); err == nil {
			// Also ensure shell is available (mongosh for >= 4.0, mongo for < 4.0)
			versionParts := strings.Split(version, ".")
			if len(versionParts) >= 2 {
				majorVersion := versionParts[0]
				if majorVersion >= "4" {
					if err := bm.ensureMongosh(version, platform, binPath); err != nil {
						// Log warning but don't fail - mongosh might not be available for all versions
						fmt.Printf("  Warning: failed to ensure mongosh: %v\n", err)
					}
				} else {
					if err := bm.ensureMongo(version, platform, binPath); err != nil {
						// Log warning but don't fail - mongo might not be available for all versions
						fmt.Printf("  Warning: failed to ensure mongo: %v\n", err)
					}
				}
			}
			fmt.Printf("  ✓ MongoDB %s for %s cached at %s\n", version, platformKey, binPath)
			return binPath, nil
		}
	}

	// Need to download - get download URL
	url, err := bm.getDownloadURLForPlatform(version, platform)
	if err != nil {
		return "", fmt.Errorf("failed to get download URL: %w", err)
	}

	fmt.Printf("  Downloading MongoDB %s for %s from %s...\n", version, platformKey, url)

	// Download the archive
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download: HTTP %d", resp.StatusCode)
	}

	// Create temp directory for extraction
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("mongodb-%s-%s-*", version, platformKey))
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Create temp file for archive
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("mongodb-%s-*.tgz", platformKey))
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Download to temp file
	if _, err := tmpFile.ReadFrom(resp.Body); err != nil {
		return "", fmt.Errorf("failed to download to temp file: %w", err)
	}

	// Reset file pointer
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return "", fmt.Errorf("failed to seek temp file: %w", err)
	}

	// Extract archive
	if err := bm.extractArchive(tmpFile, tempDir); err != nil {
		return "", fmt.Errorf("failed to extract archive: %w", err)
	}

	// Find bin directory in extracted files
	binDir, err := bm.findBinDirectory(tempDir)
	if err != nil {
		return "", fmt.Errorf("failed to find bin directory: %w", err)
	}

	// Create cache directory
	if err := os.MkdirAll(binPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Copy binaries to cache
	if err := bm.copyBinaries(binDir, binPath); err != nil {
		return "", fmt.Errorf("failed to copy binaries: %w", err)
	}

	// Ensure shell is available in the same bin directory
	// mongosh (>= 4.0) or mongo (< 4.0) is not always included in server archives
	versionParts := strings.Split(version, ".")
	if len(versionParts) >= 2 {
		majorVersion := versionParts[0]
		if majorVersion >= "4" {
			// MongoDB >= 4.0: ensure mongosh
			if err := bm.ensureMongosh(version, platform, binPath); err != nil {
				// Log warning but don't fail - mongosh might not be available for all versions
				fmt.Printf("  Warning: failed to ensure mongosh: %v\n", err)
			}
		} else {
			// MongoDB < 4.0: ensure mongo (legacy shell)
			if err := bm.ensureMongo(version, platform, binPath); err != nil {
				// Log warning but don't fail - mongo might not be available for all versions
				fmt.Printf("  Warning: failed to ensure mongo: %v\n", err)
			}
		}
	}

	fmt.Printf("  ✓ MongoDB %s for %s cached at %s\n", version, platformKey, binPath)
	return binPath, nil
}

// MongoDBFullJSON represents the full.json structure from MongoDB
type MongoDBFullJSON struct {
	Versions []MongoDBVersionInfo `json:"versions"`
}

// MongoDBVersionInfo represents version information from full.json
type MongoDBVersionInfo struct {
	Version   string            `json:"version"`
	Downloads []MongoDBDownload `json:"downloads"`
}

// MongoDBDownload represents a download entry in full.json
type MongoDBDownload struct {
	Arch    string         `json:"arch"`
	Target  string         `json:"target"`
	Archive MongoDBArchive `json:"archive"`
	Edition string         `json:"edition"`
}

// MongoDBArchive represents archive information
type MongoDBArchive struct {
	URL string `json:"url"`
}

// getDownloadURLForPlatform gets the download URL for a specific platform using full.json
func (bm *BinaryManager) getDownloadURLForPlatform(version string, platform Platform) (string, error) {
	// Get cached or fetch MongoDB versions
	fullJSON, err := bm.getMongoDBVersions()
	if err != nil {
		return "", err
	}

	// Map Go arch to MongoDB arch
	mongoArch := platform.Arch
	if mongoArch == "amd64" {
		mongoArch = "x86_64"
	}

	// Map Go OS to MongoDB target
	var targetOS string
	switch platform.OS {
	case "darwin":
		targetOS = "osx"
	case "linux":
		targetOS = "linux"
	default:
		return "", fmt.Errorf("unsupported OS: %s", platform.OS)
	}

	// Find the version
	var targetVersion *MongoDBVersionInfo
	versionParts := strings.Split(version, ".")

	if len(versionParts) >= 3 {
		// Exact patch version
		for _, v := range fullJSON.Versions {
			if v.Version == version {
				targetVersion = &v
				break
			}
		}
	} else {
		// Minor version - find latest patch
		majorMinor := versionParts[0] + "." + versionParts[1]
		var matchingVersions []MongoDBVersionInfo
		for _, v := range fullJSON.Versions {
			if strings.HasPrefix(v.Version, majorMinor+".") {
				// Skip RC/pre-release unless explicitly requested
				if !strings.Contains(strings.ToLower(v.Version), "rc") &&
					!strings.Contains(strings.ToLower(v.Version), "alpha") &&
					!strings.Contains(strings.ToLower(v.Version), "beta") {
					matchingVersions = append(matchingVersions, v)
				}
			}
		}
		if len(matchingVersions) > 0 {
			// Find latest version using semver comparison
			latestVersion := matchingVersions[0]
			latestSemver := "v" + latestVersion.Version

			for _, v := range matchingVersions[1:] {
				currentSemver := "v" + v.Version
				if semver.Compare(currentSemver, latestSemver) > 0 {
					latestVersion = v
					latestSemver = currentSemver
				}
			}
			targetVersion = &latestVersion
		}
	}

	if targetVersion == nil {
		return "", fmt.Errorf("version %s not found", version)
	}

	// Find matching download
	for _, download := range targetVersion.Downloads {
		// Check arch match
		if download.Arch != mongoArch {
			continue
		}

		// Check OS match
		matchesOS := false
		if platform.OS == "darwin" {
			matchesOS = download.Target == targetOS ||
				download.Target == "osx" ||
				download.Target == "osx-ssl" ||
				strings.Contains(strings.ToLower(download.Target), "macos") ||
				strings.Contains(strings.ToLower(download.Target), "osx")
		} else if platform.OS == "linux" {
			matchesOS = strings.Contains(strings.ToLower(download.Target), "linux") ||
				strings.Contains(strings.ToLower(download.Target), "ubuntu") ||
				strings.Contains(strings.ToLower(download.Target), "rhel") ||
				strings.Contains(strings.ToLower(download.Target), "debian") ||
				strings.Contains(strings.ToLower(download.Archive.URL), "linux")
		}

		if matchesOS && download.Archive.URL != "" {
			// Prefer community edition (base/targeted) over enterprise
			if download.Edition == "" || download.Edition == "base" || download.Edition == "targeted" {
				return download.Archive.URL, nil
			}
		}
	}

	// Fallback: try to construct URL directly
	return bm.constructFallbackURL(targetVersion.Version, targetOS, mongoArch)
}

// constructFallbackURL constructs a download URL directly as fallback
func (bm *BinaryManager) constructFallbackURL(version, targetOS, mongoArch string) (string, error) {
	var urls []string

	if targetOS == "osx" {
		if mongoArch == "x86_64" {
			urls = []string{
				fmt.Sprintf("https://fastdl.mongodb.org/osx/mongodb-macos-x86_64-%s.tgz", version),
				fmt.Sprintf("https://fastdl.mongodb.org/osx/mongodb-osx-x86_64-%s.tgz", version),
			}
		} else if mongoArch == "arm64" {
			urls = []string{
				fmt.Sprintf("https://fastdl.mongodb.org/osx/mongodb-macos-arm64-%s.tgz", version),
				fmt.Sprintf("https://fastdl.mongodb.org/osx/mongodb-osx-arm64-%s.tgz", version),
			}
		}
	} else if targetOS == "linux" {
		urls = []string{
			fmt.Sprintf("https://fastdl.mongodb.org/linux/mongodb-linux-%s-%s.tgz", mongoArch, version),
		}
	}

	// Try each URL
	for _, url := range urls {
		resp, err := http.Head(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound {
				return url, nil
			}
		}
	}

	platformStr := fmt.Sprintf("%s/%s", targetOS, mongoArch)
	// Provide helpful error message for common incompatibilities
	if targetOS == "osx" && mongoArch == "arm64" {
		return "", fmt.Errorf("no download URL found for MongoDB %s on %s (Apple Silicon requires MongoDB 6.0+)", version, platformStr)
	}
	return "", fmt.Errorf("no download URL found for MongoDB %s on %s", version, platformStr)
}

// resolveVersion resolves a version string to the exact patch version
// If user specified patch version (X.Y.Z), returns it as-is
// If user specified minor version (X.Y), finds and returns the latest patch version
func (bm *BinaryManager) resolveVersion(version string) (string, error) {
	// Get cached or fetch MongoDB versions
	fullJSON, err := bm.getMongoDBVersions()
	if err != nil {
		return "", err
	}

	versionParts := strings.Split(version, ".")
	if len(versionParts) < 2 {
		return "", fmt.Errorf("invalid version format: %s (expected X.Y or X.Y.Z)", version)
	}

	// If it's a patch version (X.Y.Z), return as-is
	if len(versionParts) >= 3 {
		// Verify the exact version exists
		exactVersion := version
		for _, v := range fullJSON.Versions {
			if v.Version == exactVersion {
				return exactVersion, nil
			}
		}
		return "", fmt.Errorf("exact version %s not found", exactVersion)
	}

	// User specified minor version (e.g., "4.0") - find latest patch
	majorMinor := versionParts[0] + "." + versionParts[1]

	// Collect all matching versions and find the latest patch version
	var matchingVersions []MongoDBVersionInfo
	for _, v := range fullJSON.Versions {
		// Skip RC and pre-release versions
		if strings.Contains(strings.ToLower(v.Version), "rc") ||
			strings.Contains(strings.ToLower(v.Version), "alpha") ||
			strings.Contains(strings.ToLower(v.Version), "beta") {
			continue
		}

		if strings.HasPrefix(v.Version, majorMinor+".") {
			matchingVersions = append(matchingVersions, v)
		}
	}

	if len(matchingVersions) == 0 {
		return "", fmt.Errorf("no versions found for MongoDB %s", majorMinor)
	}

	// Use semantic version comparison to find the latest version
	latestVersion := matchingVersions[0]
	latestSemver := "v" + latestVersion.Version

	for _, v := range matchingVersions[1:] {
		currentSemver := "v" + v.Version
		// Compare using semver.Compare (returns -1 if current < latest, 0 if equal, 1 if current > latest)
		if semver.Compare(currentSemver, latestSemver) > 0 {
			latestVersion = v
			latestSemver = currentSemver
		}
	}

	return latestVersion.Version, nil
}

// extractArchive extracts a tar.gz archive
func (bm *BinaryManager) extractArchive(archive *os.File, targetDir string) error {
	// Reset file pointer
	if _, err := archive.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek archive: %w", err)
	}

	// Create gzip reader
	gzReader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzReader)

	// Extract files
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar archive: %w", err)
		}

		// Skip the root directory (strip-components=1)
		parts := strings.Split(header.Name, "/")
		if len(parts) <= 1 {
			continue
		}
		relPath := filepath.Join(parts[1:]...)
		targetPath := filepath.Join(targetDir, relPath)

		switch header.Typeflag {
		case tar.TypeDir:
			// Create directory
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		case tar.TypeReg:
			// Create file
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory: %w", err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}

			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return fmt.Errorf("failed to write file: %w", err)
			}
			outFile.Close()
		}
	}

	return nil
}

// extractZipArchive extracts a zip archive
func (bm *BinaryManager) extractZipArchive(archive *os.File, targetDir string) error {
	// Reset file pointer
	if _, err := archive.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek archive: %w", err)
	}

	// Get file info to determine size
	info, err := archive.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat archive: %w", err)
	}

	// Open zip reader
	zipReader, err := zip.NewReader(archive, info.Size())
	if err != nil {
		return fmt.Errorf("failed to create zip reader: %w", err)
	}

	// Extract files
	for _, file := range zipReader.File {
		// Skip the root directory (strip-components=1)
		parts := strings.Split(file.Name, "/")
		if len(parts) <= 1 {
			continue
		}
		relPath := filepath.Join(parts[1:]...)
		targetPath := filepath.Join(targetDir, relPath)

		if file.FileInfo().IsDir() {
			// Create directory
			if err := os.MkdirAll(targetPath, file.FileInfo().Mode()); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

		// Create parent directory
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory: %w", err)
		}

		// Open file in zip
		rc, err := file.Open()
		if err != nil {
			return fmt.Errorf("failed to open file in zip: %w", err)
		}

		// Create target file
		outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.FileInfo().Mode())
		if err != nil {
			rc.Close()
			return fmt.Errorf("failed to create file: %w", err)
		}

		// Copy file contents
		if _, err := io.Copy(outFile, rc); err != nil {
			outFile.Close()
			rc.Close()
			return fmt.Errorf("failed to write file: %w", err)
		}

		outFile.Close()
		rc.Close()
	}

	return nil
}

// findBinDirectory finds the bin directory in extracted files
func (bm *BinaryManager) findBinDirectory(extractDir string) (string, error) {
	// Look for bin directory
	directBinPath := filepath.Join(extractDir, "bin")
	if _, err := os.Stat(directBinPath); err == nil {
		return directBinPath, nil
	}

	// Look in mongodb-* subdirectories
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "mongodb-") {
			potentialBinPath := filepath.Join(extractDir, entry.Name(), "bin")
			if _, err := os.Stat(potentialBinPath); err == nil {
				return potentialBinPath, nil
			}
		}
	}

	return "", fmt.Errorf("bin directory not found in extracted archive")
}

// copyBinaries copies all executable files from source to target
func (bm *BinaryManager) copyBinaries(sourceBinDir, targetBinDir string) error {
	entries, err := os.ReadDir(sourceBinDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Check if it's an executable (simplified check)
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Copy executable files
		isExecutable := false
		if runtime.GOOS == "windows" {
			name := entry.Name()
			isExecutable = strings.HasSuffix(strings.ToLower(name), ".exe") ||
				strings.HasSuffix(strings.ToLower(name), ".bat") ||
				strings.HasSuffix(strings.ToLower(name), ".cmd")
		} else {
			isExecutable = info.Mode().Perm()&0111 != 0
		}

		if !isExecutable {
			continue
		}

		sourcePath := filepath.Join(sourceBinDir, entry.Name())
		targetPath := filepath.Join(targetBinDir, entry.Name())

		// Read source file
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", sourcePath, err)
		}

		// Write target file
		if err := os.WriteFile(targetPath, data, info.Mode()); err != nil {
			return fmt.Errorf("failed to write %s: %w", targetPath, err)
		}
	}

	return nil
}

// ensureMongosh ensures mongosh is available in the binPath directory
// mongosh is downloaded separately from the server binaries (from GitHub releases)
func (bm *BinaryManager) ensureMongosh(version string, platform Platform, binPath string) error {
	// Check if mongosh already exists
	mongoshPath := filepath.Join(binPath, "mongosh")
	if platform.OS == "windows" {
		mongoshPath = filepath.Join(binPath, "mongosh.exe")
	}
	if _, err := os.Stat(mongoshPath); err == nil {
		// mongosh already exists
		fmt.Printf("  ✓ mongosh already exists at %s\n", mongoshPath)
		return nil
	}

	// mongosh is only available for MongoDB >= 4.0
	// Check version
	versionParts := strings.Split(version, ".")
	if len(versionParts) < 2 {
		return fmt.Errorf("invalid version format for mongosh check")
	}
	majorVersion := versionParts[0]
	if majorVersion < "4" {
		// mongosh not available for versions < 4.0
		return nil
	}

	// Get latest mongosh version and download URL
	// mongosh version doesn't need to match MongoDB server version
	mongoshVersion, mongoshURL, err := bm.getLatestMongoshDownloadURL(platform)
	if err != nil {
		return fmt.Errorf("failed to get mongosh download URL: %w", err)
	}

	fmt.Printf("  Downloading mongosh %s for %s from %s...\n", mongoshVersion, platform.Key(), mongoshURL)
	// Download mongosh archive
	resp, err := http.Get(mongoshURL)
	if err != nil {
		return fmt.Errorf("failed to download mongosh: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download mongosh: HTTP %d", resp.StatusCode)
	}

	// Create temp directory for extraction
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("mongosh-%s-%s-*", mongoshVersion, platform.Key()))
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Determine file extension based on platform
	var ext string
	if platform.OS == "darwin" {
		ext = "zip"
	} else {
		ext = "tgz"
	}

	// Create temp file for archive
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("mongosh-%s-*.%s", platform.Key(), ext))
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Download to temp file
	if _, err := tmpFile.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("failed to download to temp file: %w", err)
	}

	// Reset file pointer
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek temp file: %w", err)
	}

	// Extract archive (zip for darwin, tgz for linux)
	if platform.OS == "darwin" {
		if err := bm.extractZipArchive(tmpFile, tempDir); err != nil {
			return fmt.Errorf("failed to extract mongosh zip archive: %w", err)
		}
	} else {
		if err := bm.extractArchive(tmpFile, tempDir); err != nil {
			return fmt.Errorf("failed to extract mongosh archive: %w", err)
		}
	}

	// Find mongosh binary in extracted files
	mongoshBin, err := bm.findMongoshBinary(tempDir, platform)
	if err != nil {
		return fmt.Errorf("failed to find mongosh binary: %w", err)
	}

	// Copy mongosh to the same binPath folder as server binaries (mongod, mongos, etc.)
	// This ensures all MongoDB binaries are in the same location
	mongoshTarget := filepath.Join(binPath, filepath.Base(mongoshBin))
	mongoshData, err := os.ReadFile(mongoshBin)
	if err != nil {
		return fmt.Errorf("failed to read mongosh binary: %w", err)
	}

	// Get file mode from source
	info, err := os.Stat(mongoshBin)
	if err != nil {
		return fmt.Errorf("failed to stat mongosh binary: %w", err)
	}

	if err := os.WriteFile(mongoshTarget, mongoshData, info.Mode()); err != nil {
		return fmt.Errorf("failed to write mongosh binary: %w", err)
	}

	fmt.Printf("  ✓ mongosh %s installed at %s\n", mongoshVersion, mongoshTarget)
	return nil
}

// getLatestMongoshDownloadURL gets the mongosh version and download URL from GitHub releases
// Uses a hardcoded version (2.5.9) to avoid GitHub API rate limiting
func (bm *BinaryManager) getLatestMongoshDownloadURL(platform Platform) (string, string, error) {
	// Use hardcoded version to avoid GitHub API rate limiting (403 errors)
	mongoshVersion := "2.5.9"
	tagName := "v" + mongoshVersion

	// Map Go arch to mongosh arch naming
	mongoshArch := platform.Arch
	if mongoshArch == "amd64" {
		mongoshArch = "x64"
	} else if mongoshArch == "arm64" {
		mongoshArch = "arm64"
	}

	// Map Go OS to mongosh OS naming
	var targetOS string
	switch platform.OS {
	case "darwin":
		targetOS = "darwin"
	case "linux":
		targetOS = "linux"
	default:
		return "", "", fmt.Errorf("unsupported OS for mongosh: %s", platform.OS)
	}

	// Construct download URL directly
	// GitHub releases format:
	// - darwin: https://github.com/mongodb-js/mongosh/releases/download/v{version}/mongosh-{version}-{os}-{arch}.zip
	// - linux: https://github.com/mongodb-js/mongosh/releases/download/v{version}/mongosh-{version}-{os}-{arch}.tgz
	var ext string
	if targetOS == "darwin" {
		ext = "zip"
	} else {
		ext = "tgz"
	}

	url := fmt.Sprintf("https://github.com/mongodb-js/mongosh/releases/download/%s/mongosh-%s-%s-%s.%s",
		tagName, mongoshVersion, targetOS, mongoshArch, ext)

	// Verify URL exists before returning
	resp, err := http.Head(url)
	if err != nil {
		return "", "", fmt.Errorf("failed to verify mongosh URL: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMovedPermanently && resp.StatusCode != http.StatusFound {
		return "", "", fmt.Errorf("mongosh download URL not found (HTTP %d): %s", resp.StatusCode, url)
	}

	return mongoshVersion, url, nil
}

// getMongoshDownloadURL gets the download URL for mongosh from GitHub releases (deprecated - use getLatestMongoshDownloadURL)
func (bm *BinaryManager) getMongoshDownloadURL(version string, platform Platform) (string, error) {
	// Map Go arch to mongosh arch naming
	mongoshArch := platform.Arch
	if mongoshArch == "amd64" {
		mongoshArch = "x64"
	} else if mongoshArch == "arm64" {
		mongoshArch = "arm64"
	}

	// Map Go OS to mongosh OS naming
	var targetOS string
	switch platform.OS {
	case "darwin":
		targetOS = "darwin"
	case "linux":
		targetOS = "linux"
	default:
		return "", fmt.Errorf("unsupported OS for mongosh: %s", platform.OS)
	}

	// GitHub releases URL pattern:
	// https://github.com/mongodb-js/mongosh/releases/download/v{version}/mongosh-{version}-{os}-{arch}.tgz
	// Version format: ensure it starts with 'v' for GitHub releases
	githubVersion := version
	if !strings.HasPrefix(githubVersion, "v") {
		githubVersion = "v" + githubVersion
	}

	url := fmt.Sprintf("https://github.com/mongodb-js/mongosh/releases/download/%s/mongosh-%s-%s-%s.tgz",
		githubVersion, version, targetOS, mongoshArch)

	// Verify URL exists
	resp, err := http.Head(url)
	if err != nil {
		return "", fmt.Errorf("failed to verify mongosh URL: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMovedPermanently && resp.StatusCode != http.StatusFound {
		// Try alternative arch naming (x86_64 instead of x64)
		if mongoshArch == "x64" {
			altURL := fmt.Sprintf("https://github.com/mongodb-js/mongosh/releases/download/%s/mongosh-%s-%s-x86_64.tgz",
				githubVersion, version, targetOS)
			// Verify alternative
			altResp, err := http.Head(altURL)
			if err == nil {
				altResp.Body.Close()
				if altResp.StatusCode == http.StatusOK || altResp.StatusCode == http.StatusMovedPermanently || altResp.StatusCode == http.StatusFound {
					return altURL, nil
				}
			}
		}
		return "", fmt.Errorf("mongosh %s not found at GitHub releases (HTTP %d)", version, resp.StatusCode)
	}

	return url, nil
}

// findMongoshBinary finds the mongosh binary in extracted files
func (bm *BinaryManager) findMongoshBinary(extractDir string, platform Platform) (string, error) {
	binaryName := "mongosh"
	if platform.OS == "windows" {
		binaryName = "mongosh.exe"
	}

	// Look directly in extractDir/bin
	directPath := filepath.Join(extractDir, "bin", binaryName)
	if _, err := os.Stat(directPath); err == nil {
		return directPath, nil
	}

	// Look in mongosh-* subdirectories
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "mongosh") {
			potentialPath := filepath.Join(extractDir, entry.Name(), "bin", binaryName)
			if _, err := os.Stat(potentialPath); err == nil {
				return potentialPath, nil
			}
		}
	}

	return "", fmt.Errorf("mongosh binary not found in extracted archive")
}

// ensureMongo ensures mongo (legacy shell) is available in the binPath directory
// mongo is typically included in server archives for versions < 4.0, but we verify it exists
func (bm *BinaryManager) ensureMongo(version string, platform Platform, binPath string) error {
	// Check if mongo already exists
	mongoPath := filepath.Join(binPath, "mongo")
	if platform.OS == "windows" {
		mongoPath = filepath.Join(binPath, "mongo.exe")
	}
	if _, err := os.Stat(mongoPath); err == nil {
		// mongo already exists (likely from server archive)
		return nil
	}

	// mongo should be in the server archive for versions < 4.0
	// If it's not there, it might be a packaging issue
	// For now, we'll just log that it's missing but not fail
	// The connection command will fall back to system PATH
	return fmt.Errorf("mongo binary not found in binPath (expected in server archive for MongoDB < 4.0)")
}

// CollectPlatforms collects all unique platforms from the topology
func (d *Deployer) CollectPlatforms(ctx context.Context) (map[Platform]bool, error) {
	platforms := make(map[Platform]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	hosts := d.topology.GetAllHosts()

	for _, host := range hosts {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			exec := d.executors[h]

			osInfo, err := exec.GetOSInfo()
			if err != nil {
				// Skip if we can't get OS info
				return
			}

			// Map executor OS/Arch to Platform
			platform := Platform{
				OS:   osInfo.OS,
				Arch: osInfo.Arch,
			}

			// Normalize arch names
			if platform.Arch == "x86_64" {
				platform.Arch = "amd64"
			}

			mu.Lock()
			platforms[platform] = true
			mu.Unlock()
		}(host)
	}

	wg.Wait()
	return platforms, nil
}
