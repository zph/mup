//go:build integration

package deploy

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	perconaIntegrationSeries = "80"
	perconaIntegrationDistro = "jammy"
)

var (
	elfMagic = []byte{0x7f, 'E', 'L', 'F'}
	// elfMachineX8664 is the little-endian e_machine value for x86-64.
	elfMachineX8664   = []byte{0x3e, 0x00}
	perconaServerDeb  = regexp.MustCompile(`percona-server-mongodb-server_([0-9.]+-[0-9]+)\.` + perconaIntegrationDistro + `_amd64\.deb$`)
	elfMachineOffset  = 18
	minimumBinarySize = int64(1 << 20)
)

// TestPerconaDebFallback_RealRepository downloads real Percona .deb packages
// from the live apt repository, verifies each against the repository index
// and unpacks the server binaries. It needs network access.
// LOCAL-038, LOCAL-041, LOCAL-044
func TestPerconaDebFallback_RealRepository(t *testing.T) {
	storageDir := t.TempDir()
	bm := &BinaryManager{
		cacheDir:   filepath.Join(storageDir, "packages"),
		storageDir: storageDir,
		binPaths:   make(map[string]string),
	}

	// Use whichever version the repository currently lists, so the test does
	// not rot when Percona retires a release.
	index, err := bm.fetchAptIndex("https://repo.percona.com/psmdb-" + perconaIntegrationSeries +
		"/apt/dists/" + perconaIntegrationDistro + "/main/binary-amd64/Packages")
	require.NoError(t, err)
	var versions []string
	for filename := range index {
		if m := perconaServerDeb.FindStringSubmatch(filename); m != nil {
			versions = append(versions, m[1])
		}
	}
	require.NotEmpty(t, versions, "repository index lists no server packages")
	sort.Strings(versions)
	version := versions[len(versions)-1]
	t.Logf("using Percona Server for MongoDB %s", version)

	urls, err := bm.buildPerconaDebURLs(version, Platform{OS: "linux", Arch: "amd64"})
	require.NoError(t, err)
	assert.Contains(t, urls, "server")
	assert.Contains(t, urls, "mongos")
	assert.NotContains(t, urls, "shell", "8.0 uses mup's pinned mongosh, not a shell package")
	for component, url := range urls {
		_, ok := bm.publishedChecksum(url)
		assert.True(t, ok, "no checksum recorded for %s", component)
	}

	binDir := t.TempDir()
	require.NoError(t, bm.downloadAndExtractDebPackages(urls, binDir))

	for _, name := range []string{"mongod", "mongos"} {
		path := filepath.Join(binDir, name)
		info, err := os.Stat(path)
		require.NoError(t, err, "%s was not extracted", name)
		assert.Greater(t, info.Size(), minimumBinarySize, "%s is implausibly small", name)
		assert.NotZero(t, info.Mode()&0o111, "%s is not executable", name)

		header := make([]byte, elfMachineOffset+len(elfMachineX8664))
		f, err := os.Open(path)
		require.NoError(t, err)
		_, err = f.Read(header)
		require.NoError(t, f.Close())
		require.NoError(t, err)
		assert.True(t, bytes.HasPrefix(header, elfMagic), "%s is not an ELF binary", name)
		assert.Equal(t, elfMachineX8664, header[elfMachineOffset:], "%s is not built for x86-64", name)
	}
}
