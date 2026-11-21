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
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zph/mup/pkg/paths"

	"golang.org/x/mod/semver"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/zph/mup/pkg/mongo"
)

const (
	mongoshVersionFile    = ".mongosh-version"
	mongoshReleaseBaseURL = "https://github.com/mongodb-js/mongosh/releases/download"
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

// GetCurrentPlatform returns the platform for the current runtime environment
func GetCurrentPlatform() Platform {
	return Platform{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}
}

// BinaryManager manages MongoDB binaries for multiple platforms
type BinaryManager struct {
	cacheDir    string            // Base cache directory (~/.mup/storage/packages)
	storageDir  string            // Storage directory (~/.mup/storage)
	binPaths    map[string]string // platformKey -> binPath
	mu          sync.Mutex
	versionJSON *MongoDBFullJSON // Cached version data
	versionMu   sync.Mutex
	httpClient  *http.Client

	// archiveSHA256 maps a download URL to the checksum published for it.
	archiveSHA256 map[string]string

	// mongoshReleaseBaseURL is overridden by tests to serve release archives locally.
	mongoshReleaseBaseURL string

	// mongoshChecksum returns the expected SHA-256 of a mongosh release
	// archive by asset name; nil uses the checksums pinned in pkg/mongo.
	mongoshChecksum func(archiveName string) (string, bool)
}

// NewBinaryManager creates a new binary manager
func NewBinaryManager() (*BinaryManager, error) {
	storageDir, err := paths.StorageDir()
	if err != nil {
		return nil, err
	}
	cacheDir, err := paths.PackagesDir()
	if err != nil {
		return nil, err
	}

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

// downloadTimeout bounds one archive download so a stalled connection cannot
// hang a deploy or upgrade indefinitely.
const downloadTimeout = 30 * time.Minute

// downloadClient returns the HTTP client used for large archive downloads.
func (bm *BinaryManager) downloadClient() *http.Client {
	if bm.httpClient != nil {
		return bm.httpClient
	}
	return &http.Client{Timeout: downloadTimeout}
}

// verifyMongoshArchive checks a downloaded mongosh archive against its
// pinned checksum. An archive with no pinned checksum is refused.
// LOCAL-034
func (bm *BinaryManager) verifyMongoshArchive(archivePath, archiveName string) error {
	lookup := bm.mongoshChecksum
	if lookup == nil {
		lookup = mongo.ShellArchiveSHA256
	}
	expected, ok := lookup(archiveName)
	if !ok {
		return fmt.Errorf("no pinned checksum for %s; refusing to install an unverified shell", archiveName)
	}
	if err := verifySHA256(archivePath, expected); err != nil {
		return err
	}
	fmt.Printf("  ✓ Checksum verified (SHA-256)\n")
	return nil
}

// fetchPerconaChecksum records the checksum Percona publishes beside a
// tarball as <url>.sha256sum. A missing checksum file is not an error: the
// download then proceeds unverified, as before.
// LOCAL-035
func (bm *BinaryManager) fetchPerconaChecksum(tarballURL string) error {
	resp, err := bm.client().Get(tarballURL + perconaChecksumSuffix)
	if err != nil {
		return fmt.Errorf("failed to fetch checksum for %s: %w", path.Base(tarballURL), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		fmt.Printf("  ⚠️  No published checksum for %s; it will not be verified\n", path.Base(tarballURL))
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch checksum for %s: HTTP %d", path.Base(tarballURL), resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, perconaChecksumMaxBytes))
	if err != nil {
		return fmt.Errorf("failed to read checksum for %s: %w", path.Base(tarballURL), err)
	}
	sum, err := parseSHA256SumFile(string(body))
	if err != nil {
		return fmt.Errorf("checksum file for %s is malformed: %w", path.Base(tarballURL), err)
	}
	bm.rememberChecksum(tarballURL, sum)
	return nil
}

// legacyShellMaxMajor is the last major series whose shell is the legacy
// "mongo" binary shipped as a Percona package.
const legacyShellMaxMajor = 4

// usesLegacyShellPackage reports whether a Percona version needs the legacy
// shell .deb. The major version is compared as a number, not as text.
// LOCAL-041
func usesLegacyShellPackage(version string) bool {
	majorText, _, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil {
		return false
	}
	return major <= legacyShellMaxMajor
}

// fetchPerconaDebChecksums records the checksums the Percona apt repository
// publishes for a set of .deb packages. Every package in the repository pool
// is listed in its index (checked across the 3.6 to 8.0 series), so a package
// missing from the index is refused rather than downloaded unverified.
// LOCAL-038
func (bm *BinaryManager) fetchPerconaDebChecksums(debURLs map[string]string, distro string) error {
	indexes := make(map[string]map[string]string) // index URL -> Filename -> SHA256
	for _, debURL := range debURLs {
		indexURL, filename, err := aptIndexLocation(debURL, distro)
		if err != nil {
			return err
		}

		sums, fetched := indexes[indexURL]
		if !fetched {
			sums, err = bm.fetchAptIndex(indexURL)
			if err != nil {
				return err
			}
			indexes[indexURL] = sums
		}

		sum, ok := sums[filename]
		if !ok {
			return fmt.Errorf("%s is not listed in the repository index %s, so it cannot be verified", path.Base(debURL), indexURL)
		}
		bm.rememberChecksum(debURL, sum)
	}
	return nil
}

// fetchAptIndex downloads and parses an apt Packages index. A repository
// without one yields no checksums.
func (bm *BinaryManager) fetchAptIndex(indexURL string) (map[string]string, error) {
	resp, err := bm.client().Get(indexURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch package index %s: %w", indexURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch package index %s: HTTP %d", indexURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, aptIndexMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read package index %s: %w", indexURL, err)
	}
	return parseAptPackagesIndex(string(body)), nil
}

// exactVersionParts is how many dot-separated parts a full X.Y.Z version has.
const exactVersionParts = 3

// isExactVersion reports whether a version names one release (X.Y.Z, with an
// optional build suffix) rather than a series (X.Y) to be resolved.
func isExactVersion(version string) bool {
	return len(strings.Split(version, ".")) >= exactVersionParts
}

// rememberChecksum records the checksum published for a download URL.
func (bm *BinaryManager) rememberChecksum(url, sha256sum string) {
	if sha256sum == "" {
		return
	}
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.archiveSHA256 == nil {
		bm.archiveSHA256 = make(map[string]string)
	}
	bm.archiveSHA256[url] = sha256sum
}

// publishedChecksum returns the checksum recorded for a download URL, if any.
func (bm *BinaryManager) publishedChecksum(url string) (string, bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	sum, ok := bm.archiveSHA256[url]
	return sum, ok
}

func (bm *BinaryManager) client() *http.Client {
	if bm.httpClient != nil {
		return bm.httpClient
	}
	return http.DefaultClient
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

	// LOCAL-043: use the index on disk while it is fresh
	cacheFile := filepath.Join(bm.storageDir, versionIndexFile)
	cached, cachedAt := loadVersionIndex(cacheFile)
	if cached != nil && time.Since(cachedAt) < versionIndexMaxAge {
		bm.versionJSON = cached
		return cached, nil
	}

	fetched, data, err := bm.fetchVersionIndex()
	if err != nil {
		// A stale index still lists every version that existed when it was
		// saved; that beats failing because the network is slow or down.
		if cached != nil {
			fmt.Printf("  ⚠️  Could not refresh the MongoDB version index (%v); using the copy from %s\n",
				err, cachedAt.Format(time.RFC3339))
			bm.versionJSON = cached
			return cached, nil
		}
		return nil, err
	}

	if err := os.MkdirAll(bm.storageDir, 0755); err != nil {
		fmt.Printf("  ⚠️  Could not cache the MongoDB version index: %v\n", err)
	} else if err := atomicWriteFile(cacheFile, data, 0644); err != nil {
		fmt.Printf("  ⚠️  Could not cache the MongoDB version index: %v\n", err)
	}

	bm.versionJSON = fetched
	return fetched, nil
}

const (
	versionIndexFile   = "mongo-versions.json"
	versionIndexURL    = "https://downloads.mongodb.org/full.json"
	versionIndexMaxAge = 24 * time.Hour
)

// loadVersionIndex reads the version index cached on disk and when it was
// saved. It returns nil if there is no usable cache.
func loadVersionIndex(cacheFile string) (*MongoDBFullJSON, time.Time) {
	info, err := os.Stat(cacheFile)
	if err != nil {
		return nil, time.Time{}
	}
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, time.Time{}
	}
	var index MongoDBFullJSON
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, time.Time{}
	}
	return &index, info.ModTime()
}

// fetchVersionIndex downloads the MongoDB version index. The download is
// large (tens of megabytes) and is bounded by the download client's timeout.
// LOCAL-043
func (bm *BinaryManager) fetchVersionIndex() (*MongoDBFullJSON, []byte, error) {
	resp, err := bm.downloadClient().Get(versionIndexURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch MongoDB versions: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("failed to fetch MongoDB versions: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read MongoDB versions: %w", err)
	}

	var index MongoDBFullJSON
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, nil, fmt.Errorf("failed to parse MongoDB versions: %w", err)
	}
	return &index, data, nil
}

// GetBinPathForPlatform gets the bin path for a specific platform
// Downloads and caches binaries for any platform/architecture combination
// Uses VariantMongo by default for backward compatibility
func (bm *BinaryManager) GetBinPathForPlatform(version string, platform Platform) (string, error) {
	return bm.GetBinPathWithVariant(version, VariantMongo, platform)
}

// GetBinPathWithVariant gets the bin path for a specific platform and variant
// [UPG-002] Supports both "mongo" and "percona" variants
// Downloads and caches binaries for any variant/platform/architecture combination
func (bm *BinaryManager) GetBinPathWithVariant(version string, variant Variant, platform Platform) (string, error) {
	platformKey := platform.Key()
	cacheKey := fmt.Sprintf("%s-%s-%s", variant.String(), version, platformKey)

	// Check in-memory cache first
	bm.mu.Lock()
	if cached, ok := bm.binPaths[cacheKey]; ok {
		bm.mu.Unlock()
		return cached, nil
	}
	bm.mu.Unlock()

	// LOCAL-043: an exact version that is already cached needs neither the
	// version index nor a download.
	if isExactVersion(version) {
		cacheDir := filepath.Join(bm.cacheDir, fmt.Sprintf("%s-%s-%s", variant, version, platformKey))
		if cacheIsComplete(cacheDir) {
			binPath := filepath.Join(cacheDir, cacheBinDirName)
			if err := bm.ensureShell(version, platform, binPath); err != nil {
				return "", fmt.Errorf("failed to ensure a compatible MongoDB shell: %w", err)
			}
			fmt.Printf("  ✓ MongoDB %s for %s cached at %s\n", version, platformKey, binPath)
			bm.mu.Lock()
			bm.binPaths[cacheKey] = binPath
			bm.mu.Unlock()
			return binPath, nil
		}
	}

	// Resolve version (X.Y -> latest X.Y.Z) - only for mongo variant
	// Percona versions are used as-is since they have build numbers
	resolvedVersion := version
	if variant == VariantMongo {
		var err error
		resolvedVersion, err = bm.resolveVersion(version)
		if err != nil {
			return "", fmt.Errorf("failed to resolve version: %w", err)
		}
	}

	// Download and cache for this variant/platform
	binPath, err := bm.downloadWithVariant(resolvedVersion, variant, platform)
	if err != nil {
		return "", fmt.Errorf("failed to download %s %s for platform %s: %w", variant, resolvedVersion, platformKey, err)
	}

	bm.mu.Lock()
	bm.binPaths[cacheKey] = binPath
	bm.mu.Unlock()

	return binPath, nil
}

// downloadForPlatform downloads MongoDB binaries for a specific platform
// Uses VariantMongo for backward compatibility
func (bm *BinaryManager) downloadForPlatform(version string, platform Platform) (string, error) {
	return bm.downloadWithVariant(version, VariantMongo, platform)
}

// downloadWithVariant downloads binaries for a specific variant and platform
// [UPG-002] Supports both "mongo" and "percona" variants
func (bm *BinaryManager) downloadWithVariant(version string, variant Variant, platform Platform) (string, error) {
	// Cache location: ~/.mup/storage/packages/{variant}-{version}-{os}-{arch}/bin
	platformKey := platform.Key()
	fullVersion := fmt.Sprintf("%s-%s", variant, version)
	cacheDir := filepath.Join(bm.cacheDir, fmt.Sprintf("%s-%s", fullVersion, platformKey))
	binPath := filepath.Join(cacheDir, "bin")

	// Check if already cached in storage/packages
	if _, err := os.Stat(binPath); err == nil {
		// Verify mongod exists
		mongodPath := filepath.Join(binPath, "mongod")
		if platform.OS == "windows" {
			mongodPath = filepath.Join(binPath, "mongod.exe")
		}
		if _, err := os.Stat(mongodPath); err == nil {
			if err := bm.ensureShell(version, platform, binPath); err != nil {
				return "", fmt.Errorf("failed to ensure a compatible MongoDB shell: %w", err)
			}
			fmt.Printf("  ✓ MongoDB %s for %s cached at %s\n", version, platformKey, binPath)
			return binPath, nil
		}
	}

	// Need to download - get download URL based on variant
	var url string
	var err error
	var useDebPackages bool
	var debURLs map[string]string

	switch variant {
	case VariantMongo:
		url, err = bm.getDownloadURLForPlatform(version, platform)
		if err != nil {
			// For Apple Silicon, fall back to x86_64 (runs via Rosetta)
			if platform.OS == "darwin" && platform.Arch == "arm64" {
				fmt.Printf("  ⚠️  ARM64 binaries not available for MongoDB %s\n", version)
				fmt.Printf("  Falling back to x86_64 (will run via Rosetta)\n")

				// Retry with x86_64
				fallbackPlatform := Platform{OS: "darwin", Arch: "amd64"}
				url, err = bm.getDownloadURLForPlatform(version, fallbackPlatform)
				if err != nil {
					return "", fmt.Errorf("failed to get download URL (tried arm64 and x86_64): %w", err)
				}

				// Update platform for rest of download process
				platform = fallbackPlatform
				// Recalculate cache directory with fallback platform
				platformKey = platform.Key()
				fullVersion := fmt.Sprintf("%s-%s", variant, version)
				cacheDir = filepath.Join(bm.cacheDir, fmt.Sprintf("%s-%s", fullVersion, platformKey))
				binPath = filepath.Join(cacheDir, "bin")

				// Re-check cache for fallback platform
				if _, err := os.Stat(binPath); err == nil {
					mongodPath := filepath.Join(binPath, "mongod")
					if platform.OS == "windows" {
						mongodPath = filepath.Join(binPath, "mongod.exe")
					}
					if _, err := os.Stat(mongodPath); err == nil {
						fmt.Printf("  ✓ MongoDB %s for %s (x86_64) already cached at %s\n", version, platformKey, binPath)
						return binPath, nil
					}
				}
			} else {
				return "", fmt.Errorf("failed to get download URL: %w", err)
			}
		}
	case VariantPercona:
		// Try tarball first
		url, err = bm.buildPerconaURL(version, platform)
		if err == nil {
			// LOCAL-035
			if err := bm.fetchPerconaChecksum(url); err != nil {
				return "", err
			}
		}
		if err != nil {
			// Tarball not found, try .deb packages for Linux
			fmt.Printf("  Tarball not available, trying .deb packages...\n")
			debURLs, err = bm.buildPerconaDebURLs(version, platform)
			if err != nil {
				return "", fmt.Errorf("failed to get Percona binaries: no tarballs or .deb packages available: %w", err)
			}
			useDebPackages = true
		}
	default:
		return "", fmt.Errorf("unknown variant: %s", variant)
	}

	if !useDebPackages {
		fmt.Printf("  Downloading %s %s for %s from %s...\n", variant, version, platformKey, url)
	} else {
		fmt.Printf("  Downloading %s %s for %s from .deb packages...\n", variant, version, platformKey)
	}

	// LOCAL-025: assemble the package in a staging directory and publish it
	// whole, so an interrupted download never leaves a half-populated cache.
	stagingDir, err := newCacheStaging(cacheDir)
	if err != nil {
		return "", err
	}
	published := false
	defer func() {
		if !published {
			if err := os.RemoveAll(stagingDir); err != nil {
				fmt.Printf("Warning: failed to remove staging directory %s: %v\n", stagingDir, err)
			}
		}
	}()

	stagingBin := filepath.Join(stagingDir, cacheBinDirName)
	if err := os.MkdirAll(stagingBin, 0755); err != nil {
		return "", fmt.Errorf("failed to create staging directory: %w", err)
	}

	// Handle .deb packages differently from tarballs
	if useDebPackages {
		// Download and extract .deb packages
		if err := bm.downloadAndExtractDebPackages(debURLs, stagingBin); err != nil {
			return "", fmt.Errorf("failed to download and extract .deb packages: %w", err)
		}
	} else {
		// Download tarball
		resp, err := bm.downloadClient().Get(url)
		if err != nil {
			return "", fmt.Errorf("failed to download: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to download: HTTP %d", resp.StatusCode)
		}

		// Create temp directory for extraction
		tempDir, err := os.MkdirTemp("", fmt.Sprintf("mongodb-%s-%s-*", version, platformKey))
		if err != nil {
			return "", fmt.Errorf("failed to create temp directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(tempDir) }()

		// Create temp file for archive
		tmpFile, err := os.CreateTemp("", fmt.Sprintf("mongodb-%s-*.tgz", platformKey))
		if err != nil {
			return "", fmt.Errorf("failed to create temp file: %w", err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()
		defer func() { _ = tmpFile.Close() }()

		// Download to temp file
		if _, err := tmpFile.ReadFrom(resp.Body); err != nil {
			return "", fmt.Errorf("failed to download to temp file: %w", err)
		}

		// LOCAL-024: verify against the published checksum when there is one
		if expected, ok := bm.publishedChecksum(url); ok {
			if err := verifySHA256(tmpFile.Name(), expected); err != nil {
				return "", err
			}
			fmt.Printf("  ✓ Checksum verified (SHA-256)\n")
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

		// Copy binaries into staging
		if err := bm.copyBinaries(binDir, stagingBin); err != nil {
			return "", fmt.Errorf("failed to copy binaries: %w", err)
		}
	}

	// A package is complete only when it includes the shell selected for the
	// server's wire protocol.
	if err := bm.ensureShell(version, platform, stagingBin); err != nil {
		return "", fmt.Errorf("failed to ensure a compatible MongoDB shell: %w", err)
	}

	if err := publishStagedCache(stagingDir, cacheDir); err != nil {
		return "", err
	}
	published = true

	fmt.Printf("  ✓ %s %s for %s cached at %s\n", cases.Title(language.English).String(variant.String()), version, platformKey, binPath)
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
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
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
		switch platform.OS {
		case "darwin":
			matchesOS = download.Target == targetOS ||
				download.Target == "osx" ||
				download.Target == "osx-ssl" ||
				strings.Contains(strings.ToLower(download.Target), "macos") ||
				strings.Contains(strings.ToLower(download.Target), "osx")
		case "linux":
			matchesOS = strings.Contains(strings.ToLower(download.Target), "linux") ||
				strings.Contains(strings.ToLower(download.Target), "ubuntu") ||
				strings.Contains(strings.ToLower(download.Target), "rhel") ||
				strings.Contains(strings.ToLower(download.Target), "debian") ||
				strings.Contains(strings.ToLower(download.Archive.URL), "linux")
		}

		if matchesOS && download.Archive.URL != "" {
			// Prefer community edition (base/targeted) over enterprise
			if download.Edition == "" || download.Edition == "base" || download.Edition == "targeted" {
				bm.rememberChecksum(download.Archive.URL, download.Archive.SHA256)
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

	switch targetOS {
	case "osx":
		switch mongoArch {
		case "x86_64":
			urls = []string{
				fmt.Sprintf("https://fastdl.mongodb.org/osx/mongodb-macos-x86_64-%s.tgz", version),
				fmt.Sprintf("https://fastdl.mongodb.org/osx/mongodb-osx-x86_64-%s.tgz", version),
			}
		case "arm64":
			urls = []string{
				fmt.Sprintf("https://fastdl.mongodb.org/osx/mongodb-macos-arm64-%s.tgz", version),
				fmt.Sprintf("https://fastdl.mongodb.org/osx/mongodb-osx-arm64-%s.tgz", version),
			}
		}
	case "linux":
		urls = []string{
			fmt.Sprintf("https://fastdl.mongodb.org/linux/mongodb-linux-%s-%s.tgz", mongoArch, version),
		}
	}

	// Try each URL
	for _, url := range urls {
		resp, err := http.Head(url)
		if err == nil {
			_ = resp.Body.Close()
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

// buildPerconaURL constructs a download URL for Percona Server for MongoDB
// [UPG-002] Percona URL format:
// https://downloads.percona.com/downloads/percona-server-mongodb-{major.minor}/percona-server-mongodb-{version}/binary/tarball/percona-server-mongodb-{version}-{platform}-{arch}.tar.gz
func (bm *BinaryManager) buildPerconaURL(version string, platform Platform) (string, error) {
	// Extract major.minor version (e.g., "7.0" from "7.0.5-4")
	versionParts := strings.Split(version, ".")
	if len(versionParts) < 2 {
		return "", fmt.Errorf("invalid Percona version format: %s", version)
	}
	majorMinor := versionParts[0] + "." + versionParts[1]

	// Map Go arch to Percona arch
	perconaArch := platform.Arch
	switch perconaArch {
	case "amd64":
		perconaArch = "x86_64"
	case "arm64":
		perconaArch = "aarch64"
	}

	// Validate OS - Percona only provides Linux binaries
	if platform.OS == "darwin" {
		return "", fmt.Errorf("percona Server for MongoDB does not provide macOS binaries (only Linux)")
	}
	if platform.OS != "linux" {
		return "", fmt.Errorf("unsupported OS for Percona: %s (only Linux is supported)", platform.OS)
	}

	// Try different URL formats based on user example:
	// Example: https://downloads.percona.com/downloads/percona-server-mongodb-8.0/percona-server-mongodb-8.0.12-4/binary/tarball/percona-server-mongodb-8.0.12-4-x86_64.bookworm-minimal.tar.gz
	// Format: percona-server-mongodb-{version}-{arch}.{os}-minimal.tar.gz

	var urlVariants []string

	// Linux OS identifiers (Debian/Ubuntu codenames, RHEL versions)
	osIdentifiers := []string{
		"bookworm", // Debian 12
		"jammy",    // Ubuntu 22.04 LTS
		"focal",    // Ubuntu 20.04 LTS
		"noble",    // Ubuntu 24.04 LTS
		"bullseye", // Debian 11
	}

	// Try OS-specific minimal variants (Linux only)
	if platform.OS == "linux" {
		for _, osID := range osIdentifiers {
			url := fmt.Sprintf(
				"https://downloads.percona.com/downloads/percona-server-mongodb-%s/percona-server-mongodb-%s/binary/tarball/percona-server-mongodb-%s-%s.%s-minimal.tar.gz",
				majorMinor, version, version, perconaArch, osID,
			)
			urlVariants = append(urlVariants, url)
		}

		// Also try generic minimal (without OS)
		genericURL := fmt.Sprintf(
			"https://downloads.percona.com/downloads/percona-server-mongodb-%s/percona-server-mongodb-%s/binary/tarball/percona-server-mongodb-%s-%s-minimal.tar.gz",
			majorMinor, version, version, perconaArch,
		)
		urlVariants = append(urlVariants, genericURL)
	}

	var lastErr error
	for _, url := range urlVariants {
		// Verify URL exists with HEAD request
		resp, err := http.Head(url)
		if err != nil {
			lastErr = err
			continue
		}
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound {
			return url, nil
		}
		lastErr = fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	// No valid URL found for tarballs, return error
	// The caller (downloadWithVariant) will try .deb packages as fallback
	if lastErr != nil {
		return "", fmt.Errorf("no valid Percona tarball URL found for version %s on %s/%s: %w", version, platform.OS, platform.Arch, lastErr)
	}
	return "", fmt.Errorf("no valid Percona tarball URL found for version %s on %s/%s", version, platform.OS, platform.Arch)
}

// buildPerconaDebURL constructs URLs for Percona .deb packages
// [UPG-002] Fallback for older versions without minimal tarballs (6.0, 4.4, 4.2, 4.0, 3.6)
// Format: http://repo.percona.com/psmdb-{major.minor}/apt/pool/main/p/percona-server-mongodb/percona-server-mongodb-{component}_{version}.{distro}_amd64.deb
func (bm *BinaryManager) buildPerconaDebURLs(version string, platform Platform) (map[string]string, error) {
	// Only support Linux for .deb packages
	if platform.OS != "linux" {
		return nil, fmt.Errorf(".deb packages only available for Linux")
	}

	// Only support amd64 for now
	if platform.Arch != "amd64" {
		return nil, fmt.Errorf(".deb packages only available for amd64, not %s", platform.Arch)
	}

	// Extract major.minor version
	versionParts := strings.Split(version, ".")
	if len(versionParts) < 2 {
		return nil, fmt.Errorf("invalid Percona version format: %s", version)
	}
	majorMinor := versionParts[0] + "." + versionParts[1]

	// Percona apt repo uses format without dots: psmdb-60, psmdb-44, etc.
	repoVersion := strings.ReplaceAll(majorMinor, ".", "")

	// Ubuntu/Debian distro codes to try (newest to oldest)
	distros := []string{"noble", "jammy", "focal", "bookworm", "bullseye", "bionic", "xenial", "stretch", "buster"}

	// Special handling for version 3.6 (different package structure)
	is36 := majorMinor == "3.6"
	packagePrefix := "percona-server-mongodb"
	pkgDir := "percona-server-mongodb"

	if is36 {
		// 3.6 uses different directory and package naming
		packagePrefix = "percona-server-mongodb-36"
		pkgDir = "percona-server-mongodb-36"

		// 3.6 versions need ".0" appended (e.g., "3.6.23-13" -> "3.6.23-13.0")
		// Check if version already has the .0 suffix
		if !strings.Contains(version, "-") || !strings.HasSuffix(version, ".0") {
			// Parse version like "3.6.23-13" -> "3.6.23-13.0"
			versionWithSuffix := version
			if strings.Contains(version, "-") && !strings.HasSuffix(version, ".0") {
				versionWithSuffix = version + ".0"
			}
			version = versionWithSuffix
		}
	}

	// Base URL pattern (note: uses psmdb-60, psmdb-44, psmdb-36, etc. without dots in version)
	baseURL := fmt.Sprintf("https://repo.percona.com/psmdb-%s/apt/pool/main/p/%s", repoVersion, pkgDir)

	// Packages we need: server (mongod) and mongos. Only series before 5.0
	// also need a shell package (the legacy mongo shell); for 5.0 and later
	// mup installs its own pinned mongosh afterwards.
	// LOCAL-041
	components := []string{"server", "mongos"}
	if usesLegacyShellPackage(version) {
		components = append(components, "shell")
	}

	// Try each distro until we find one that works
	for _, distro := range distros {
		urls := make(map[string]string)
		allFound := true

		for _, component := range components {
			packageName := fmt.Sprintf("%s-%s_%s.%s_amd64.deb", packagePrefix, component, version, distro)

			url := fmt.Sprintf("%s/%s", baseURL, packageName)

			// Verify URL exists
			resp, err := bm.client().Head(url)
			if err != nil {
				allFound = false
				break
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				allFound = false
				break
			}

			urls[component] = url
		}

		if allFound {
			// LOCAL-038
			if err := bm.fetchPerconaDebChecksums(urls, distro); err != nil {
				return nil, err
			}
			return urls, nil
		}
	}

	return nil, fmt.Errorf("no valid .deb packages found for Percona %s", version)
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

// downloadAndExtractDebPackages downloads and extracts Percona .deb packages
// [UPG-002] Extracts binaries from .deb packages for older Percona versions
func (bm *BinaryManager) downloadAndExtractDebPackages(debURLs map[string]string, targetBinDir string) error {
	// Create temp directory for .deb extraction
	tempDir, err := os.MkdirTemp("", "percona-deb-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Download and extract each .deb package
	for component, url := range debURLs {
		fmt.Printf("  Downloading %s from %s...\n", component, url)

		// Download .deb file
		resp, err := bm.downloadClient().Get(url)
		if err != nil {
			return fmt.Errorf("failed to download %s: %w", component, err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to download %s: HTTP %d", component, resp.StatusCode)
		}

		// Save to temp file
		debFile := filepath.Join(tempDir, fmt.Sprintf("%s.deb", component))
		out, err := os.Create(debFile)
		if err != nil {
			return fmt.Errorf("failed to create temp file: %w", err)
		}

		_, err = io.Copy(out, resp.Body)
		_ = out.Close()
		if err != nil {
			return fmt.Errorf("failed to save .deb file: %w", err)
		}

		// LOCAL-038: every package is verified against the repository index
		expected, ok := bm.publishedChecksum(url)
		if !ok {
			return fmt.Errorf("no published checksum for %s; refusing to install it unverified", path.Base(url))
		}
		if err := verifySHA256(debFile, expected); err != nil {
			return err
		}
		fmt.Printf("  ✓ Checksum verified (SHA-256)\n")

		// Extract .deb package (ar format)
		// .deb contains: debian-binary, control.tar.*, data.tar.*
		// We need data.tar.* which contains the actual binaries
		if err := bm.extractDebPackage(debFile, targetBinDir); err != nil {
			return fmt.Errorf("failed to extract %s: %w", component, err)
		}
	}

	return nil
}

// extractDebPackage extracts binaries from a .deb package
func (bm *BinaryManager) extractDebPackage(debFile, targetBinDir string) error {
	// Create temp directory for this .deb
	tempDir, err := os.MkdirTemp("", "deb-extract-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Extract .deb using ar
	arCmd := exec.Command("ar", "-x", debFile)
	arCmd.Dir = tempDir
	if output, err := arCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ar extraction failed: %w\nOutput: %s", err, output)
	}

	// Find data.tar.* file
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return fmt.Errorf("failed to read temp directory: %w", err)
	}

	var dataTar string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "data.tar") {
			dataTar = filepath.Join(tempDir, entry.Name())
			break
		}
	}

	if dataTar == "" {
		return fmt.Errorf("data.tar.* not found in .deb package")
	}

	// Extract data.tar.* and copy binaries
	// Binaries are typically in ./usr/bin/
	dataExtractDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(dataExtractDir, 0755); err != nil {
		return fmt.Errorf("failed to create data extract directory: %w", err)
	}

	// Open the data tar file
	dataFile, err := os.Open(dataTar)
	if err != nil {
		return fmt.Errorf("failed to open data tar: %w", err)
	}
	defer func() { _ = dataFile.Close() }()

	// Determine compression type and create appropriate reader
	var tarReader *tar.Reader
	if strings.HasSuffix(dataTar, ".gz") {
		gzReader, err := gzip.NewReader(dataFile)
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer func() { _ = gzReader.Close() }()
		tarReader = tar.NewReader(gzReader)
	} else if strings.HasSuffix(dataTar, ".xz") {
		// xz compression - use external xz command
		xzCmd := exec.Command("xz", "-dc", dataTar)
		xzOut, err := xzCmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create xz pipe: %w", err)
		}
		if err := xzCmd.Start(); err != nil {
			return fmt.Errorf("failed to start xz: %w", err)
		}
		defer xzCmd.Wait()
		tarReader = tar.NewReader(xzOut)
	} else {
		tarReader = tar.NewReader(dataFile)
	}

	// Extract files from tar, looking for binaries in usr/bin/
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		// Check if this is a binary we want (mongod, mongos, mongosh, or mongo)
		if header.Typeflag == tar.TypeReg {
			baseName := filepath.Base(header.Name)
			if baseName == "mongod" || baseName == "mongos" || baseName == "mongosh" || baseName == "mongo" {
				// Extract to target bin directory
				targetPath := filepath.Join(targetBinDir, baseName)

				outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
				if err != nil {
					return fmt.Errorf("failed to create file %s: %w", targetPath, err)
				}

				if _, err := io.Copy(outFile, tarReader); err != nil {
					outFile.Close()
					return fmt.Errorf("failed to write file %s: %w", targetPath, err)
				}
				outFile.Close()

				fmt.Printf("  ✓ Extracted %s\n", baseName)
			}
		}
	}

	return nil
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
		if relPath == "" {
			// The archive's root directory entry itself
			continue
		}
		// LOCAL-023: never write outside the extraction directory
		targetPath, err := safeExtractPath(targetDir, relPath)
		if err != nil {
			return err
		}

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
		if relPath == "" {
			// The archive's root directory entry itself
			continue
		}
		// LOCAL-023: never write outside the extraction directory
		targetPath, err := safeExtractPath(targetDir, relPath)
		if err != nil {
			return err
		}

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

func (bm *BinaryManager) ensureShell(version string, platform Platform, binPath string) error {
	compatibility, err := mongo.ResolveShellCompatibility(version)
	if err != nil {
		return err
	}

	switch compatibility.Source {
	case mongo.ShellSourceDownload:
		return bm.ensureMongosh(version, platform, binPath)
	case mongo.ShellSourceServerArchive:
		return bm.ensureMongo(version, platform, binPath)
	default:
		return fmt.Errorf(
			"unsupported shell acquisition source %q for MongoDB %s",
			compatibility.Source, version,
		)
	}
}

// ensureMongosh ensures mongosh is available in the binPath directory
// mongosh is downloaded separately from the server binaries (from GitHub releases)
func (bm *BinaryManager) ensureMongosh(version string, platform Platform, binPath string) error {
	compatibility, err := mongo.ResolveShellCompatibility(version)
	if err != nil {
		return err
	}
	if compatibility.Binary != "mongosh" || compatibility.Source != mongo.ShellSourceDownload {
		return fmt.Errorf(
			"MongoDB %s uses %s from %s, not a separately downloaded mongosh",
			version, compatibility.Binary, compatibility.Source,
		)
	}
	mongoshVersion := compatibility.ShellVersion

	mongoshPath := filepath.Join(binPath, "mongosh")
	if platform.OS == "windows" {
		mongoshPath = filepath.Join(binPath, "mongosh.exe")
	}
	versionPath := filepath.Join(binPath, mongoshVersionFile)

	if _, err := os.Stat(mongoshPath); err == nil {
		installedVersion, readErr := os.ReadFile(versionPath)
		if readErr == nil && strings.TrimSpace(string(installedVersion)) == mongoshVersion {
			fmt.Printf("  ✓ mongosh %s already exists at %s\n", mongoshVersion, mongoshPath)
			return nil
		}

		// Packages created before the version marker existed may contain an
		// incompatible mongosh. Refresh them once instead of trusting the binary.
		fmt.Printf("  Updating mongosh for MongoDB %s to pinned version %s (Node.js driver %s, minimum wire version %d)...\n",
			version, mongoshVersion, compatibility.NodeDriverVersion, compatibility.MinimumWireVersion)
	}

	mongoshURL, err := bm.getMongoshDownloadURL(mongoshVersion, platform)
	if err != nil {
		return fmt.Errorf("failed to get mongosh download URL: %w", err)
	}

	fmt.Printf("  Downloading mongosh %s for %s from %s...\n", mongoshVersion, platform.Key(), mongoshURL)
	// Download mongosh archive
	resp, err := bm.client().Get(mongoshURL)
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

	// LOCAL-034: mongosh versions are pinned, so their archives are too
	if err := bm.verifyMongoshArchive(tmpFile.Name(), path.Base(mongoshURL)); err != nil {
		return err
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

	mongoshData, err := os.ReadFile(mongoshBin)
	if err != nil {
		return fmt.Errorf("failed to read mongosh binary: %w", err)
	}

	// Get file mode from source
	info, err := os.Stat(mongoshBin)
	if err != nil {
		return fmt.Errorf("failed to stat mongosh binary: %w", err)
	}

	// Replace the binary only after the compatible release has been fully
	// downloaded and extracted, preserving a usable cached shell on failure.
	if err := atomicWriteFile(mongoshPath, mongoshData, info.Mode()); err != nil {
		return fmt.Errorf("failed to install mongosh binary: %w", err)
	}
	if err := atomicWriteFile(versionPath, []byte(mongoshVersion+"\n"), 0644); err != nil {
		return fmt.Errorf("failed to record mongosh version: %w", err)
	}

	fmt.Printf("  ✓ mongosh %s installed at %s\n", mongoshVersion, mongoshPath)
	return nil
}

// getMongoshDownloadURL constructs and verifies a pinned mongosh release URL.
func (bm *BinaryManager) getMongoshDownloadURL(mongoshVersion string, platform Platform) (string, error) {
	tagName := "v" + mongoshVersion

	// Map Go arch to mongosh arch naming
	mongoshArch := platform.Arch
	switch mongoshArch {
	case "amd64":
		mongoshArch = "x64"
	case "arm64":
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

	baseURL := bm.mongoshReleaseBaseURL
	if baseURL == "" {
		baseURL = mongoshReleaseBaseURL
	}
	url := fmt.Sprintf("%s/%s/mongosh-%s-%s-%s.%s",
		strings.TrimRight(baseURL, "/"), tagName, mongoshVersion, targetOS, mongoshArch, ext)

	// Verify URL exists before returning
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create mongosh URL verification request: %w", err)
	}
	resp, err := bm.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to verify mongosh URL: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMovedPermanently && resp.StatusCode != http.StatusFound {
		return "", fmt.Errorf("mongosh download URL not found (HTTP %d): %s", resp.StatusCode, url)
	}

	return url, nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	tempFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Chmod(mode); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
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

// ensureMongo ensures the legacy mongo shell bundled with MongoDB 3.6 is
// available in the binPath directory.
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

	return fmt.Errorf(
		"mongo binary not found in binPath (expected in the MongoDB %s server archive)",
		version,
	)
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
