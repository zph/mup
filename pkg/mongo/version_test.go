package mongo

import (
	"strings"
	"testing"
)

func TestResolveShellCompatibility(t *testing.T) {
	tests := []struct {
		name            string
		version         string
		wantBinary      string
		wantSource      ShellSource
		wantShell       string
		wantDriver      string
		wantMinimumWire int
		wantErr         string
	}{
		{
			name:            "MongoDB 3.6 uses bundled legacy shell",
			version:         "3.6.23",
			wantBinary:      "mongo",
			wantSource:      ShellSourceServerArchive,
			wantMinimumWire: 6,
		},
		{
			name:            "MongoDB 4.0 downloads last wire version 7 compatible mongosh",
			version:         "4.0.28",
			wantBinary:      "mongosh",
			wantSource:      ShellSourceDownload,
			wantShell:       "2.5.1",
			wantDriver:      "6.16.0",
			wantMinimumWire: 7,
		},
		{
			name:            "Percona MongoDB 4.0 build uses the same policy",
			version:         "4.0.28-23",
			wantBinary:      "mongosh",
			wantSource:      ShellSourceDownload,
			wantShell:       "2.5.1",
			wantDriver:      "6.16.0",
			wantMinimumWire: 7,
		},
		{
			name:            "MongoDB 4.2 downloads modern pinned mongosh",
			version:         "4.2.25",
			wantBinary:      "mongosh",
			wantSource:      ShellSourceDownload,
			wantShell:       "2.5.9",
			wantDriver:      "6.19.0",
			wantMinimumWire: 8,
		},
		{
			name:            "MongoDB 8.0 uses wire version 8 compatibility floor",
			version:         "v8.0.12",
			wantBinary:      "mongosh",
			wantSource:      ShellSourceDownload,
			wantShell:       "2.5.9",
			wantDriver:      "6.19.0",
			wantMinimumWire: 8,
		},
		{
			name:    "unsupported MongoDB release does not silently select mongo",
			version: "3.4.24",
			wantErr: "shell compatibility starts at MongoDB 3.6",
		},
		{
			name:    "invalid version",
			version: "4",
			wantErr: "expected X.Y or X.Y.Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveShellCompatibility(tt.version)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolveShellCompatibility(%q) error = nil, want %q", tt.version, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ResolveShellCompatibility(%q) error = %q, want substring %q", tt.version, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveShellCompatibility(%q) error = %v", tt.version, err)
			}
			if got.Binary != tt.wantBinary {
				t.Errorf("binary = %q, want %q", got.Binary, tt.wantBinary)
			}
			if got.Source != tt.wantSource {
				t.Errorf("source = %q, want %q", got.Source, tt.wantSource)
			}
			if got.ShellVersion != tt.wantShell {
				t.Errorf("shell version = %q, want %q", got.ShellVersion, tt.wantShell)
			}
			if got.NodeDriverVersion != tt.wantDriver {
				t.Errorf("Node.js driver version = %q, want %q", got.NodeDriverVersion, tt.wantDriver)
			}
			if got.MinimumWireVersion != tt.wantMinimumWire {
				t.Errorf("minimum wire version = %d, want %d", got.MinimumWireVersion, tt.wantMinimumWire)
			}
		})
	}
}

func TestGetShellBinary(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{version: "3.6.23", want: "mongo"},
		{version: "4.0.28", want: "mongosh"},
		{version: "4.2.25", want: "mongosh"},
		{version: "8.0.12", want: "mongosh"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got, err := GetShellBinary(tt.version)
			if err != nil {
				t.Fatalf("GetShellBinary(%q) error = %v", tt.version, err)
			}
			if got != tt.want {
				t.Errorf("GetShellBinary(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}
