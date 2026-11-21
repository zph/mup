package deploy

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// A .deb is an ar archive: a magic line, then members each preceded by a
// fixed 60-byte header and padded to an even length.
const (
	arMagic           = "!<arch>\n"
	arHeaderSize      = 60
	arNameEnd         = 16
	arSizeStart       = 48
	arSizeEnd         = 58
	arHeaderTrailer   = "`\n"
	debDataPrefix     = "data.tar"
	debBinaryFileMode = 0o755
)

// debServerBinaries are the files taken out of a package's payload.
var debServerBinaries = map[string]bool{"mongod": true, "mongos": true, "mongosh": true, "mongo": true}

// openDebData finds the payload member (data.tar[.gz|.zst|.xz]) of a .deb and
// returns its name and a reader limited to its bytes.
// LOCAL-044: read in Go rather than with the external "ar" tool, which a
// minimal Linux install does not have.
func openDebData(deb *os.File) (string, io.Reader, error) {
	magic := make([]byte, len(arMagic))
	if _, err := io.ReadFull(deb, magic); err != nil || string(magic) != arMagic {
		return "", nil, fmt.Errorf("not a .deb package (missing ar header)")
	}

	offset := int64(len(arMagic))
	header := make([]byte, arHeaderSize)
	for {
		if _, err := deb.ReadAt(header, offset); err != nil {
			if errors.Is(err, io.EOF) {
				return "", nil, fmt.Errorf("%s.* not found in .deb package", debDataPrefix)
			}
			return "", nil, fmt.Errorf("failed to read .deb member header: %w", err)
		}
		if string(header[arSizeEnd:]) != arHeaderTrailer {
			return "", nil, fmt.Errorf("corrupt .deb package (bad member header)")
		}

		// GNU ar terminates names with "/"; dpkg-deb pads them with spaces.
		name := strings.TrimSuffix(strings.TrimSpace(string(header[:arNameEnd])), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(header[arSizeStart:arSizeEnd])), 10, 64)
		if err != nil || size < 0 {
			return "", nil, fmt.Errorf("corrupt .deb package (bad size for member %q)", name)
		}

		dataStart := offset + arHeaderSize
		if strings.HasPrefix(name, debDataPrefix) {
			return name, io.NewSectionReader(deb, dataStart, size), nil
		}
		offset = dataStart + size + size%2
	}
}

// debPayloadReader returns a tar reader over a package payload, decompressing
// by the member's extension. The returned function releases the decompressor.
func debPayloadReader(name string, payload io.Reader) (*tar.Reader, func() error, error) {
	noop := func() error { return nil }
	switch filepath.Ext(name) {
	case ".tar":
		return tar.NewReader(payload), noop, nil
	case ".gz":
		gz, err := gzip.NewReader(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read %s: %w", name, err)
		}
		return tar.NewReader(gz), gz.Close, nil
	case ".zst":
		zr, err := zstd.NewReader(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read %s: %w", name, err)
		}
		return tar.NewReader(zr), func() error { zr.Close(); return nil }, nil
	case ".xz":
		// xz has no decoder in this module; fall back to the system tool.
		cmd := exec.Command("xz", "-dc")
		cmd.Stdin = payload
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read %s: %w", name, err)
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, fmt.Errorf("%s needs the xz tool, which could not be started: %w", name, err)
		}
		return tar.NewReader(out), cmd.Wait, nil
	}
	return nil, nil, fmt.Errorf("unsupported .deb payload compression: %s", name)
}

// extractDebPackage copies the MongoDB server binaries out of a .deb package
// into targetBinDir.
// LOCAL-044
func (bm *BinaryManager) extractDebPackage(debFile, targetBinDir string) (err error) {
	deb, err := os.Open(debFile)
	if err != nil {
		return fmt.Errorf("failed to open .deb package: %w", err)
	}
	defer func() { err = errors.Join(err, deb.Close()) }()

	name, payload, err := openDebData(deb)
	if err != nil {
		return err
	}
	tarReader, release, err := debPayloadReader(name, payload)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()

	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", name, err)
		}

		// Packages also ship init scripts and config files named after the
		// server (/etc/init.d/mongod, /etc/default/mongod); only the
		// executable in usr/bin is wanted.
		baseName := filepath.Base(header.Name)
		if header.Typeflag != tar.TypeReg || !debServerBinaries[baseName] || !isDebBinaryPath(header.Name) {
			continue
		}

		// baseName is a bare file name from the allow-list, so the target
		// cannot leave targetBinDir.
		if err := writeDebBinary(filepath.Join(targetBinDir, baseName), tarReader); err != nil {
			return err
		}
		fmt.Printf("  ✓ Extracted %s\n", baseName)
	}
}

// debBinaryDir is where a package installs its executables.
const debBinaryDir = "usr/bin"

// isDebBinaryPath reports whether a payload entry is directly inside usr/bin.
func isDebBinaryPath(entryName string) bool {
	dir := filepath.ToSlash(filepath.Dir(filepath.Clean(entryName)))
	return dir == debBinaryDir || dir == "/"+debBinaryDir
}

// writeDebBinary writes an executable file from r.
func writeDebBinary(path string, r io.Reader) (err error) {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, debBinaryFileMode)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, out.Close()) }()

	if _, err := io.Copy(out, r); err != nil {
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}
	return nil
}
