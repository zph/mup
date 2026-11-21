package deploy

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// arMember is one file inside a test .deb.
type arMember struct {
	name string
	data []byte
}

// buildAr writes an ar archive the way dpkg-deb does.
func buildAr(members ...arMember) []byte {
	var buf bytes.Buffer
	buf.WriteString(arMagic)
	for _, m := range members {
		fmt.Fprintf(&buf, "%-16s%-12d%-6d%-6d%-8s%-10d`\n", m.name, 0, 0, 0, "100644", len(m.data))
		buf.Write(m.data)
		if len(m.data)%2 == 1 {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

func tarWith(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	// Sorted, so entries after "./usr/bin/..." would overwrite it if the
	// extractor matched on file name alone.
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		content := files[name]
		require.NoError(t, w.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}))
		_, err := w.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func zstded(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	require.NoError(t, err)
	_, err = w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func writeDeb(t *testing.T, members ...arMember) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pkg.deb")
	require.NoError(t, os.WriteFile(path, buildAr(members...), 0o644))
	return path
}

// LOCAL-044: .deb packages are unpacked without external tools.
func TestExtractDebPackage(t *testing.T) {
	payload := map[string]string{
		"./usr/bin/mongod":             "mongod-binary",
		"./usr/share/doc/README":       "docs",
		"./usr/bin/mongod-helper-tool": "not wanted",
		// Same file name elsewhere in the package: not the executable.
		"./etc/init.d/mongod":        "#!/bin/sh init script",
		"./usr/share/lintian/mongod": "lintian override",
		"./usr/bin/nested/mongod":    "not directly in usr/bin",
	}
	// An odd-sized first member exercises ar's padding rule.
	debianBinary := arMember{"debian-binary", []byte("2.0\n\n\n")[:5]}
	control := arMember{"control.tar.gz", gzipped(t, tarWith(t, map[string]string{"./control": "Package: x"}))}

	for name, data := range map[string]arMember{
		"gzip": {"data.tar.gz", gzipped(t, tarWith(t, payload))},
		"zstd": {"data.tar.zst", zstded(t, tarWith(t, payload))},
		"none": {"data.tar", tarWith(t, payload)},
	} {
		t.Run(name, func(t *testing.T) {
			deb := writeDeb(t, debianBinary, control, data)
			binDir := t.TempDir()

			require.NoError(t, (&BinaryManager{}).extractDebPackage(deb, binDir))

			content, err := os.ReadFile(filepath.Join(binDir, "mongod"))
			require.NoError(t, err)
			assert.Equal(t, "mongod-binary", string(content))
			entries, err := os.ReadDir(binDir)
			require.NoError(t, err)
			assert.Len(t, entries, 1, "only server binaries are extracted")
		})
	}
}

// LOCAL-044
func TestExtractDebPackage_RejectsBadArchives(t *testing.T) {
	bm := &BinaryManager{}

	notAr := filepath.Join(t.TempDir(), "x.deb")
	require.NoError(t, os.WriteFile(notAr, []byte("this is not an ar archive"), 0o644))
	require.Error(t, bm.extractDebPackage(notAr, t.TempDir()))

	noData := writeDeb(t, arMember{"debian-binary", []byte("2.0\n")})
	err := bm.extractDebPackage(noData, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "data.tar")

	truncated := filepath.Join(t.TempDir(), "t.deb")
	whole := buildAr(arMember{"data.tar.gz", gzipped(t, tarWith(t, map[string]string{"./usr/bin/mongod": "x"}))})
	require.NoError(t, os.WriteFile(truncated, whole[:len(whole)-10], 0o644))
	require.Error(t, bm.extractDebPackage(truncated, t.TempDir()))

	unsupported := writeDeb(t, arMember{"data.tar.lzma", []byte("????")})
	err = bm.extractDebPackage(unsupported, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "data.tar.lzma")
}

// LOCAL-044
func TestIsDebBinaryPath(t *testing.T) {
	for _, name := range []string{"./usr/bin/mongod", "usr/bin/mongos", "/usr/bin/mongod"} {
		assert.True(t, isDebBinaryPath(name), name)
	}
	for _, name := range []string{"./etc/init.d/mongod", "./usr/bin/nested/mongod", "./usr/sbin/mongod", "mongod", "./opt/usr/bin/mongod"} {
		assert.False(t, isDebBinaryPath(name), name)
	}
}
