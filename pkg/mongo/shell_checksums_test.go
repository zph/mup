package mongo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const sha256HexLength = 64

// LOCAL-034: every mongosh version mup can download has a pinned checksum for
// every platform, so bumping a pinned version without updating the table fails here.
func TestEveryDownloadableShellHasPinnedChecksums(t *testing.T) {
	var downloadable int
	for _, compat := range shellCompatibilityByWireVersion {
		if compat.Source != ShellSourceDownload {
			continue
		}
		downloadable++
		for targetOS, ext := range shellArchiveOSes {
			for _, arch := range shellArchiveArchs {
				name := ShellArchiveName(compat.ShellVersion, targetOS, arch, ext)
				sum, ok := ShellArchiveSHA256(name)
				assert.True(t, ok, "no pinned checksum for %s", name)
				assert.Len(t, sum, sha256HexLength, "checksum for %s", name)
			}
		}
	}
	assert.Positive(t, downloadable)
}

func TestShellArchiveSHA256_UnknownArchive(t *testing.T) {
	_, ok := ShellArchiveSHA256("mongosh-9.9.9-darwin-arm64.zip")
	assert.False(t, ok)
}
