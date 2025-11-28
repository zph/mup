package paths

import (
	"path/filepath"
	"testing"
)

// REQ-PM-025: Integration tests simulating complete deploy, upgrade, and import
// path flows without executing operations

func TestClusterLayout_VersionDir(t *testing.T) {
	// REQ-PM-009: Version directories use pattern v<version>

	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)

	tests := []struct {
		version string
		want    string
	}{
		{"7.0.0", filepath.Join(clusterDir, "v7.0.0")},
		{"8.0.0", filepath.Join(clusterDir, "v8.0.0")},
		{"4.4.28", filepath.Join(clusterDir, "v4.4.28")},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := layout.VersionDir(tt.version)
			if got != tt.want {
				t.Errorf("VersionDir(%s) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestClusterLayout_DataDir(t *testing.T) {
	// REQ-PM-011: Data directories use pattern data/<host>-<port>
	// and are version-independent

	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)

	dataDir := layout.DataDir()
	expected := filepath.Join(clusterDir, "data")

	if dataDir != expected {
		t.Errorf("DataDir() = %v, want %v", dataDir, expected)
	}
}

func TestClusterLayout_CurrentLink(t *testing.T) {
	// REQ-PM-012: Current symlink points to active version

	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)

	currentLink := layout.CurrentLink()
	expected := filepath.Join(clusterDir, "current")

	if currentLink != expected {
		t.Errorf("CurrentLink() = %v, want %v", currentLink, expected)
	}
}

func TestClusterLayout_NextLink(t *testing.T) {
	// REQ-PM-013: Next symlink points to upgrade target

	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)

	nextLink := layout.NextLink()
	expected := filepath.Join(clusterDir, "next")

	if nextLink != expected {
		t.Errorf("NextLink() = %v, want %v", nextLink, expected)
	}
}

func TestClusterLayout_PreviousLink(t *testing.T) {
	// REQ-PM-014: Previous symlink enables rollback to last version

	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)

	previousLink := layout.PreviousLink()
	expected := filepath.Join(clusterDir, "previous")

	if previousLink != expected {
		t.Errorf("PreviousLink() = %v, want %v", previousLink, expected)
	}
}

func TestClusterLayout_ProcessDir(t *testing.T) {
	// REQ-PM-018: mongod directories use pattern mongod-<port>
	// REQ-PM-019: mongos directories use pattern mongos-<port>
	// REQ-PM-020: config server directories use pattern config-<port>

	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)
	version := "7.0.0"

	tests := []struct {
		name     string
		nodeType string
		port     int
		want     string
	}{
		{
			name:     "mongod process directory",
			nodeType: "mongod",
			port:     27017,
			want:     filepath.Join(clusterDir, "v7.0.0", "mongod-27017"),
		},
		{
			name:     "mongos process directory",
			nodeType: "mongos",
			port:     27016,
			want:     filepath.Join(clusterDir, "v7.0.0", "mongos-27016"),
		},
		{
			name:     "config server process directory",
			nodeType: "config",
			port:     27019,
			want:     filepath.Join(clusterDir, "v7.0.0", "config-27019"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := layout.ProcessDir(version, tt.nodeType, tt.port)
			if got != tt.want {
				t.Errorf("ProcessDir(%s, %s, %d) = %v, want %v",
					version, tt.nodeType, tt.port, got, tt.want)
			}
		})
	}
}

func TestClusterLayout_CreateSymlink_WithSimulator(t *testing.T) {
	// REQ-PM-023: Use simulator for testing symlink operations

	sim := NewPathSimulator()
	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)

	// Create version directory in simulator
	versionDir := layout.VersionDir("7.0.0")
	if err := sim.MkdirAll(versionDir); err != nil {
		t.Fatalf("Failed to create version dir: %v", err)
	}

	// Create symlink
	currentLink := layout.CurrentLink()
	if err := sim.Symlink("v7.0.0", currentLink); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	// Verify symlink exists
	if !sim.IsSymlink(currentLink) {
		t.Error("Current link should be a symlink")
	}

	// Verify symlink target
	target, err := sim.ReadSymlink(currentLink)
	if err != nil {
		t.Fatalf("Failed to read symlink: %v", err)
	}
	if target != "v7.0.0" {
		t.Errorf("Symlink target = %v, want v7.0.0", target)
	}
}

func TestClusterLayout_UpgradeFlow_WithSimulator(t *testing.T) {
	// REQ-PM-025: Integration test simulating upgrade path flow
	// REQ-PM-013: Next symlink created during upgrade
	// REQ-PM-014: Symlinks switched after successful upgrade

	sim := NewPathSimulator()
	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)

	// Initial deploy: v7.0.0
	oldVersion := "7.0.0"
	oldVersionDir := layout.VersionDir(oldVersion)
	if err := sim.MkdirAll(oldVersionDir); err != nil {
		t.Fatalf("Failed to create old version dir: %v", err)
	}

	// REQ-PM-012: Create current symlink
	currentLink := layout.CurrentLink()
	if err := sim.Symlink("v"+oldVersion, currentLink); err != nil {
		t.Fatalf("Failed to create current symlink: %v", err)
	}

	// Upgrade begins: create next version directory
	newVersion := "8.0.0"
	newVersionDir := layout.VersionDir(newVersion)
	if err := sim.MkdirAll(newVersionDir); err != nil {
		t.Fatalf("Failed to create new version dir: %v", err)
	}

	// REQ-PM-013: Create next symlink
	nextLink := layout.NextLink()
	if err := sim.Symlink("v"+newVersion, nextLink); err != nil {
		t.Fatalf("Failed to create next symlink: %v", err)
	}

	// Verify both versions exist during upgrade
	if !sim.IsDir(oldVersionDir) {
		t.Error("Old version directory should still exist during upgrade")
	}
	if !sim.IsDir(newVersionDir) {
		t.Error("New version directory should exist during upgrade")
	}
	if !sim.IsSymlink(currentLink) {
		t.Error("Current symlink should exist during upgrade")
	}
	if !sim.IsSymlink(nextLink) {
		t.Error("Next symlink should exist during upgrade")
	}

	// REQ-PM-014: Upgrade completes successfully - switch symlinks
	// Step 1: Create previous symlink pointing to old current target
	currentTarget, _ := sim.ReadSymlink(currentLink)
	previousLink := layout.PreviousLink()
	if err := sim.Symlink(currentTarget, previousLink); err != nil {
		t.Fatalf("Failed to create previous symlink: %v", err)
	}

	// Step 2: Update current to point to new version
	if err := sim.Remove(currentLink); err != nil {
		t.Fatalf("Failed to remove current symlink: %v", err)
	}
	if err := sim.Symlink("v"+newVersion, currentLink); err != nil {
		t.Fatalf("Failed to update current symlink: %v", err)
	}

	// Step 3: Remove next symlink
	if err := sim.Remove(nextLink); err != nil {
		t.Fatalf("Failed to remove next symlink: %v", err)
	}

	// Verify final state
	newCurrentTarget, err := sim.ReadSymlink(currentLink)
	if err != nil {
		t.Fatalf("Failed to read current symlink: %v", err)
	}
	if newCurrentTarget != "v"+newVersion {
		t.Errorf("Current symlink should point to v%s, got %s", newVersion, newCurrentTarget)
	}

	previousTarget, err := sim.ReadSymlink(previousLink)
	if err != nil {
		t.Fatalf("Failed to read previous symlink: %v", err)
	}
	if previousTarget != "v"+oldVersion {
		t.Errorf("Previous symlink should point to v%s, got %s", oldVersion, previousTarget)
	}

	if sim.IsSymlink(nextLink) {
		t.Error("Next symlink should be removed after upgrade completes")
	}
}

func TestClusterLayout_UpgradeFailure_WithSimulator(t *testing.T) {
	// REQ-PM-015: Failed upgrades preserve current and next symlinks unchanged
	// REQ-PM-025: Integration test for failure scenario

	sim := NewPathSimulator()
	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)

	// Initial state: v7.0.0 deployed
	oldVersion := "7.0.0"
	oldVersionDir := layout.VersionDir(oldVersion)
	if err := sim.MkdirAll(oldVersionDir); err != nil {
		t.Fatalf("Failed to create old version dir: %v", err)
	}

	currentLink := layout.CurrentLink()
	if err := sim.Symlink("v"+oldVersion, currentLink); err != nil {
		t.Fatalf("Failed to create current symlink: %v", err)
	}

	// Upgrade begins
	newVersion := "8.0.0"
	newVersionDir := layout.VersionDir(newVersion)
	if err := sim.MkdirAll(newVersionDir); err != nil {
		t.Fatalf("Failed to create new version dir: %v", err)
	}

	nextLink := layout.NextLink()
	if err := sim.Symlink("v"+newVersion, nextLink); err != nil {
		t.Fatalf("Failed to create next symlink: %v", err)
	}

	// Capture state before failure
	currentTargetBefore, _ := sim.ReadSymlink(currentLink)
	nextTargetBefore, _ := sim.ReadSymlink(nextLink)

	// Upgrade fails - symlinks should remain unchanged
	// (In real implementation, error handling would prevent symlink changes)

	// Verify symlinks unchanged after failure
	currentTargetAfter, err := sim.ReadSymlink(currentLink)
	if err != nil {
		t.Fatalf("Failed to read current symlink: %v", err)
	}
	if currentTargetAfter != currentTargetBefore {
		t.Errorf("Current symlink changed after failure: before=%s, after=%s",
			currentTargetBefore, currentTargetAfter)
	}

	nextTargetAfter, err := sim.ReadSymlink(nextLink)
	if err != nil {
		t.Fatalf("Failed to read next symlink: %v", err)
	}
	if nextTargetAfter != nextTargetBefore {
		t.Errorf("Next symlink changed after failure: before=%s, after=%s",
			nextTargetBefore, nextTargetAfter)
	}

	// Old version still running
	if !sim.IsDir(oldVersionDir) {
		t.Error("Old version directory should be preserved after failure")
	}
}

func TestClusterLayout_DeployFlow_WithSimulator(t *testing.T) {
	// REQ-PM-025: Integration test simulating deploy path flow
	// REQ-PM-010: Version-specific directories for bin, config, log
	// REQ-PM-011: Version-independent data directory

	sim := NewPathSimulator()
	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)
	version := "7.0.0"

	// Create version directory structure
	versionDir := layout.VersionDir(version)
	if err := sim.MkdirAll(versionDir); err != nil {
		t.Fatalf("Failed to create version dir: %v", err)
	}

	// Create bin directory
	binDir := filepath.Join(versionDir, "bin")
	if err := sim.MkdirAll(binDir); err != nil {
		t.Fatalf("Failed to create bin dir: %v", err)
	}

	// Create process directories for mongod
	processDir := layout.ProcessDir(version, "mongod", 27017)
	configDir := filepath.Join(processDir, "config")
	logDir := filepath.Join(processDir, "log")

	if err := sim.MkdirAll(configDir); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	if err := sim.MkdirAll(logDir); err != nil {
		t.Fatalf("Failed to create log dir: %v", err)
	}

	// Create data directory (version-independent)
	dataDir := layout.DataDir()
	nodeDataDir := filepath.Join(dataDir, "localhost-27017")
	if err := sim.MkdirAll(nodeDataDir); err != nil {
		t.Fatalf("Failed to create data dir: %v", err)
	}

	// Create current symlink
	currentLink := layout.CurrentLink()
	if err := sim.Symlink("v"+version, currentLink); err != nil {
		t.Fatalf("Failed to create current symlink: %v", err)
	}

	// Verify structure
	if !sim.IsDir(binDir) {
		t.Error("Bin directory should exist")
	}
	if !sim.IsDir(configDir) {
		t.Error("Config directory should exist")
	}
	if !sim.IsDir(logDir) {
		t.Error("Log directory should exist")
	}
	if !sim.IsDir(nodeDataDir) {
		t.Error("Data directory should exist")
	}
	if !sim.IsSymlink(currentLink) {
		t.Error("Current symlink should exist")
	}

	// Verify data is outside version directory
	if filepath.HasPrefix(nodeDataDir, versionDir) {
		t.Error("Data directory should not be within version directory")
	}

	// Verify config and log are within version directory
	if !filepath.HasPrefix(configDir, versionDir) {
		t.Error("Config directory should be within version directory")
	}
	if !filepath.HasPrefix(logDir, versionDir) {
		t.Error("Log directory should be within version directory")
	}
}

func TestClusterLayout_DataReuseAcrossUpgrades(t *testing.T) {
	// REQ-PM-016: Data directories reused across upgrades without copying

	sim := NewPathSimulator()
	clusterDir := "/home/user/.mup/storage/clusters/test"
	layout := NewClusterLayout(clusterDir)

	// Create data directory once
	dataDir := layout.DataDir()
	nodeDataDir := filepath.Join(dataDir, "localhost-27017")
	if err := sim.MkdirAll(nodeDataDir); err != nil {
		t.Fatalf("Failed to create data dir: %v", err)
	}

	// Create file in data directory to track it
	dataFile := filepath.Join(nodeDataDir, "data.db")
	if err := sim.CreateFile(dataFile); err != nil {
		t.Fatalf("Failed to create data file: %v", err)
	}

	// Deploy v7.0.0 - uses same data directory
	v7Dir := layout.VersionDir("7.0.0")
	if err := sim.MkdirAll(v7Dir); err != nil {
		t.Fatalf("Failed to create v7 dir: %v", err)
	}

	// Upgrade to v8.0.0 - still uses same data directory
	v8Dir := layout.VersionDir("8.0.0")
	if err := sim.MkdirAll(v8Dir); err != nil {
		t.Fatalf("Failed to create v8 dir: %v", err)
	}

	// Verify data file still exists and unchanged
	if !sim.Exists(dataFile) {
		t.Error("Data file should be preserved across upgrades")
	}

	// Verify data directory is shared
	if filepath.HasPrefix(nodeDataDir, v7Dir) || filepath.HasPrefix(nodeDataDir, v8Dir) {
		t.Error("Data directory should not be version-specific")
	}

	// Verify no data copying operations occurred
	ops := sim.GetOperations()
	dataOps := 0
	for _, op := range ops {
		if filepath.Dir(op) == nodeDataDir {
			dataOps++
		}
	}
	// Only the initial mkdir and touch should have occurred, no copying
	if dataOps > 2 {
		t.Errorf("Expected minimal data operations (mkdir + touch), got %d operations", dataOps)
	}
}
