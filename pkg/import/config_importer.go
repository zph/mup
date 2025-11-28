package importer

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zph/mup/pkg/template"
)

// ConfigImporter handles importing and converting MongoDB configurations
type ConfigImporter struct{}

// ConfigPaths contains mup-specific paths for config generation
type ConfigPaths struct {
	DataDir string
	LogPath string
}

// ParseMongoConfig parses a MongoDB YAML config file
// IMP-010: Parse existing mongod.conf files
func (ci *ConfigImporter) ParseMongoConfig(configYAML string) (*template.MongodConfig, error) {
	config := &template.MongodConfig{}

	// Parse YAML
	if err := yaml.Unmarshal([]byte(configYAML), config); err != nil {
		return nil, fmt.Errorf("failed to parse MongoDB config: %w", err)
	}

	return config, nil
}

// ParseMongosConfig parses a mongos YAML config file
func (ci *ConfigImporter) ParseMongosConfig(configYAML string) (*template.MongosConfig, error) {
	config := &template.MongosConfig{}

	// Parse YAML
	if err := yaml.Unmarshal([]byte(configYAML), config); err != nil {
		return nil, fmt.Errorf("failed to parse mongos config: %w", err)
	}

	return config, nil
}

// GenerateMupConfig generates a mup-compatible config from an existing config
// IMP-011: Generate mup-compatible configuration files
func (ci *ConfigImporter) GenerateMupConfig(existing *template.MongodConfig, paths ConfigPaths) *template.MongodConfig {
	// Create new config based on existing
	mupConfig := *existing

	// IMP-011: Update paths to mup's structure
	mupConfig.Storage.DBPath = paths.DataDir
	mupConfig.SystemLog.Path = paths.LogPath

	// Supervisor manages processes, so disable fork
	// IMP-032: Set fork: false for supervisor management
	mupConfig.ProcessManagement.Fork = false
	mupConfig.ProcessManagement.PIDFilePath = "" // Supervisor tracks PIDs

	// Ensure systemLog destination is file
	if mupConfig.SystemLog.Destination == "" {
		mupConfig.SystemLog.Destination = "file"
	}

	// Default logAppend to true
	if !mupConfig.SystemLog.LogAppend {
		mupConfig.SystemLog.LogAppend = true
	}

	return &mupConfig
}

// GenerateMupMongosConfig generates a mup-compatible mongos config
func (ci *ConfigImporter) GenerateMupMongosConfig(existing *template.MongosConfig, logPath string) *template.MongosConfig {
	// Create new config based on existing
	mupConfig := *existing

	// Update log path
	mupConfig.SystemLog.Path = logPath

	// Supervisor manages processes
	mupConfig.ProcessManagement.Fork = false
	mupConfig.ProcessManagement.PIDFilePath = ""

	// Ensure systemLog destination is file
	if mupConfig.SystemLog.Destination == "" {
		mupConfig.SystemLog.Destination = "file"
	}

	if !mupConfig.SystemLog.LogAppend {
		mupConfig.SystemLog.LogAppend = true
	}

	return &mupConfig
}

// ParseLegacyConfig parses legacy (pre-2.6) MongoDB config format
// Legacy format uses key=value instead of YAML
func (ci *ConfigImporter) ParseLegacyConfig(configContent string) (*template.MongodConfig, error) {
	config := &template.MongodConfig{
		SystemLog: template.SystemLogConfig{
			Destination: "file",
			LogAppend:   true,
		},
		ProcessManagement: template.ProcessManagementConfig{},
		Storage: template.StorageConfig{
			Journal: template.JournalConfig{
				Enabled: true,
			},
		},
	}

	scanner := bufio.NewScanner(strings.NewReader(configContent))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Map legacy keys to modern structure
		switch key {
		case "port":
			if port, err := strconv.Atoi(value); err == nil {
				config.Net.Port = port
			}
		case "bind_ip", "bindIp":
			config.Net.BindIP = value
		case "dbpath":
			config.Storage.DBPath = value
		case "logpath":
			config.SystemLog.Path = value
		case "fork":
			config.ProcessManagement.Fork = (value == "true" || value == "1")
		case "pidfilepath":
			config.ProcessManagement.PIDFilePath = value
		case "replSet":
			if config.Replication == nil {
				config.Replication = &template.ReplicationConfig{}
			}
			config.Replication.ReplSetName = value
		case "journal":
			config.Storage.Journal.Enabled = (value == "true" || value == "1")
		case "storageEngine":
			config.Storage.Engine = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan legacy config: %w", err)
	}

	return config, nil
}

// MergeCustomSettings merges custom settings from existing config
// IMP-012: Preserve custom MongoDB settings not in default templates
func (ci *ConfigImporter) MergeCustomSettings(base, custom *template.MongodConfig) *template.MongodConfig {
	merged := *base

	// IMP-012: Preserve SetParameter settings (custom runtime parameters)
	if custom.SetParameter != nil && len(custom.SetParameter) > 0 {
		if merged.SetParameter == nil {
			merged.SetParameter = make(map[string]interface{})
		}
		for k, v := range custom.SetParameter {
			merged.SetParameter[k] = v
		}
	}

	// Preserve operation profiling if set
	if custom.OperationProfiling != nil {
		merged.OperationProfiling = custom.OperationProfiling
	}

	// Preserve security settings if present
	if custom.Security != nil {
		merged.Security = custom.Security
	}

	// Preserve TLS settings if present
	if custom.Net.TLS != nil {
		merged.Net.TLS = custom.Net.TLS
	}

	// Preserve WiredTiger settings if present
	if custom.Storage.WiredTiger != nil {
		// Don't overwrite, merge specific fields
		if merged.Storage.WiredTiger == nil {
			merged.Storage.WiredTiger = &template.WiredTigerConfig{}
		}

		if custom.Storage.WiredTiger.EngineConfig.CacheSizeGB > 0 {
			merged.Storage.WiredTiger.EngineConfig.CacheSizeGB = custom.Storage.WiredTiger.EngineConfig.CacheSizeGB
		}

		if custom.Storage.WiredTiger.CollectionConfig.BlockCompressor != "" {
			merged.Storage.WiredTiger.CollectionConfig.BlockCompressor = custom.Storage.WiredTiger.CollectionConfig.BlockCompressor
		}
	}

	// Preserve max incoming connections if set
	if custom.Net.MaxIncomingConnections > 0 {
		merged.Net.MaxIncomingConnections = custom.Net.MaxIncomingConnections
	}

	// Preserve oplog size if set
	if custom.Replication != nil && custom.Replication.OplogSizeMB > 0 {
		if merged.Replication == nil {
			merged.Replication = &template.ReplicationConfig{}
		}
		merged.Replication.OplogSizeMB = custom.Replication.OplogSizeMB
	}

	return &merged
}

// DetectConfigFormat detects whether config is YAML or legacy format
func (ci *ConfigImporter) DetectConfigFormat(configContent string) string {
	// Check for YAML indicators
	lines := strings.Split(configContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// YAML typically has "key:" format
		if strings.Contains(line, ":") && !strings.Contains(line, "=") {
			return "yaml"
		}

		// Legacy has "key=value" format
		if strings.Contains(line, "=") && !strings.Contains(line, ":") {
			return "legacy"
		}
	}

	// Default to YAML (modern format)
	return "yaml"
}

// ImportConfig is the main entry point for importing a config file
// IMP-010: Parse existing config, IMP-011: Generate mup config, IMP-012: Preserve custom settings
func (ci *ConfigImporter) ImportConfig(existingConfigContent string, nodeType string, paths ConfigPaths) (*template.MongodConfig, error) {
	// Detect format
	format := ci.DetectConfigFormat(existingConfigContent)

	var existingConfig *template.MongodConfig
	var err error

	// Parse existing config
	if format == "yaml" {
		if nodeType == "mongos" {
			// For mongos, parse as MongosConfig (not implemented in this function yet)
			return nil, fmt.Errorf("mongos config import not yet supported in this function")
		}
		existingConfig, err = ci.ParseMongoConfig(existingConfigContent)
	} else {
		existingConfig, err = ci.ParseLegacyConfig(existingConfigContent)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to parse existing config: %w", err)
	}

	// Generate mup-compatible config
	mupConfig := ci.GenerateMupConfig(existingConfig, paths)

	// IMP-012: Merge custom settings from existing config
	finalConfig := ci.MergeCustomSettings(mupConfig, existingConfig)

	return finalConfig, nil
}
