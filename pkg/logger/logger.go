package logger

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// LogLevel represents the logging level
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

var currentLevel = LevelInfo

func init() {
	// Read LOG_LEVEL from environment
	levelStr := strings.ToLower(os.Getenv("LOG_LEVEL"))
	switch levelStr {
	case "debug":
		currentLevel = LevelDebug
	case "info":
		currentLevel = LevelInfo
	case "warn", "warning":
		currentLevel = LevelWarn
	case "error":
		currentLevel = LevelError
	}
}

// Debug logs a debug message
func Debug(format string, args ...interface{}) {
	if currentLevel <= LevelDebug {
		timestamp := time.Now().Format("15:04:05.000")
		msg := fmt.Sprintf(format, args...)
		fmt.Printf("[DEBUG] [%s] %s\n", timestamp, msg)
	}
}

// Info logs an info message
func Info(format string, args ...interface{}) {
	if currentLevel <= LevelInfo {
		timestamp := time.Now().Format("15:04:05.000")
		msg := fmt.Sprintf(format, args...)
		fmt.Printf("[INFO] [%s] %s\n", timestamp, msg)
	}
}

// Warn logs a warning message
func Warn(format string, args ...interface{}) {
	if currentLevel <= LevelWarn {
		timestamp := time.Now().Format("15:04:05.000")
		msg := fmt.Sprintf(format, args...)
		fmt.Printf("[WARN] [%s] %s\n", timestamp, msg)
	}
}

// Error logs an error message
func Error(format string, args ...interface{}) {
	if currentLevel <= LevelError {
		timestamp := time.Now().Format("15:04:05.000")
		msg := fmt.Sprintf(format, args...)
		fmt.Printf("[ERROR] [%s] %s\n", timestamp, msg)
	}
}

// IsDebug returns true if debug logging is enabled
func IsDebug() bool {
	return currentLevel <= LevelDebug
}
