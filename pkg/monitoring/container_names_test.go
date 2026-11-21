package monitoring

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LOCAL-036
func TestContainerName(t *testing.T) {
	assert.Equal(t, "mup-playground-grafana", ContainerName(GrafanaContainerName, "playground"))
	assert.Equal(t, "mup-my_rs.1-victoria-metrics", ContainerName(VictoriaMetricsContainerName, "my_rs.1"))
	assert.Equal(t, "mup-a-b-c-grafana", ContainerName(GrafanaContainerName, "a/b c"))
	assert.Equal(t, GrafanaContainerName, ContainerName(GrafanaContainerName, ""), "no cluster name keeps the legacy name")
	assert.NotEqual(t,
		ContainerName(GrafanaContainerName, "cluster-a"),
		ContainerName(GrafanaContainerName, "cluster-b"))
}

// LOCAL-036: each cluster's monitoring containers get their own names.
func TestMonitoringContainersAreNamedPerCluster(t *testing.T) {
	vm, err := NewVictoriaMetricsManager(t.TempDir(), 8428, "30d")
	require.NoError(t, err)
	vm.SetContainerName(ContainerName(VictoriaMetricsContainerName, "alpha"))
	assert.Contains(t, vm.GenerateSupervisorConfig("/tmp/scrape.yaml"), "--name mup-alpha-victoria-metrics ")

	grafana, err := NewGrafanaManager(t.TempDir(), 3000, "admin")
	require.NoError(t, err)
	grafana.SetContainerName(ContainerName(GrafanaContainerName, "alpha"))
	assert.Contains(t, grafana.GenerateSupervisorConfig(), "--name mup-alpha-grafana ")
}
