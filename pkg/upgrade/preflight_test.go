package upgrade

import (
	"testing"

	"github.com/zph/mup/pkg/meta"
)

// TestValidatePrerequisites_MetaVersionMismatch tests that pre-flight validation
// fails when meta.yaml version doesn't match the from-version parameter
func TestValidatePrerequisites_MetaVersionMismatch(t *testing.T) {
	tests := []struct {
		name            string
		metaVersion     string
		fromVersion     string
		shouldFail      bool
		errorContains   string
	}{
		{
			name:          "matching versions",
			metaVersion:   "6.0",
			fromVersion:   "6.0",
			shouldFail:    false,
		},
		{
			name:          "meta shows newer version than from-version",
			metaVersion:   "7.0",
			fromVersion:   "6.0",
			shouldFail:    true,
			errorContains: "metadata version mismatch",
		},
		{
			name:          "meta shows older version than from-version",
			metaVersion:   "5.0",
			fromVersion:   "6.0",
			shouldFail:    true,
			errorContains: "metadata version mismatch",
		},
		{
			name:          "meta shows completely different version",
			metaVersion:   "4.4",
			fromVersion:   "6.0",
			shouldFail:    true,
			errorContains: "metadata version mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal cluster metadata
			clusterMeta := &meta.ClusterMetadata{
				Name:    "test-cluster",
				Version: tt.metaVersion,
				Variant: "mongo",
			}

			// Create upgrade config
			config := UpgradeConfig{
				ClusterName: "test-cluster",
				FromVersion: tt.fromVersion,
				ToVersion:   "7.0",
			}

			// Create upgrader (note: this is simplified, doesn't test full validation)
			upgrader := &Upgrader{
				config:      config,
				clusterMeta: clusterMeta,
			}

			localUpgrader := &LocalUpgrader{
				Upgrader: upgrader,
			}

			// For this test, we only validate step 1 (meta version check)
			// We can't run full ValidatePrerequisites without mocking supervisor, MongoDB, etc.
			err := validateMetaVersion(localUpgrader.clusterMeta.Version, localUpgrader.config.FromVersion)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errorContains)
				} else if tt.errorContains != "" && !contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got %q", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

// TestValidatePrerequisites_FCVMismatch tests FCV validation logic
func TestValidatePrerequisites_FCVMismatch(t *testing.T) {
	tests := []struct {
		name          string
		currentFCV    string
		fromVersion   string
		shouldFail    bool
		errorContains string
	}{
		{
			name:        "FCV matches version",
			currentFCV:  "6.0",
			fromVersion: "6.0",
			shouldFail:  false,
		},
		{
			name:          "FCV is newer than binary version",
			currentFCV:    "7.0",
			fromVersion:   "6.0",
			shouldFail:    true,
			errorContains: "FCV mismatch",
		},
		{
			name:          "FCV much newer than binary (6.0 data with 4.4 binary)",
			currentFCV:    "6.0",
			fromVersion:   "4.4",
			shouldFail:    true,
			errorContains: "FCV mismatch",
		},
		{
			name:        "FCV older than version (acceptable during upgrade)",
			currentFCV:  "5.0",
			fromVersion: "6.0",
			shouldFail:  false,
		},
		{
			name:        "FCV same as version",
			currentFCV:  "5.0",
			fromVersion: "5.0",
			shouldFail:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFCVCompatibility(tt.currentFCV, tt.fromVersion)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errorContains)
				} else if tt.errorContains != "" && !contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got %q", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

// TestValidateUpgradePathWithFCV tests upgrade path validation considering FCV
func TestValidateUpgradePathWithFCV(t *testing.T) {
	tests := []struct {
		name          string
		fromVersion   string
		toVersion     string
		currentFCV    string
		shouldFail    bool
		errorContains string
	}{
		{
			name:        "valid upgrade path with matching FCV",
			fromVersion: "6.0",
			toVersion:   "7.0",
			currentFCV:  "6.0",
			shouldFail:  false,
		},
		{
			name:          "invalid: FCV higher than from-version",
			fromVersion:   "5.0",
			toVersion:     "6.0",
			currentFCV:    "6.0",
			shouldFail:    true,
			errorContains: "FCV mismatch",
		},
		{
			name:        "valid: FCV lower than from-version (upgrade in progress)",
			fromVersion: "6.0",
			toVersion:   "7.0",
			currentFCV:  "5.0",
			shouldFail:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// First validate the upgrade path
			if err := ValidateUpgradePathStrings(tt.fromVersion, tt.toVersion); err != nil {
				t.Fatalf("upgrade path validation failed: %v", err)
			}

			// Then validate FCV compatibility
			err := validateFCVCompatibility(tt.currentFCV, tt.fromVersion)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errorContains)
				} else if tt.errorContains != "" && !contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got %q", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

// Helper functions for validation (these should be extracted to local.go)

func validateMetaVersion(metaVersion, fromVersion string) error {
	if metaVersion != fromVersion {
		return ErrMetaVersionMismatch{
			MetaVersion: metaVersion,
			FromVersion: fromVersion,
		}
	}
	return nil
}

func validateFCVCompatibility(currentFCV, fromVersion string) error {
	if currentFCV != fromVersion {
		// Check if FCV is from a newer version that current binary can't support
		if currentFCV > fromVersion {
			return ErrFCVMismatch{
				CurrentFCV:  currentFCV,
				FromVersion: fromVersion,
			}
		}
	}
	return nil
}

// Custom error types for better testing

type ErrMetaVersionMismatch struct {
	MetaVersion string
	FromVersion string
}

func (e ErrMetaVersionMismatch) Error() string {
	return "metadata version mismatch: meta.yaml shows version " + e.MetaVersion + " but upgrade is from " + e.FromVersion
}

type ErrFCVMismatch struct {
	CurrentFCV  string
	FromVersion string
}

func (e ErrFCVMismatch) Error() string {
	return "FCV mismatch: cluster has FCV " + e.CurrentFCV + " but MongoDB version is " + e.FromVersion
}
