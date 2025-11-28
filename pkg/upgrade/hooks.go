package upgrade

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// [UPG-009] Custom safety check hooks and wait time configuration

// HookType represents different lifecycle points where hooks can be executed
type HookType string

const (
	// Node-level hooks
	HookBeforeNodeUpgrade HookType = "before-node-upgrade"
	HookAfterNodeUpgrade  HookType = "after-node-upgrade"
	HookOnNodeFailure     HookType = "on-node-failure"

	// Replica set hooks
	HookBeforePrimaryStepdown HookType = "before-primary-stepdown"
	HookAfterPrimaryStepdown  HookType = "after-primary-stepdown"
	HookBeforeSecondaryUpgrade HookType = "before-secondary-upgrade"
	HookAfterSecondaryUpgrade HookType = "after-secondary-upgrade"

	// Phase hooks
	HookBeforePhase HookType = "before-phase"
	HookAfterPhase  HookType = "after-phase"

	// Critical operation hooks
	HookBeforeFCVUpgrade    HookType = "before-fcv-upgrade"
	HookAfterFCVUpgrade     HookType = "after-fcv-upgrade"
	HookBeforeBalancerStop  HookType = "before-balancer-stop"
	HookAfterBalancerStart  HookType = "after-balancer-start"

	// Shard hooks
	HookBeforeShardUpgrade HookType = "before-shard-upgrade"
	HookAfterShardUpgrade  HookType = "after-shard-upgrade"

	// Global hooks
	HookOnUpgradeStart    HookType = "on-upgrade-start"
	HookOnUpgradeComplete HookType = "on-upgrade-complete"
	HookOnUpgradeFailure  HookType = "on-upgrade-failure"
)

// WaitConfig defines wait times at various points in the upgrade
// [UPG-009] Configurable delays for upgrade pacing
type WaitConfig struct {
	// Node-level waits
	AfterNodeUpgrade       time.Duration `yaml:"after_node_upgrade"`        // Default: 5s
	AfterNodeFailure       time.Duration `yaml:"after_node_failure"`        // Default: 10s

	// Replica set waits
	AfterPrimaryStepdown   time.Duration `yaml:"after_primary_stepdown"`    // Default: 30s
	AfterSecondaryUpgrade  time.Duration `yaml:"after_secondary_upgrade"`   // Default: 10s
	BeforePrimaryUpgrade   time.Duration `yaml:"before_primary_upgrade"`    // Default: 15s

	// Shard waits
	BetweenShards          time.Duration `yaml:"between_shards"`            // Default: 60s
	AfterShardComplete     time.Duration `yaml:"after_shard_complete"`      // Default: 30s

	// Phase waits
	BetweenPhases          time.Duration `yaml:"between_phases"`            // Default: 10s

	// Critical operation waits
	AfterBalancerStop      time.Duration `yaml:"after_balancer_stop"`       // Default: 30s
	AfterFCVUpgrade        time.Duration `yaml:"after_fcv_upgrade"`         // Default: 60s

	// Health check intervals
	HealthCheckInterval    time.Duration `yaml:"health_check_interval"`     // Default: 5s
	HealthCheckTimeout     time.Duration `yaml:"health_check_timeout"`      // Default: 300s
}

// DefaultWaitConfig returns default wait times
func DefaultWaitConfig() *WaitConfig {
	return &WaitConfig{
		AfterNodeUpgrade:       5 * time.Second,
		AfterNodeFailure:       10 * time.Second,
		AfterPrimaryStepdown:   30 * time.Second,
		AfterSecondaryUpgrade:  10 * time.Second,
		BeforePrimaryUpgrade:   15 * time.Second,
		BetweenShards:          60 * time.Second,
		AfterShardComplete:     30 * time.Second,
		BetweenPhases:          10 * time.Second,
		AfterBalancerStop:      30 * time.Second,
		AfterFCVUpgrade:        60 * time.Second,
		HealthCheckInterval:    5 * time.Second,
		HealthCheckTimeout:     300 * time.Second,
	}
}

// HookContext provides context information to hooks
type HookContext struct {
	HookType     HookType
	ClusterName  string
	Phase        PhaseName
	Node         string // host:port
	NodeRole     string // PRIMARY, SECONDARY, MONGOS, etc.
	ShardName    string
	FromVersion  string
	ToVersion    string
	AttemptCount int
	Error        error // For failure hooks
	Metadata     map[string]string // Additional context
}

// Hook represents a lifecycle hook
type Hook interface {
	Execute(ctx context.Context, hookCtx HookContext) error
	Name() string
	Type() HookType
}

// CommandHook executes a shell command
type CommandHook struct {
	name     string
	hookType HookType
	command  string
	timeout  time.Duration
	env      map[string]string
}

// NewCommandHook creates a new command hook
func NewCommandHook(name string, hookType HookType, command string, timeout time.Duration) *CommandHook {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &CommandHook{
		name:     name,
		hookType: hookType,
		command:  command,
		timeout:  timeout,
		env:      make(map[string]string),
	}
}

// Execute runs the command hook
func (h *CommandHook) Execute(ctx context.Context, hookCtx HookContext) error {
	fmt.Printf("  → Executing hook: %s\n", h.name)

	// Create command context with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	// Prepare command
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", h.command)

	// Set environment variables
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_HOOK_TYPE=%s", hookCtx.HookType))
	cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_CLUSTER_NAME=%s", hookCtx.ClusterName))
	cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_PHASE=%s", hookCtx.Phase))
	cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_NODE=%s", hookCtx.Node))
	cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_NODE_ROLE=%s", hookCtx.NodeRole))
	cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_SHARD_NAME=%s", hookCtx.ShardName))
	cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_FROM_VERSION=%s", hookCtx.FromVersion))
	cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_TO_VERSION=%s", hookCtx.ToVersion))
	cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_ATTEMPT=%d", hookCtx.AttemptCount))

	if hookCtx.Error != nil {
		cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_ERROR=%s", hookCtx.Error.Error()))
	}

	// Add custom env vars
	for k, v := range h.env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Add metadata
	for k, v := range hookCtx.Metadata {
		cmd.Env = append(cmd.Env, fmt.Sprintf("MUP_META_%s=%s", strings.ToUpper(k), v))
	}

	// Run command and capture output
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("  ✗ Hook failed: %v\n", err)
		if len(output) > 0 {
			fmt.Printf("  Output: %s\n", string(output))
		}
		return fmt.Errorf("hook %s failed: %w", h.name, err)
	}

	if len(output) > 0 {
		fmt.Printf("  Output: %s\n", string(output))
	}
	fmt.Printf("  ✓ Hook completed\n")

	return nil
}

func (h *CommandHook) Name() string     { return h.name }
func (h *CommandHook) Type() HookType   { return h.hookType }
func (h *CommandHook) SetEnv(key, value string) { h.env[key] = value }

// FunctionHook executes a Go function
type FunctionHook struct {
	name     string
	hookType HookType
	fn       func(context.Context, HookContext) error
}

// NewFunctionHook creates a new function hook
func NewFunctionHook(name string, hookType HookType, fn func(context.Context, HookContext) error) *FunctionHook {
	return &FunctionHook{
		name:     name,
		hookType: hookType,
		fn:       fn,
	}
}

// Execute runs the function hook
func (h *FunctionHook) Execute(ctx context.Context, hookCtx HookContext) error {
	fmt.Printf("  → Executing hook: %s\n", h.name)
	if err := h.fn(ctx, hookCtx); err != nil {
		fmt.Printf("  ✗ Hook failed: %v\n", err)
		return fmt.Errorf("hook %s failed: %w", h.name, err)
	}
	fmt.Printf("  ✓ Hook completed\n")
	return nil
}

func (h *FunctionHook) Name() string   { return h.name }
func (h *FunctionHook) Type() HookType { return h.hookType }

// HookRegistry manages hooks for an upgrade
type HookRegistry struct {
	hooks map[HookType][]Hook
}

// NewHookRegistry creates a new hook registry
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		hooks: make(map[HookType][]Hook),
	}
}

// Register adds a hook to the registry
func (r *HookRegistry) Register(hook Hook) {
	r.hooks[hook.Type()] = append(r.hooks[hook.Type()], hook)
}

// Execute runs all hooks for a given type
func (r *HookRegistry) Execute(ctx context.Context, hookCtx HookContext) error {
	hooks, exists := r.hooks[hookCtx.HookType]
	if !exists || len(hooks) == 0 {
		return nil // No hooks registered for this type
	}

	fmt.Printf("  Running %d hook(s) for %s...\n", len(hooks), hookCtx.HookType)

	for _, hook := range hooks {
		if err := hook.Execute(ctx, hookCtx); err != nil {
			return fmt.Errorf("hook execution failed: %w", err)
		}
	}

	return nil
}

// HasHooks returns whether any hooks are registered for the given type
func (r *HookRegistry) HasHooks(hookType HookType) bool {
	hooks, exists := r.hooks[hookType]
	return exists && len(hooks) > 0
}

// Validate checks that all registered hooks are valid
// For CommandHooks: validates that script files exist and are executable
// For FunctionHooks: no validation needed (compile-time checked)
func (r *HookRegistry) Validate() error {
	for hookType, hooks := range r.hooks {
		for _, hook := range hooks {
			// Only validate CommandHooks
			if cmdHook, ok := hook.(*CommandHook); ok {
				if err := validateCommandHook(cmdHook); err != nil {
					return fmt.Errorf("hook %s (%s): %w", cmdHook.Name(), hookType, err)
				}
			}
		}
	}
	return nil
}

// validateCommandHook checks if a command hook is valid
// If the command appears to be a script file path, validate it exists and is executable
func validateCommandHook(hook *CommandHook) error {
	// Extract the first token from the command
	// This handles cases like "./script.sh arg1 arg2" or "/path/to/script.sh"
	command := strings.TrimSpace(hook.command)
	if command == "" {
		return fmt.Errorf("empty command")
	}

	// Split on whitespace to get first token
	tokens := strings.Fields(command)
	if len(tokens) == 0 {
		return fmt.Errorf("empty command")
	}

	firstToken := tokens[0]

	// Check if first token looks like a file path (contains '/')
	// This distinguishes "echo hello" from "./script.sh" or "/usr/bin/script"
	if !strings.Contains(firstToken, "/") {
		// Not a file path, it's a shell command - no validation needed
		return nil
	}

	// It's a file path - validate it exists and is executable
	fileInfo, err := os.Stat(firstToken)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("script file not found: %s", firstToken)
		}
		return fmt.Errorf("cannot access script file %s: %w", firstToken, err)
	}

	// Check if it's a regular file
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("script path is not a regular file: %s", firstToken)
	}

	// Check if it's executable (has any execute bit set)
	if fileInfo.Mode()&0111 == 0 {
		return fmt.Errorf("script file is not executable: %s (use chmod +x)", firstToken)
	}

	return nil
}

// WaitManager handles wait times during upgrade
type WaitManager struct {
	config *WaitConfig
}

// NewWaitManager creates a new wait manager
func NewWaitManager(config *WaitConfig) *WaitManager {
	if config == nil {
		config = DefaultWaitConfig()
	}
	return &WaitManager{config: config}
}

// Wait pauses for the configured duration
func (w *WaitManager) Wait(ctx context.Context, waitType string) error {
	var duration time.Duration
	var description string

	switch waitType {
	case "after-node-upgrade":
		duration = w.config.AfterNodeUpgrade
		description = "Waiting for node to stabilize"
	case "after-node-failure":
		duration = w.config.AfterNodeFailure
		description = "Waiting after node failure"
	case "after-primary-stepdown":
		duration = w.config.AfterPrimaryStepdown
		description = "Waiting for new primary election"
	case "after-secondary-upgrade":
		duration = w.config.AfterSecondaryUpgrade
		description = "Waiting for secondary to catch up"
	case "before-primary-upgrade":
		duration = w.config.BeforePrimaryUpgrade
		description = "Waiting before primary upgrade"
	case "between-shards":
		duration = w.config.BetweenShards
		description = "Waiting between shard upgrades"
	case "after-shard-complete":
		duration = w.config.AfterShardComplete
		description = "Waiting after shard completion"
	case "between-phases":
		duration = w.config.BetweenPhases
		description = "Waiting between phases"
	case "after-balancer-stop":
		duration = w.config.AfterBalancerStop
		description = "Waiting for balancer to stop"
	case "after-fcv-upgrade":
		duration = w.config.AfterFCVUpgrade
		description = "Waiting for FCV upgrade to propagate"
	default:
		return fmt.Errorf("unknown wait type: %s", waitType)
	}

	if duration == 0 {
		return nil // Skip if duration is 0
	}

	fmt.Printf("  %s (%s)...\n", description, duration)

	select {
	case <-time.After(duration):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitWithProgress waits with a progress indicator
func (w *WaitManager) WaitWithProgress(ctx context.Context, waitType string) error {
	var duration time.Duration
	var description string

	switch waitType {
	case "after-primary-stepdown":
		duration = w.config.AfterPrimaryStepdown
		description = "Waiting for new primary election"
	case "between-shards":
		duration = w.config.BetweenShards
		description = "Waiting between shard upgrades"
	case "after-fcv-upgrade":
		duration = w.config.AfterFCVUpgrade
		description = "Waiting for FCV upgrade to propagate"
	default:
		return w.Wait(ctx, waitType)
	}

	if duration == 0 {
		return nil
	}

	fmt.Printf("  %s (%s)\n", description, duration)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(duration)

	for {
		select {
		case <-ticker.C:
			remaining := time.Until(deadline)
			if remaining <= 0 {
				fmt.Println()
				return nil
			}
			fmt.Printf("\r  Time remaining: %s   ", remaining.Round(time.Second))
		case <-ctx.Done():
			fmt.Println()
			return ctx.Err()
		}
	}
}

// Example hook configurations

// CreateHealthCheckHook creates a hook that waits for external health check
func CreateHealthCheckHook(healthCheckURL string) *CommandHook {
	command := fmt.Sprintf(`
		echo "Checking external health system at %s..."
		for i in {1..30}; do
			if curl -sf "%s" > /dev/null 2>&1; then
				echo "Health check passed"
				exit 0
			fi
			echo "Attempt $i failed, retrying..."
			sleep 5
		done
		echo "Health check failed after 30 attempts"
		exit 1
	`, healthCheckURL, healthCheckURL)

	return NewCommandHook("external-health-check", HookAfterNodeUpgrade, command, 3*time.Minute)
}

// CreateSlackNotificationHook creates a hook that sends Slack notifications
func CreateSlackNotificationHook(webhookURL string) *CommandHook {
	command := fmt.Sprintf(`
		curl -X POST -H 'Content-type: application/json' \
		--data "{\"text\":\"Upgrade Event: $MUP_HOOK_TYPE\nCluster: $MUP_CLUSTER_NAME\nNode: $MUP_NODE ($MUP_NODE_ROLE)\nPhase: $MUP_PHASE\"}" \
		%s
	`, webhookURL)

	return NewCommandHook("slack-notification", HookAfterNodeUpgrade, command, 10*time.Second)
}

// CreateCustomWaitHook creates a hook that pauses for user confirmation
func CreateCustomWaitHook(message string) *FunctionHook {
	return NewFunctionHook("custom-wait", HookBeforePrimaryStepdown, func(ctx context.Context, hookCtx HookContext) error {
		fmt.Printf("\n%s\n", message)
		fmt.Printf("Node: %s (%s)\n", hookCtx.Node, hookCtx.NodeRole)
		fmt.Print("Press Enter to continue...")
		fmt.Scanln()
		return nil
	})
}
