package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeBinary(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755))
}

// LOCAL-023
func TestSafeExtractPath(t *testing.T) {
	base := t.TempDir()

	path, err := safeExtractPath(base, "bin/mongod")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "bin", "mongod"), path)

	for _, rel := range []string{"../escape", "bin/../../escape", "/etc/passwd", ""} {
		_, err := safeExtractPath(base, rel)
		assert.Error(t, err, "path %q must be rejected", rel)
	}
}

// LOCAL-024
func TestVerifySHA256(t *testing.T) {
	file := filepath.Join(t.TempDir(), "archive.tgz")
	content := []byte("archive-bytes")
	require.NoError(t, os.WriteFile(file, content, 0o644))
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])

	require.NoError(t, verifySHA256(file, good))
	require.NoError(t, verifySHA256(file, "  "+good+"\n"), "surrounding whitespace is ignored")

	err := verifySHA256(file, "deadbeef")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")

	require.Error(t, verifySHA256(filepath.Join(t.TempDir(), "missing"), good))
}

// LOCAL-025: a cache directory appears only once it is fully populated.
func TestPublishStagedCache(t *testing.T) {
	t.Run("publishes into an empty slot", func(t *testing.T) {
		root := t.TempDir()
		staging := filepath.Join(root, "pkg.staging-1")
		final := filepath.Join(root, "pkg")
		writeBinary(t, filepath.Join(staging, "bin"), "mongod", "new")

		require.NoError(t, publishStagedCache(staging, final))

		assert.FileExists(t, filepath.Join(final, "bin", "mongod"))
		assert.NoDirExists(t, staging)
	})

	t.Run("replaces an incomplete cache", func(t *testing.T) {
		root := t.TempDir()
		staging := filepath.Join(root, "pkg.staging-1")
		final := filepath.Join(root, "pkg")
		writeBinary(t, filepath.Join(staging, "bin"), "mongod", "new")
		writeBinary(t, filepath.Join(final, "bin"), "mongos", "leftover")

		require.NoError(t, publishStagedCache(staging, final))

		assert.FileExists(t, filepath.Join(final, "bin", "mongod"))
		assert.NoFileExists(t, filepath.Join(final, "bin", "mongos"))
	})

	t.Run("keeps a complete cache that another process published", func(t *testing.T) {
		root := t.TempDir()
		staging := filepath.Join(root, "pkg.staging-1")
		final := filepath.Join(root, "pkg")
		writeBinary(t, filepath.Join(staging, "bin"), "mongod", "new")
		writeBinary(t, filepath.Join(final, "bin"), "mongod", "existing")

		require.NoError(t, publishStagedCache(staging, final))

		content, err := os.ReadFile(filepath.Join(final, "bin", "mongod"))
		require.NoError(t, err)
		assert.Equal(t, "existing", string(content), "binaries a cluster may be running are never replaced")
		assert.NoDirExists(t, staging)
	})
}

// LOCAL-025
func TestCacheIsComplete(t *testing.T) {
	root := t.TempDir()
	assert.False(t, cacheIsComplete(filepath.Join(root, "missing")))

	partial := filepath.Join(root, "partial")
	writeBinary(t, filepath.Join(partial, "bin"), "mongos", "x")
	assert.False(t, cacheIsComplete(partial))

	complete := filepath.Join(root, "complete")
	writeBinary(t, filepath.Join(complete, "bin"), "mongod", "x")
	assert.True(t, cacheIsComplete(complete))
}
