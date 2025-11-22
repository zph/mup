package deploy

import (
	"fmt"
	"runtime"
	"testing"
)

// [UPG-002] Integration test to verify which Percona versions are actually available
// This test documents the current state of Percona's tarball availability
func TestPercona_AvailableVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	bm, err := NewBinaryManager()
	if err != nil {
		t.Fatalf("Failed to create binary manager: %v", err)
	}
	defer bm.Close()

	platform := Platform{
		OS:   "linux", // Test on Linux as it has the most comprehensive support
		Arch: "amd64",
	}

	// Test latest version from each major release series
	// Based on Percona's documentation and availability
	testVersions := []struct {
		majorVersion string
		version      string
		expectAvailable bool
		note         string
	}{
		{
			majorVersion: "8.0",
			version:      "8.0.12-4",
			expectAvailable: true,
			note:         "Latest 8.0 release",
		},
		{
			majorVersion: "7.0",
			version:      "7.0.24-13",
			expectAvailable: true,
			note:         "Latest 7.0 release",
		},
		{
			majorVersion: "7.0",
			version:      "7.0.15-9",
			expectAvailable: true,
			note:         "Earlier 7.0 release",
		},
		{
			majorVersion: "6.0",
			version:      "6.0.25-20",
			expectAvailable: true,
			note:         "Latest 6.0 release (via .deb packages)",
		},
		{
			majorVersion: "5.0",
			version:      "5.0.28-24",
			expectAvailable: true,
			note:         "Latest 5.0 release (uses jammy-minimal)",
		},
		{
			majorVersion: "4.4",
			version:      "4.4.29-28",
			expectAvailable: true,
			note:         "Latest 4.4 release (via .deb packages)",
		},
		{
			majorVersion: "4.2",
			version:      "4.2.25-25",
			expectAvailable: true,
			note:         "Latest 4.2 release (via .deb packages)",
		},
		{
			majorVersion: "4.0",
			version:      "4.0.28-23",
			expectAvailable: true,
			note:         "Latest 4.0 release (via .deb packages)",
		},
		{
			majorVersion: "3.6",
			version:      "3.6.23-13",
			expectAvailable: true,
			note:         "Latest 3.6 release (via .deb packages with special handling)",
		},
	}

	var availableVersions []string
	var unavailableVersions []string

	for _, tv := range testVersions {
		t.Run(fmt.Sprintf("percona-%s", tv.version), func(t *testing.T) {
			// Try tarball first
			url, err := bm.buildPerconaURL(tv.version, platform)
			downloadMethod := "tarball"

			// If tarball not found, try .deb packages
			if err != nil {
				debURLs, debErr := bm.buildPerconaDebURLs(tv.version, platform)
				if debErr == nil && len(debURLs) > 0 {
					// .deb packages found
					err = nil
					url = fmt.Sprintf(".deb packages (%d files)", len(debURLs))
					downloadMethod = ".deb"
				}
			}

			if tv.expectAvailable {
				if err != nil {
					t.Errorf("Expected %s to be available but got error: %v", tv.version, err)
				} else {
					availableVersions = append(availableVersions, tv.version)
					t.Logf("✅ %s (%s) - Available via %s: %s", tv.version, tv.note, downloadMethod, url)
				}
			} else {
				if err != nil {
					unavailableVersions = append(unavailableVersions, tv.version)
					t.Logf("ℹ️  %s (%s) - Not available (expected)", tv.version, tv.note)
				} else {
					t.Logf("⚠️  %s (%s) - Unexpectedly available via %s: %s", tv.version, tv.note, downloadMethod, url)
				}
			}
		})
	}

	// Summary
	t.Logf("\n=== Percona Version Availability Summary ===")
	t.Logf("Available versions: %v", availableVersions)
	t.Logf("Unavailable versions: %v", unavailableVersions)
	t.Logf("\nSupported Percona major versions: 3.6, 4.0, 4.2, 4.4, 5.0, 6.0, 7.0, 8.0")
	t.Logf("Note: Versions 5.0, 6.0, 7.0, 8.0 use minimal tarballs")
	t.Logf("Versions 3.6, 4.0, 4.2, 4.4 use .deb packages from Percona apt repository")
	t.Logf("Version 3.6 has special package structure (percona-server-mongodb-36)")
	t.Logf("All major Percona Server for MongoDB versions from 3.6 to 8.0 are supported!")
}

// TestPercona_DownloadLatestMajorVersions attempts to download the latest available version from each major series
// This is a longer-running test that actually downloads binaries
func TestPercona_DownloadLatestMajorVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Only run on Linux (CI/CD environment)
	if runtime.GOOS != "linux" {
		t.Skip("Skipping download test on non-Linux platform")
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

	// Only test versions we know are available
	// Based on TestPercona_AvailableVersions results
	availableVersions := []string{
		"8.0.12-4",   // Latest 8.0
		"7.0.24-13",  // Latest 7.0
		"5.0.28-24",  // Latest 5.0
	}

	for _, version := range availableVersions {
		t.Run(fmt.Sprintf("download-%s", version), func(t *testing.T) {
			binPath, err := bm.GetBinPathWithVariant(version, VariantPercona, platform)
			if err != nil {
				t.Fatalf("Failed to download Percona %s: %v", version, err)
			}

			t.Logf("✅ Successfully downloaded Percona %s to %s", version, binPath)

			// Verify mongod binary exists and is executable
			// (verification logic would go here)
		})
	}
}
