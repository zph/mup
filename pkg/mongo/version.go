package mongo

import (
	"fmt"
	"strings"
)

// GetShellBinary returns the appropriate MongoDB shell binary name for the given version
// MongoDB 5.0+ uses "mongosh", earlier versions use "mongo"
func GetShellBinary(version string) string {
	if IsVersion5OrHigher(version) {
		return "mongosh"
	}
	return "mongo"
}

// IsVersion5OrHigher checks if MongoDB version is 5.0 or higher
func IsVersion5OrHigher(version string) bool {
	// Parse major version number
	parts := strings.Split(version, ".")
	if len(parts) == 0 {
		return false
	}

	major := 0
	fmt.Sscanf(parts[0], "%d", &major)
	return major >= 5
}
