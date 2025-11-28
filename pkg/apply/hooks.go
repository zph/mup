package apply

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/zph/mup/pkg/plan"
)

// HookManager manages lifecycle hooks
type HookManager struct {
	hooks map[plan.HookEvent]*plan.Hook
}

// NewHookManager creates a new hook manager
func NewHookManager() *HookManager {
	return &HookManager{
		hooks: make(map[plan.HookEvent]*plan.Hook),
	}
}

// RegisterHook registers a hook for an event
func (m *HookManager) RegisterHook(event plan.HookEvent, hook *plan.Hook) {
	m.hooks[event] = hook
}

// ExecuteHook executes a hook for an event if one is registered
func (m *HookManager) ExecuteHook(ctx context.Context, event plan.HookEvent, p *plan.Plan, state *ApplyState) error {
	hook, ok := m.hooks[event]
	if !ok {
		return nil // No hook registered for this event
	}

	return m.ExecuteCustomHook(ctx, hook, p, state)
}

// ExecuteCustomHook executes a specific hook
func (m *HookManager) ExecuteCustomHook(ctx context.Context, hook *plan.Hook, p *plan.Plan, state *ApplyState) error {
	if hook == nil {
		return nil
	}

	state.Log("info", state.CurrentPhase, "", fmt.Sprintf("Executing hook: %s", hook.Name))

	// Prepare hook context
	env := m.prepareHookEnvironment(hook, p, state)

	// Create command
	cmd := exec.CommandContext(ctx, "sh", "-c", hook.Command)
	cmd.Env = append(os.Environ(), env...)

	// Set timeout if specified
	if hook.Timeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, hook.Timeout)
		defer cancel()
		cmd = exec.CommandContext(timeoutCtx, "sh", "-c", hook.Command)
		cmd.Env = append(os.Environ(), env...)
	}

	// Execute command
	output, err := cmd.CombinedOutput()
	if err != nil {
		state.Log("error", state.CurrentPhase, "", fmt.Sprintf("Hook %s failed: %v\nOutput: %s", hook.Name, err, string(output)))
		if !hook.ContinueOnError {
			return fmt.Errorf("hook %s failed: %w", hook.Name, err)
		}
		// Log error but continue
		state.Log("warn", state.CurrentPhase, "", fmt.Sprintf("Hook %s failed but continuing due to continue_on_error", hook.Name))
		return nil
	}

	state.Log("info", state.CurrentPhase, "", fmt.Sprintf("Hook %s completed successfully\nOutput: %s", hook.Name, string(output)))
	return nil
}

// prepareHookEnvironment prepares environment variables for hooks
func (m *HookManager) prepareHookEnvironment(hook *plan.Hook, p *plan.Plan, state *ApplyState) []string {
	env := []string{
		fmt.Sprintf("MUP_CLUSTER_NAME=%s", p.ClusterName),
		fmt.Sprintf("MUP_OPERATION=%s", p.Operation),
		fmt.Sprintf("MUP_PLAN_ID=%s", p.PlanID),
		fmt.Sprintf("MUP_STATE_ID=%s", state.StateID),
		fmt.Sprintf("MUP_CURRENT_PHASE=%s", state.CurrentPhase),
		fmt.Sprintf("MUP_STATUS=%s", state.Status),
	}

	// Add plan-specific environment
	if p.Version != "" {
		env = append(env, fmt.Sprintf("MUP_VERSION=%s", p.Version))
	}
	if p.Variant != "" {
		env = append(env, fmt.Sprintf("MUP_VARIANT=%s", p.Variant))
	}

	// Add custom environment from plan
	for k, v := range p.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Add custom environment from hook
	for k, v := range hook.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

// HookContext contains context information for hooks
type HookContext struct {
	ClusterName string
	Operation   string
	PlanID      string
	StateID     string
	Phase       string
	Status      ApplyStatus
	Version     string
	Variant     string
}

// ToEnvironment converts hook context to environment variables
func (ctx *HookContext) ToEnvironment() []string {
	env := []string{
		fmt.Sprintf("MUP_CLUSTER_NAME=%s", ctx.ClusterName),
		fmt.Sprintf("MUP_OPERATION=%s", ctx.Operation),
		fmt.Sprintf("MUP_PLAN_ID=%s", ctx.PlanID),
		fmt.Sprintf("MUP_STATE_ID=%s", ctx.StateID),
		fmt.Sprintf("MUP_CURRENT_PHASE=%s", ctx.Phase),
		fmt.Sprintf("MUP_STATUS=%s", ctx.Status),
	}

	if ctx.Version != "" {
		env = append(env, fmt.Sprintf("MUP_VERSION=%s", ctx.Version))
	}
	if ctx.Variant != "" {
		env = append(env, fmt.Sprintf("MUP_VARIANT=%s", ctx.Variant))
	}

	return env
}
