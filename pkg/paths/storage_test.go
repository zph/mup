package paths

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LOCAL-020: MUP_STORAGE_DIR overrides the default storage location.
func TestStorageDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("defaults under home", func(t *testing.T) {
		t.Setenv(EnvStorageDir, "")

		dir, err := StorageDir()

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, ".mup", "storage"), dir)
	})

	t.Run("honours override", func(t *testing.T) {
		override := filepath.Join(t.TempDir(), "custom")
		t.Setenv(EnvStorageDir, override)

		dir, err := StorageDir()
		require.NoError(t, err)
		assert.Equal(t, override, dir)

		clusters, err := ClustersDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(override, "clusters"), clusters)

		clusterDir, err := ClusterDir("c1")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(override, "clusters", "c1"), clusterDir)

		packages, err := PackagesDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(override, "packages"), packages)

		bin, err := BinCacheDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(override, "bin"), bin)
	})

	t.Run("rejects relative override", func(t *testing.T) {
		t.Setenv(EnvStorageDir, "relative/dir")

		_, err := StorageDir()
		require.Error(t, err)
	})
}
