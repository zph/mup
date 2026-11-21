package template

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LOCAL-009: Percona-style versions (X.Y.Z-R) select the same template as the
// upstream X.Y.Z release.
func TestSelectTemplateVersion(t *testing.T) {
	tests := []struct {
		mongoVersion string
		want         string
	}{
		{"8.0.12", "7.0"},
		{"8.0.12-4", "7.0"},
		{"7.0.5-4", "7.0"},
		{"7.0", "7.0"},
		{"6.0.4-3", "5.0"},
		{"5.0.9", "5.0"},
		{"4.4.29-28", "4.2"},
		{"4.2.0", "4.2"},
		{"4.0.28", "3.6"},
		{"3.6.23-13.0", "3.6"},
	}

	m, err := NewManager()
	require.NoError(t, err)
	for _, tt := range tests {
		t.Run(tt.mongoVersion, func(t *testing.T) {
			assert.Equal(t, tt.want, m.selectTemplateVersion(tt.mongoVersion))
		})
	}
}

// LOCAL-009
func TestSupportsJournalEnabled(t *testing.T) {
	m, err := NewManager()
	require.NoError(t, err)

	assert.True(t, m.supportsJournalEnabled("6.0.4-3"))
	assert.True(t, m.supportsJournalEnabled("4.4.29"))
	assert.False(t, m.supportsJournalEnabled("6.1.0"))
	assert.False(t, m.supportsJournalEnabled("7.0.5-4"))
	assert.False(t, m.supportsJournalEnabled("8.0.12"))
}
