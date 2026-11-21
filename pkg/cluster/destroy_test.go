package cluster

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/paths"
)

const (
	testClusterName = "destroy-test"
	testDataFile    = "WiredTiger"
)

type destroyFixture struct {
	mgr         *Manager
	clusterDir  string
	clusterData string
	outsideDir  string
}

// newDestroyFixture builds a cluster directory under a temporary HOME whose
// metadata node paths point outside the cluster directory, mirroring topology
// values such as `data_dir: /data/mongodb`.
func newDestroyFixture(t *testing.T) *destroyFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	mgr, err := NewManager()
	require.NoError(t, err)

	outsideDir := filepath.Join(t.TempDir(), "unrelated")
	for _, sub := range []string{"data", "logs", "conf"} {
		require.NoError(t, os.MkdirAll(filepath.Join(outsideDir, sub), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(outsideDir, sub, "keep"), []byte("x"), 0o644))
	}

	require.NoError(t, mgr.metaMgr.Save(&meta.ClusterMetadata{
		Name:       testClusterName,
		DeployMode: "local",
		Status:     "stopped",
		Nodes: []meta.NodeMetadata{{
			Type:      "mongod",
			Host:      "127.0.0.1",
			Port:      30000,
			DataDir:   filepath.Join(outsideDir, "data"),
			LogDir:    filepath.Join(outsideDir, "logs"),
			ConfigDir: filepath.Join(outsideDir, "conf"),
		}},
	}))

	clusterDir := mgr.metaMgr.GetClusterDir(testClusterName)
	clusterData := paths.NewClusterLayout(clusterDir).NodeDataDir("127.0.0.1", 30000)
	require.NoError(t, os.MkdirAll(clusterData, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(clusterData, testDataFile), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(clusterDir, "v7.0", "logs"), 0o755))

	return &destroyFixture{mgr: mgr, clusterDir: clusterDir, clusterData: clusterData, outsideDir: outsideDir}
}

func (f *destroyFixture) assertOutsideUntouched(t *testing.T) {
	t.Helper()
	for _, sub := range []string{"data", "logs", "conf"} {
		assert.FileExists(t, filepath.Join(f.outsideDir, sub, "keep"))
	}
}

// LOCAL-001, LOCAL-002
func TestDestroy_RemovesOnlyClusterDir(t *testing.T) {
	f := newDestroyFixture(t)

	require.NoError(t, f.mgr.Destroy(context.Background(), testClusterName, false))

	assert.NoDirExists(t, f.clusterDir)
	f.assertOutsideUntouched(t)
}

// LOCAL-001, LOCAL-003
func TestDestroy_KeepDataPreservesDataDir(t *testing.T) {
	f := newDestroyFixture(t)

	require.NoError(t, f.mgr.Destroy(context.Background(), testClusterName, true))

	assert.FileExists(t, filepath.Join(f.clusterData, testDataFile))
	assert.NoFileExists(t, f.mgr.metaMgr.GetMetaFile(testClusterName))
	assert.NoDirExists(t, filepath.Join(f.clusterDir, "v7.0"))
	f.assertOutsideUntouched(t)

	clusters, err := f.mgr.metaMgr.List()
	require.NoError(t, err)
	assert.NotContains(t, clusters, testClusterName)
}

// LOCAL-040: a deploy that failed before saving metadata can still be destroyed.
func TestDestroy_PartialDeploymentWithoutMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mgr, err := NewManager()
	require.NoError(t, err)

	clusterDir := mgr.metaMgr.GetClusterDir(testClusterName)
	require.NoError(t, os.MkdirAll(filepath.Join(clusterDir, "plans"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(clusterDir, "v7.0.0", "mongod-30000", "log"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(clusterDir, "plans", "p.json"), []byte("{}"), 0o644))

	require.NoError(t, mgr.Destroy(context.Background(), testClusterName, false))

	assert.NoDirExists(t, clusterDir)
}

// LOCAL-040: a name with neither metadata nor a directory is still "not found".
func TestDestroy_UnknownCluster(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mgr, err := NewManager()
	require.NoError(t, err)

	err = mgr.Destroy(context.Background(), "no-such-cluster", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// LOCAL-040
func TestSupervisorDirs(t *testing.T) {
	clusterDir := t.TempDir()
	for _, version := range []string{"v7.0.0", "v7.0.48"} {
		require.NoError(t, os.MkdirAll(filepath.Join(clusterDir, version), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(clusterDir, version, "supervisor.ini"), []byte("[supervisord]\n"), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(clusterDir, "v6.0.0"), 0o755)) // no supervisor config
	require.NoError(t, os.MkdirAll(filepath.Join(clusterDir, "data"), 0o755))

	dirs, err := supervisorDirs(clusterDir)

	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(clusterDir, "v7.0.0"), filepath.Join(clusterDir, "v7.0.48")}, dirs)
}
