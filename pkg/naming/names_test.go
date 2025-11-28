package naming

import "testing"

func TestGetProgramName(t *testing.T) {
	tests := []struct {
		name     string
		nodeType string
		port     int
		want     string
	}{
		{
			name:     "config server",
			nodeType: "config",
			port:     30000,
			want:     "config-30000",
		},
		{
			name:     "config server port 30001",
			nodeType: "config",
			port:     30001,
			want:     "config-30001",
		},
		{
			name:     "mongod shard node",
			nodeType: "mongod",
			port:     30100,
			want:     "mongod-30100",
		},
		{
			name:     "mongod shard node port 30200",
			nodeType: "mongod",
			port:     30200,
			want:     "mongod-30200",
		},
		{
			name:     "mongos router",
			nodeType: "mongos",
			port:     30300,
			want:     "mongos-30300",
		},
		{
			name:     "mongos router port 30301",
			nodeType: "mongos",
			port:     30301,
			want:     "mongos-30301",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetProgramName(tt.nodeType, tt.port)
			if got != tt.want {
				t.Errorf("GetProgramName(%q, %d) = %q, want %q", tt.nodeType, tt.port, got, tt.want)
			}
		})
	}
}

func TestGetConfigFileName(t *testing.T) {
	tests := []struct {
		name     string
		nodeType string
		want     string
	}{
		{
			name:     "config server",
			nodeType: "config",
			want:     "config.conf",
		},
		{
			name:     "mongod shard node",
			nodeType: "mongod",
			want:     "mongod.conf",
		},
		{
			name:     "mongos router",
			nodeType: "mongos",
			want:     "mongos.conf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetConfigFileName(tt.nodeType)
			if got != tt.want {
				t.Errorf("GetConfigFileName(%q) = %q, want %q", tt.nodeType, got, tt.want)
			}
		})
	}
}

func TestGetProcessDir(t *testing.T) {
	tests := []struct {
		name     string
		nodeType string
		port     int
		want     string
	}{
		{
			name:     "config server",
			nodeType: "config",
			port:     30000,
			want:     "config-30000",
		},
		{
			name:     "config server port 30002",
			nodeType: "config",
			port:     30002,
			want:     "config-30002",
		},
		{
			name:     "mongod shard node",
			nodeType: "mongod",
			port:     30100,
			want:     "mongod-30100",
		},
		{
			name:     "mongod shard node port 30202",
			nodeType: "mongod",
			port:     30202,
			want:     "mongod-30202",
		},
		{
			name:     "mongos router",
			nodeType: "mongos",
			port:     30300,
			want:     "mongos-30300",
		},
		{
			name:     "mongos router port 30301",
			nodeType: "mongos",
			port:     30301,
			want:     "mongos-30301",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetProcessDir(tt.nodeType, tt.port)
			if got != tt.want {
				t.Errorf("GetProcessDir(%q, %d) = %q, want %q", tt.nodeType, tt.port, got, tt.want)
			}
		})
	}
}
