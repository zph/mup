package deploy

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zph/mup/pkg/mongo"
)

const testVersionIndex = `{"versions":[{"version":"7.0.0","downloads":[]},{"version":"7.0.5","downloads":[]}]}`

// countingClient fails every request and counts them.
func countingClient(requests *atomic.Int32, respond func(*http.Request) (*http.Response, error)) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return respond(r)
	})}
}

func unreachable(*http.Request) (*http.Response, error) {
	return nil, errors.New("network unreachable")
}

// seedPackageCache creates a complete cached package with the shell mup expects.
func seedPackageCache(t *testing.T, cacheDir, version string, platform Platform) string {
	t.Helper()
	binDir := filepath.Join(cacheDir, "mongo-"+version+"-"+platform.Key(), cacheBinDirName)
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	compat, err := mongo.ResolveShellCompatibility(version)
	require.NoError(t, err)
	for _, name := range []string{cacheServerBinary, compat.Binary} {
		require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte("binary"), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(binDir, mongoshVersionFile), []byte(compat.ShellVersion+"\n"), 0o644))
	return binDir
}

// LOCAL-043: an exact version that is already cached needs no network.
func TestGetBinPath_CachedExactVersionMakesNoRequests(t *testing.T) {
	storageDir := t.TempDir()
	cacheDir := filepath.Join(storageDir, "packages")
	platform := Platform{OS: "linux", Arch: "amd64"}
	wantBin := seedPackageCache(t, cacheDir, "7.0.0", platform)

	var requests atomic.Int32
	bm := &BinaryManager{
		cacheDir:   cacheDir,
		storageDir: storageDir,
		binPaths:   make(map[string]string),
		httpClient: countingClient(&requests, unreachable),
	}

	binPath, err := bm.GetBinPathWithVariant("7.0.0", VariantMongo, platform)

	require.NoError(t, err)
	assert.Equal(t, wantBin, binPath)
	assert.Zero(t, requests.Load(), "a cached exact version must not touch the network")
}

// LOCAL-043: a fresh index on disk is used without a request.
func TestGetMongoDBVersions_UsesFreshCache(t *testing.T) {
	storageDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(storageDir, versionIndexFile), []byte(testVersionIndex), 0o644))

	var requests atomic.Int32
	bm := &BinaryManager{storageDir: storageDir, httpClient: countingClient(&requests, unreachable)}

	index, err := bm.getMongoDBVersions()

	require.NoError(t, err)
	assert.Len(t, index.Versions, 2)
	assert.Zero(t, requests.Load())
}

// LOCAL-043: when the index cannot be fetched, a stale copy is better than failing.
func TestGetMongoDBVersions_FallsBackToStaleCache(t *testing.T) {
	storageDir := t.TempDir()
	cacheFile := filepath.Join(storageDir, versionIndexFile)
	require.NoError(t, os.WriteFile(cacheFile, []byte(testVersionIndex), 0o644))
	stale := time.Now().Add(-2 * versionIndexMaxAge)
	require.NoError(t, os.Chtimes(cacheFile, stale, stale))

	var requests atomic.Int32
	bm := &BinaryManager{storageDir: storageDir, httpClient: countingClient(&requests, unreachable)}

	index, err := bm.getMongoDBVersions()

	require.NoError(t, err)
	assert.Len(t, index.Versions, 2)
	assert.EqualValues(t, 1, requests.Load(), "a stale cache is refreshed first")
}

// LOCAL-043: with no cache at all, a failed fetch is an error.
func TestGetMongoDBVersions_NoCacheAndNoNetworkFails(t *testing.T) {
	var requests atomic.Int32
	bm := &BinaryManager{storageDir: t.TempDir(), httpClient: countingClient(&requests, unreachable)}

	_, err := bm.getMongoDBVersions()

	require.Error(t, err)
}

// LOCAL-043: a fetched index is cached for later runs.
func TestGetMongoDBVersions_FetchesAndCaches(t *testing.T) {
	storageDir := t.TempDir()
	var requests atomic.Int32
	bm := &BinaryManager{storageDir: storageDir, httpClient: countingClient(&requests, func(r *http.Request) (*http.Response, error) {
		return testHTTPResponse(r, http.StatusOK, []byte(testVersionIndex)), nil
	})}

	index, err := bm.getMongoDBVersions()

	require.NoError(t, err)
	assert.Len(t, index.Versions, 2)
	cached, err := os.ReadFile(filepath.Join(storageDir, versionIndexFile))
	require.NoError(t, err)
	assert.JSONEq(t, testVersionIndex, string(cached))
}
