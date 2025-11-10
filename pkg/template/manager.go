package template

import (
	"embed"
	"fmt"
	"text/template"

	"github.com/hashicorp/go-version"
)

//go:embed mongod/*.tmpl mongos/*.tmpl config/*.tmpl
var templates embed.FS

type Manager struct {
	// Templates are loaded on-demand in GetTemplate() with version-specific functions
}

// NewManager creates a new template manager
func NewManager() (*Manager, error) {
	return &Manager{}, nil
}

// GetTemplate returns the appropriate template for a node type and version
func (m *Manager) GetTemplate(nodeType string, mongoVersion string) (*template.Template, error) {
	templateVersion := m.selectTemplateVersion(mongoVersion)
	templateName := fmt.Sprintf("%s-%s.conf.tmpl", nodeType, templateVersion)

	// Get the template content
	dirs := []string{"mongod", "mongos", "config"}
	var content []byte
	var err error
	for _, dir := range dirs {
		path := fmt.Sprintf("%s/%s", dir, templateName)
		content, err = templates.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("template not found: %s", templateName)
	}

	// Create new template with version-specific functions
	tmpl := template.New(templateName).Funcs(template.FuncMap{
		"supportsJournalEnabled": func() bool {
			return m.supportsJournalEnabled(mongoVersion)
		},
	})

	// Parse the template content
	tmpl, err = tmpl.Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse template %s: %w", templateName, err)
	}

	return tmpl, nil
}

// supportsJournalEnabled checks if the MongoDB version supports storage.journal.enabled
// This option was removed in MongoDB 6.1+
func (m *Manager) supportsJournalEnabled(mongoVersion string) bool {
	v, err := version.NewVersion(mongoVersion)
	if err != nil {
		// If version parsing fails, assume it's a newer version that doesn't support it
		return false
	}

	// Check if version is < 6.1
	constraint, err := version.NewConstraint("< 6.1")
	if err != nil {
		return false
	}

	return constraint.Check(v)
}

// selectTemplateVersion maps MongoDB version to template version
func (m *Manager) selectTemplateVersion(mongoVersion string) string {
	v, err := version.NewVersion(mongoVersion)
	if err != nil {
		// Default to latest if version parsing fails
		return "7.0"
	}

	// Define version constraints
	constraints := []struct {
		constraint string
		template   string
	}{
		{">= 7.0", "7.0"},
		{">= 5.0, < 7.0", "5.0"},
		{">= 4.2, < 5.0", "4.2"},
		{"< 4.2", "3.6"},
	}

	for _, c := range constraints {
		constraint, err := version.NewConstraint(c.constraint)
		if err != nil {
			continue
		}

		if constraint.Check(v) {
			return c.template
		}
	}

	// Default to oldest supported version
	return "3.6"
}
