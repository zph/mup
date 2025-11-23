package upgrade

import (
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		want        *MongoVersion
		expectError bool
	}{
		{
			name:    "valid version with patch",
			version: "7.0.26",
			want:    &MongoVersion{Major: 7, Minor: 0, Patch: 26, Raw: "7.0.26"},
		},
		{
			name:    "valid version without patch",
			version: "4.4.0",
			want:    &MongoVersion{Major: 4, Minor: 4, Patch: 0, Raw: "4.4.0"},
		},
		{
			name:    "valid version with only major.minor",
			version: "5.0",
			want:    &MongoVersion{Major: 5, Minor: 0, Patch: 0, Raw: "5.0"},
		},
		{
			name:        "invalid version - missing minor",
			version:     "7",
			expectError: true,
		},
		{
			name:        "invalid version - non-numeric",
			version:     "7.x.26",
			expectError: true,
		},
		{
			name:        "invalid version - empty",
			version:     "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVersion(tt.version)
			if tt.expectError {
				if err == nil {
					t.Errorf("ParseVersion(%q) expected error but got none", tt.version)
				}
				return
			}

			if err != nil {
				t.Errorf("ParseVersion(%q) unexpected error: %v", tt.version, err)
				return
			}

			if got.Major != tt.want.Major || got.Minor != tt.want.Minor || got.Patch != tt.want.Patch {
				t.Errorf("ParseVersion(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestMongoVersionCompare(t *testing.T) {
	tests := []struct {
		name string
		v1   string
		v2   string
		want int
	}{
		{
			name: "equal versions",
			v1:   "7.0.26",
			v2:   "7.0.26",
			want: 0,
		},
		{
			name: "v1 less than v2 - patch",
			v1:   "7.0.1",
			v2:   "7.0.26",
			want: -1,
		},
		{
			name: "v1 greater than v2 - patch",
			v1:   "7.0.26",
			v2:   "7.0.1",
			want: 1,
		},
		{
			name: "v1 less than v2 - minor",
			v1:   "7.0.26",
			v2:   "7.1.0",
			want: -1,
		},
		{
			name: "v1 greater than v2 - minor",
			v1:   "7.1.0",
			v2:   "7.0.26",
			want: 1,
		},
		{
			name: "v1 less than v2 - major",
			v1:   "6.0.26",
			v2:   "7.0.1",
			want: -1,
		},
		{
			name: "v1 greater than v2 - major",
			v1:   "7.0.1",
			v2:   "6.0.26",
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v1, err := ParseVersion(tt.v1)
			if err != nil {
				t.Fatalf("ParseVersion(%q) failed: %v", tt.v1, err)
			}

			v2, err := ParseVersion(tt.v2)
			if err != nil {
				t.Fatalf("ParseVersion(%q) failed: %v", tt.v2, err)
			}

			got := v1.Compare(v2)
			if got != tt.want {
				t.Errorf("Compare(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
			}
		})
	}
}

func TestValidateUpgradePath(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		target      string
		expectError bool
		errorMsg    string
	}{
		// Patch upgrades - always allowed
		{
			name:        "patch upgrade same minor",
			source:      "7.0.1",
			target:      "7.0.26",
			expectError: false,
		},
		{
			name:        "patch upgrade large skip",
			source:      "6.0.0",
			target:      "6.0.15",
			expectError: false,
		},

		// Version upgrades - must follow exact path: 3.6→4.0→4.2→4.4→5.0→6.0→7.0→8.0
		{
			name:        "allowed: 4.0 to 4.2",
			source:      "4.0.0",
			target:      "4.2.0",
			expectError: false,
		},
		{
			name:        "allowed: 4.2 to 4.4",
			source:      "4.2.0",
			target:      "4.4.0",
			expectError: false,
		},
		{
			name:        "not allowed: 4.0 to 4.4 (must go through 4.2)",
			source:      "4.0.0",
			target:      "4.4.0",
			expectError: true,
			errorMsg:    "must upgrade to 4.2 next",
		},
		{
			name:        "not allowed: 7.0 to 7.1 (not in upgrade path)",
			source:      "7.0.0",
			target:      "7.1.0",
			expectError: true,
			errorMsg:    "must upgrade to 8.0 next",
		},
		{
			name:        "not allowed: 5.0 to 5.3 (not in upgrade path)",
			source:      "5.0.0",
			target:      "5.3.0",
			expectError: true,
			errorMsg:    "must upgrade to 6.0 next",
		},

		// Major version upgrades - only specific transitions
		{
			name:        "allowed: 3.6 to 4.0",
			source:      "3.6.8",
			target:      "4.0.0",
			expectError: false,
		},
		{
			name:        "allowed: 4.4 to 5.0",
			source:      "4.4.29",
			target:      "5.0.0",
			expectError: false,
		},
		{
			name:        "allowed: 5.0 to 6.0",
			source:      "5.0.26",
			target:      "6.0.0",
			expectError: false,
		},
		{
			name:        "allowed: 6.0 to 7.0",
			source:      "6.0.15",
			target:      "7.0.0",
			expectError: false,
		},
		{
			name:        "allowed: 7.0 to 8.0",
			source:      "7.0.26",
			target:      "8.0.0",
			expectError: false,
		},
		{
			name:        "not allowed: 3.4 to 4.0 (3.4 not in upgrade path)",
			source:      "3.4.0",
			target:      "4.0.0",
			expectError: true,
			errorMsg:    "not in the supported upgrade path",
		},
		{
			name:        "not allowed: 4.2 to 5.0 (must go through 4.4)",
			source:      "4.2.0",
			target:      "5.0.0",
			expectError: true,
			errorMsg:    "must upgrade to 4.4 next",
		},
		{
			name:        "not allowed: 3.6 to 5.0 (must go through 4.0)",
			source:      "3.6.0",
			target:      "5.0.0",
			expectError: true,
			errorMsg:    "must upgrade to 4.0 next",
		},
		{
			name:        "not allowed: 4.4 to 6.0 (must go through 5.0)",
			source:      "4.4.0",
			target:      "6.0.0",
			expectError: true,
			errorMsg:    "must upgrade to 5.0 next",
		},

		// Downgrades - never allowed
		{
			name:        "downgrade patch",
			source:      "7.0.26",
			target:      "7.0.1",
			expectError: true,
			errorMsg:    "cannot downgrade",
		},
		{
			name:        "downgrade minor",
			source:      "7.1.0",
			target:      "7.0.26",
			expectError: true,
			errorMsg:    "cannot downgrade",
		},
		{
			name:        "downgrade major",
			source:      "7.0.0",
			target:      "6.0.15",
			expectError: true,
			errorMsg:    "cannot downgrade",
		},

		// Same version
		{
			name:        "same version",
			source:      "7.0.26",
			target:      "7.0.26",
			expectError: true,
			errorMsg:    "cannot downgrade",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUpgradePathStrings(tt.source, tt.target)

			if tt.expectError {
				if err == nil {
					t.Errorf("ValidateUpgradePath(%q, %q) expected error but got none", tt.source, tt.target)
					return
				}
				if tt.errorMsg != "" && !contains(err.Error(), tt.errorMsg) {
					t.Errorf("ValidateUpgradePath(%q, %q) error = %q, want error containing %q",
						tt.source, tt.target, err.Error(), tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateUpgradePath(%q, %q) unexpected error: %v", tt.source, tt.target, err)
				}
			}
		})
	}
}

func TestValidateUpgradePathStrings_InvalidVersions(t *testing.T) {
	tests := []struct {
		name   string
		source string
		target string
	}{
		{
			name:   "invalid source version",
			source: "invalid",
			target: "7.0.0",
		},
		{
			name:   "invalid target version",
			source: "7.0.0",
			target: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUpgradePathStrings(tt.source, tt.target)
			if err == nil {
				t.Errorf("ValidateUpgradePathStrings(%q, %q) expected error but got none", tt.source, tt.target)
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && hasSubstring(s, substr)))
}

func hasSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
