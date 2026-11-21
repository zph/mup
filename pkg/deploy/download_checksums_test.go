package deploy

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTarballURL = "https://downloads.percona.test/psmdb-8.0.12-4-x86_64.jammy-minimal.tar.gz"
	testSHA256     = "20e10f8e810c2d7bb3a6cc47fff2c87cd194ca5d8b3012151440d2116c542927"
)

// LOCAL-035
func TestParseSHA256SumFile(t *testing.T) {
	sum, err := parseSHA256SumFile(testSHA256 + "  psmdb.tar.gz\n")
	require.NoError(t, err)
	assert.Equal(t, testSHA256, sum)

	sum, err = parseSHA256SumFile(strings.ToUpper(testSHA256))
	require.NoError(t, err)
	assert.Equal(t, testSHA256, sum)

	for _, bad := range []string{"", "   \n", "deadbeef  file", strings.Repeat("z", sha256HexLength) + "  file", "<html>Not Found</html>"} {
		_, err := parseSHA256SumFile(bad)
		assert.Error(t, err, "content %q", bad)
	}
}

func checksumServer(status int, body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != testTarballURL+perconaChecksumSuffix {
			return testHTTPResponse(r, http.StatusNotFound, nil), nil
		}
		return testHTTPResponse(r, status, []byte(body)), nil
	})}
}

// LOCAL-035
func TestFetchPerconaChecksum(t *testing.T) {
	t.Run("published checksum is recorded for the tarball", func(t *testing.T) {
		bm := &BinaryManager{httpClient: checksumServer(http.StatusOK, testSHA256+"  psmdb.tar.gz\n")}

		require.NoError(t, bm.fetchPerconaChecksum(testTarballURL))

		sum, ok := bm.publishedChecksum(testTarballURL)
		require.True(t, ok)
		assert.Equal(t, testSHA256, sum)
	})

	t.Run("no checksum file leaves the tarball unverified", func(t *testing.T) {
		bm := &BinaryManager{httpClient: checksumServer(http.StatusNotFound, "")}

		require.NoError(t, bm.fetchPerconaChecksum(testTarballURL))

		_, ok := bm.publishedChecksum(testTarballURL)
		assert.False(t, ok)
	})

	t.Run("a server error is not treated as 'no checksum'", func(t *testing.T) {
		bm := &BinaryManager{httpClient: checksumServer(http.StatusInternalServerError, "")}

		require.Error(t, bm.fetchPerconaChecksum(testTarballURL))
	})

	t.Run("a malformed checksum file is an error", func(t *testing.T) {
		bm := &BinaryManager{httpClient: checksumServer(http.StatusOK, "<html>oops</html>")}

		require.Error(t, bm.fetchPerconaChecksum(testTarballURL))
	})
}

// LOCAL-034
func TestVerifyMongoshArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "mongosh.zip")
	require.NoError(t, os.WriteFile(archive, []byte("not the real archive"), 0o644))

	t.Run("an archive that does not match its pinned checksum is refused", func(t *testing.T) {
		bm := &BinaryManager{}

		err := bm.verifyMongoshArchive(archive, "mongosh-2.5.9-darwin-arm64.zip")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum mismatch")
	})

	t.Run("an archive with no pinned checksum is refused", func(t *testing.T) {
		bm := &BinaryManager{}

		err := bm.verifyMongoshArchive(archive, "mongosh-9.9.9-darwin-arm64.zip")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no pinned checksum")
	})
}

const testAptIndex = `Package: percona-server-mongodb-server
Version: 8.0.38-16.jammy
Architecture: amd64
Filename: pool/main/p/percona-server-mongodb/percona-server-mongodb-server_8.0.38-16.jammy_amd64.deb
Size: 96449068
SHA256: 8d4e6a9822ee4f17a903b38a217d007ab16a1de209e10e68756435c6540d5dab
Description: Percona Server for MongoDB
 multi-line description

Package: percona-server-mongodb-mongos
Version: 8.0.38-16.jammy
SHA256: 1111111111111111111111111111111111111111111111111111111111111111
Filename: pool/main/p/percona-server-mongodb/percona-server-mongodb-mongos_8.0.38-16.jammy_amd64.deb

Package: no-checksum
Filename: pool/main/p/x/no-checksum.deb
`

// LOCAL-038
func TestParseAptPackagesIndex(t *testing.T) {
	sums := parseAptPackagesIndex(testAptIndex)

	assert.Equal(t, map[string]string{
		"pool/main/p/percona-server-mongodb/percona-server-mongodb-server_8.0.38-16.jammy_amd64.deb": "8d4e6a9822ee4f17a903b38a217d007ab16a1de209e10e68756435c6540d5dab",
		"pool/main/p/percona-server-mongodb/percona-server-mongodb-mongos_8.0.38-16.jammy_amd64.deb": "1111111111111111111111111111111111111111111111111111111111111111",
	}, sums)
	assert.Empty(t, parseAptPackagesIndex(""))
}

// LOCAL-038
func TestAptIndexLocation(t *testing.T) {
	const debURL = "https://repo.percona.com/psmdb-80/apt/pool/main/p/percona-server-mongodb/percona-server-mongodb-server_8.0.38-16.jammy_amd64.deb"

	indexURL, filename, err := aptIndexLocation(debURL, "jammy")

	require.NoError(t, err)
	assert.Equal(t, "https://repo.percona.com/psmdb-80/apt/dists/jammy/main/binary-amd64/Packages", indexURL)
	assert.Equal(t, "pool/main/p/percona-server-mongodb/percona-server-mongodb-server_8.0.38-16.jammy_amd64.deb", filename)

	_, _, err = aptIndexLocation("https://example.test/not-an-apt-repo/file.deb", "jammy")
	require.Error(t, err)
}

// LOCAL-038: packages listed in the repository index get their checksum;
// a package the index does not list is refused.
func TestFetchPerconaDebChecksums(t *testing.T) {
	const (
		listed   = "https://repo.percona.com/psmdb-80/apt/pool/main/p/percona-server-mongodb/percona-server-mongodb-server_8.0.38-16.jammy_amd64.deb"
		unlisted = "https://repo.percona.com/psmdb-80/apt/pool/main/p/percona-server-mongodb/percona-server-mongodb-server_8.0.12-4.jammy_amd64.deb"
	)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == "https://repo.percona.com/psmdb-80/apt/dists/jammy/main/binary-amd64/Packages" {
			return testHTTPResponse(r, http.StatusOK, []byte(testAptIndex)), nil
		}
		return testHTTPResponse(r, http.StatusNotFound, nil), nil
	})}
	bm := &BinaryManager{httpClient: client}

	require.NoError(t, bm.fetchPerconaDebChecksums(map[string]string{"server": listed}, "jammy"))

	sum, ok := bm.publishedChecksum(listed)
	require.True(t, ok)
	assert.Equal(t, "8d4e6a9822ee4f17a903b38a217d007ab16a1de209e10e68756435c6540d5dab", sum)

	err := bm.fetchPerconaDebChecksums(map[string]string{"old": unlisted}, "jammy")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be verified")
}

// LOCAL-038: a .deb is never installed without a matching checksum.
func TestDownloadDebPackages_RefusesUnverifiedOrTamperedPackage(t *testing.T) {
	const debURL = "https://repo.percona.com/psmdb-80/apt/pool/main/p/percona-server-mongodb/percona-server-mongodb-server_8.0.38-16.jammy_amd64.deb"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testHTTPResponse(r, http.StatusOK, []byte("not the published package")), nil
	})}

	t.Run("no checksum recorded", func(t *testing.T) {
		bm := &BinaryManager{httpClient: client}

		err := bm.downloadAndExtractDebPackages(map[string]string{"server": debURL}, t.TempDir())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no published checksum")
	})

	t.Run("content does not match the checksum", func(t *testing.T) {
		bm := &BinaryManager{httpClient: client}
		bm.rememberChecksum(debURL, testSHA256)

		err := bm.downloadAndExtractDebPackages(map[string]string{"server": debURL}, t.TempDir())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum mismatch")
	})
}

// debRepoClient serves HEAD 200 for the given package URLs and an index
// listing all of them.
func debRepoClient(indexURL string, debURLs ...string) *http.Client {
	var index strings.Builder
	known := make(map[string]bool)
	for _, debURL := range debURLs {
		known[debURL] = true
		_, filename, err := aptIndexLocation(debURL, "noble")
		if err != nil {
			panic(err)
		}
		index.WriteString("Package: p\nFilename: " + filename + "\nSHA256: " + testSHA256 + "\n\n")
	}
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.String() == indexURL:
			return testHTTPResponse(r, http.StatusOK, []byte(index.String())), nil
		case known[r.URL.String()]:
			return testHTTPResponse(r, http.StatusOK, nil), nil
		}
		return testHTTPResponse(r, http.StatusNotFound, nil), nil
	})}
}

// LOCAL-041: for 5.0 and later the shell is mup's pinned mongosh, so the
// .deb fallback needs only the server and mongos packages.
func TestBuildPerconaDebURLs_ShellPackageOnlyForLegacyShell(t *testing.T) {
	linux := Platform{OS: "linux", Arch: "amd64"}

	t.Run("8.0 needs server and mongos only", func(t *testing.T) {
		const pool = "https://repo.percona.com/psmdb-80/apt/pool/main/p/percona-server-mongodb/"
		server := pool + "percona-server-mongodb-server_8.0.38-16.noble_amd64.deb"
		mongos := pool + "percona-server-mongodb-mongos_8.0.38-16.noble_amd64.deb"
		bm := &BinaryManager{httpClient: debRepoClient(
			"https://repo.percona.com/psmdb-80/apt/dists/noble/main/binary-amd64/Packages", server, mongos)}

		urls, err := bm.buildPerconaDebURLs("8.0.38-16", linux)

		require.NoError(t, err)
		assert.Equal(t, map[string]string{"server": server, "mongos": mongos}, urls)
	})

	t.Run("4.4 also needs the legacy shell package", func(t *testing.T) {
		const pool = "https://repo.percona.com/psmdb-44/apt/pool/main/p/percona-server-mongodb/"
		server := pool + "percona-server-mongodb-server_4.4.29-28.noble_amd64.deb"
		mongos := pool + "percona-server-mongodb-mongos_4.4.29-28.noble_amd64.deb"
		shell := pool + "percona-server-mongodb-shell_4.4.29-28.noble_amd64.deb"
		bm := &BinaryManager{httpClient: debRepoClient(
			"https://repo.percona.com/psmdb-44/apt/dists/noble/main/binary-amd64/Packages", server, mongos, shell)}

		urls, err := bm.buildPerconaDebURLs("4.4.29-28", linux)

		require.NoError(t, err)
		assert.Equal(t, map[string]string{"server": server, "mongos": mongos, "shell": shell}, urls)
	})
}

// LOCAL-041
func TestUsesLegacyShellPackage(t *testing.T) {
	assert.True(t, usesLegacyShellPackage("3.6.23-13"))
	assert.True(t, usesLegacyShellPackage("4.4.29-28"))
	assert.False(t, usesLegacyShellPackage("5.0.9-8"))
	assert.False(t, usesLegacyShellPackage("8.0.38-16"))
	assert.False(t, usesLegacyShellPackage("10.0.1-1"))
}
