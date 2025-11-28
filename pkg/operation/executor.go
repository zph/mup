package operation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/plan"
)

// Executor executes planned operations
type Executor struct {
	executors  map[string]executor.Executor // host -> executor mapping
	handlers   map[plan.OperationType]OperationHandler
	storageDir string
}

// OperationHandler handles execution of a specific operation type using four-phase pattern
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler interface
type OperationHandler interface {
	// IsComplete checks if the operation was already completed (for resume)
	IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error)

	// PreHook validates preconditions and runs user hooks before execution
	PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error)

	// Execute performs the operation (must be idempotent)
	Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error)

	// PostHook verifies success and runs user hooks after execution
	PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error)
}

// NewExecutor creates a new operation executor
func NewExecutor(executors map[string]executor.Executor) *Executor {
	return NewExecutorWithStorage(executors, "")
}

// NewExecutorWithStorage creates a new operation executor with custom storage directory
func NewExecutorWithStorage(executors map[string]executor.Executor, storageDir string) *Executor {
	if storageDir == "" {
		// Default to ~/.mup/storage
		homeDir, err := os.UserHomeDir()
		if err == nil {
			storageDir = filepath.Join(homeDir, ".mup", "storage")
		}
	}

	e := &Executor{
		executors:  executors,
		handlers:   make(map[plan.OperationType]OperationHandler),
		storageDir: storageDir,
	}

	// Create handlers (some need initialization)
	downloadHandler, err := NewDownloadBinaryHandler()
	if err != nil {
		fmt.Printf("Warning: failed to create DownloadBinaryHandler: %v\n", err)
		downloadHandler = &DownloadBinaryHandler{} // Use empty handler as fallback
	}

	configHandler, err := NewGenerateConfigHandler()
	if err != nil {
		fmt.Printf("Warning: failed to create GenerateConfigHandler: %v\n", err)
		configHandler = &GenerateConfigHandler{} // Use empty handler as fallback
	}

	saveMetadataHandler := NewSaveMetadataHandler(storageDir)

	// Register default handlers
	// All handlers migrated to four-phase interface
	e.RegisterHandler(plan.OpDownloadBinary, downloadHandler)
	e.RegisterHandler(plan.OpCopyBinary, NewCopyBinaryHandler())
	e.RegisterHandler(plan.OpCreateDirectory, &CreateDirectoryHandler{})
	e.RegisterHandler(plan.OpCreateSymlink, &CreateSymlinkHandler{})
	e.RegisterHandler(plan.OpUploadFile, &UploadFileHandler{})
	e.RegisterHandler(plan.OpRemoveDirectory, &RemoveDirectoryHandler{})
	e.RegisterHandler(plan.OpStartSupervisor, &StartSupervisorHandler{})
	e.RegisterHandler(plan.OpStartProcess, &StartProcessHandler{})
	e.RegisterHandler(plan.OpGenerateConfig, configHandler)
	e.RegisterHandler(plan.OpWaitForProcess, &WaitForProcessHandler{})
	e.RegisterHandler(plan.OpWaitForReady, &WaitForReadyHandler{})
	e.RegisterHandler(plan.OpInitReplicaSet, &InitReplicaSetHandler{})
	e.RegisterHandler(plan.OpAddShard, &AddShardHandler{})
	e.RegisterHandler(plan.OpVerifyHealth, &VerifyHealthHandler{})
	e.RegisterHandler(plan.OpSaveMetadata, saveMetadataHandler)
	e.RegisterHandler(plan.OpStopProcess, &StopProcessHandler{})
	e.RegisterHandler(plan.OpGenerateSupervisorCfg, &GenerateSupervisorConfigHandler{})

	return e
}

// RegisterHandler registers a handler for an operation type
func (e *Executor) RegisterHandler(opType plan.OperationType, handler OperationHandler) {
	e.handlers[opType] = handler
}

// Execute executes a single operation
func (e *Executor) Execute(ctx context.Context, op *plan.PlannedOperation) (*apply.OperationResult, error) {
	// Get the appropriate handler
	handler, ok := e.handlers[op.Type]
	if !ok {
		return nil, fmt.Errorf("no handler registered for operation type: %s", op.Type)
	}

	// Get the executor for the target host
	exec, err := e.getExecutor(op)
	if err != nil {
		return nil, fmt.Errorf("failed to get executor: %w", err)
	}

	// Execute the operation
	result, err := handler.Execute(ctx, op, exec)
	if err != nil {
		return nil, fmt.Errorf("operation execution failed: %w", err)
	}

	return result, nil
}

// Validate performs pre-execution validation
func (e *Executor) Validate(ctx context.Context, op *plan.PlannedOperation) error {
	// Get the appropriate handler
	handler, ok := e.handlers[op.Type]
	if !ok {
		return fmt.Errorf("no handler registered for operation type: %s", op.Type)
	}

	// Get the executor for the target host
	exec, err := e.getExecutor(op)
	if err != nil {
		return fmt.Errorf("failed to get executor: %w", err)
	}

	// Run pre-conditions from the plan
	if err := e.validatePreConditions(ctx, op, exec); err != nil {
		return fmt.Errorf("pre-condition validation failed: %w", err)
	}

	// Run handler-specific validation via PreHook
	preResult, err := handler.PreHook(ctx, op, exec)
	if err != nil {
		return fmt.Errorf("handler pre-hook failed: %w", err)
	}
	if !preResult.Valid {
		return fmt.Errorf("handler validation failed: %v", preResult.Errors)
	}

	return nil
}

// validatePreConditions validates all pre-conditions for an operation
func (e *Executor) validatePreConditions(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) error {
	for _, check := range op.PreConditions {
		if err := e.validateSafetyCheck(ctx, &check, exec); err != nil {
			if check.Required {
				return fmt.Errorf("required safety check %s failed: %w", check.ID, err)
			}
			// Optional check failed - log but continue
			fmt.Printf("Warning: optional safety check %s failed: %v\n", check.ID, err)
		}
	}
	return nil
}

// validateSafetyCheck validates a single safety check
func (e *Executor) validateSafetyCheck(ctx context.Context, check *plan.SafetyCheck, exec executor.Executor) error {
	switch check.CheckType {
	case "port_available":
		port, ok := check.Params["port"].(float64) // JSON numbers are float64
		if !ok {
			return fmt.Errorf("invalid port parameter")
		}
		available, err := exec.CheckPortAvailable(int(port))
		if err != nil {
			return err
		}
		if !available {
			return fmt.Errorf("port %d is not available", int(port))
		}
		return nil

	case "disk_space":
		path, ok := check.Params["path"].(string)
		if !ok {
			return fmt.Errorf("invalid path parameter")
		}
		requiredGB, ok := check.Params["required_gb"].(float64)
		if !ok {
			return fmt.Errorf("invalid required_gb parameter")
		}
		available, err := exec.GetDiskSpace(path)
		if err != nil {
			return err
		}
		availableGB := float64(available) / (1024 * 1024 * 1024)
		if availableGB < requiredGB {
			return fmt.Errorf("insufficient disk space: %.2fGB available, %.2fGB required", availableGB, requiredGB)
		}
		return nil

	case "process_not_running":
		pidFloat, ok := check.Params["pid"].(float64)
		if !ok {
			return fmt.Errorf("invalid pid parameter")
		}
		pid := int(pidFloat)
		running, err := exec.IsProcessRunning(pid)
		if err != nil {
			return err
		}
		if running {
			return fmt.Errorf("process %d is still running", pid)
		}
		return nil

	case "file_exists":
		path, ok := check.Params["path"].(string)
		if !ok {
			return fmt.Errorf("invalid path parameter")
		}
		// Use Execute to check file existence
		_, err := exec.Execute(fmt.Sprintf("test -f %s", path))
		if err != nil {
			return fmt.Errorf("file %s does not exist", path)
		}
		return nil

	case "directory_exists":
		path, ok := check.Params["path"].(string)
		if !ok {
			return fmt.Errorf("invalid path parameter")
		}
		_, err := exec.Execute(fmt.Sprintf("test -d %s", path))
		if err != nil {
			return fmt.Errorf("directory %s does not exist", path)
		}
		return nil

	default:
		return fmt.Errorf("unknown check type: %s", check.CheckType)
	}
}

// getExecutor returns the executor for an operation's target host
func (e *Executor) getExecutor(op *plan.PlannedOperation) (executor.Executor, error) {
	host := op.Target.Host
	if host == "" {
		// Use a default executor if no specific host is specified
		for _, exec := range e.executors {
			return exec, nil
		}
		return nil, fmt.Errorf("no executor available")
	}

	exec, ok := e.executors[host]
	if !ok {
		return nil, fmt.Errorf("no executor found for host: %s", host)
	}

	return exec, nil
}
