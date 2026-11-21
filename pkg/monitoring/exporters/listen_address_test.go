package exporters

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// LOCAL-037: the exporter bind address is a setting, not a literal.
func TestExporterListenAddress(t *testing.T) {
	node := &NodeExporterManager{}
	assert.Contains(t, node.GenerateSupervisorConfig("node", "localhost", 9100, "/bin/node_exporter", "/tmp/n.log"),
		"--web.listen-address="+DefaultListenAddress+":9100")

	node.SetListenAddress("127.0.0.1")
	assert.Contains(t, node.GenerateSupervisorConfig("node", "localhost", 9100, "/bin/node_exporter", "/tmp/n.log"),
		"--web.listen-address=127.0.0.1:9100")

	mongodb := &MongoDBExporterManager{}
	assert.Contains(t, mongodb.GenerateSupervisorConfig("mdb", "localhost", 9216, 27017, "/bin/mongodb_exporter", "/tmp/m.log"),
		"--web.listen-address="+DefaultListenAddress+":9216")

	mongodb.SetListenAddress("127.0.0.1")
	assert.Contains(t, mongodb.GenerateSupervisorConfig("mdb", "localhost", 9216, 27017, "/bin/mongodb_exporter", "/tmp/m.log"),
		"--web.listen-address=127.0.0.1:9216")
}
