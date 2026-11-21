package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UPGSAFE-009
func TestReconcileWithExistingState(t *testing.T) {
	t.Run("resume takes the target version and FCV choice from state", func(t *testing.T) {
		config := UpgradeConfig{}
		state := &UpgradeState{TargetVersion: "mongo-7.0.5", UpgradeFCV: true}

		require.NoError(t, reconcileWithExistingState(&config, "mongo", state))

		assert.Equal(t, "7.0.5", config.ToVersion)
		assert.True(t, config.UpgradeFCV)
	})

	t.Run("percona versions keep their release suffix", func(t *testing.T) {
		config := UpgradeConfig{}
		state := &UpgradeState{TargetVersion: "percona-7.0.5-4"}

		require.NoError(t, reconcileWithExistingState(&config, "percona", state))

		assert.Equal(t, "7.0.5-4", config.ToVersion)
	})

	t.Run("same target continues the existing upgrade", func(t *testing.T) {
		config := UpgradeConfig{ToVersion: "7.0.5"}
		state := &UpgradeState{TargetVersion: "mongo-7.0.5"}

		require.NoError(t, reconcileWithExistingState(&config, "mongo", state))
	})

	t.Run("different target is refused", func(t *testing.T) {
		config := UpgradeConfig{ToVersion: "7.0.6"}
		state := &UpgradeState{TargetVersion: "mongo-7.0.5", UpgradeID: "upg-1"}

		err := reconcileWithExistingState(&config, "mongo", state)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "7.0.5")
		assert.Contains(t, err.Error(), "7.0.6")
	})

	t.Run("state without a usable target is refused", func(t *testing.T) {
		config := UpgradeConfig{}

		require.Error(t, reconcileWithExistingState(&config, "mongo", &UpgradeState{TargetVersion: "mongo-"}))
		require.Error(t, reconcileWithExistingState(&config, "mongo", &UpgradeState{TargetVersion: "percona-7.0"}))
	})
}

// UPGSAFE-009
func TestCanResumeFrom(t *testing.T) {
	assert.True(t, canResumeFrom(OverallStatusPaused))
	assert.True(t, canResumeFrom(OverallStatusFailed))
	assert.True(t, canResumeFrom(OverallStatusInProgress))
	assert.False(t, canResumeFrom(OverallStatusCompleted))
	assert.False(t, canResumeFrom(OverallStatusRolledBack))
}

// UPGSAFE-010: a binary that is already in place is not rewritten, because a
// node upgraded by an earlier run may be executing it.
func TestInstallBinary_LeavesIdenticalBinaryUntouched(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src-mongod")
	dst := filepath.Join(dir, "dst-mongod")
	require.NoError(t, os.WriteFile(src, []byte("binary-v2"), 0o755))
	require.NoError(t, os.WriteFile(dst, []byte("binary-v2"), 0o755))
	before, err := os.Stat(dst)
	require.NoError(t, err)

	copied, err := installBinary(src, dst)

	require.NoError(t, err)
	assert.False(t, copied)
	after, err := os.Stat(dst)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "destination must not be replaced")
}

// UPGSAFE-010: a missing or different binary is installed by rename, never by
// truncating the destination in place.
func TestInstallBinary_ReplacesByRename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src-mongod")
	dst := filepath.Join(dir, "dst-mongod")
	require.NoError(t, os.WriteFile(src, []byte("binary-v2-longer"), 0o755))
	require.NoError(t, os.WriteFile(dst, []byte("binary-v1"), 0o755))
	before, err := os.Stat(dst)
	require.NoError(t, err)

	copied, err := installBinary(src, dst)

	require.NoError(t, err)
	assert.True(t, copied)
	content, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "binary-v2-longer", string(content))
	after, err := os.Stat(dst)
	require.NoError(t, err)
	assert.False(t, os.SameFile(before, after), "destination must be a new file, not a truncated one")
	assert.Equal(t, os.FileMode(0o755), after.Mode().Perm())

	leftovers, err := filepath.Glob(filepath.Join(dir, "*.tmp*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}
