package upgrade

import (
	"fmt"
	"strconv"
	"strings"
)

// MongoVersion represents a parsed MongoDB version
type MongoVersion struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

// ParseVersion parses a MongoDB version string (e.g., "7.0.26" or "4.4.0")
func ParseVersion(version string) (*MongoVersion, error) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid version format: %s (expected major.minor.patch)", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid major version: %s", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	patch := 0
	if len(parts) >= 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("invalid patch version: %s", parts[2])
		}
	}

	return &MongoVersion{
		Major: major,
		Minor: minor,
		Patch: patch,
		Raw:   version,
	}, nil
}

// Compare compares two versions. Returns:
// -1 if v < other
//  0 if v == other
//  1 if v > other
func (v *MongoVersion) Compare(other *MongoVersion) int {
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}

	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}

	if v.Patch != other.Patch {
		if v.Patch < other.Patch {
			return -1
		}
		return 1
	}

	return 0
}

// String returns the version string
func (v *MongoVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// ValidateUpgradePath validates that an upgrade from source to target version is safe
// according to MongoDB upgrade rules:
//
// 1. Patch level upgrades: Always allowed within same minor version (e.g., 7.0.1 → 7.0.26)
// 2. Version upgrades must follow the specific path: 3.6→4.0→4.2→4.4→5.0→6.0→7.0→8.0
//    You cannot skip any step in this path (e.g., cannot go 4.0→4.4, must go 4.0→4.2→4.4)
//
// Returns an error if the upgrade path is not safe.
func ValidateUpgradePath(source, target *MongoVersion) error {
	// Cannot downgrade
	if target.Compare(source) <= 0 {
		return fmt.Errorf("cannot downgrade from %s to %s", source.Raw, target.Raw)
	}

	// Same major.minor: patch upgrade is always allowed
	if source.Major == target.Major && source.Minor == target.Minor {
		return nil
	}

	// Define the allowed upgrade path: 3.6→4.0→4.2→4.4→5.0→6.0→7.0→8.0
	// Map from source version to next allowed version
	allowedTransitions := map[string]string{
		"3.6": "4.0",
		"4.0": "4.2",
		"4.2": "4.4",
		"4.4": "5.0",
		"5.0": "6.0",
		"6.0": "7.0",
		"7.0": "8.0",
	}

	sourceKey := fmt.Sprintf("%d.%d", source.Major, source.Minor)
	targetKey := fmt.Sprintf("%d.%d", target.Major, target.Minor)
	expectedNext := allowedTransitions[sourceKey]

	if expectedNext == "" {
		return fmt.Errorf("cannot upgrade from %s: version %s is not in the supported upgrade path (3.6→4.0→4.2→4.4→5.0→6.0→7.0→8.0)",
			source.Raw, sourceKey)
	}

	if targetKey != expectedNext {
		return fmt.Errorf("cannot upgrade from %s to %s: must upgrade to %s next (upgrade path: 3.6→4.0→4.2→4.4→5.0→6.0→7.0→8.0)",
			source.Raw, target.Raw, expectedNext)
	}

	// This is a valid transition in the upgrade path
	return nil
}

// ValidateUpgradePathStrings is a convenience wrapper around ValidateUpgradePath
// that accepts version strings instead of parsed MongoVersion objects
func ValidateUpgradePathStrings(source, target string) error {
	sourceVer, err := ParseVersion(source)
	if err != nil {
		return fmt.Errorf("invalid source version: %w", err)
	}

	targetVer, err := ParseVersion(target)
	if err != nil {
		return fmt.Errorf("invalid target version: %w", err)
	}

	return ValidateUpgradePath(sourceVer, targetVer)
}
