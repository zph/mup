package meta

import (
	"testing"
)

// [UPG-002] Test variant-aware version handling
func TestClusterMetadata_GetFullVersion(t *testing.T) {
	tests := []struct {
		name     string
		metadata *ClusterMetadata
		want     string
	}{
		{
			name: "mongo variant explicitly set",
			metadata: &ClusterMetadata{
				Variant: "mongo",
				Version: "6.0.15",
			},
			want: "mongo-6.0.15",
		},
		{
			name: "percona variant",
			metadata: &ClusterMetadata{
				Variant: "percona",
				Version: "7.0.5-4",
			},
			want: "percona-7.0.5-4",
		},
		{
			name: "default to mongo if variant not set",
			metadata: &ClusterMetadata{
				Version: "7.0.0",
			},
			want: "mongo-7.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.metadata.GetFullVersion()
			if got != tt.want {
				t.Errorf("GetFullVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterMetadata_SetFullVersion(t *testing.T) {
	tests := []struct {
		name        string
		fullVersion string
		wantVariant string
		wantVersion string
		wantErr     bool
	}{
		{
			name:        "mongo variant",
			fullVersion: "mongo-6.0.15",
			wantVariant: "mongo",
			wantVersion: "6.0.15",
			wantErr:     false,
		},
		{
			name:        "percona variant",
			fullVersion: "percona-7.0.5-4",
			wantVariant: "percona",
			wantVersion: "7.0.5-4",
			wantErr:     false,
		},
		{
			name:        "invalid format - no hyphen",
			fullVersion: "mongo7.0.0",
			wantErr:     true,
		},
		{
			name:        "invalid variant",
			fullVersion: "mariadb-10.5",
			wantErr:     true,
		},
		{
			name:        "empty string",
			fullVersion: "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &ClusterMetadata{}
			err := cm.SetFullVersion(tt.fullVersion)

			if (err != nil) != tt.wantErr {
				t.Errorf("SetFullVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if cm.Variant != tt.wantVariant {
					t.Errorf("SetFullVersion() variant = %v, want %v", cm.Variant, tt.wantVariant)
				}
				if cm.Version != tt.wantVersion {
					t.Errorf("SetFullVersion() version = %v, want %v", cm.Version, tt.wantVersion)
				}
			}
		})
	}
}

func TestClusterMetadata_FullVersionRoundTrip(t *testing.T) {
	testCases := []string{
		"mongo-6.0.15",
		"mongo-7.0.0",
		"percona-6.0.15",
		"percona-7.0.5-4",
	}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			cm := &ClusterMetadata{}
			if err := cm.SetFullVersion(tc); err != nil {
				t.Fatalf("SetFullVersion(%q) failed: %v", tc, err)
			}

			got := cm.GetFullVersion()
			if got != tc {
				t.Errorf("Round trip failed: SetFullVersion(%q) -> GetFullVersion() = %q", tc, got)
			}
		})
	}
}
