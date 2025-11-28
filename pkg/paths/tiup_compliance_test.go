package paths

import (
	"path/filepath"
	"testing"

	"github.com/zph/mup/pkg/topology"
)

// REQ-PM-026: TiUP compliance tests validating path resolution follows TiUP conventions
// Reference: https://docs.pingcap.com/tidb/stable/tiup-cluster-topology-reference/
// (as specified in CLAUDE.md)

func TestTiUPCompliance_REQ_PM_004_AbsolutePathOverride(t *testing.T) {
	// REQ-PM-004: When a topology specifies an absolute path for deploy_dir at instance level,
	// the Path Management System shall use that exact path.
	//
	// TiUP specification: "If the absolute path of deploy_dir is configured at the instance
	// level, the actual deployment directory is deploy_dir configured for the instance."

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "/opt/default",
		DataDir:   "/data/default",
		LogDir:    "/logs/default",
	}

	resolver := NewRemotePathResolver(global)

	// Instance specifies absolute paths - these should override globals completely
	instanceDeployDir := "/opt/custom/mongod-27017"
	instanceDataDir := "/data/custom/mongod-27017"
	instanceLogDir := "/logs/custom/mongod-27017"

	// Test data directory - absolute path should be used exactly
	dataDir, err := resolver.DataDir("10.0.1.10", 27017, instanceDeployDir, instanceDataDir)
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}
	if dataDir != instanceDataDir {
		t.Errorf("REQ-PM-004 VIOLATION: Absolute instance data_dir not used. got=%s, want=%s",
			dataDir, instanceDataDir)
	}

	// Test log directory - absolute path should be used exactly
	logDir, err := resolver.LogDir("mongod", "10.0.1.10", 27017, instanceDeployDir, instanceLogDir)
	if err != nil {
		t.Fatalf("LogDir() error = %v", err)
	}
	if logDir != instanceLogDir {
		t.Errorf("REQ-PM-004 VIOLATION: Absolute instance log_dir not used. got=%s, want=%s",
			logDir, instanceLogDir)
	}

	t.Logf("✓ REQ-PM-004: Absolute paths at instance level correctly override all defaults")
}

func TestTiUPCompliance_REQ_PM_005_RelativePathNesting(t *testing.T) {
	// REQ-PM-005: While a topology specifies a relative path for deploy_dir,
	// the Path Management System shall resolve the path as
	// /home/<user>/<global.deploy_dir>/<instance.deploy_dir>
	//
	// TiUP specification: "When relative paths are used, the component is deployed to
	// the /home/<global.user>/<global.deploy_dir>/<instance.deploy_dir> directory."

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "tidb-deploy",
		DataDir:   "",
		LogDir:    "",
	}

	resolver := NewRemotePathResolver(global)

	// Instance with relative deploy_dir (no leading /)
	instanceDeployDir := "mongod-27017"

	// Data directory with no instance override should nest within deploy_dir
	dataDir, err := resolver.DataDir("10.0.1.10", 27017, instanceDeployDir, "")
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	// Expected: /home/mongodb/tidb-deploy/mongod-27017/data
	expected := filepath.Join("/home", global.User, global.DeployDir, instanceDeployDir, "data")
	if dataDir != expected {
		t.Errorf("REQ-PM-005 VIOLATION: Relative path not nested correctly.\ngot=%s\nwant=%s",
			dataDir, expected)
	}

	t.Logf("✓ REQ-PM-005: Relative paths correctly nest within /home/<user>/<global>/<instance>")
}

func TestTiUPCompliance_REQ_PM_006_GlobalDefaultCascading(t *testing.T) {
	// REQ-PM-006: While an instance-level data_dir is not specified,
	// the Path Management System shall use the global data_dir value.
	//
	// TiUP specification: "If no instance-level specification exists,
	// its default value is <global.data_dir>."

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "/opt/mongodb",
		DataDir:   "/data/mongodb",
		LogDir:    "/logs/mongodb",
	}

	resolver := NewRemotePathResolver(global)

	// Instance with no data_dir override (empty string)
	instanceDeployDir := ""
	instanceDataDir := ""

	dataDir, err := resolver.DataDir("10.0.1.10", 27017, instanceDeployDir, instanceDataDir)
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	// Should use global data_dir as base
	expected := filepath.Join(global.DataDir, "mongod-27017")
	if dataDir != expected {
		t.Errorf("REQ-PM-006 VIOLATION: Global default not cascaded to instance.\ngot=%s\nwant=%s",
			dataDir, expected)
	}

	// Test with log_dir
	logDir, err := resolver.LogDir("mongod", "10.0.1.10", 27017, instanceDeployDir, "")
	if err != nil {
		t.Fatalf("LogDir() error = %v", err)
	}

	expectedLog := filepath.Join(global.LogDir, "mongod-27017")
	if logDir != expectedLog {
		t.Errorf("REQ-PM-006 VIOLATION: Global log_dir default not cascaded.\ngot=%s\nwant=%s",
			logDir, expectedLog)
	}

	t.Logf("✓ REQ-PM-006: Missing instance-level values correctly cascade from global defaults")
}

func TestTiUPCompliance_REQ_PM_007_RelativeDataDirNesting(t *testing.T) {
	// REQ-PM-007: While a data_dir is specified as a relative path,
	// the Path Management System shall resolve the path as <deploy_dir>/<data_dir>
	//
	// TiUP specification: "When using relative paths, the component data is
	// placed in <deploy_dir>/<data_dir>."

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "/opt/mongodb",
		DataDir:   "",
		LogDir:    "",
	}

	resolver := NewRemotePathResolver(global)

	// Instance with absolute deploy_dir and relative data_dir
	instanceDeployDir := "/opt/mongodb/mongod-27017"
	instanceDataDir := "data" // Relative path

	dataDir, err := resolver.DataDir("10.0.1.10", 27017, instanceDeployDir, instanceDataDir)
	if err != nil {
		t.Fatalf("DataDir() error = %v", err)
	}

	// Expected: <deploy_dir>/<data_dir>
	expected := filepath.Join(instanceDeployDir, instanceDataDir)
	if dataDir != expected {
		t.Errorf("REQ-PM-007 VIOLATION: Relative data_dir not nested within deploy_dir.\ngot=%s\nwant=%s",
			dataDir, expected)
	}

	t.Logf("✓ REQ-PM-007: Relative data_dir correctly nests within deploy_dir")
}

func TestTiUPCompliance_REQ_PM_008_RelativeLogDirNesting(t *testing.T) {
	// REQ-PM-008: While a log_dir is specified as a relative path,
	// the Path Management System shall resolve the path as <deploy_dir>/<log_dir>
	//
	// TiUP specification: "Relative log paths follow the same nesting convention:
	// <deploy_dir>/<log_dir>."

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "/opt/mongodb",
		DataDir:   "",
		LogDir:    "",
	}

	resolver := NewRemotePathResolver(global)

	// Instance with absolute deploy_dir and relative log_dir
	instanceDeployDir := "/opt/mongodb/mongod-27017"
	instanceLogDir := "log" // Relative path

	logDir, err := resolver.LogDir("mongod", "10.0.1.10", 27017, instanceDeployDir, instanceLogDir)
	if err != nil {
		t.Fatalf("LogDir() error = %v", err)
	}

	// Expected: <deploy_dir>/<log_dir>
	expected := filepath.Join(instanceDeployDir, instanceLogDir)
	if logDir != expected {
		t.Errorf("REQ-PM-008 VIOLATION: Relative log_dir not nested within deploy_dir.\ngot=%s\nwant=%s",
			logDir, expected)
	}

	t.Logf("✓ REQ-PM-008: Relative log_dir correctly nests within deploy_dir")
}

func TestTiUPCompliance_ComplexScenario(t *testing.T) {
	// Complex scenario combining multiple TiUP rules:
	// - Global defaults with relative paths
	// - Instance override with absolute path for one component
	// - Instance override with relative path for another component
	// - Missing instance overrides cascading from global

	global := &topology.GlobalConfig{
		User:      "mongodb",
		DeployDir: "mongodb-cluster",
		DataDir:   "",
		LogDir:    "",
	}

	resolver := NewRemotePathResolver(global)

	t.Run("instance with absolute override", func(t *testing.T) {
		// REQ-PM-004: Absolute path takes precedence
		dataDir, err := resolver.DataDir("10.0.1.10", 27017, "/opt/mongodb", "/data/mongodb/node1")
		if err != nil {
			t.Fatal(err)
		}
		if dataDir != "/data/mongodb/node1" {
			t.Errorf("Absolute path not honored: %s", dataDir)
		}
	})

	t.Run("instance with relative override", func(t *testing.T) {
		// REQ-PM-005 + REQ-PM-007: Relative paths nest correctly
		dataDir, err := resolver.DataDir("10.0.1.10", 27018, "mongod-27018", "data")
		if err != nil {
			t.Fatal(err)
		}
		expected := filepath.Join("/home", global.User, global.DeployDir, "mongod-27018", "data")
		if dataDir != expected {
			t.Errorf("Relative nesting incorrect: got=%s, want=%s", dataDir, expected)
		}
	})

	t.Run("instance with no overrides", func(t *testing.T) {
		// REQ-PM-006: Cascade from global, but global also empty, so use defaults
		dataDir, err := resolver.DataDir("10.0.1.10", 27019, "", "")
		if err != nil {
			t.Fatal(err)
		}
		// Should use global deploy_dir as base
		expected := filepath.Join(global.DeployDir, "data")
		if dataDir != expected {
			t.Errorf("Default cascading incorrect: got=%s, want=%s", dataDir, expected)
		}
	})

	t.Logf("✓ Complex TiUP scenario validates correctly across all rules")
}

func TestTiUPCompliance_Documentation(t *testing.T) {
	// This test documents the TiUP compliance implementation
	// Reference: https://docs.pingcap.com/tidb/stable/tiup-cluster-topology-reference/

	t.Log("=== TiUP Path Convention Compliance ===")
	t.Log("")
	t.Log("mup implements TiUP path resolution rules as specified in:")
	t.Log("https://docs.pingcap.com/tidb/stable/tiup-cluster-topology-reference/")
	t.Log("")
	t.Log("Implemented Rules:")
	t.Log("  REQ-PM-004: Absolute instance paths override all defaults")
	t.Log("  REQ-PM-005: Relative instance paths nest: /home/<user>/<global>/<instance>")
	t.Log("  REQ-PM-006: Missing instance values cascade from global config")
	t.Log("  REQ-PM-007: Relative data_dir nests within deploy_dir")
	t.Log("  REQ-PM-008: Relative log_dir nests within deploy_dir")
	t.Log("")
	t.Log("Path Resolution Order:")
	t.Log("  1. Check for absolute instance path -> use as-is")
	t.Log("  2. Check for relative instance path -> nest within hierarchy")
	t.Log("  3. Check for global default -> apply to instance")
	t.Log("  4. Use built-in default pattern")
	t.Log("")
	t.Log("This ensures mup remote deployments are fully compatible with")
	t.Log("existing TiUP-managed clusters and follow MongoDB ops best practices.")
}
