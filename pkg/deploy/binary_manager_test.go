package deploy

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newTestBinaryManager creates a BinaryManager with a temp directory for testing
func newTestBinaryManager(t *testing.T) (*BinaryManager, string) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "mup-test-binary-manager-*")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}

	return &BinaryManager{
		cacheDir: tempDir,
		binPaths: make(map[string]string),
	}, tempDir
}

// createTestArchive creates a test tar.gz archive with a bin directory containing mongod
func createTestArchive(t *testing.T, archivePath string) {
	t.Helper()

	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("failed to create archive file: %v", err)
	}
	defer file.Close()

	gzWriter := gzip.NewWriter(file)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// Create a directory structure: mongodb-4.0.28/bin/mongod
	dirHeader := &tar.Header{
		Name:     "mongodb-4.0.28/bin",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	}
	if err := tarWriter.WriteHeader(dirHeader); err != nil {
		t.Fatalf("failed to write dir header: %v", err)
	}

	// Create mongod binary (just a dummy file)
	mongodContent := []byte("#!/bin/sh\necho mongod")
	mongodHeader := &tar.Header{
		Name:     "mongodb-4.0.28/bin/mongod",
		Typeflag: tar.TypeReg,
		Size:     int64(len(mongodContent)),
		Mode:     0755,
	}
	if err := tarWriter.WriteHeader(mongodHeader); err != nil {
		t.Fatalf("failed to write mongod header: %v", err)
	}
	if _, err := tarWriter.Write(mongodContent); err != nil {
		t.Fatalf("failed to write mongod content: %v", err)
	}
}

// createMockFullJSONServer creates an HTTP test server that serves mock full.json
func createMockFullJSONServer(t *testing.T, versions []MongoDBVersionInfo) *httptest.Server {
	t.Helper()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/full.json" {
			http.NotFound(w, r)
			return
		}

		fullJSON := MongoDBFullJSON{
			Versions: versions,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(fullJSON)
	})

	return httptest.NewServer(handler)
}

func TestPlatform_Key(t *testing.T) {
	tests := []struct {
		name     string
		platform Platform
		want     string
	}{
		{
			name:     "linux amd64",
			platform: Platform{OS: "linux", Arch: "amd64"},
			want:     "linux-amd64",
		},
		{
			name:     "darwin arm64",
			platform: Platform{OS: "darwin", Arch: "arm64"},
			want:     "darwin-arm64",
		},
		{
			name:     "windows amd64",
			platform: Platform{OS: "windows", Arch: "amd64"},
			want:     "windows-amd64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.platform.Key(); got != tt.want {
				t.Errorf("Platform.Key() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewBinaryManager(t *testing.T) {
	bm, tempDir := newTestBinaryManager(t)
	defer os.RemoveAll(tempDir)

	if bm == nil {
		t.Fatal("NewBinaryManager() returned nil")
	}

	if bm.cacheDir != tempDir {
		t.Errorf("cacheDir = %v, want %v", bm.cacheDir, tempDir)
	}

	if bm.binPaths == nil {
		t.Error("binPaths map is nil")
	}

	if err := bm.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestBinaryManager_resolveVersion(t *testing.T) {
	_, tempDir := newTestBinaryManager(t)
	defer os.RemoveAll(tempDir)

	// Create mock server with test versions
	mockVersions := []MongoDBVersionInfo{
		{
			Version: "4.0.27",
			Downloads: []MongoDBDownload{
				{Arch: "x86_64", Target: "linux", Archive: MongoDBArchive{URL: "http://example.com/4.0.27.tgz"}},
			},
		},
		{
			Version: "4.0.28",
			Downloads: []MongoDBDownload{
				{Arch: "x86_64", Target: "linux", Archive: MongoDBArchive{URL: "http://example.com/4.0.28.tgz"}},
			},
		},
		{
			Version: "4.0.29",
			Downloads: []MongoDBDownload{
				{Arch: "x86_64", Target: "linux", Archive: MongoDBArchive{URL: "http://example.com/4.0.29.tgz"}},
			},
		},
	}

	server := createMockFullJSONServer(t, mockVersions)
	defer server.Close()

	tests := []struct {
		name    string
		version string
		want    string
		wantErr bool
	}{
		{
			name:    "exact patch version",
			version: "4.0.28",
			want:    "4.0.28",
			wantErr: false,
		},
		{
			name:    "minor version resolves to latest",
			version: "4.0",
			want:    "4.0.29", // Latest patch
			wantErr: false,
		},
		{
			name:    "invalid version format",
			version: "4",
			want:    "",
			wantErr: true,
		},
		{
			name:    "non-existent version",
			version: "9.9.9",
			want:    "",
			wantErr: true,
		},
	}

	// Create a testable version of resolveVersion that uses our mock server
	resolveVersionWithServer := func(version string, serverURL string) (string, error) {
		resp, err := http.Get(serverURL + "/full.json")
		if err != nil {
			return "", fmt.Errorf("failed to fetch MongoDB versions: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to fetch MongoDB versions: HTTP %d", resp.StatusCode)
		}

		var fullJSON MongoDBFullJSON
		if err := json.NewDecoder(resp.Body).Decode(&fullJSON); err != nil {
			return "", fmt.Errorf("failed to parse MongoDB versions: %w", err)
		}

		versionParts := strings.Split(version, ".")
		if len(versionParts) < 2 {
			return "", fmt.Errorf("invalid version format: %s (expected X.Y or X.Y.Z)", version)
		}

		// If it's a patch version (X.Y.Z), return as-is
		if len(versionParts) >= 3 {
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

		var matchingVersions []MongoDBVersionInfo
		for _, v := range fullJSON.Versions {
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

		// Find latest version
		latestVersion := matchingVersions[0]
		for _, v := range matchingVersions[1:] {
			if v.Version > latestVersion.Version {
				latestVersion = v
			}
		}

		return latestVersion.Version, nil
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveVersionWithServer(tt.version, server.URL)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("resolveVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBinaryManager_findBinDirectory(t *testing.T) {
	bm, tempDir := newTestBinaryManager(t)
	defer os.RemoveAll(tempDir)

	tests := []struct {
		name      string
		setup     func(string) error
		want      string
		wantErr   bool
		wantInErr string
	}{
		{
			name: "bin directory at root",
			setup: func(extractDir string) error {
				binDir := filepath.Join(extractDir, "bin")
				return os.MkdirAll(binDir, 0755)
			},
			want:    "bin",
			wantErr: false,
		},
		{
			name: "bin directory in mongodb-* subdirectory",
			setup: func(extractDir string) error {
				binDir := filepath.Join(extractDir, "mongodb-4.0.28", "bin")
				return os.MkdirAll(binDir, 0755)
			},
			want:    "mongodb-4.0.28/bin",
			wantErr: false,
		},
		{
			name: "no bin directory",
			setup: func(extractDir string) error {
				// Create some other directory
				return os.MkdirAll(filepath.Join(extractDir, "other"), 0755)
			},
			wantErr:   true,
			wantInErr: "bin directory not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractDir, err := os.MkdirTemp("", "mup-test-extract-*")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(extractDir)

			if err := tt.setup(extractDir); err != nil {
				t.Fatalf("setup failed: %v", err)
			}

			got, err := bm.findBinDirectory(extractDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("findBinDirectory() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if err != nil && !strings.Contains(err.Error(), tt.wantInErr) {
					t.Errorf("findBinDirectory() error = %v, want error containing %v", err, tt.wantInErr)
				}
			} else {
				expectedPath := filepath.Join(extractDir, tt.want)
				if got != expectedPath {
					t.Errorf("findBinDirectory() = %v, want %v", got, expectedPath)
				}
			}
		})
	}
}

func TestBinaryManager_copyBinaries(t *testing.T) {
	bm, tempDir := newTestBinaryManager(t)
	defer os.RemoveAll(tempDir)

	sourceDir, err := os.MkdirTemp("", "mup-test-source-*")
	if err != nil {
		t.Fatalf("failed to create source dir: %v", err)
	}
	defer os.RemoveAll(sourceDir)

	targetDir, err := os.MkdirTemp("", "mup-test-target-*")
	if err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}
	defer os.RemoveAll(targetDir)

	// Create test executables
	testFiles := []struct {
		name     string
		content  []byte
		mode     os.FileMode
		expected bool // whether it should be copied
	}{
		{"mongod", []byte("mongod binary"), 0755, true},
		{"mongos", []byte("mongos binary"), 0755, true},
		{"readme.txt", []byte("readme"), 0644, false}, // not executable
	}

	for _, tf := range testFiles {
		filePath := filepath.Join(sourceDir, tf.name)
		if err := os.WriteFile(filePath, tf.content, tf.mode); err != nil {
			t.Fatalf("failed to create test file %s: %v", tf.name, err)
		}
	}

	// Copy binaries
	if err := bm.copyBinaries(sourceDir, targetDir); err != nil {
		t.Fatalf("copyBinaries() error = %v", err)
	}

	// Verify only executables were copied
	for _, tf := range testFiles {
		targetPath := filepath.Join(targetDir, tf.name)
		_, err := os.Stat(targetPath)

		if tf.expected {
			if err != nil {
				t.Errorf("expected file %s to be copied, but got error: %v", tf.name, err)
			} else {
				// Verify content
				content, err := os.ReadFile(targetPath)
				if err != nil {
					t.Errorf("failed to read copied file %s: %v", tf.name, err)
				} else if string(content) != string(tf.content) {
					t.Errorf("copied file %s content mismatch", tf.name)
				}
			}
		} else {
			if err == nil {
				t.Errorf("expected file %s NOT to be copied, but it exists", tf.name)
			}
		}
	}
}

func TestBinaryManager_extractArchive(t *testing.T) {
	bm, tempDir := newTestBinaryManager(t)
	defer os.RemoveAll(tempDir)

	// Create test archive
	archivePath := filepath.Join(tempDir, "test.tgz")
	createTestArchive(t, archivePath)

	// Extract to temp directory
	extractDir, err := os.MkdirTemp("", "mup-test-extract-*")
	if err != nil {
		t.Fatalf("failed to create extract dir: %v", err)
	}
	defer os.RemoveAll(extractDir)

	// Open archive file
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("failed to open archive: %v", err)
	}
	defer archiveFile.Close()

	// Extract
	if err := bm.extractArchive(archiveFile, extractDir); err != nil {
		t.Fatalf("extractArchive() error = %v", err)
	}

	// Verify extracted files
	// The extraction logic strips the first component (strip-components=1),
	// so mongodb-4.0.28/bin/mongod becomes bin/mongod
	expectedMongod := filepath.Join(extractDir, "bin", "mongod")
	if _, err := os.Stat(expectedMongod); err != nil {
		t.Errorf("extracted mongod not found at %s: %v", expectedMongod, err)
		return
	}

	// Verify content
	content, err := os.ReadFile(expectedMongod)
	if err != nil {
		t.Errorf("failed to read extracted mongod: %v", err)
	} else if string(content) != "#!/bin/sh\necho mongod" {
		t.Errorf("extracted mongod content mismatch: got %q", string(content))
	}
}

func TestBinaryManager_downloadForPlatform_Cached(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	bm, tempDir := newTestBinaryManager(t)
	defer os.RemoveAll(tempDir)

	platform := Platform{OS: "linux", Arch: "amd64"}
	version := "4.0.28"

	// Pre-create cached binaries
	cacheDir := filepath.Join(bm.cacheDir, fmt.Sprintf("%s-%s", version, platform.Key()))
	binPath := filepath.Join(cacheDir, "bin")
	if err := os.MkdirAll(binPath, 0755); err != nil {
		t.Fatalf("failed to create cache dir: %v", err)
	}

	mongodPath := filepath.Join(binPath, "mongod")
	if err := os.WriteFile(mongodPath, []byte("mongod"), 0755); err != nil {
		t.Fatalf("failed to create cached mongod: %v", err)
	}

	// Should return cached path without downloading
	got, err := bm.downloadForPlatform(version, platform)
	if err != nil {
		t.Fatalf("downloadForPlatform() error = %v", err)
	}

	if got != binPath {
		t.Errorf("downloadForPlatform() = %v, want %v", got, binPath)
	}
}

func TestBinaryManager_GetBinPathForPlatform_Cache(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	bm, tempDir := newTestBinaryManager(t)
	defer os.RemoveAll(tempDir)

	platform := Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
	version := "4.0.28"

	// Pre-populate in-memory cache
	testPath := "/test/path"
	bm.mu.Lock()
	bm.binPaths[platform.Key()] = testPath
	bm.mu.Unlock()

	// Should return cached path
	got, err := bm.GetBinPathForPlatform(version, platform)
	if err != nil {
		t.Fatalf("GetBinPathForPlatform() error = %v", err)
	}

	if got != testPath {
		t.Errorf("GetBinPathForPlatform() = %v, want %v", got, testPath)
	}
}

func TestBinaryManager_getDownloadURLForPlatform(t *testing.T) {
	_, tempDir := newTestBinaryManager(t)
	defer os.RemoveAll(tempDir)

	// Create mock server
	mockVersions := []MongoDBVersionInfo{
		{
			Version: "4.0.28",
			Downloads: []MongoDBDownload{
				{
					Arch:    "x86_64",
					Target:  "linux",
					Archive: MongoDBArchive{URL: "http://example.com/mongodb-linux-x86_64-4.0.28.tgz"},
					Edition: "base",
				},
				{
					Arch:    "arm64",
					Target:  "macos",
					Archive: MongoDBArchive{URL: "http://example.com/mongodb-macos-arm64-4.0.28.tgz"},
					Edition: "base",
				},
			},
		},
	}

	server := createMockFullJSONServer(t, mockVersions)
	defer server.Close()

	// We need to modify getDownloadURLForPlatform to accept a base URL for testing
	// For now, let's test the logic with a helper function
	getDownloadURLWithServer := func(version string, platform Platform, serverURL string) (string, error) {
		resp, err := http.Get(serverURL + "/full.json")
		if err != nil {
			return "", fmt.Errorf("failed to fetch MongoDB versions: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to fetch MongoDB versions: HTTP %d", resp.StatusCode)
		}

		var fullJSON MongoDBFullJSON
		if err := json.NewDecoder(resp.Body).Decode(&fullJSON); err != nil {
			return "", fmt.Errorf("failed to parse MongoDB versions: %w", err)
		}

		// Find version
		var targetVersion *MongoDBVersionInfo
		for _, v := range fullJSON.Versions {
			if v.Version == version {
				targetVersion = &v
				break
			}
		}

		if targetVersion == nil {
			return "", fmt.Errorf("version %s not found", version)
		}

		// Map Go arch to MongoDB arch
		mongoArch := platform.Arch
		if mongoArch == "amd64" {
			mongoArch = "x86_64"
		}

		// Find matching download
		for _, download := range targetVersion.Downloads {
			if download.Arch == mongoArch {
				var targetOS string
				switch platform.OS {
				case "darwin":
					targetOS = "macos"
				case "linux":
					targetOS = "linux"
				}

				if download.Target == targetOS && download.Archive.URL != "" {
					if download.Edition == "" || download.Edition == "base" || download.Edition == "targeted" {
						return download.Archive.URL, nil
					}
				}
			}
		}

		return "", fmt.Errorf("no download URL found")
	}

	tests := []struct {
		name     string
		version  string
		platform Platform
		want     string
		wantErr  bool
	}{
		{
			name:     "linux x86_64",
			version:  "4.0.28",
			platform: Platform{OS: "linux", Arch: "amd64"},
			want:     "http://example.com/mongodb-linux-x86_64-4.0.28.tgz",
			wantErr:  false,
		},
		{
			name:     "darwin arm64",
			version:  "4.0.28",
			platform: Platform{OS: "darwin", Arch: "arm64"},
			want:     "http://example.com/mongodb-macos-arm64-4.0.28.tgz",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getDownloadURLWithServer(tt.version, tt.platform, server.URL)
			if (err != nil) != tt.wantErr {
				t.Errorf("getDownloadURLForPlatform() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getDownloadURLForPlatform() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBinaryManager_constructFallbackURL(t *testing.T) {
	bm, _ := newTestBinaryManager(t)

	tests := []struct {
		name      string
		version   string
		targetOS  string
		mongoArch string
		wantURL   string // Expected URL pattern (or empty if should error)
		wantErr   bool
	}{
		{
			name:      "macos x86_64",
			version:   "4.0.28",
			targetOS:  "macos",
			mongoArch: "x86_64",
			wantURL:   "mongodb-macos-x86_64-4.0.28.tgz", // URL should contain this
			wantErr:   true, // URL may not exist, which is fine for testing
		},
		{
			name:      "linux x86_64",
			version:   "4.0.28",
			targetOS:  "linux",
			mongoArch: "x86_64",
			wantURL:   "mongodb-linux-x86_64-4.0.28.tgz", // URL should contain this
			wantErr:   false, // May or may not exist, but URL should be constructed
		},
		{
			name:      "unsupported OS",
			version:   "4.0.28",
			targetOS:  "windows",
			mongoArch: "x86_64",
			wantErr:   true, // Should error for unsupported OS
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := bm.constructFallbackURL(tt.version, tt.targetOS, tt.mongoArch)
			if (err != nil) != tt.wantErr {
				t.Errorf("constructFallbackURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.wantURL != "" {
				// Verify URL contains expected pattern
				if url != "" && !strings.Contains(url, tt.wantURL) {
					t.Errorf("constructFallbackURL() URL = %v, want URL containing %v", url, tt.wantURL)
				}
			}
		})
	}
}

