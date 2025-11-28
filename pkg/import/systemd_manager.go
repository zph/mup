package importer

import (
	"fmt"
	"strings"

	"github.com/zph/mup/pkg/executor"
)

// SystemdManager handles systemd service management during import
type SystemdManager struct {
	executor         executor.Executor
	disabledServices []string // Track services we've disabled for rollback
}

// NewSystemdManager creates a new SystemdManager
func NewSystemdManager(exec executor.Executor) *SystemdManager {
	return &SystemdManager{
		executor:         exec,
		disabledServices: []string{},
	}
}

// DisableService disables a systemd service without stopping it
// IMP-008: Disable systemd services using systemctl disable
func (sm *SystemdManager) DisableService(serviceName string) error {
	normalizedName := sm.normalizeServiceName(serviceName)

	// Disable the service (prevents auto-start on boot)
	command := sm.generateDisableCommand(normalizedName)
	_, err := sm.executor.Execute(command)
	if err != nil {
		return fmt.Errorf("failed to disable systemd service %s: %w", normalizedName, err)
	}

	// Track disabled service for potential rollback
	sm.TrackDisabledService(normalizedName)

	return nil
}

// StopService stops a running systemd service
func (sm *SystemdManager) StopService(serviceName string) error {
	normalizedName := sm.normalizeServiceName(serviceName)

	command := sm.generateStopCommand(normalizedName)
	_, err := sm.executor.Execute(command)
	if err != nil {
		return fmt.Errorf("failed to stop systemd service %s: %w", normalizedName, err)
	}

	return nil
}

// DisableAndStopService disables and stops a systemd service
// IMP-008: Disable systemd services during import
func (sm *SystemdManager) DisableAndStopService(serviceName string) error {
	normalizedName := sm.normalizeServiceName(serviceName)

	// First disable to prevent auto-start
	if err := sm.DisableService(normalizedName); err != nil {
		return err
	}

	// Then stop the running service
	if err := sm.StopService(normalizedName); err != nil {
		// Service might not be running, which is fine
		// Don't return error, just log
	}

	return nil
}

// EnableService re-enables a systemd service
// IMP-009: Re-enable systemd services on rollback
func (sm *SystemdManager) EnableService(serviceName string) error {
	normalizedName := sm.normalizeServiceName(serviceName)

	command := sm.generateEnableCommand(normalizedName)
	_, err := sm.executor.Execute(command)
	if err != nil {
		return fmt.Errorf("failed to enable systemd service %s: %w", normalizedName, err)
	}

	return nil
}

// StartService starts a systemd service
func (sm *SystemdManager) StartService(serviceName string) error {
	normalizedName := sm.normalizeServiceName(serviceName)

	command := sm.generateStartCommand(normalizedName)
	_, err := sm.executor.Execute(command)
	if err != nil {
		return fmt.Errorf("failed to start systemd service %s: %w", normalizedName, err)
	}

	return nil
}

// EnableAndStartService re-enables and starts a systemd service
// IMP-009: Re-enable systemd services on failure (rollback)
func (sm *SystemdManager) EnableAndStartService(serviceName string) error {
	normalizedName := sm.normalizeServiceName(serviceName)

	// First enable
	if err := sm.EnableService(normalizedName); err != nil {
		return err
	}

	// Then start
	if err := sm.StartService(normalizedName); err != nil {
		// Starting might fail if service is already running, which is fine
		// Don't return error
	}

	return nil
}

// IsServiceActive checks if a systemd service is currently active/running
func (sm *SystemdManager) IsServiceActive(serviceName string) (bool, error) {
	normalizedName := sm.normalizeServiceName(serviceName)

	command := sm.generateStatusCommand(normalizedName)
	output, err := sm.executor.Execute(command)
	if err != nil {
		// Service is not active
		return false, nil
	}

	// systemctl is-active returns "active" if running
	return strings.TrimSpace(output) == "active", nil
}

// TrackDisabledService adds a service to the list of disabled services for rollback
func (sm *SystemdManager) TrackDisabledService(serviceName string) {
	normalizedName := sm.normalizeServiceName(serviceName)

	// Check if already tracked
	for _, svc := range sm.disabledServices {
		if svc == normalizedName {
			return
		}
	}

	sm.disabledServices = append(sm.disabledServices, normalizedName)
}

// GetDisabledServices returns the list of services that have been disabled
func (sm *SystemdManager) GetDisabledServices() []string {
	return sm.disabledServices
}

// ClearTrackedServices clears the list of tracked disabled services
func (sm *SystemdManager) ClearTrackedServices() {
	sm.disabledServices = []string{}
}

// RollbackAll re-enables and starts all services that were disabled
// IMP-009: Automatic rollback on import failure
func (sm *SystemdManager) RollbackAll() error {
	var errors []string

	for _, serviceName := range sm.disabledServices {
		if err := sm.EnableAndStartService(serviceName); err != nil {
			errors = append(errors, fmt.Sprintf("failed to rollback %s: %v", serviceName, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("rollback errors: %s", strings.Join(errors, "; "))
	}

	return nil
}

// GenerateRollbackCommands generates the list of commands needed for rollback
// Useful for dry-run or manual rollback
func (sm *SystemdManager) GenerateRollbackCommands() []SystemdCommand {
	var commands []SystemdCommand

	for _, serviceName := range sm.disabledServices {
		// Enable command
		commands = append(commands, SystemdCommand{
			Command: sm.generateEnableCommand(serviceName),
		})

		// Start command
		commands = append(commands, SystemdCommand{
			Command: sm.generateStartCommand(serviceName),
		})
	}

	return commands
}

// SystemdCommand represents a systemd command to execute
type SystemdCommand struct {
	Command string
}

// Contains checks if the command contains a substring
func (c *SystemdCommand) Contains(s string) bool {
	return strings.Contains(c.Command, s)
}

// Command generation methods

func (sm *SystemdManager) generateDisableCommand(serviceName string) string {
	return fmt.Sprintf("systemctl disable %s", serviceName)
}

func (sm *SystemdManager) generateEnableCommand(serviceName string) string {
	return fmt.Sprintf("systemctl enable %s", serviceName)
}

func (sm *SystemdManager) generateStopCommand(serviceName string) string {
	return fmt.Sprintf("systemctl stop %s", serviceName)
}

func (sm *SystemdManager) generateStartCommand(serviceName string) string {
	return fmt.Sprintf("systemctl start %s", serviceName)
}

func (sm *SystemdManager) generateStatusCommand(serviceName string) string {
	return fmt.Sprintf("systemctl is-active %s", serviceName)
}

// normalizeServiceName removes .service suffix if present
func (sm *SystemdManager) normalizeServiceName(serviceName string) string {
	// Remove .service suffix
	if strings.HasSuffix(serviceName, ".service") {
		return strings.TrimSuffix(serviceName, ".service")
	}
	return serviceName
}

// DisableMultipleServices disables multiple services in sequence
func (sm *SystemdManager) DisableMultipleServices(serviceNames []string) error {
	for _, serviceName := range serviceNames {
		if err := sm.DisableService(serviceName); err != nil {
			return fmt.Errorf("failed to disable service %s: %w", serviceName, err)
		}
	}
	return nil
}

// StopMultipleServices stops multiple services in sequence
func (sm *SystemdManager) StopMultipleServices(serviceNames []string) error {
	for _, serviceName := range serviceNames {
		if err := sm.StopService(serviceName); err != nil {
			// Continue even if one fails (service might not be running)
			continue
		}
	}
	return nil
}
