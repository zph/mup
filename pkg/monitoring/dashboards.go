package monitoring

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// dashboardFiles holds the Grafana dashboards shipped with mup.
//
//go:embed dashboards/*.json
var dashboardFiles embed.FS

const (
	dashboardsEmbedDir = "dashboards"
	dashboardFileMode  = 0o644
	dashboardDirMode   = 0o755
)

// writeDashboards installs every embedded dashboard into destDir and returns
// how many were written.
// LOCAL-027
func writeDashboards(destDir string) (int, error) {
	if err := os.MkdirAll(destDir, dashboardDirMode); err != nil {
		return 0, fmt.Errorf("failed to create dashboards directory: %w", err)
	}

	entries, err := dashboardFiles.ReadDir(dashboardsEmbedDir)
	if err != nil {
		return 0, fmt.Errorf("failed to list embedded dashboards: %w", err)
	}
	if len(entries) == 0 {
		return 0, fmt.Errorf("no dashboards are embedded in this build")
	}

	for _, entry := range entries {
		// embed.FS paths always use forward slashes
		data, err := dashboardFiles.ReadFile(dashboardsEmbedDir + "/" + entry.Name())
		if err != nil {
			return 0, fmt.Errorf("failed to read dashboard %s: %w", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(destDir, entry.Name()), data, dashboardFileMode); err != nil {
			return 0, fmt.Errorf("failed to write dashboard %s: %w", entry.Name(), err)
		}
	}
	return len(entries), nil
}
