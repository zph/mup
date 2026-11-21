package monitoring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LOCAL-027: dashboards ship inside the binary, so they are installed no
// matter which directory mup runs from.
func TestWriteDashboards_WorksOutsideTheRepository(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "dashboards")
	t.Chdir(t.TempDir())

	written, err := writeDashboards(dest)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, written, 5)
	for _, name := range []string{"mongodb-overview.json", "system-overview.json"} {
		data, err := os.ReadFile(filepath.Join(dest, name))
		require.NoError(t, err)
		assert.NotEmpty(t, data)
	}
}
