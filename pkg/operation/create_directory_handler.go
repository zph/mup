package operation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/plan"
)

// CreateDirectoryParams defines typed parameters for create_directory operation
// REQ-PES-002, REQ-PES-003: Typed parameter structs
type CreateDirectoryParams struct {
	Path string `json:"path" validate:"required"`
	Mode int    `json:"mode" validate:"required,min=0,max=0777"`
}

// CreateDirectoryHandlerV2 implements directory creation with mkdir -p behavior using four-phase pattern
// REQ-PES-036, REQ-PES-047, REQ-PES-048
type CreateDirectoryHandlerV2 struct{}

// NewCreateDirectoryHandlerV2 creates a new CreateDirectoryHandlerV2
func NewCreateDirectoryHandlerV2() *CreateDirectoryHandlerV2 {
	return &CreateDirectoryHandlerV2{}
}

// IsComplete checks if the directory already exists
// REQ-PES-036: Check if operation was already completed
func (h *CreateDirectoryHandlerV2) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	params, err := h.unmarshalParams(op.Params)
	if err != nil {
		return false, fmt.Errorf("unmarshal params: %w", err)
	}

	// Check if directory exists
	exists, err := exec.FileExists(params.Path)
	if err != nil {
		return false, fmt.Errorf("check directory exists: %w", err)
	}

	return exists, nil
}

// PreHook validates that we can create the directory
// REQ-PES-047: Pre-execution validation and user hooks
func (h *CreateDirectoryHandlerV2) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	params, err := h.unmarshalParams(op.Params)
	if err != nil {
		return nil, fmt.Errorf("unmarshal params: %w", err)
	}

	result := NewHookResult()

	// Validate path is not empty
	if params.Path == "" {
		result.AddError("path cannot be empty")
		return result, nil
	}

	// Validate mode is reasonable
	if params.Mode < 0 || params.Mode > 0777 {
		result.AddError(fmt.Sprintf("invalid mode: %o (must be 0-0777)", params.Mode))
		return result, nil
	}

	// Check if directory already exists (warning, not error - idempotent)
	exists, _ := exec.FileExists(params.Path)
	if exists {
		result.AddWarning(fmt.Sprintf("directory already exists: %s", params.Path))
	}

	// In real implementation, we might check:
	// - Parent directory is writable
	// - Sufficient disk space
	// - No file exists at this path (would conflict)

	return result, nil
}

// Execute creates the directory with mkdir -p behavior
// REQ-PES-013: Execute the operation idempotently
func (h *CreateDirectoryHandlerV2) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	params, err := h.unmarshalParams(op.Params)
	if err != nil {
		return nil, fmt.Errorf("unmarshal params: %w", err)
	}

	// CreateDirectory uses mkdir -p behavior (creates parents)
	mode := os.FileMode(params.Mode)
	if err := exec.CreateDirectory(params.Path, mode); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	result := &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Created directory: %s", params.Path),
		Changes: []plan.Change{
			{
				ResourceType: "directory",
				ResourceID:   params.Path,
				Action:       plan.ActionCreate,
				After: map[string]interface{}{
					"path": params.Path,
					"mode": params.Mode,
				},
			},
		},
		Metadata: make(map[string]interface{}),
	}

	return result, nil
}

// PostHook verifies the directory was created successfully
// REQ-PES-048: Post-execution verification and user hooks
func (h *CreateDirectoryHandlerV2) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	params, err := h.unmarshalParams(op.Params)
	if err != nil {
		return nil, fmt.Errorf("unmarshal params: %w", err)
	}

	result := NewHookResult()

	// Verify directory exists
	exists, err := exec.FileExists(params.Path)
	if err != nil {
		return nil, fmt.Errorf("check directory exists: %w", err)
	}

	if !exists {
		result.AddError(fmt.Sprintf("directory was not created: %s", params.Path))
		return result, nil
	}

	// In real implementation, we might also verify:
	// - Correct permissions
	// - Correct ownership
	// - Directory is writable

	result.Metadata["verified"] = true
	result.Metadata["path"] = params.Path

	return result, nil
}

// unmarshalParams handles JSON unmarshaling with type conversion
// Handles the JSON float64 -> int conversion issue
func (h *CreateDirectoryHandlerV2) unmarshalParams(params map[string]interface{}) (*CreateDirectoryParams, error) {
	// Marshal back to JSON to use standard unmarshaling
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	var typed CreateDirectoryParams
	if err := json.Unmarshal(data, &typed); err != nil {
		return nil, fmt.Errorf("unmarshal params: %w", err)
	}

	// Validate
	if typed.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	return &typed, nil
}
