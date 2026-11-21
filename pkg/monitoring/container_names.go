package monitoring

import (
	"regexp"
	"strings"
)

const containerNamePrefix = "mup-"

// containerNameUnsafe matches characters container runtimes reject in names.
var containerNameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// ContainerName returns the container name for a monitoring component of a
// cluster, for example "mup-playground-grafana". With no cluster name it
// returns the legacy shared name unchanged.
// LOCAL-036
func ContainerName(baseName, clusterName string) string {
	if clusterName == "" {
		return baseName
	}
	component := strings.TrimPrefix(baseName, containerNamePrefix)
	return containerNamePrefix + containerNameUnsafe.ReplaceAllString(clusterName, "-") + "-" + component
}
