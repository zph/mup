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
	templates map[string]*template.Template
}

// NewManager creates a new template manager
func NewManager() (*Manager, error) {
	m := &Manager{
		templates: make(map[string]*template.Template),
	}

	// Load all templates at initialization
	if err := m.loadTemplates(); err != nil {
		return nil, err
	}

	return m, nil
}

// GetTemplate returns the appropriate template for a node type and version
func (m *Manager) GetTemplate(nodeType string, mongoVersion string) (*template.Template, error) {
	templateVersion := m.selectTemplateVersion(mongoVersion)
	templateName := fmt.Sprintf("%s-%s.conf.tmpl", nodeType, templateVersion)

	tmpl, ok := m.templates[templateName]
	if !ok {
		return nil, fmt.Errorf("template not found: %s", templateName)
	}

	return tmpl, nil
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

func (m *Manager) loadTemplates() error {
	dirs := []string{"mongod", "mongos", "config"}

	for _, dir := range dirs {
		entries, err := templates.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("failed to read template dir %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			path := fmt.Sprintf("%s/%s", dir, entry.Name())
			content, err := templates.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read template %s: %w", path, err)
			}

			tmpl, err := template.New(entry.Name()).Parse(string(content))
			if err != nil {
				return fmt.Errorf("failed to parse template %s: %w", path, err)
			}

			m.templates[entry.Name()] = tmpl
		}
	}

	return nil
}
