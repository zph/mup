package naming

import "fmt"

// GetProgramName returns the supervisor program name for a MongoDB node.
// This name is used by supervisor to identify and control the process.
//
// Examples:
//   - GetProgramName("config", 30000) returns "config-30000"
//   - GetProgramName("mongod", 30100) returns "mongod-30100"
//   - GetProgramName("mongos", 30300) returns "mongos-30300"
func GetProgramName(nodeType string, port int) string {
	return fmt.Sprintf("%s-%d", nodeType, port)
}

// GetConfigFileName returns the configuration filename for a MongoDB node type.
// Different node types use different configuration file names.
//
// Examples:
//   - GetConfigFileName("config") returns "config.conf"
//   - GetConfigFileName("mongod") returns "mongod.conf"
//   - GetConfigFileName("mongos") returns "mongos.conf"
func GetConfigFileName(nodeType string) string {
	return fmt.Sprintf("%s.conf", nodeType)
}

// GetProcessDir returns the process directory name for a MongoDB node.
// This directory contains the node's data, logs, and configuration files.
// Currently, it returns the same format as GetProgramName.
//
// Examples:
//   - GetProcessDir("config", 30000) returns "config-30000"
//   - GetProcessDir("mongod", 30100) returns "mongod-30100"
//   - GetProcessDir("mongos", 30300) returns "mongos-30300"
func GetProcessDir(nodeType string, port int) string {
	return fmt.Sprintf("%s-%d", nodeType, port)
}

// GetLogFileName returns the log filename for any MongoDB node type.
// All node types use the same generic log file name; the parent directory
// provides the node type context.
//
// Example:
//   - GetLogFileName() returns "process.log"
func GetLogFileName() string {
	return "process.log"
}

// GetPIDFileName returns the PID filename for any MongoDB node type.
// All node types use the same generic PID file name; the parent directory
// provides the node type context.
//
// Example:
//   - GetPIDFileName() returns "process.pid"
func GetPIDFileName() string {
	return "process.pid"
}
