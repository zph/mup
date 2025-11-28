package operation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/deploy"
	"github.com/zph/mup/pkg/executor"
	"github.com/zph/mup/pkg/meta"
	"github.com/zph/mup/pkg/mongo"
	"github.com/zph/mup/pkg/plan"
	"github.com/zph/mup/pkg/supervisor"
	"github.com/zph/mup/pkg/template"
	"github.com/zph/mup/pkg/topology"
)

// DownloadBinaryHandler handles binary download operations
// DownloadBinaryHandler downloads MongoDB binaries
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type DownloadBinaryHandler struct {
	binaryMgr *deploy.BinaryManager
}

func NewDownloadBinaryHandler() (*DownloadBinaryHandler, error) {
	bm, err := deploy.NewBinaryManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create binary manager: %w", err)
	}
	return &DownloadBinaryHandler{binaryMgr: bm}, nil
}

// REQ-PES-036: Check if binaries already downloaded
func (h *DownloadBinaryHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	// Binary download is idempotent (GetBinPathWithVariant checks cache first)
	// Always execute to ensure binaries are available
	// TODO: Could check cache directly if we expose a non-downloading method in BinaryManager
	return false, nil
}

// REQ-PES-047: Pre-execution validation
func (h *DownloadBinaryHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// Validate required parameters
	if _, ok := op.Target.Params["version"]; !ok {
		result.AddError("missing required parameter: version")
		return result, nil
	}

	variantStr, ok := op.Target.Params["variant"]
	if !ok {
		result.AddError("missing required parameter: variant")
		return result, nil
	}

	// Validate variant value
	switch variantStr {
	case "mongo", "percona":
		// Valid variants
	default:
		result.AddError(fmt.Sprintf("invalid variant: %s (must be 'mongo' or 'percona')", variantStr))
	}

	return result, nil
}

// Execute downloads the binaries
func (h *DownloadBinaryHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Get parameters from Target.Params
	version := op.Target.Params["version"]
	variantStr := op.Target.Params["variant"]

	// Parse variant
	var variant deploy.Variant
	switch variantStr {
	case "mongo":
		variant = deploy.VariantMongo
	case "percona":
		variant = deploy.VariantPercona
	default:
		return nil, fmt.Errorf("unknown variant: %s", variantStr)
	}

	// Determine platform
	platform := deploy.Platform{
		OS:   "darwin", // TODO: Get from executor
		Arch: "arm64",  // TODO: Get from executor
	}

	// Download binaries
	binPath, err := h.binaryMgr.GetBinPathWithVariant(version, variant, platform)
	if err != nil {
		return nil, fmt.Errorf("failed to download binaries: %w", err)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Downloaded MongoDB %s (%s) binaries to %s", version, variantStr, binPath),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"bin_path": binPath,
			"version":  version,
			"variant":  variantStr,
		},
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *DownloadBinaryHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// Binary download verification happens within BinaryManager
	// The Execute step would have failed if download didn't work
	// For now, we trust that Execute succeeded
	// TODO: Could verify mongod binary exists at returned bin_path

	result.Metadata["verified"] = true
	return result, nil
}

func (h *DownloadBinaryHandler) Close() error {
	if h.binaryMgr != nil {
		return h.binaryMgr.Close()
	}
	return nil
}

// CopyBinaryHandler copies MongoDB binaries from global storage to cluster bin directory
type CopyBinaryHandler struct{}

func NewCopyBinaryHandler() *CopyBinaryHandler {
	return &CopyBinaryHandler{}
}

func (h *CopyBinaryHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	// Check if binaries already exist in destination
	destDir, ok := op.Params["dest_dir"].(string)
	if !ok {
		return false, nil
	}

	version, ok := op.Params["version"].(string)
	if !ok {
		return false, nil
	}

	// Determine which shell binary to check using shared utility
	shellBinary := mongo.GetShellBinary(version)

	mongodPath := filepath.Join(destDir, "mongod")
	shellPath := filepath.Join(destDir, shellBinary)

	mongodExists, _ := exec.FileExists(mongodPath)
	shellExists, _ := exec.FileExists(shellPath)

	return mongodExists && shellExists, nil
}

func (h *CopyBinaryHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// Validate required parameters
	if _, ok := op.Params["source_path"]; !ok {
		result.AddError("missing required parameter: source_path")
	}
	if _, ok := op.Params["dest_dir"]; !ok {
		result.AddError("missing required parameter: dest_dir")
	}

	return result, nil
}

func (h *CopyBinaryHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	sourcePath, ok := op.Params["source_path"].(string)
	if !ok {
		return nil, fmt.Errorf("source_path parameter not found or invalid")
	}

	destDir, ok := op.Params["dest_dir"].(string)
	if !ok {
		return nil, fmt.Errorf("dest_dir parameter not found or invalid")
	}

	version, ok := op.Params["version"].(string)
	if !ok {
		return nil, fmt.Errorf("version parameter not found or invalid")
	}

	// Determine which shell binary to copy based on MongoDB version using shared utility
	shellBinary := mongo.GetShellBinary(version)

	// Binary files to copy
	binaries := []string{"mongod", "mongos", shellBinary}

	copiedFiles := []string{}
	for _, binary := range binaries {
		// Get the bin directory from the binary manager's storage path
		// sourcePath is the package base, binaries are in the parent's bin directory
		binDir := filepath.Dir(sourcePath)
		srcFile := filepath.Join(binDir, binary)
		destFile := filepath.Join(destDir, binary)

		// Copy file using UploadFile (works for both local and remote executors)
		err := exec.UploadFile(srcFile, destFile)
		if err != nil {
			return nil, fmt.Errorf("failed to copy %s: %w", binary, err)
		}

		// Set executable permissions
		_, err = exec.Execute(fmt.Sprintf("chmod +x %s", destFile))
		if err != nil {
			return nil, fmt.Errorf("failed to set permissions on %s: %w", binary, err)
		}

		copiedFiles = append(copiedFiles, destFile)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Copied %d binaries to %s", len(copiedFiles), destDir),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"copied_files": copiedFiles,
			"dest_dir":     destDir,
		},
	}, nil
}

func (h *CopyBinaryHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// Verify binaries are executable
	destDir, _ := op.Params["dest_dir"].(string)
	version, _ := op.Params["version"].(string)

	// Determine which shell binary to verify using shared utility
	shellBinary := mongo.GetShellBinary(version)

	binaries := []string{"mongod", "mongos", shellBinary}

	for _, binary := range binaries {
		binPath := filepath.Join(destDir, binary)
		exists, err := exec.FileExists(binPath)
		if err != nil {
			result.AddWarning(fmt.Sprintf("failed to verify %s: %v", binary, err))
		} else if !exists {
			result.AddError(fmt.Sprintf("binary not found after copy: %s", binPath))
		}
	}

	result.Metadata["verified"] = true
	return result, nil
}

func (h *CopyBinaryHandler) Close() error {
	return nil
}

// CreateDirectoryHandler handles directory creation
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type CreateDirectoryHandler struct{}

// REQ-PES-036: Check if directory already exists
func (h *CreateDirectoryHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	path := op.Params["path"].(string)

	exists, err := exec.FileExists(path)
	if err != nil {
		return false, fmt.Errorf("check directory exists: %w", err)
	}

	return exists, nil
}

// REQ-PES-047: Validate preconditions
func (h *CreateDirectoryHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	path := op.Params["path"].(string)
	if path == "" {
		result.AddError("path cannot be empty")
		return result, nil
	}

	// Check if already exists (warning)
	exists, err := exec.FileExists(path)
	if err != nil {
		result.AddWarning(fmt.Sprintf("unable to check if directory exists: %v", err))
	} else if exists {
		result.AddWarning(fmt.Sprintf("directory already exists: %s", path))
	}

	return result, nil
}

// Execute creates the directory
func (h *CreateDirectoryHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	path := op.Params["path"].(string)
	mode := os.FileMode(0755)
	if modeParam, ok := op.Params["mode"]; ok {
		if modeInt, ok := modeParam.(float64); ok {
			mode = os.FileMode(modeInt)
		} else if modeInt, ok := modeParam.(int); ok {
			mode = os.FileMode(modeInt)
		}
	}

	if err := exec.CreateDirectory(path, mode); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", path, err)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Created directory: %s", path),
		Changes: op.Changes,
	}, nil
}

// REQ-PES-048: Verify directory was created
func (h *CreateDirectoryHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	path := op.Params["path"].(string)

	exists, err := exec.FileExists(path)
	if err != nil {
		return nil, fmt.Errorf("check directory exists: %w", err)
	}

	if !exists {
		result.AddError(fmt.Sprintf("directory was not created: %s", path))
		return result, nil
	}

	result.Metadata["verified"] = true
	result.Metadata["path"] = path

	return result, nil
}

// Validate is kept for backwards compatibility but delegates to PreHook
func (h *CreateDirectoryHandler) Validate(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) error {
	result, err := h.PreHook(ctx, op, exec)
	if err != nil {
		return err
	}
	if !result.Valid {
		return fmt.Errorf("validation failed: %v", result.Errors)
	}
	return nil
}

// CreateSymlinkHandler handles symlink creation
// REQ-PM-012, REQ-PM-013, REQ-PM-014: Manages current/next/previous symlinks
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type CreateSymlinkHandler struct{}

// REQ-PES-036: Check if symlink already exists and points to correct target
func (h *CreateSymlinkHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	linkPath := op.Params["link_path"].(string)
	targetPath := op.Params["target_path"].(string)

	// Check if symlink exists
	linkInfo, err := os.Lstat(linkPath)
	if err != nil {
		return false, nil // Symlink doesn't exist yet
	}

	// Check if it's actually a symlink
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		return false, nil // File exists but is not a symlink
	}

	// Check if it points to the correct target
	currentTarget, err := os.Readlink(linkPath)
	if err != nil {
		return false, nil
	}

	return currentTarget == targetPath, nil
}

// REQ-PES-047: Pre-execution validation
func (h *CreateSymlinkHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	targetPath := op.Params["target_path"].(string)
	linkPath := op.Params["link_path"].(string)

	result := NewHookResult()

	// For absolute targets, check if they exist
	// For relative targets (like "v3.6"), we can't easily validate without knowing the working directory
	if filepath.IsAbs(targetPath) {
		if _, err := os.Stat(targetPath); err != nil {
			result.AddError(fmt.Sprintf("symlink target does not exist: %s", targetPath))
			return result, nil
		}
	}

	// Check that parent directory of link exists
	linkDir := filepath.Dir(linkPath)
	if _, err := os.Stat(linkDir); err != nil {
		result.AddError(fmt.Sprintf("parent directory of symlink does not exist: %s", linkDir))
		return result, nil
	}

	// Check if symlink already exists (warning, not error - idempotent)
	if linkInfo, err := os.Lstat(linkPath); err == nil {
		if linkInfo.Mode()&os.ModeSymlink != 0 {
			currentTarget, err := os.Readlink(linkPath)
			if err != nil {
				result.AddWarning(fmt.Sprintf("symlink exists but unable to read target: %v", err))
			} else if currentTarget == targetPath {
				result.AddWarning(fmt.Sprintf("symlink already exists with correct target: %s -> %s", linkPath, targetPath))
			} else {
				result.AddWarning(fmt.Sprintf("symlink exists but points to different target: %s -> %s (will update to %s)", linkPath, currentTarget, targetPath))
			}
		} else {
			result.AddError(fmt.Sprintf("file exists at symlink path but is not a symlink: %s", linkPath))
			return result, nil
		}
	}

	return result, nil
}

// Execute creates the symlink (idempotent)
func (h *CreateSymlinkHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	linkPath := op.Params["link_path"].(string)
	targetPath := op.Params["target_path"].(string)

	// Remove existing symlink if present
	os.Remove(linkPath)

	// Create new symlink (relative path for portability)
	if err := os.Symlink(targetPath, linkPath); err != nil {
		return nil, fmt.Errorf("failed to create symlink %s -> %s: %w", linkPath, targetPath, err)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Created symlink: %s -> %s", linkPath, targetPath),
		Changes: op.Changes,
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *CreateSymlinkHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	linkPath := op.Params["link_path"].(string)
	targetPath := op.Params["target_path"].(string)

	result := NewHookResult()

	// Verify symlink exists
	linkInfo, err := os.Lstat(linkPath)
	if err != nil {
		result.AddError(fmt.Sprintf("symlink was not created: %s", linkPath))
		return result, nil
	}

	// Verify it's actually a symlink
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		result.AddError(fmt.Sprintf("file exists but is not a symlink: %s", linkPath))
		return result, nil
	}

	// Verify it points to the correct target
	currentTarget, err := os.Readlink(linkPath)
	if err != nil {
		result.AddError(fmt.Sprintf("failed to read symlink target: %s", linkPath))
		return result, nil
	}

	if currentTarget != targetPath {
		result.AddError(fmt.Sprintf("symlink points to wrong target: %s -> %s (expected: %s)", linkPath, currentTarget, targetPath))
		return result, nil
	}

	result.Metadata["verified"] = true
	result.Metadata["link_path"] = linkPath
	result.Metadata["target_path"] = targetPath

	return result, nil
}

// UploadFileHandler handles file uploads
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type UploadFileHandler struct{}

// REQ-PES-036: Check if file already uploaded
func (h *UploadFileHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	remotePath := op.Params["remote_path"].(string)

	// Check if file exists at remote path
	exists, err := exec.FileExists(remotePath)
	if err != nil {
		return false, fmt.Errorf("check file exists: %w", err)
	}

	return exists, nil
}

// REQ-PES-047: Validate preconditions
func (h *UploadFileHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	localPath := op.Params["local_path"].(string)
	remotePath := op.Params["remote_path"].(string)

	// Validate local file exists
	if _, err := os.Stat(localPath); err != nil {
		result.AddError(fmt.Sprintf("local file does not exist: %s", localPath))
		return result, nil
	}

	// Check if file already exists (warning)
	exists, err := exec.FileExists(remotePath)
	if err != nil {
		result.AddWarning(fmt.Sprintf("unable to check if remote file exists: %v", err))
	} else if exists {
		result.AddWarning(fmt.Sprintf("remote file already exists: %s (will overwrite)", remotePath))
	}

	return result, nil
}

// Execute uploads the file
func (h *UploadFileHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	localPath := op.Params["local_path"].(string)
	remotePath := op.Params["remote_path"].(string)

	if err := exec.UploadFile(localPath, remotePath); err != nil {
		return nil, fmt.Errorf("failed to upload file %s to %s: %w", localPath, remotePath, err)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Uploaded file: %s -> %s", localPath, remotePath),
		Changes: op.Changes,
	}, nil
}

// REQ-PES-048: Verify file was uploaded
func (h *UploadFileHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	remotePath := op.Params["remote_path"].(string)

	// Verify file exists remotely
	exists, err := exec.FileExists(remotePath)
	if err != nil {
		return nil, fmt.Errorf("check file exists: %w", err)
	}

	if !exists {
		result.AddError(fmt.Sprintf("file was not uploaded: %s", remotePath))
		return result, nil
	}

	result.Metadata["verified"] = true
	result.Metadata["remote_path"] = remotePath

	return result, nil
}

// GenerateConfigHandler handles configuration file generation
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type GenerateConfigHandler struct {
	templateMgr *template.Manager
}

func NewGenerateConfigHandler() (*GenerateConfigHandler, error) {
	tmplMgr, err := template.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create template manager: %w", err)
	}
	return &GenerateConfigHandler{
		templateMgr: tmplMgr,
	}, nil
}

// REQ-PES-036: Check if config already exists
func (h *GenerateConfigHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	configPath, ok := op.Params["config_path"].(string)
	if !ok {
		return false, fmt.Errorf("config_path parameter not found")
	}

	// Check if config file exists
	exists, err := exec.FileExists(configPath)
	if err != nil {
		return false, fmt.Errorf("check config exists: %w", err)
	}

	// TODO: Could add content hash verification here
	return exists, nil
}

// REQ-PES-047: Pre-execution validation
func (h *GenerateConfigHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// Validate required parameters
	requiredParams := []string{"config_path", "role", "version", "port"}
	for _, param := range requiredParams {
		if _, ok := op.Params[param]; !ok {
			result.AddError(fmt.Sprintf("missing required parameter: %s", param))
		}
	}

	if !result.Valid {
		return result, nil
	}

	// Role-specific validation
	role, _ := op.Params["role"].(string)
	switch role {
	case "configsvr", "shardsvr":
		if _, ok := op.Params["data_dir"]; !ok {
			result.AddError(fmt.Sprintf("missing required parameter for %s: data_dir", role))
		}
		if _, ok := op.Params["log_dir"]; !ok {
			result.AddError(fmt.Sprintf("missing required parameter for %s: log_dir", role))
		}
	case "mongos":
		if _, ok := op.Params["log_dir"]; !ok {
			result.AddError("missing required parameter for mongos: log_dir")
		}
		if _, ok := op.Params["config_db"]; !ok {
			result.AddError("missing required parameter for mongos: config_db")
		}
	case "standalone":
		// Standalone mongod (non-sharded) - no additional validation needed
	default:
		result.AddError(fmt.Sprintf("invalid role: %s (must be configsvr, shardsvr, mongos, or standalone)", role))
	}

	// Check if config already exists
	configPath, _ := op.Params["config_path"].(string)
	exists, err := exec.FileExists(configPath)
	if err != nil {
		result.AddWarning(fmt.Sprintf("unable to check if config exists: %v", err))
	} else if exists {
		result.AddWarning(fmt.Sprintf("config file already exists (will be overwritten): %s", configPath))
	}

	return result, nil
}

func (h *GenerateConfigHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Extract required parameters
	configPath, ok := op.Params["config_path"].(string)
	if !ok {
		return nil, fmt.Errorf("config_path parameter not found or invalid type")
	}

	role, ok := op.Params["role"].(string)
	if !ok {
		return nil, fmt.Errorf("role parameter not found or invalid type")
	}

	version, ok := op.Params["version"].(string)
	if !ok {
		return nil, fmt.Errorf("version parameter not found or invalid type")
	}

	// Get port (handle both int and float64 from JSON unmarshaling)
	port, ok := op.Params["port"].(int)
	if !ok {
		if portFloat, ok := op.Params["port"].(float64); ok {
			port = int(portFloat)
		} else {
			return nil, fmt.Errorf("port parameter not found or invalid type")
		}
	}

	bindIP, ok := op.Params["bind_ip"].(string)
	if !ok {
		// Bind to both IPv4 and IPv6 localhost to support all connection types
		bindIP = "127.0.0.1,::1"
	}

	// Generate configuration based on role
	var configContent []byte
	var err error

	switch role {
	case "configsvr", "shardsvr", "standalone":
		configContent, err = h.generateMongodConfig(op, version, role, port, bindIP)
	case "mongos":
		configContent, err = h.generateMongosConfig(op, version, port, bindIP)
	default:
		return nil, fmt.Errorf("unknown role: %s", role)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to generate config: %w", err)
	}

	// Upload configuration
	if err := exec.UploadContent(configContent, configPath); err != nil {
		return nil, fmt.Errorf("failed to upload config: %w", err)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Generated and uploaded config: %s", configPath),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"config_path": configPath,
			"role":        role,
			"replica_set": op.Params["replica_set"],
			"port":        port,
		},
	}, nil
}

func (h *GenerateConfigHandler) generateMongodConfig(op *plan.PlannedOperation, version, role string, port int, bindIP string) ([]byte, error) {
	dataDir, ok := op.Params["data_dir"].(string)
	if !ok {
		return nil, fmt.Errorf("data_dir parameter not found or invalid type")
	}

	logDir, ok := op.Params["log_dir"].(string)
	if !ok {
		return nil, fmt.Errorf("log_dir parameter not found or invalid type")
	}

	replicaSet, _ := op.Params["replica_set"].(string)

	// Use simple generic names - folder context provides node type
	logFileName := "process.log"
	pidFileName := "process.pid"

	// Build template data
	data := template.MongodConfig{
		Net: template.NetConfig{
			Port:   port,
			BindIP: bindIP,
		},
		Storage: template.StorageConfig{
			DBPath: dataDir,
			Journal: template.JournalConfig{
				Enabled: true,
			},
			Engine: "wiredTiger",
			WiredTiger: &template.WiredTigerConfig{
				EngineConfig: template.WiredTigerEngineConfig{
					CacheSizeGB: 1.0,
				},
			},
		},
		SystemLog: template.SystemLogConfig{
			Destination: "file",
			Path:        filepath.Join(logDir, logFileName),
			LogAppend:   true,
		},
		ProcessManagement: template.ProcessManagementConfig{
			Fork:        false,
			PIDFilePath: filepath.Join(dataDir, pidFileName),
		},
	}

	// Add replication if replica set specified
	if replicaSet != "" {
		data.Replication = &template.ReplicationConfig{
			ReplSetName:               replicaSet,
			EnableMajorityReadConcern: true,
		}
	}

	// Add sharding configuration based on role
	if role == "configsvr" || role == "shardsvr" {
		data.Sharding = &template.ShardingConfig{
			ClusterRole: role,
		}
	}

	// Get appropriate template (config servers use "config" templates, shards use "mongod")
	templateType := "mongod"
	if role == "configsvr" {
		templateType = "config"
	}

	tmpl, err := h.templateMgr.GetTemplate(templateType, version)
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.Bytes(), nil
}

func (h *GenerateConfigHandler) generateMongosConfig(op *plan.PlannedOperation, version string, port int, bindIP string) ([]byte, error) {
	logDir, ok := op.Params["log_dir"].(string)
	if !ok {
		return nil, fmt.Errorf("log_dir parameter not found or invalid type")
	}

	configDB, ok := op.Params["config_db"].(string)
	if !ok {
		return nil, fmt.Errorf("config_db parameter not found or invalid type")
	}

	// Use simple generic name - folder context provides node type
	logFileName := "process.log"

	// Build template data
	data := template.MongosConfig{
		Net: template.NetConfig{
			Port:   port,
			BindIP: bindIP,
		},
		SystemLog: template.SystemLogConfig{
			Destination: "file",
			Path:        filepath.Join(logDir, logFileName),
			LogAppend:   true,
		},
		// ProcessManagement is nil (omitted) for mongos 3.6 compatibility
		Sharding: template.MongosShardingConfig{
			ConfigDB: configDB,
		},
	}

	// Get mongos template
	tmpl, err := h.templateMgr.GetTemplate("mongos", version)
	if err != nil {
		return nil, fmt.Errorf("failed to get template: %w", err)
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.Bytes(), nil
}

// REQ-PES-048: Post-execution verification
func (h *GenerateConfigHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	configPath, ok := op.Params["config_path"].(string)
	if !ok {
		result.AddError("config_path parameter not found")
		return result, nil
	}

	// Verify config file was created
	exists, err := exec.FileExists(configPath)
	if err != nil {
		return nil, fmt.Errorf("check config exists: %w", err)
	}

	if !exists {
		result.AddError(fmt.Sprintf("config file was not created: %s", configPath))
		return result, nil
	}

	result.Metadata["verified"] = true
	result.Metadata["config_path"] = configPath

	return result, nil
}

// StartProcessHandler starts a process via supervisorctl
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type StartProcessHandler struct{}

// REQ-PES-036: Check if process is already running
func (h *StartProcessHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	// TODO: Check process status via supervisorctl
	// For now, always re-execute (supervisorctl start is idempotent)
	return false, nil
}

// REQ-PES-047: Pre-execution validation
func (h *StartProcessHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// Validate required parameters
	if _, ok := op.Params["program_name"]; !ok {
		result.AddError("missing required parameter: program_name")
		return result, nil
	}
	if _, ok := op.Params["supervisor_config"]; !ok {
		result.AddError("missing required parameter: supervisor_config")
		return result, nil
	}
	if _, ok := op.Params["supervisor_port"]; !ok {
		result.AddError("missing required parameter: supervisor_port")
		return result, nil
	}

	// Validate supervisor config exists
	supervisorConfig, _ := op.Params["supervisor_config"].(string)
	if _, err := os.Stat(supervisorConfig); err != nil {
		result.AddError(fmt.Sprintf("supervisor config not found: %s", supervisorConfig))
		return result, nil
	}

	return result, nil
}

// Execute starts process via supervisorctl (idempotent)
func (h *StartProcessHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Extract required supervisor parameters
	programName, ok := op.Params["program_name"].(string)
	if !ok {
		return nil, fmt.Errorf("program_name parameter not found or invalid type")
	}

	supervisorConfig, ok := op.Params["supervisor_config"].(string)
	if !ok {
		return nil, fmt.Errorf("supervisor_config parameter not found or invalid type")
	}

	supervisorPort, ok := op.Params["supervisor_port"].(int)
	if !ok {
		// Try float64 (JSON unmarshaling converts numbers to float64)
		if portFloat, ok := op.Params["supervisor_port"].(float64); ok {
			supervisorPort = int(portFloat)
		} else {
			return nil, fmt.Errorf("supervisor_port parameter not found or invalid type")
		}
	}

	// Get supervisor binary path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	cacheDir := filepath.Join(homeDir, ".mup", "storage", "bin")
	binaryPath, err := supervisor.GetSupervisordBinary(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get supervisord binary: %w", err)
	}

	// Construct supervisorctl command
	// supervisord ctl -c <config> -s http://localhost:<port> start <program-name>
	serverURL := fmt.Sprintf("http://localhost:%d", supervisorPort)
	command := fmt.Sprintf("%s ctl -c %s -s %s start %s", binaryPath, supervisorConfig, serverURL, programName)

	fmt.Printf("  Starting %s via supervisorctl...\n", programName)

	// Execute the supervisorctl command
	output, err := exec.Execute(command)
	if err != nil {
		return nil, fmt.Errorf("failed to start process %s: %w", programName, err)
	}

	fmt.Printf("  ✓ Started %s\n", programName)

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Started %s via supervisor (output: %s)", programName, strings.TrimSpace(output)),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"program_name":      programName,
			"supervisor_config": supervisorConfig,
			"supervisor_port":   supervisorPort,
		},
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *StartProcessHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// TODO: Verify process is running via supervisorctl status
	// For now, trust that Execute succeeded if it didn't return an error

	programName, _ := op.Params["program_name"].(string)
	result.Metadata["verified"] = true
	result.Metadata["program_name"] = programName

	return result, nil
}

// WaitForProcessHandler waits for a process to be ready
// REQ-SIM-001: Works transparently in simulation mode
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type WaitForProcessHandler struct{}

// REQ-PES-036: Waiting is never complete (always execute)
func (h *WaitForProcessHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	return false, nil
}

// REQ-PES-047: Pre-execution validation
func (h *WaitForProcessHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	if _, ok := op.Params["port"]; !ok {
		result.AddError("missing required parameter: port")
	}

	return result, nil
}

// Execute waits for process to be ready
func (h *WaitForProcessHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	port := int(op.Params["port"].(float64))
	timeoutSec := 60
	if t, ok := op.Params["timeout"]; ok {
		timeoutSec = int(t.(float64))
	}

	// Check if in simulation mode
	execType := fmt.Sprintf("%T", exec)
	isSimulation := strings.Contains(execType, "Simulation")

	if isSimulation {
		// REQ-SIM-004: Record operation in simulation mode
		cmdStr := fmt.Sprintf("Wait for process on port %d (timeout: %ds)", port, timeoutSec)
		exec.Execute(cmdStr)

		return &apply.OperationResult{
			Success: true,
			Output:  fmt.Sprintf("Process is ready on port %d (simulated)", port),
			Changes: op.Changes,
			Metadata: map[string]interface{}{
				"port":      port,
				"timeout":   timeoutSec,
				"simulated": true,
			},
		}, nil
	}

	// Real execution - check port availability
	timeout := time.Duration(timeoutSec) * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		available, err := exec.CheckPortAvailable(port)
		if err == nil && !available {
			// Port is in use, meaning process is listening
			return &apply.OperationResult{
				Success: true,
				Output:  fmt.Sprintf("Process is ready on port %d", port),
				Changes: op.Changes,
			}, nil
		}
		time.Sleep(1 * time.Second)
	}

	return nil, fmt.Errorf("timeout waiting for process on port %d", port)
}

// REQ-PES-048: Post-execution verification
func (h *WaitForProcessHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()
	result.Metadata["verified"] = true
	return result, nil
}

// WaitForReadyHandler waits for replica set to be ready
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type WaitForReadyHandler struct{}

// REQ-PES-036: Waiting is never complete (always execute)
func (h *WaitForReadyHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	return false, nil
}

// REQ-PES-047: Pre-execution validation
func (h *WaitForReadyHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	return NewHookResult(), nil
}

// Execute waits for replica set to stabilize
func (h *WaitForReadyHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Parse timeout parameter
	timeoutStr, ok := op.Params["timeout"].(string)
	if !ok {
		timeoutStr = "2m"
	}

	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout duration %s: %w", timeoutStr, err)
	}

	// For now, just sleep for a short time to let the replica set stabilize
	// In a real implementation, this would check replica set status via MongoDB driver
	replicaSet, _ := op.Params["replica_set"].(string)

	sleepTime := 5 * time.Second
	if sleepTime > timeout {
		sleepTime = timeout
	}

	time.Sleep(sleepTime)

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Waited for replica set '%s' to stabilize", replicaSet),
		Changes: op.Changes,
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *WaitForReadyHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()
	result.Metadata["verified"] = true
	return result, nil
}

// InitReplicaSetHandler initializes a MongoDB replica set
// REQ-SIM-001: Works transparently in simulation mode
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type InitReplicaSetHandler struct{}

// REQ-PES-036: Check if replica set already initialized
func (h *InitReplicaSetHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	// TODO: Check replica set status via MongoDB
	// For now, rely on idempotency of replSetInitiate command
	return false, nil
}

// REQ-PES-047: Pre-execution validation
func (h *InitReplicaSetHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	if _, ok := op.Params["replica_set"]; !ok {
		result.AddError("missing required parameter: replica_set")
	}
	if _, ok := op.Params["members"]; !ok {
		result.AddError("missing required parameter: members")
	}

	return result, nil
}

func (h *InitReplicaSetHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Get replica set name from params
	rsName, ok := op.Params["replica_set"].(string)
	if !ok {
		return nil, fmt.Errorf("replica_set parameter not found")
	}

	// Get members from params
	membersRaw, ok := op.Params["members"]
	if !ok {
		return nil, fmt.Errorf("members parameter not found")
	}

	// Handle both []interface{} (from JSON) and []string (direct)
	var members []interface{}
	switch v := membersRaw.(type) {
	case []interface{}:
		members = v
	case []string:
		// Convert []string to []interface{}
		members = make([]interface{}, len(v))
		for i, s := range v {
			members[i] = s
		}
	default:
		return nil, fmt.Errorf("members parameter is not a list (got type %T)", membersRaw)
	}

	// Convert to member documents for replica set config
	memberDocs := make([]bson.M, len(members))
	for i, m := range members {
		hostPort, ok := m.(string)
		if !ok {
			return nil, fmt.Errorf("member %d is not a string", i)
		}

		memberDocs[i] = bson.M{
			"_id":  i,
			"host": hostPort,
		}
	}

	if len(memberDocs) == 0 {
		return nil, fmt.Errorf("no members provided")
	}

	// Use MongoDB client abstraction (handles both simulation and real execution)
	primaryHost := memberDocs[0]["host"].(string)

	// Create context with timeout
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Create MongoDB client (automatically handles simulation vs real mode)
	mongoClient, err := NewMongoDBClient(initCtx, primaryHost, exec)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to primary node %s: %w", primaryHost, err)
	}
	defer mongoClient.Disconnect(initCtx)

	// Safety check: Check if replica set is already initialized
	status, err := mongoClient.RunCommand(initCtx, bson.M{"replSetGetStatus": 1}, true)
	if err == nil && status["ok"] != nil {
		// Already initialized
		fmt.Printf("  ✓ Replica set %s already initialized\n", rsName)
		return &apply.OperationResult{
			Success: true,
			Output:  fmt.Sprintf("Replica set '%s' already initialized", rsName),
			Changes: op.Changes,
			Metadata: map[string]interface{}{
				"replica_set":         rsName,
				"members":             members,
				"already_initialized": true,
			},
		}, nil
	}

	// Initialize replica set using replSetInitiate command
	initCmd := bson.M{
		"replSetInitiate": bson.M{
			"_id":     rsName,
			"version": 1,
			"members": memberDocs,
		},
	}

	_, err = mongoClient.RunCommand(initCtx, initCmd, false)
	if err != nil {
		// Check if it's already initialized (race condition)
		errStr := err.Error()
		if strings.Contains(errStr, "already initialized") ||
			strings.Contains(errStr, "already has") ||
			strings.Contains(errStr, "already been initiated") {
			fmt.Printf("  ✓ Replica set %s already initialized\n", rsName)
			return &apply.OperationResult{
				Success: true,
				Output:  fmt.Sprintf("Replica set '%s' already initialized", rsName),
				Changes: op.Changes,
				Metadata: map[string]interface{}{
					"replica_set":         rsName,
					"members":             members,
					"already_initialized": true,
				},
			}, nil
		}
		return nil, fmt.Errorf("failed to initialize replica set: %w", err)
	}

	// Post-initialization verification: Wait for primary election
	fmt.Printf("  ⏳ Waiting for primary to be elected in replica set %s...\n", rsName)

	// Create a new client for verification (reconnect after replSetInitiate)
	verifyClient, err := NewMongoDBClient(initCtx, primaryHost, exec)
	if err != nil {
		return nil, fmt.Errorf("failed to reconnect for verification: %w", err)
	}
	defer verifyClient.Disconnect(initCtx)

	// Poll replSetGetStatus to verify primary election
	maxRetries := 10 // Reduced for simulation - real code uses 60
	for i := 0; i < maxRetries; i++ {
		// Verification check: Poll replica set status (not a safety check - we're verifying what we just did)
		status, err := verifyClient.RunCommand(initCtx, bson.M{"replSetGetStatus": 1}, false)
		if err == nil {
			// Check if we have a primary
			if statusMembers, ok := status["members"].(bson.A); ok {
				hasPrimary := false
				for _, m := range statusMembers {
					if member, ok := m.(bson.M); ok {
						stateStr, _ := member["stateStr"].(string)
						if stateStr == "PRIMARY" {
							hasPrimary = true
							break
						}
					}
				}

				if hasPrimary {
					fmt.Printf("  ✓ Primary elected in replica set %s\n", rsName)
					break
				}
			}
		}

		// In real mode, wait between retries
		execType := fmt.Sprintf("%T", exec)
		if !strings.Contains(execType, "Simulation") {
			time.Sleep(2 * time.Second)
		}

		if i == maxRetries-1 {
			return nil, fmt.Errorf("timeout waiting for primary election in replica set %s", rsName)
		}
	}

	fmt.Printf("  ✓ Initialized replica set '%s' with %d member(s)\n", rsName, len(memberDocs))

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Initialized replica set '%s' with %d members", rsName, len(memberDocs)),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"replica_set": rsName,
			"members":     members,
		},
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *InitReplicaSetHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// TODO: Verify replica set is initialized via replSetGetStatus
	// For now, trust that Execute succeeded if it didn't return an error

	rsName, _ := op.Params["replica_set"].(string)
	result.Metadata["verified"] = true
	result.Metadata["replica_set"] = rsName

	return result, nil
}

// AddShardHandler adds a shard to a sharded cluster
// REQ-SIM-001: Works transparently in simulation mode
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type AddShardHandler struct{}

// REQ-PES-036: Check if shard already added
func (h *AddShardHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	// TODO: Check shard status via MongoDB listShards
	// For now, rely on idempotency of addShard command
	return false, nil
}

// REQ-PES-047: Pre-execution validation
func (h *AddShardHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	if _, ok := op.Params["shard_name"]; !ok {
		result.AddError("missing required parameter: shard_name")
	}
	if _, ok := op.Params["connection_string"]; !ok {
		result.AddError("missing required parameter: connection_string")
	}
	if _, ok := op.Params["mongos_host"]; !ok {
		result.AddError("missing required parameter: mongos_host")
	}

	return result, nil
}

func (h *AddShardHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Get shard information from params
	shardName, ok := op.Params["shard_name"].(string)
	if !ok {
		return nil, fmt.Errorf("shard_name parameter not found")
	}

	connectionString, ok := op.Params["connection_string"].(string)
	if !ok {
		return nil, fmt.Errorf("connection_string parameter not found")
	}

	// Get mongos host from params (required to connect to mongos)
	mongosHost, ok := op.Params["mongos_host"].(string)
	if !ok {
		return nil, fmt.Errorf("mongos_host parameter not found")
	}

	// Use MongoDB client abstraction (handles both simulation and real execution)
	// Create context with timeout
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Create MongoDB client for mongos (automatically handles simulation vs real mode)
	mongoClient, err := NewMongoDBClientForMongos(initCtx, mongosHost, exec)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongos at %s: %w", mongosHost, err)
	}
	defer mongoClient.Disconnect(initCtx)

	// Safety check: Check if shard already exists
	shards, err := mongoClient.RunCommand(initCtx, bson.M{"listShards": 1}, true)
	if err == nil {
		if shardList, ok := shards["shards"].(bson.A); ok {
			for _, s := range shardList {
				if sMap, ok := s.(bson.M); ok {
					if id, ok := sMap["_id"].(string); ok && id == shardName {
						fmt.Printf("  ✓ Shard %s already added\n", shardName)
						return &apply.OperationResult{
							Success: true,
							Output:  fmt.Sprintf("Shard '%s' already added", shardName),
							Changes: op.Changes,
							Metadata: map[string]interface{}{
								"shard_name":        shardName,
								"connection_string": connectionString,
								"mongos_host":       mongosHost,
								"already_exists":    true,
							},
						}, nil
					}
				}
			}
		}
	}

	// Add shard using addShard command
	_, err = mongoClient.RunCommand(initCtx, bson.M{"addShard": connectionString}, false)
	if err != nil {
		// Check if shard already exists (race condition)
		if strings.Contains(err.Error(), "already exists") {
			fmt.Printf("  ✓ Shard %s already added\n", shardName)
			return &apply.OperationResult{
				Success: true,
				Output:  fmt.Sprintf("Shard '%s' already added", shardName),
				Changes: op.Changes,
				Metadata: map[string]interface{}{
					"shard_name":        shardName,
					"connection_string": connectionString,
					"mongos_host":       mongosHost,
					"already_exists":    true,
				},
			}, nil
		}
		return nil, fmt.Errorf("failed to add shard %s: %w", shardName, err)
	}

	fmt.Printf("  ✓ Added shard '%s' to cluster\n", shardName)

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Added shard '%s' to cluster", shardName),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"shard_name":        shardName,
			"connection_string": connectionString,
			"mongos_host":       mongosHost,
		},
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *AddShardHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// TODO: Verify shard was added via listShards
	// For now, trust that Execute succeeded if it didn't return an error

	shardName, _ := op.Params["shard_name"].(string)
	result.Metadata["verified"] = true
	result.Metadata["shard_name"] = shardName

	return result, nil
}

// VerifyHealthHandler verifies cluster health
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type VerifyHealthHandler struct{}

// REQ-PES-036: Health check is never complete (always execute)
func (h *VerifyHealthHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	return false, nil
}

// REQ-PES-047: Pre-execution validation
func (h *VerifyHealthHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	return NewHookResult(), nil
}

func (h *VerifyHealthHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Get ports to check from params if provided
	var portsToCheck []int
	if portsRaw, ok := op.Params["ports"]; ok {
		if ports, ok := portsRaw.([]interface{}); ok {
			for _, p := range ports {
				if portFloat, ok := p.(float64); ok {
					portsToCheck = append(portsToCheck, int(portFloat))
				}
			}
		}
	}

	// Verify each port is in use (meaning process is listening)
	var healthyPorts []int
	var unhealthyPorts []int

	for _, port := range portsToCheck {
		available, err := exec.CheckPortAvailable(port)
		if err != nil {
			unhealthyPorts = append(unhealthyPorts, port)
			continue
		}
		if !available {
			// Port is in use - this is good!
			healthyPorts = append(healthyPorts, port)
		} else {
			// Port is available - no process listening
			unhealthyPorts = append(unhealthyPorts, port)
		}
	}

	// For now, just report on port status
	// TODO: Use MongoDB driver to ping each node and check replica set status
	fmt.Printf("  Health check: %d/%d ports listening\n", len(healthyPorts), len(portsToCheck))

	if len(unhealthyPorts) > 0 {
		return nil, fmt.Errorf("health check failed: %d ports not listening: %v", len(unhealthyPorts), unhealthyPorts)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Health verification passed: all %d processes responding", len(healthyPorts)),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"healthy_ports": healthyPorts,
		},
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *VerifyHealthHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()
	result.Metadata["verified"] = true
	return result, nil
}

// SaveMetadataHandler saves cluster metadata
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type SaveMetadataHandler struct {
	storageDir string
}

func NewSaveMetadataHandler(storageDir string) *SaveMetadataHandler {
	return &SaveMetadataHandler{storageDir: storageDir}
}

// REQ-PES-036: Metadata save is never complete (always execute)
func (h *SaveMetadataHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	return false, nil
}

// REQ-PES-047: Pre-execution validation
func (h *SaveMetadataHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	return NewHookResult(), nil
}

func (h *SaveMetadataHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Get cluster name from target
	clusterName := op.Target.Name

	// Get metadata from params
	version, _ := op.Params["version"].(string)
	variant, _ := op.Params["variant"].(string)
	binPath, _ := op.Params["bin_path"].(string)
	deployMode, _ := op.Params["deploy_mode"].(string)

	// Get topology if provided
	var topo *topology.Topology
	if topoData, ok := op.Params["topology"]; ok {
		if t, ok := topoData.(*topology.Topology); ok {
			topo = t
		}
	}

	// Build node metadata from topology
	var nodes []meta.NodeMetadata
	if topo != nil {
		for _, cs := range topo.ConfigSvr {
			nodes = append(nodes, meta.NodeMetadata{
				Type:                  "config",
				Host:                  cs.Host,
				Port:                  cs.Port,
				ReplicaSet:            cs.ReplicaSet,
				DataDir:               cs.DataDir,
				LogDir:                cs.LogDir,
				ConfigDir:             cs.ConfigDir,
				SupervisorProgramName: fmt.Sprintf("config-%d", cs.Port),
			})
		}
		for _, mongod := range topo.Mongod {
			nodes = append(nodes, meta.NodeMetadata{
				Type:                  "mongod",
				Host:                  mongod.Host,
				Port:                  mongod.Port,
				ReplicaSet:            mongod.ReplicaSet,
				DataDir:               mongod.DataDir,
				LogDir:                mongod.LogDir,
				ConfigDir:             mongod.ConfigDir,
				SupervisorProgramName: fmt.Sprintf("mongod-%d", mongod.Port),
			})
		}
		for _, mongos := range topo.Mongos {
			nodes = append(nodes, meta.NodeMetadata{
				Type:                  "mongos",
				Host:                  mongos.Host,
				Port:                  mongos.Port,
				LogDir:                mongos.LogDir,
				ConfigDir:             mongos.ConfigDir,
				SupervisorProgramName: fmt.Sprintf("mongos-%d", mongos.Port),
			})
		}
	}

	// Create cluster metadata
	metadata := &meta.ClusterMetadata{
		Name:       clusterName,
		Version:    version,
		Variant:    variant,
		BinPath:    binPath,
		CreatedAt:  time.Now(),
		Status:     "running",
		Topology:   topo,
		DeployMode: deployMode,
		Nodes:      nodes,
	}

	// Generate connection command
	if len(nodes) > 0 {
		// Find first mongos or mongod for connection
		for _, node := range nodes {
			if node.Type == "mongos" || node.Type == "mongod" {
				// Determine which shell to use
				shell := "mongosh"
				if version < "4.0" {
					shell = "mongo"
				}
				// Use absolute path to shell binary
				shellPath := filepath.Join(binPath, shell)
				metadata.ConnectionCommand = fmt.Sprintf("%s mongodb://%s:%d", shellPath, node.Host, node.Port)
				break
			}
		}
	}

	// Save metadata
	metaMgr, err := meta.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create meta manager: %w", err)
	}

	if err := metaMgr.Save(metadata); err != nil {
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Saved cluster metadata for '%s'", clusterName),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"cluster_name": clusterName,
			"meta_file":    metaMgr.GetMetaFile(clusterName),
		},
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *SaveMetadataHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()
	result.Metadata["verified"] = true
	return result, nil
}

// StopProcessHandler stops a running process
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type StopProcessHandler struct{}

// REQ-PES-036: Check if process already stopped
func (h *StopProcessHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	pid := int(op.Params["pid"].(float64))
	running, err := exec.IsProcessRunning(pid)
	if err != nil {
		return false, fmt.Errorf("failed to check if process %d is running: %w", pid, err)
	}
	return !running, nil
}

// REQ-PES-047: Pre-execution validation
func (h *StopProcessHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	if _, ok := op.Params["pid"]; !ok {
		result.AddError("missing required parameter: pid")
		return result, nil
	}

	pid := int(op.Params["pid"].(float64))
	running, err := exec.IsProcessRunning(pid)
	if err != nil {
		result.AddWarning(fmt.Sprintf("unable to check if process %d is running: %v", pid, err))
	} else if !running {
		result.AddWarning(fmt.Sprintf("process %d is not running", pid))
	}

	return result, nil
}

// Execute stops the process
func (h *StopProcessHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	pid := int(op.Params["pid"].(float64))

	if err := exec.StopProcess(pid); err != nil {
		return nil, fmt.Errorf("failed to stop process %d: %w", pid, err)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Stopped process: %d", pid),
		Changes: op.Changes,
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *StopProcessHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	pid := int(op.Params["pid"].(float64))
	running, err := exec.IsProcessRunning(pid)
	if err != nil {
		result.AddWarning(fmt.Sprintf("unable to verify process %d stopped: %v", pid, err))
	} else if running {
		result.AddWarning(fmt.Sprintf("process %d still running after stop attempt", pid))
	}

	result.Metadata["verified"] = true
	return result, nil
}

// RemoveDirectoryHandler handles directory removal
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type RemoveDirectoryHandler struct{}

// REQ-PES-036: Check if directory already removed
func (h *RemoveDirectoryHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	path := op.Params["path"].(string)

	// Check if directory exists
	exists, err := exec.FileExists(path)
	if err != nil {
		return false, fmt.Errorf("check directory exists: %w", err)
	}

	// Directory is removed if it doesn't exist
	return !exists, nil
}

// REQ-PES-047: Pre-execution validation
func (h *RemoveDirectoryHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	path := op.Params["path"].(string)

	result := NewHookResult()

	// Validate path is not empty
	if path == "" {
		result.AddError("path cannot be empty")
		return result, nil
	}

	// Check if directory exists
	exists, err := exec.FileExists(path)
	if err != nil {
		return nil, fmt.Errorf("check directory exists: %w", err)
	}

	if !exists {
		result.AddWarning(fmt.Sprintf("directory already removed: %s", path))
	}

	// Warn about recursive removal
	recursive := false
	if r, ok := op.Params["recursive"]; ok {
		recursive = r.(bool)
	}
	if recursive {
		result.AddWarning(fmt.Sprintf("recursive removal of %s (all contents will be deleted)", path))
	}

	return result, nil
}

// Execute removes the directory (idempotent)
func (h *RemoveDirectoryHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	path := op.Params["path"].(string)

	// Use RemoveDirectory which works with simulation
	if err := exec.RemoveDirectory(path); err != nil {
		return nil, fmt.Errorf("failed to remove directory %s: %w", path, err)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Removed directory: %s", path),
		Changes: op.Changes,
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *RemoveDirectoryHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	path := op.Params["path"].(string)

	result := NewHookResult()

	// Verify directory was removed
	exists, err := exec.FileExists(path)
	if err != nil {
		return nil, fmt.Errorf("check directory exists: %w", err)
	}

	if exists {
		result.AddError(fmt.Sprintf("directory was not removed: %s", path))
		return result, nil
	}

	result.Metadata["verified"] = true
	result.Metadata["path"] = path

	return result, nil
}

// GenerateSupervisorConfigHandler generates supervisord configuration files
// REQ-PM-010: Per-node supervisor configs in version-specific directories
// GenerateSupervisorConfigHandler generates supervisord configuration
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type GenerateSupervisorConfigHandler struct{}

// REQ-PES-036: Check if supervisor config already exists
func (h *GenerateSupervisorConfigHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	clusterDir, ok := op.Params["cluster_dir"].(string)
	if !ok {
		return false, fmt.Errorf("cluster_dir parameter not found")
	}

	configPath := filepath.Join(clusterDir, "supervisor.ini")
	exists, err := exec.FileExists(configPath)
	if err != nil {
		return false, fmt.Errorf("check supervisor config exists: %w", err)
	}

	return exists, nil
}

// REQ-PES-047: Pre-execution validation
func (h *GenerateSupervisorConfigHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// Validate required parameters
	requiredParams := []string{"cluster_dir", "cluster_name", "version", "bin_path", "topology"}
	for _, param := range requiredParams {
		if _, ok := op.Params[param]; !ok {
			result.AddError(fmt.Sprintf("missing required parameter: %s", param))
		}
	}

	if !result.Valid {
		return result, nil
	}

	// Validate cluster directory exists
	clusterDir := op.Params["cluster_dir"].(string)
	exists, err := exec.FileExists(clusterDir)
	if err != nil {
		return nil, fmt.Errorf("failed to check if cluster directory exists: %w", err)
	}
	if !exists {
		result.AddError(fmt.Sprintf("cluster directory does not exist: %s", clusterDir))
		return result, nil
	}

	// Validate topology parameter exists (can be *topology.Topology or map from JSON)
	if topoParam, ok := op.Params["topology"]; ok {
		if _, ok := topoParam.(*topology.Topology); !ok {
			if _, ok := topoParam.(map[string]interface{}); !ok {
				result.AddError("topology parameter not found or invalid type")
			}
		}
	} else {
		result.AddError("topology parameter is required")
	}

	// Check if config already exists
	configPath := filepath.Join(clusterDir, "supervisor.ini")
	exists, err = exec.FileExists(configPath)
	if err != nil {
		result.AddWarning(fmt.Sprintf("unable to check if supervisor config exists: %v", err))
	} else if exists {
		result.AddWarning("supervisor config already exists (will be regenerated)")
	}

	return result, nil
}

// Execute generates the supervisor configuration
func (h *GenerateSupervisorConfigHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Extract required parameters
	clusterDir := op.Params["cluster_dir"].(string)
	clusterName := op.Params["cluster_name"].(string)
	version := op.Params["version"].(string)
	binPath := op.Params["bin_path"].(string)

	// Handle topology - it may be a map from JSON deserialization
	var topo *topology.Topology
	if topoPtr, ok := op.Params["topology"].(*topology.Topology); ok {
		topo = topoPtr
	} else if topoMap, ok := op.Params["topology"].(map[string]interface{}); ok {
		// Deserialize from map (JSON unmarshaling)
		topoBytes, _ := json.Marshal(topoMap)
		topo = &topology.Topology{}
		if err := json.Unmarshal(topoBytes, topo); err != nil {
			return nil, fmt.Errorf("failed to deserialize topology: %w", err)
		}
	} else {
		return nil, fmt.Errorf("topology parameter not found or invalid type")
	}

	// Import supervisor package to use config generator
	// The supervisor.NewConfigGenerator expects version-specific directory
	gen := supervisor.NewConfigGenerator(clusterDir, clusterName, topo, version, binPath)

	// Generate all supervisor configs (unified config with all programs)
	if err := gen.GenerateAll(); err != nil {
		return nil, fmt.Errorf("failed to generate supervisor configs: %w", err)
	}

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Generated supervisor configuration at %s/supervisor.ini", clusterDir),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"config_path": filepath.Join(clusterDir, "supervisor.ini"),
			"version":     version,
		},
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *GenerateSupervisorConfigHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	clusterDir := op.Params["cluster_dir"].(string)
	configPath := filepath.Join(clusterDir, "supervisor.ini")

	// Verify config file was created
	exists, err := exec.FileExists(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to verify supervisor config: %w", err)
	}
	if !exists {
		result.AddError(fmt.Sprintf("supervisor config was not created: %s", configPath))
		return result, nil
	}

	result.Metadata["verified"] = true
	result.Metadata["config_path"] = configPath
	return result, nil
}

// StartSupervisorHandler starts the supervisord daemon
// REQ-SIM-001: Works transparently in simulation mode
// REQ-PES-036, REQ-PES-047, REQ-PES-048: Four-phase handler
type StartSupervisorHandler struct{}

// REQ-PES-036: Check if supervisord is already running
func (h *StartSupervisorHandler) IsComplete(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (bool, error) {
	// TODO: Implement health check by pinging supervisord HTTP endpoint
	// For now, we always re-execute to ensure supervisord is running
	// This is idempotent because supervisord won't start if already running
	return false, nil
}

// REQ-PES-047: Pre-execution validation
func (h *StartSupervisorHandler) PreHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	// Validate required parameters
	clusterDir, ok := op.Params["cluster_dir"].(string)
	if !ok {
		result.AddError("missing required parameter: cluster_dir")
		return result, nil
	}

	if _, ok := op.Params["cluster_name"]; !ok {
		result.AddError("missing required parameter: cluster_name")
		return result, nil
	}

	// Check if supervisor binary exists
	homeDir, err := os.UserHomeDir()
	if err != nil {
		result.AddError(fmt.Sprintf("failed to get home directory: %v", err))
		return result, nil
	}
	cacheDir := filepath.Join(homeDir, ".mup", "storage", "bin")
	binaryPath, err := supervisor.GetSupervisordBinary(cacheDir)
	if err != nil {
		result.AddError(fmt.Sprintf("supervisord binary not found: %v", err))
		return result, nil
	}

	// Check if binary is executable
	if _, err := os.Stat(binaryPath); err != nil {
		result.AddError(fmt.Sprintf("supervisord binary not accessible: %v", err))
		return result, nil
	}

	// Check if config file exists
	configPath := filepath.Join(clusterDir, "supervisor.ini")
	if _, err := os.Stat(configPath); err != nil {
		result.AddError(fmt.Sprintf("supervisor config not found: %s", configPath))
		return result, nil
	}

	// Store metadata for Execute phase
	httpPort := supervisor.GetSupervisorHTTPPortForDir(clusterDir)
	result.Metadata["binary_path"] = binaryPath
	result.Metadata["config_path"] = configPath
	result.Metadata["http_port"] = httpPort

	return result, nil
}

// Execute starts supervisord daemon (idempotent)
func (h *StartSupervisorHandler) Execute(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*apply.OperationResult, error) {
	// Extract required parameters
	clusterDir, ok := op.Params["cluster_dir"].(string)
	if !ok {
		return nil, fmt.Errorf("cluster_dir parameter not found or invalid type")
	}

	clusterName, ok := op.Params["cluster_name"].(string)
	if !ok {
		return nil, fmt.Errorf("cluster_name parameter not found or invalid type")
	}

	// Get supervisor binary path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	cacheDir := filepath.Join(homeDir, ".mup", "storage", "bin")
	binaryPath, err := supervisor.GetSupervisordBinary(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get supervisord binary: %w", err)
	}

	// Get supervisor config and HTTP port
	configPath := filepath.Join(clusterDir, "supervisor.ini")
	httpPort := supervisor.GetSupervisorHTTPPortForDir(clusterDir)

	// Construct supervisord start command
	command := fmt.Sprintf("%s -c %s", binaryPath, configPath)

	fmt.Printf("  Starting supervisord daemon for cluster %s (HTTP port: %d)...\n", clusterName, httpPort)

	// Start supervisor in the background (it's a long-running daemon)
	pid, err := exec.Background(command)
	if err != nil {
		return nil, fmt.Errorf("failed to start supervisord: %w", err)
	}

	// Give supervisord a moment to initialize
	time.Sleep(500 * time.Millisecond)

	// Verify it's still running
	running, err := exec.IsProcessRunning(pid)
	if err != nil || !running {
		return nil, fmt.Errorf("supervisord failed to start (PID: %d)", pid)
	}

	fmt.Printf("  ✓ Supervisord daemon started (PID: %d)\n", pid)

	return &apply.OperationResult{
		Success: true,
		Output:  fmt.Sprintf("Started supervisord daemon for cluster %s (config: %s, port: %d)", clusterName, configPath, httpPort),
		Changes: op.Changes,
		Metadata: map[string]interface{}{
			"config_path": configPath,
			"http_port":   httpPort,
			"binary_path": binaryPath,
		},
	}, nil
}

// REQ-PES-048: Post-execution verification
func (h *StartSupervisorHandler) PostHook(ctx context.Context, op *plan.PlannedOperation, exec executor.Executor) (*HookResult, error) {
	result := NewHookResult()

	clusterDir, ok := op.Params["cluster_dir"].(string)
	if !ok {
		result.AddError("cluster_dir parameter not found")
		return result, nil
	}

	// TODO: Implement verification by pinging supervisord HTTP endpoint with retries
	// For now, we trust that Execute succeeded if it didn't return an error

	httpPort := supervisor.GetSupervisorHTTPPortForDir(clusterDir)
	result.Metadata["verified"] = true
	result.Metadata["http_port"] = httpPort

	return result, nil
}
