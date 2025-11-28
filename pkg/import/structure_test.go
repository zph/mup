package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/executor"
)

// IMP-013: Create version-specific directory structure
func TestCreateVersionDirectories(t *testing.T) {
	// Create temp directory for testing
	tempDir := t.TempDir()

	exec := executor.NewLocalExecutor()
	builder := NewStructureBuilder(exec)

	t.Run("create version-specific directories", func(t *testing.T) {
		clusterDir := filepath.Join(tempDir, "test-cluster")
		version := "7.0.5"

		// IMP-013: Create per-version directory structure
		err := builder.CreateVersionDirectories(clusterDir, version)
		require.NoError(t, err)

		// Verify directories exist
		versionDir := filepath.Join(clusterDir, "v"+version)
		assert.DirExists(t, versionDir)

		// IMP-015: Verify bin/, conf/, logs/ directories
		assert.DirExists(t, filepath.Join(versionDir, "bin"))
		assert.DirExists(t, filepath.Join(versionDir, "conf"))
		assert.DirExists(t, filepath.Join(versionDir, "logs"))
	})
}

// IMP-014: Create symlinks to existing data directories
func TestCreateDataSymlinks(t *testing.T) {
	tempDir := t.TempDir()

	exec := executor.NewLocalExecutor()
	builder := NewStructureBuilder(exec)

	t.Run("create symlink to existing data directory", func(t *testing.T) {
		clusterDir := filepath.Join(tempDir, "test-cluster")
		dataDir := filepath.Join(clusterDir, "data")

		// Create cluster directory
		require.NoError(t, os.MkdirAll(clusterDir, 0755))

		// Create existing data directory to symlink to
		existingDataDir := filepath.Join(tempDir, "existing-mongodb-data")
		require.NoError(t, os.MkdirAll(existingDataDir, 0755))

		// Create a test file in existing data dir
		testFile := filepath.Join(existingDataDir, "test.db")
		require.NoError(t, os.WriteFile(testFile, []byte("test"), 0644))

		// IMP-014: Create symlink (no data movement)
		nodeID := "localhost-27017"
		err := builder.CreateDataSymlink(dataDir, nodeID, existingDataDir)
		require.NoError(t, err)

		// Verify symlink exists
		symlinkPath := filepath.Join(dataDir, nodeID)
		assert.FileExists(t, symlinkPath)

		// Verify it's actually a symlink
		info, err := os.Lstat(symlinkPath)
		require.NoError(t, err)
		assert.NotEqual(t, 0, info.Mode()&os.ModeSymlink, "Should be a symlink")

		// Verify symlink points to correct location
		target, err := os.Readlink(symlinkPath)
		require.NoError(t, err)
		assert.Equal(t, existingDataDir, target)

		// Verify we can access files through symlink
		testFileViaSymlink := filepath.Join(symlinkPath, "test.db")
		content, err := os.ReadFile(testFileViaSymlink)
		require.NoError(t, err)
		assert.Equal(t, "test", string(content))
	})

	t.Run("create multiple data symlinks", func(t *testing.T) {
		clusterDir := filepath.Join(tempDir, "test-cluster-multi")
		dataDir := filepath.Join(clusterDir, "data")

		require.NoError(t, os.MkdirAll(clusterDir, 0755))

		// Create multiple existing data directories
		nodes := []struct {
			nodeID      string
			existingDir string
		}{
			{"localhost-27017", filepath.Join(tempDir, "data1")},
			{"localhost-27018", filepath.Join(tempDir, "data2")},
			{"localhost-27019", filepath.Join(tempDir, "data3")},
		}

		for _, node := range nodes {
			require.NoError(t, os.MkdirAll(node.existingDir, 0755))

			err := builder.CreateDataSymlink(dataDir, node.nodeID, node.existingDir)
			require.NoError(t, err)

			symlinkPath := filepath.Join(dataDir, node.nodeID)
			assert.FileExists(t, symlinkPath)
		}

		// Verify all symlinks exist
		for _, node := range nodes {
			symlinkPath := filepath.Join(dataDir, node.nodeID)
			target, err := os.Readlink(symlinkPath)
			require.NoError(t, err)
			assert.Equal(t, node.existingDir, target)
		}
	})
}

// IMP-016: Create 'current' symlink
func TestCreateCurrentSymlink(t *testing.T) {
	tempDir := t.TempDir()

	exec := executor.NewLocalExecutor()
	builder := NewStructureBuilder(exec)

	t.Run("create current symlink to version directory", func(t *testing.T) {
		clusterDir := filepath.Join(tempDir, "test-cluster")
		version := "7.0.5"
		versionDir := filepath.Join(clusterDir, "v"+version)

		// Create version directory
		require.NoError(t, os.MkdirAll(versionDir, 0755))

		// IMP-016: Create 'current' symlink
		err := builder.CreateCurrentSymlink(clusterDir, version)
		require.NoError(t, err)

		// Verify symlink exists
		currentLink := filepath.Join(clusterDir, "current")
		assert.FileExists(t, currentLink)

		// Verify it's a symlink
		info, err := os.Lstat(currentLink)
		require.NoError(t, err)
		assert.NotEqual(t, 0, info.Mode()&os.ModeSymlink)

		// Verify symlink points to version directory
		target, err := os.Readlink(currentLink)
		require.NoError(t, err)
		assert.Equal(t, "v"+version, target)
	})
}

// Test complete directory structure setup
func TestSetupCompleteStructure(t *testing.T) {
	tempDir := t.TempDir()

	exec := executor.NewLocalExecutor()
	builder := NewStructureBuilder(exec)

	t.Run("setup complete import structure", func(t *testing.T) {
		clusterName := "test-cluster"
		clusterDir := filepath.Join(tempDir, clusterName)
		version := "7.0.5"

		// Create existing data directories
		existingDataDirs := map[string]string{
			"localhost-27017": filepath.Join(tempDir, "mongo-data-1"),
			"localhost-27018": filepath.Join(tempDir, "mongo-data-2"),
			"localhost-27019": filepath.Join(tempDir, "mongo-data-3"),
		}

		for _, dir := range existingDataDirs {
			require.NoError(t, os.MkdirAll(dir, 0755))
		}

		// Setup complete structure
		setupConfig := StructureSetupConfig{
			ClusterDir:       clusterDir,
			Version:          version,
			ExistingDataDirs: existingDataDirs,
		}

		err := builder.SetupImportStructure(setupConfig)
		require.NoError(t, err)

		// Verify all components
		versionDir := filepath.Join(clusterDir, "v"+version)

		// Version directories
		assert.DirExists(t, versionDir)
		assert.DirExists(t, filepath.Join(versionDir, "bin"))
		assert.DirExists(t, filepath.Join(versionDir, "conf"))
		assert.DirExists(t, filepath.Join(versionDir, "logs"))

		// Data symlinks
		dataDir := filepath.Join(clusterDir, "data")
		for nodeID, existingDir := range existingDataDirs {
			symlinkPath := filepath.Join(dataDir, nodeID)
			target, err := os.Readlink(symlinkPath)
			require.NoError(t, err)
			assert.Equal(t, existingDir, target)
		}

		// Current symlink
		currentLink := filepath.Join(clusterDir, "current")
		target, err := os.Readlink(currentLink)
		require.NoError(t, err)
		assert.Equal(t, "v"+version, target)
	})
}

// Test handling of existing directories
func TestHandleExistingDirectories(t *testing.T) {
	tempDir := t.TempDir()

	exec := executor.NewLocalExecutor()
	builder := NewStructureBuilder(exec)

	t.Run("handle existing version directory", func(t *testing.T) {
		clusterDir := filepath.Join(tempDir, "test-cluster")
		version := "7.0.5"
		versionDir := filepath.Join(clusterDir, "v"+version)

		// Create version directory that already exists
		require.NoError(t, os.MkdirAll(versionDir, 0755))

		// Create a file in it
		existingFile := filepath.Join(versionDir, "existing.txt")
		require.NoError(t, os.WriteFile(existingFile, []byte("existing"), 0644))

		// Should handle existing directory gracefully
		err := builder.CreateVersionDirectories(clusterDir, version)
		require.NoError(t, err)

		// Existing file should still be there
		assert.FileExists(t, existingFile)
	})

	t.Run("skip symlink when target and source are the same", func(t *testing.T) {
		clusterDir := filepath.Join(tempDir, "test-cluster-same")
		dataDir := filepath.Join(clusterDir, "data")
		nodeID := "localhost-27017"

		// Create the exact path that would be the symlink
		symlinkPath := filepath.Join(dataDir, nodeID)
		require.NoError(t, os.MkdirAll(symlinkPath, 0755))

		// Create a test file in it
		testFile := filepath.Join(symlinkPath, "test.db")
		require.NoError(t, os.WriteFile(testFile, []byte("test"), 0644))

		// Try to create symlink to the same location
		// This should be skipped gracefully without error
		err := builder.CreateDataSymlink(dataDir, nodeID, symlinkPath)
		require.NoError(t, err)

		// Verify the directory still exists (not replaced with symlink)
		info, err := os.Lstat(symlinkPath)
		require.NoError(t, err)
		assert.True(t, info.IsDir(), "Should remain a directory, not become a symlink")

		// Verify test file is still accessible
		content, err := os.ReadFile(testFile)
		require.NoError(t, err)
		assert.Equal(t, "test", string(content))
	})
}
