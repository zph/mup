package importer

import (
	"fmt"
	"regexp"
	"strings"
)

// SystemdUnit represents a parsed systemd unit file
type SystemdUnit struct {
	Description     string
	Documentation   string
	User            string
	Group           string
	ExecStart       string
	ExecStartPre    []string
	Environment     []string
	EnvironmentFile string
	PIDFile         string
	Type            string
	Restart         string
	ConfigPath      string            // Extracted from ExecStart --config or -f
	Params          map[string]string // Additional parameters from ExecStart
}

// SystemdParser parses systemd unit files
type SystemdParser struct{}

// ParseUnit parses a systemd unit file content
// IMP-006: Parse systemd unit file to extract MongoDB configuration
func (p *SystemdParser) ParseUnit(content string) (*SystemdUnit, error) {
	unit := &SystemdUnit{
		ExecStartPre: []string{},
		Environment:  []string{},
		Params:       make(map[string]string),
	}

	lines := strings.Split(content, "\n")
	currentSection := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Check for section headers
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[]")
			continue
		}

		// Parse key=value pairs
		if currentSection == "Unit" {
			p.parseUnitSection(line, unit)
		} else if currentSection == "Service" {
			p.parseServiceSection(line, unit)
		}
	}

	// IMP-007: Extract config path from ExecStart
	if unit.ExecStart != "" {
		p.extractConfigPath(unit)
		unit.Params = p.extractExecStartParams(unit.ExecStart)
	}

	return unit, nil
}

// parseUnitSection parses the [Unit] section
func (p *SystemdParser) parseUnitSection(line string, unit *SystemdUnit) {
	if strings.HasPrefix(line, "Description=") {
		unit.Description = strings.TrimPrefix(line, "Description=")
	} else if strings.HasPrefix(line, "Documentation=") {
		unit.Documentation = strings.TrimPrefix(line, "Documentation=")
	}
}

// parseServiceSection parses the [Service] section
func (p *SystemdParser) parseServiceSection(line string, unit *SystemdUnit) {
	if strings.HasPrefix(line, "User=") {
		unit.User = strings.TrimPrefix(line, "User=")
	} else if strings.HasPrefix(line, "Group=") {
		unit.Group = strings.TrimPrefix(line, "Group=")
	} else if strings.HasPrefix(line, "ExecStart=") {
		unit.ExecStart = strings.TrimPrefix(line, "ExecStart=")
	} else if strings.HasPrefix(line, "ExecStartPre=") {
		cmd := strings.TrimPrefix(line, "ExecStartPre=")
		unit.ExecStartPre = append(unit.ExecStartPre, cmd)
	} else if strings.HasPrefix(line, "Environment=") {
		p.parseEnvironmentLine(line, unit)
	} else if strings.HasPrefix(line, "EnvironmentFile=") {
		unit.EnvironmentFile = strings.TrimPrefix(line, "EnvironmentFile=")
		// Remove leading dash (means optional file)
		unit.EnvironmentFile = strings.TrimPrefix(unit.EnvironmentFile, "-")
	} else if strings.HasPrefix(line, "PIDFile=") {
		unit.PIDFile = strings.TrimPrefix(line, "PIDFile=")
	} else if strings.HasPrefix(line, "Type=") {
		unit.Type = strings.TrimPrefix(line, "Type=")
	} else if strings.HasPrefix(line, "Restart=") {
		unit.Restart = strings.TrimPrefix(line, "Restart=")
	}
}

// parseEnvironmentLine parses an Environment= line
func (p *SystemdParser) parseEnvironmentLine(line string, unit *SystemdUnit) {
	envValue := strings.TrimPrefix(line, "Environment=")
	// Remove quotes if present
	envValue = strings.Trim(envValue, `"`)
	unit.Environment = append(unit.Environment, envValue)
}

// extractConfigPath extracts the MongoDB config file path from ExecStart
// IMP-007: Extract config paths from ExecStart directive
func (p *SystemdParser) extractConfigPath(unit *SystemdUnit) {
	if unit.ExecStart == "" {
		return
	}

	// Try --config /path or -f /path
	configRegex := regexp.MustCompile(`(?:--config|-f)(?:=|\s+)([^\s]+)`)
	matches := configRegex.FindStringSubmatch(unit.ExecStart)
	if len(matches) > 1 {
		unit.ConfigPath = matches[1]
	}
}

// extractExecStartParams extracts all parameters from ExecStart command
func (p *SystemdParser) extractExecStartParams(execStart string) map[string]string {
	params := make(map[string]string)

	// Match --param value or --param=value
	paramRegex := regexp.MustCompile(`--([a-zA-Z_]+)(?:=|\s+)([^\s]+)`)
	matches := paramRegex.FindAllStringSubmatch(execStart, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			paramName := match[1]
			paramValue := match[2]
			params[paramName] = paramValue
		}
	}

	// Also handle short form -f
	shortFormRegex := regexp.MustCompile(`-([a-z])\s+([^\s]+)`)
	shortMatches := shortFormRegex.FindAllStringSubmatch(execStart, -1)

	for _, match := range shortMatches {
		if len(match) >= 3 {
			paramName := match[1]
			paramValue := match[2]
			// Map short forms to long forms
			if paramName == "f" {
				params["config"] = paramValue
			}
		}
	}

	return params
}

// ParseUnitFile reads and parses a systemd unit file from disk
func (p *SystemdParser) ParseUnitFile(unitFilePath string, content string) (*SystemdUnit, error) {
	if content == "" {
		return nil, fmt.Errorf("empty unit file content")
	}

	return p.ParseUnit(content)
}

// DetectMongoDBParams detects MongoDB-specific parameters from the systemd unit
func (p *SystemdParser) DetectMongoDBParams(unit *SystemdUnit) (MongoInstance, error) {
	instance := MongoInstance{
		ConfigFile: unit.ConfigPath,
	}

	// Extract params from ExecStart if no config file
	if instance.ConfigFile == "" && unit.Params != nil {
		if port, ok := unit.Params["port"]; ok {
			instance.Port = parseInt(port, 27017)
		} else {
			instance.Port = 27017 // Default MongoDB port
		}

		if dbpath, ok := unit.Params["dbpath"]; ok {
			instance.DataDir = dbpath
		}

		if logpath, ok := unit.Params["logpath"]; ok {
			instance.LogPath = logpath
		}

		if bindIP, ok := unit.Params["bind_ip"]; ok {
			instance.Host = bindIP
			if instance.Host == "0.0.0.0" || instance.Host == "::" {
				instance.Host = "localhost"
			}
		} else {
			instance.Host = "localhost"
		}
	}

	return instance, nil
}

// parseInt safely converts string to int with fallback
func parseInt(s string, fallback int) int {
	var result int
	_, err := fmt.Sscanf(s, "%d", &result)
	if err != nil {
		return fallback
	}
	return result
}
