package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	cacheBinDirName    = "bin"
	cacheServerBinary  = "mongod"
	cacheStagingSuffix = ".staging-"
)

// safeExtractPath joins an archive entry onto the extraction directory and
// rejects entries that would land outside it.
// LOCAL-023
func safeExtractPath(baseDir, entry string) (string, error) {
	if entry == "" || filepath.IsAbs(entry) {
		return "", fmt.Errorf("archive entry %q is not a relative path", entry)
	}

	target := filepath.Join(baseDir, entry)
	rel, err := filepath.Rel(baseDir, target)
	if err != nil {
		return "", fmt.Errorf("archive entry %q cannot be resolved: %w", entry, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes the extraction directory", entry)
	}
	return target, nil
}

// verifySHA256 checks a downloaded file against its published checksum.
// LOCAL-024
func verifySHA256(path, expected string) (err error) {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("failed to hash %s: %w", path, err)
	}

	actual := hex.EncodeToString(hash.Sum(nil))
	if want := strings.ToLower(strings.TrimSpace(expected)); actual != want {
		return fmt.Errorf("checksum mismatch for %s: expected sha256 %s, got %s", filepath.Base(path), want, actual)
	}
	return nil
}

// cacheIsComplete reports whether a package cache directory holds a server
// binary. Caches are published whole (see publishStagedCache), so the server
// binary being present means the package is usable.
func cacheIsComplete(cacheDir string) bool {
	_, err := os.Stat(filepath.Join(cacheDir, cacheBinDirName, cacheServerBinary))
	return err == nil
}

// newCacheStaging creates a private directory next to cacheDir in which a
// package is assembled before being published.
func newCacheStaging(cacheDir string) (string, error) {
	parent := filepath.Dir(cacheDir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}
	staging, err := os.MkdirTemp(parent, filepath.Base(cacheDir)+cacheStagingSuffix+"*")
	if err != nil {
		return "", fmt.Errorf("failed to create staging directory: %w", err)
	}
	return staging, nil
}

// publishStagedCache moves a fully assembled package into its cache slot, so
// an interrupted download can never leave a half-populated cache that later
// runs mistake for a complete one.
//
// A complete cache already in the slot is kept: another process published it
// first, and running clusters may be executing its binaries. An incomplete
// one is replaced.
// LOCAL-025
func publishStagedCache(stagingDir, cacheDir string) error {
	if cacheIsComplete(cacheDir) {
		return os.RemoveAll(stagingDir)
	}

	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("failed to clear incomplete cache %s: %w", cacheDir, err)
	}
	if err := os.Rename(stagingDir, cacheDir); err != nil {
		// Lost a race with another process that published in between.
		if cacheIsComplete(cacheDir) {
			return os.RemoveAll(stagingDir)
		}
		return fmt.Errorf("failed to publish cache %s: %w", cacheDir, err)
	}
	return nil
}
