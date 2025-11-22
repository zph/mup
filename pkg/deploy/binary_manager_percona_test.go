package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// [UPG-002] Integration tests for Percona Server for MongoDB downloads
// These tests verify we can download and extract Percona binaries

func TestBinaryManager_GetBinPathWithVariant_Percona(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	bm, err := NewBinaryManager()
	if err != nil {
		t.Fatalf("Failed to create binary manager: %v", err)
	}
	defer bm.Close()

	platform := Platform{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	// Test cases for different Percona versions
	testCases := []struct {
		name    string
		version string
		variant Variant
	}{
		{
			name:    "percona-8.0",
			version: "8.0.12-4",
			variant: VariantPercona,
		},
		{
			name:    "percona-7.0",
			version: "7.0.24-13",
			variant: VariantPercona,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Download Percona binaries
			binPath, err := bm.GetBinPathWithVariant(tc.version, tc.variant, platform)
			if err != nil {
				t.Fatalf("GetBinPathWithVariant(%s, %s, %+v) failed: %v", tc.version, tc.variant, platform, err)
			}

			// Verify bin path exists
			if _, err := os.Stat(binPath); os.IsNotExist(err) {
				t.Fatalf("Bin path %s does not exist", binPath)
			}

			// Verify mongod binary exists
			mongodPath := filepath.Join(binPath, "mongod")
			if runtime.GOOS == "windows" {
				mongodPath += ".exe"
			}
			if _, err := os.Stat(mongodPath); os.IsNotExist(err) {
				t.Fatalf("mongod binary not found at %s", mongodPath)
			}

			// Verify mongod is executable (Unix only)
			if runtime.GOOS != "windows" {
				info, err := os.Stat(mongodPath)
				if err != nil {
					t.Fatalf("Failed to stat mongod: %v", err)
				}
				if info.Mode().Perm()&0111 == 0 {
					t.Errorf("mongod is not executable: mode %v", info.Mode())
				}
			}

			t.Logf("✓ Successfully downloaded and verified %s %s at %s", tc.variant, tc.version, binPath)
		})
	}
}

func TestBinaryManager_BuildPerconaURL(t *testing.T) {
	bm, err := NewBinaryManager()
	if err != nil {
		t.Fatalf("Failed to create binary manager: %v", err)
	}
	defer bm.Close()

	testCases := []struct {
		name     string
		version  string
		platform Platform
		wantErr  bool
	}{
		{
			name:    "linux-amd64-8.0",
			version: "8.0.12-4",
			platform: Platform{
				OS:   "linux",
				Arch: "amd64",
			},
			wantErr: false,
		},
		{
			name:    "linux-amd64-7.0",
			version: "7.0.24-13",
			platform: Platform{
				OS:   "linux",
				Arch: "amd64",
			},
			wantErr: false,
		},
		{
			name:    "invalid-version-format",
			version: "7",
			platform: Platform{
				OS:   "linux",
				Arch: "amd64",
			},
			wantErr: true,
		},
		{
			name:    "unsupported-os",
			version: "7.0.24-13",
			platform: Platform{
				OS:   "windows",
				Arch: "amd64",
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url, err := bm.buildPerconaURL(tc.version, tc.platform)

			if tc.wantErr {
				if err == nil {
					t.Errorf("buildPerconaURL(%s, %+v) expected error, got none", tc.version, tc.platform)
				}
				return
			}

			if err != nil {
				t.Errorf("buildPerconaURL(%s, %+v) unexpected error: %v", tc.version, tc.platform, err)
				return
			}

			if url == "" {
				t.Errorf("buildPerconaURL(%s, %+v) returned empty URL", tc.version, tc.platform)
				return
			}

			// URL should contain the version and platform
			expectedSubstrings := []string{
				"percona-server-mongodb",
				tc.version,
			}

			for _, substr := range expectedSubstrings {
				if !contains(url, substr) {
					t.Errorf("URL %s does not contain expected substring %s", url, substr)
				}
			}

			t.Logf("✓ Built Percona URL for %s %+v: %s", tc.version, tc.platform, url)
		})
	}
}

func TestBinaryManager_GetBinPathWithVariant_MongoDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	bm, err := NewBinaryManager()
	if err != nil {
		t.Fatalf("Failed to create binary manager: %v", err)
	}
	defer bm.Close()

	platform := Platform{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	// Test that VariantMongo still works (backward compatibility)
	binPath, err := bm.GetBinPathWithVariant("7.0", VariantMongo, platform)
	if err != nil {
		t.Fatalf("GetBinPathWithVariant(7.0, mongo, %+v) failed: %v", platform, err)
	}

	// Verify mongod exists
	mongodPath := filepath.Join(binPath, "mongod")
	if runtime.GOOS == "windows" {
		mongodPath += ".exe"
	}
	if _, err := os.Stat(mongodPath); os.IsNotExist(err) {
		t.Fatalf("mongod binary not found at %s", mongodPath)
	}

	t.Logf("✓ Mongo variant (default) works correctly")
}

func TestBinaryManager_GetBinPathWithVariant_InvalidVariant(t *testing.T) {
	// Test that invalid variant string is rejected by ParseVariant
	_, err := ParseVariant("mariadb")
	if err == nil {
		t.Error("Expected error for invalid variant string, got none")
	}

	if !contains(err.Error(), "unknown variant") {
		t.Errorf("Expected 'unknown variant' error, got: %v", err)
	}

	t.Logf("✓ Invalid variant string correctly rejected")
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
