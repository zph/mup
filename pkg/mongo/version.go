package mongo

import (
	"fmt"
	"strconv"
	"strings"
)

// ShellSource describes how mup acquires a MongoDB shell.
type ShellSource string

const (
	// ShellSourceServerArchive means the shell is bundled with the MongoDB
	// server archive and only needs to be verified after extraction.
	ShellSourceServerArchive ShellSource = "server_archive"

	// ShellSourceDownload means the shell is downloaded separately from the
	// MongoDB server archive.
	ShellSourceDownload ShellSource = "download"
)

// ShellCompatibility describes the shell selected for a MongoDB server.
//
// MinimumWireVersion is the lowest server maxWireVersion represented by the
// policy row. ShellVersion is empty for a shell bundled with the server.
type ShellCompatibility struct {
	Binary             string
	Source             ShellSource
	ShellVersion       string
	NodeDriverVersion  string
	MinimumWireVersion int
}

// Ordered from the newest wire-protocol floor to the oldest. These are
// intentionally known-good policies, not dynamically resolved latest versions.
//
// MongoDB 3.6 bundles the legacy mongo shell. MongoDB 4.0 and newer use a
// separately downloaded mongosh whose embedded Node.js driver supports the
// server's wire protocol. Node.js driver 6.17 dropped wire version 7, so
// MongoDB 4.0 remains pinned to mongosh 2.5.1 with driver 6.16.
var shellCompatibilityByWireVersion = []ShellCompatibility{
	{
		Binary:             "mongosh",
		Source:             ShellSourceDownload,
		ShellVersion:       "2.5.9",
		NodeDriverVersion:  "6.19.0",
		MinimumWireVersion: 8,
	},
	{
		Binary:             "mongosh",
		Source:             ShellSourceDownload,
		ShellVersion:       "2.5.1",
		NodeDriverVersion:  "6.16.0",
		MinimumWireVersion: 7,
	},
	{
		Binary:             "mongo",
		Source:             ShellSourceServerArchive,
		MinimumWireVersion: 6,
	},
}

// ResolveShellCompatibility selects the shell and acquisition source for a
// MongoDB version using the server's wire-protocol compatibility floor.
func ResolveShellCompatibility(mongoDBVersion string) (ShellCompatibility, error) {
	serverWireVersionFloor, err := serverWireVersionFloor(mongoDBVersion)
	if err != nil {
		return ShellCompatibility{}, err
	}

	for _, compatibility := range shellCompatibilityByWireVersion {
		if serverWireVersionFloor >= compatibility.MinimumWireVersion {
			return compatibility, nil
		}
	}

	return ShellCompatibility{}, fmt.Errorf(
		"no compatible MongoDB shell is configured for MongoDB %s (wire version floor %d)",
		mongoDBVersion, serverWireVersionFloor,
	)
}

// GetShellBinary returns the shell binary selected for a MongoDB version.
func GetShellBinary(version string) (string, error) {
	compatibility, err := ResolveShellCompatibility(version)
	if err != nil {
		return "", err
	}
	return compatibility.Binary, nil
}

// serverWireVersionFloor returns the conservative maxWireVersion floor needed
// to select a compatible shell. Mup supports MongoDB 3.6 and newer:
// MongoDB 3.6 reports 6, MongoDB 4.0 reports 7, and MongoDB 4.2 and newer report
// at least 8.
func serverWireVersionFloor(mongoDBVersion string) (int, error) {
	parts := strings.Split(strings.TrimPrefix(mongoDBVersion, "v"), ".")
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid MongoDB version %q: expected X.Y or X.Y.Z", mongoDBVersion)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid MongoDB major version %q: %w", parts[0], err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("invalid MongoDB minor version %q: %w", parts[1], err)
	}

	switch {
	case major == 3 && minor == 6:
		return 6, nil
	case major == 4 && minor < 2:
		return 7, nil
	case major >= 4:
		return 8, nil
	default:
		return 0, fmt.Errorf(
			"unsupported MongoDB version %q: shell compatibility starts at MongoDB 3.6",
			mongoDBVersion,
		)
	}
}
