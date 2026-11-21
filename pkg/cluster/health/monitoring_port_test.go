package health

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// LOCAL-028: monitoring ports come from the cluster's recorded URLs.
func TestPortFromURL(t *testing.T) {
	const fallback = 8428

	assert.Equal(t, 9428, portFromURL("http://localhost:9428", fallback))
	assert.Equal(t, 3100, portFromURL("http://127.0.0.1:3100/d/overview", fallback))
	assert.Equal(t, fallback, portFromURL("http://localhost", fallback))
	assert.Equal(t, fallback, portFromURL("not a url", fallback))
	assert.Equal(t, fallback, portFromURL("", fallback))
}
