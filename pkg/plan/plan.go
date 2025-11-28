package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/zph/mup/pkg/topology"
)

// Plan represents a complete operation plan
type Plan struct {
	// Identity
	PlanID      string    `json:"plan_id"`      // UUID for this plan
	Operation   string    `json:"operation"`    // "deploy", "upgrade", "import", "start", etc.
	ClusterName string    `json:"cluster_name"`
	CreatedAt   time.Time `json:"created_at"`

	// Input Configuration
	Version      string                 `json:"version,omitempty"`
	Variant      string                 `json:"variant,omitempty"`
	TopologyFile string                 `json:"topology_file,omitempty"`
	Topology     *topology.Topology     `json:"topology,omitempty"`
	Config       map[string]interface{} `json:"config,omitempty"` // Operation-specific config

	// Validation Results
	Validation ValidationResult `json:"validation"`

	// Execution Plan
	Phases []PlannedPhase `json:"phases"`

	// Resource Estimates
	Resources ResourceEstimate `json:"resources"`

	// Metadata
	DryRun      bool              `json:"dry_run"`
	Environment map[string]string `json:"environment,omitempty"`
}

// PlannedPhase represents one phase of execution
type PlannedPhase struct {
	Name              string              `json:"name"`        // "prepare", "deploy", "initialize"
	Description       string              `json:"description"`
	Order             int                 `json:"order"`
	Operations        []PlannedOperation  `json:"operations"`
	BeforeHook        *Hook               `json:"before_hook,omitempty"`
	AfterHook         *Hook               `json:"after_hook,omitempty"`
	EstimatedDuration string              `json:"estimated_duration,omitempty"`
}

// PlannedOperation is a single atomic action
type PlannedOperation struct {
	ID            string           `json:"id"`           // Unique operation ID
	Type          OperationType    `json:"type"`         // "download_binary", "create_dir", etc.
	Description   string           `json:"description"`  // Human-readable
	Target        OperationTarget  `json:"target"`       // What/where
	PreConditions []SafetyCheck    `json:"pre_conditions,omitempty"`
	Changes       []Change         `json:"changes"`
	DependsOn     []string         `json:"depends_on,omitempty"` // Operation IDs
	Parallel      bool             `json:"parallel"`             // Can run in parallel with siblings
	Params        map[string]interface{} `json:"params,omitempty"` // Operation-specific parameters
}

// OperationType enumerates all operation types
type OperationType string

const (
	OpDownloadBinary   OperationType = "download_binary"
	OpCopyBinary       OperationType = "copy_binary"
	OpCreateDirectory       OperationType = "create_directory"
	OpCreateSymlink         OperationType = "create_symlink"
	OpUploadFile            OperationType = "upload_file"
	OpGenerateConfig        OperationType = "generate_config"
	OpGenerateSupervisorCfg OperationType = "generate_supervisor_config"
	OpStartSupervisor  OperationType = "start_supervisor"
	OpStartProcess     OperationType = "start_process"
	OpWaitForProcess   OperationType = "wait_for_process"
	OpInitReplicaSet   OperationType = "init_replica_set"
	OpAddShard         OperationType = "add_shard"
	OpVerifyHealth     OperationType = "verify_health"
	OpSaveMetadata     OperationType = "save_metadata"
	OpStopProcess      OperationType = "stop_process"
	OpRemoveDirectory  OperationType = "remove_directory"
	OpBackupData       OperationType = "backup_data"
	OpRestoreData      OperationType = "restore_data"
	OpSetFCV           OperationType = "set_fcv"
	OpWaitForReady     OperationType = "wait_for_ready"
	OpDrainNode        OperationType = "drain_node"
	OpImportData       OperationType = "import_data"
	OpValidateData     OperationType = "validate_data"
)

// OperationTarget describes what the operation acts on
type OperationTarget struct {
	Type   string            `json:"type"` // "host", "process", "replica_set", "cluster"
	Name   string            `json:"name"` // Identifier
	Host   string            `json:"host,omitempty"`
	Port   int               `json:"port,omitempty"`
	Params map[string]string `json:"params,omitempty"` // Target-specific parameters
}

// Change describes an expected state change
type Change struct {
	ResourceType string      `json:"resource_type"` // "file", "process", "port", "directory"
	ResourceID   string      `json:"resource_id"`
	Action       ActionType  `json:"action"` // "create", "update", "delete", "start", "stop"
	Before       interface{} `json:"before,omitempty"`
	After        interface{} `json:"after,omitempty"`
}

// ActionType represents the type of change
type ActionType string

const (
	ActionCreate ActionType = "create"
	ActionUpdate ActionType = "update"
	ActionDelete ActionType = "delete"
	ActionStart  ActionType = "start"
	ActionStop   ActionType = "stop"
	ActionNone   ActionType = "none"
)

// SafetyCheck is a runtime validation
type SafetyCheck struct {
	ID          string                 `json:"id"`
	Description string                 `json:"description"`
	CheckType   string                 `json:"check_type"` // "port_available", "disk_space", etc.
	Target      string                 `json:"target"`
	Params      map[string]interface{} `json:"params"`
	Required    bool                   `json:"required"` // Fail if check fails?
}

// ValidationResult aggregates all pre-flight checks
type ValidationResult struct {
	Valid    bool              `json:"valid"`
	Errors   []ValidationIssue `json:"errors"`
	Warnings []ValidationIssue `json:"warnings"`
	Checks   []CheckResult     `json:"checks"`
}

// ValidationIssue represents a validation problem
type ValidationIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Host     string `json:"host,omitempty"`
	Severity string `json:"severity"` // "error", "warning", "info"
}

// CheckResult represents the result of a single check
type CheckResult struct {
	Name     string        `json:"name"`
	Status   string        `json:"status"` // "passed", "failed", "warning"
	Message  string        `json:"message"`
	Host     string        `json:"host,omitempty"`
	Duration time.Duration `json:"duration"`
	Details  interface{}   `json:"details,omitempty"`
}

// ResourceEstimate shows expected resource usage
type ResourceEstimate struct {
	Hosts            int            `json:"hosts"`
	TotalProcesses   int            `json:"total_processes"`
	PortsUsed        []int          `json:"ports_used"`
	DiskSpaceGB      float64        `json:"disk_space_gb"`
	MemoryMB         int            `json:"memory_mb,omitempty"`
	DownloadSizeMB   int            `json:"download_size_mb,omitempty"`
	ProcessesPerHost map[string]int `json:"processes_per_host"`
}

// Hook represents a user-defined script
type Hook struct {
	Name            string            `json:"name"`
	Command         string            `json:"command"` // Shell command to execute
	Timeout         time.Duration     `json:"timeout"`
	Environment     map[string]string `json:"environment,omitempty"`
	ContinueOnError bool              `json:"continue_on_error"`
}

// HookEvent represents when hooks can run
type HookEvent string

const (
	// Plan/Apply lifecycle hooks
	HookBeforePlan  HookEvent = "before_plan"
	HookAfterPlan   HookEvent = "after_plan"
	HookBeforeApply HookEvent = "before_apply"
	HookAfterApply  HookEvent = "after_apply"

	// Phase hooks
	HookBeforePhase HookEvent = "before_phase"
	HookAfterPhase  HookEvent = "after_phase"

	// Operation hooks
	HookBeforeOperation HookEvent = "before_operation"
	HookAfterOperation  HookEvent = "after_operation"

	// Status hooks
	HookOnError   HookEvent = "on_error"
	HookOnSuccess HookEvent = "on_success"
)

// SaveToFile serializes the plan to a JSON file
func (p *Plan) SaveToFile(path string) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadFromFile deserializes a plan from a JSON file
func LoadFromFile(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}

	return &plan, nil
}

// IsValid returns whether the plan passed validation
func (p *Plan) IsValid() bool {
	return p.Validation.Valid
}

// HasErrors returns whether the plan has validation errors
func (p *Plan) HasErrors() bool {
	return len(p.Validation.Errors) > 0
}

// HasWarnings returns whether the plan has validation warnings
func (p *Plan) HasWarnings() bool {
	return len(p.Validation.Warnings) > 0
}

// TotalOperations returns the total number of operations across all phases
func (p *Plan) TotalOperations() int {
	total := 0
	for _, phase := range p.Phases {
		total += len(phase.Operations)
	}
	return total
}

// GetOperationByID finds an operation by its ID
func (p *Plan) GetOperationByID(id string) *PlannedOperation {
	for _, phase := range p.Phases {
		for i := range phase.Operations {
			if phase.Operations[i].ID == id {
				return &phase.Operations[i]
			}
		}
	}
	return nil
}

// GetPhaseByName finds a phase by its name
func (p *Plan) GetPhaseByName(name string) *PlannedPhase {
	for i := range p.Phases {
		if p.Phases[i].Name == name {
			return &p.Phases[i]
		}
	}
	return nil
}

// Summary returns a human-readable summary of the plan
func (p *Plan) Summary() string {
	var s string
	s += "==================================================\n"
	s += "                  DEPLOYMENT PLAN                 \n"
	s += "==================================================\n\n"
	s += fmt.Sprintf("Plan ID:      %s\n", p.PlanID)
	s += fmt.Sprintf("Operation:    %s\n", p.Operation)
	s += fmt.Sprintf("Cluster:      %s\n", p.ClusterName)
	s += fmt.Sprintf("Version:      %s (%s)\n", p.Version, p.Variant)
	s += fmt.Sprintf("Created:      %s\n\n", p.CreatedAt.Format(time.RFC3339))

	s += "VALIDATION:\n"
	if p.Validation.Valid {
		s += "  ✓ All pre-flight checks passed\n"
	} else {
		s += "  ✗ Pre-flight checks failed\n"
		for _, err := range p.Validation.Errors {
			s += fmt.Sprintf("    • %s: %s\n", err.Code, err.Message)
		}
	}
	if len(p.Validation.Warnings) > 0 {
		s += "\n  Warnings:\n"
		for _, warn := range p.Validation.Warnings {
			s += fmt.Sprintf("    ⚠ %s: %s\n", warn.Code, warn.Message)
		}
	}
	s += "\n"

	s += "PHASES:\n"
	for i, phase := range p.Phases {
		s += fmt.Sprintf("  %d. %s (%d operations)\n", i+1, phase.Name, len(phase.Operations))
		if phase.Description != "" {
			s += fmt.Sprintf("     %s\n", phase.Description)
		}
		if phase.EstimatedDuration != "" {
			s += fmt.Sprintf("     Est. duration: %s\n", phase.EstimatedDuration)
		}
	}
	s += "\n"

	s += "SUMMARY:\n"
	s += fmt.Sprintf("  Total operations: %d\n", p.TotalOperations())
	s += fmt.Sprintf("  Total phases:     %d\n", len(p.Phases))
	s += fmt.Sprintf("  Estimated time:   %s\n", p.EstimatedDuration())
	s += "\n"

	return s
}

// EstimatedDuration calculates total estimated duration from phases
func (p *Plan) EstimatedDuration() string {
	// For now, just return a placeholder
	// In the future, we can parse phase durations and sum them
	return "5-10 minutes"
}
