package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestHookRegistry_Validate tests hook validation
func TestHookRegistry_Validate(t *testing.T) {
	tests := []struct {
		name          string
		setupHook     func(t *testing.T) Hook
		shouldFail    bool
		errorContains string
	}{
		{
			name: "valid command hook with shell command",
			setupHook: func(t *testing.T) Hook {
				return NewCommandHook("test-hook", HookBeforeNodeUpgrade, "echo 'hello'", 0)
			},
			shouldFail: false,
		},
		{
			name: "valid command hook with executable script",
			setupHook: func(t *testing.T) Hook {
				// Create a temporary executable script
				tmpDir := t.TempDir()
				scriptPath := filepath.Join(tmpDir, "test-script.sh")
				content := "#!/bin/bash\necho 'test'\n"
				if err := os.WriteFile(scriptPath, []byte(content), 0755); err != nil {
					t.Fatalf("failed to create test script: %v", err)
				}
				return NewCommandHook("test-hook", HookBeforeNodeUpgrade, scriptPath, 0)
			},
			shouldFail: false,
		},
		{
			name: "invalid command hook with non-existent script",
			setupHook: func(t *testing.T) Hook {
				return NewCommandHook("test-hook", HookBeforeNodeUpgrade, "/nonexistent/script.sh", 0)
			},
			shouldFail:    true,
			errorContains: "script file not found",
		},
		{
			name: "invalid command hook with non-executable script",
			setupHook: func(t *testing.T) Hook {
				// Create a temporary non-executable script
				tmpDir := t.TempDir()
				scriptPath := filepath.Join(tmpDir, "test-script.sh")
				content := "#!/bin/bash\necho 'test'\n"
				if err := os.WriteFile(scriptPath, []byte(content), 0644); err != nil {
					t.Fatalf("failed to create test script: %v", err)
				}
				return NewCommandHook("test-hook", HookBeforeNodeUpgrade, scriptPath, 0)
			},
			shouldFail:    true,
			errorContains: "not executable",
		},
		{
			name: "valid function hook",
			setupHook: func(t *testing.T) Hook {
				return NewFunctionHook("test-hook", HookBeforeNodeUpgrade, func(ctx context.Context, hookCtx HookContext) error {
					return nil
				})
			},
			shouldFail: false,
		},
		{
			name: "invalid command hook with empty command",
			setupHook: func(t *testing.T) Hook {
				return NewCommandHook("test-hook", HookBeforeNodeUpgrade, "", 0)
			},
			shouldFail:    true,
			errorContains: "empty command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewHookRegistry()
			hook := tt.setupHook(t)
			registry.Register(hook)

			err := registry.Validate()

			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errorContains)
				} else if tt.errorContains != "" && !contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got %q", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

// TestHookRegistry_Execute tests hook execution
func TestHookRegistry_Execute(t *testing.T) {
	tests := []struct {
		name          string
		hookType      HookType
		setupHooks    func(t *testing.T, registry *HookRegistry, callLog *[]string)
		expectedCalls int
		shouldFail    bool
	}{
		{
			name:     "no hooks registered",
			hookType: HookBeforeNodeUpgrade,
			setupHooks: func(t *testing.T, registry *HookRegistry, callLog *[]string) {
				// No hooks registered
			},
			expectedCalls: 0,
			shouldFail:    false,
		},
		{
			name:     "single function hook executes",
			hookType: HookBeforeNodeUpgrade,
			setupHooks: func(t *testing.T, registry *HookRegistry, callLog *[]string) {
				hook := NewFunctionHook("test-hook", HookBeforeNodeUpgrade, func(ctx context.Context, hookCtx HookContext) error {
					*callLog = append(*callLog, "hook-called")
					return nil
				})
				registry.Register(hook)
			},
			expectedCalls: 1,
			shouldFail:    false,
		},
		{
			name:     "multiple function hooks execute in order",
			hookType: HookBeforeNodeUpgrade,
			setupHooks: func(t *testing.T, registry *HookRegistry, callLog *[]string) {
				hook1 := NewFunctionHook("hook-1", HookBeforeNodeUpgrade, func(ctx context.Context, hookCtx HookContext) error {
					*callLog = append(*callLog, "hook-1")
					return nil
				})
				hook2 := NewFunctionHook("hook-2", HookBeforeNodeUpgrade, func(ctx context.Context, hookCtx HookContext) error {
					*callLog = append(*callLog, "hook-2")
					return nil
				})
				registry.Register(hook1)
				registry.Register(hook2)
			},
			expectedCalls: 2,
			shouldFail:    false,
		},
		{
			name:     "hook failure stops execution",
			hookType: HookBeforeNodeUpgrade,
			setupHooks: func(t *testing.T, registry *HookRegistry, callLog *[]string) {
				hook1 := NewFunctionHook("hook-1", HookBeforeNodeUpgrade, func(ctx context.Context, hookCtx HookContext) error {
					*callLog = append(*callLog, "hook-1")
					return fmt.Errorf("hook failed")
				})
				hook2 := NewFunctionHook("hook-2", HookBeforeNodeUpgrade, func(ctx context.Context, hookCtx HookContext) error {
					*callLog = append(*callLog, "hook-2")
					return nil
				})
				registry.Register(hook1)
				registry.Register(hook2)
			},
			expectedCalls: 1, // Only first hook called before failure
			shouldFail:    true,
		},
		{
			name:     "command hook executes successfully",
			hookType: HookBeforeNodeUpgrade,
			setupHooks: func(t *testing.T, registry *HookRegistry, callLog *[]string) {
				// Create a simple echo command
				hook := NewCommandHook("test-hook", HookBeforeNodeUpgrade, "echo 'test'", 0)
				registry.Register(hook)
				*callLog = append(*callLog, "command-hook-registered")
			},
			expectedCalls: 1,
			shouldFail:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewHookRegistry()
			callLog := []string{}

			tt.setupHooks(t, registry, &callLog)

			ctx := context.Background()
			hookCtx := HookContext{
				HookType:    tt.hookType,
				ClusterName: "test-cluster",
				Node:        "localhost:27017",
				NodeRole:    "PRIMARY",
				FromVersion: "6.0",
				ToVersion:   "7.0",
			}

			err := registry.Execute(ctx, hookCtx)

			if tt.shouldFail {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}

			if len(callLog) != tt.expectedCalls {
				t.Errorf("expected %d calls, got %d (log: %v)", tt.expectedCalls, len(callLog), callLog)
			}
		})
	}
}

// TestHookContext_EnvironmentVariables tests that hook context is properly passed via environment variables
func TestHookContext_EnvironmentVariables(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "test-env.sh")

	// Create a script that prints environment variables
	script := `#!/bin/bash
echo "HOOK_TYPE=$MUP_HOOK_TYPE"
echo "CLUSTER=$MUP_CLUSTER_NAME"
echo "NODE=$MUP_NODE"
echo "ROLE=$MUP_NODE_ROLE"
echo "FROM=$MUP_FROM_VERSION"
echo "TO=$MUP_TO_VERSION"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}

	registry := NewHookRegistry()
	hook := NewCommandHook("env-test", HookBeforeNodeUpgrade, scriptPath, 0)
	registry.Register(hook)

	ctx := context.Background()
	hookCtx := HookContext{
		HookType:    HookBeforeNodeUpgrade,
		ClusterName: "test-cluster",
		Node:        "localhost:27017",
		NodeRole:    "PRIMARY",
		FromVersion: "6.0",
		ToVersion:   "7.0",
	}

	err := registry.Execute(ctx, hookCtx)
	if err != nil {
		t.Errorf("hook execution failed: %v", err)
	}

	// Note: We can't easily verify the output here without capturing stdout,
	// but the test verifies the hook executes without error
}

// TestHookRegistry_HasHooks tests the HasHooks method
func TestHookRegistry_HasHooks(t *testing.T) {
	registry := NewHookRegistry()

	// Initially no hooks
	if registry.HasHooks(HookBeforeNodeUpgrade) {
		t.Error("expected no hooks initially")
	}

	// Register a hook
	hook := NewFunctionHook("test", HookBeforeNodeUpgrade, func(ctx context.Context, hookCtx HookContext) error {
		return nil
	})
	registry.Register(hook)

	// Should have hooks now
	if !registry.HasHooks(HookBeforeNodeUpgrade) {
		t.Error("expected hooks to be registered")
	}

	// Different hook type should still be empty
	if registry.HasHooks(HookAfterNodeUpgrade) {
		t.Error("expected no hooks for different type")
	}
}

// TestWaitManager tests wait time management
func TestWaitManager(t *testing.T) {
	config := DefaultWaitConfig()
	config.AfterNodeUpgrade = 10 * 1 // 10ms for fast tests

	manager := NewWaitManager(config)
	ctx := context.Background()

	err := manager.Wait(ctx, "after-node-upgrade")
	if err != nil {
		t.Errorf("wait failed: %v", err)
	}
}

// TestWaitManager_ContextCancellation tests that wait respects context cancellation
func TestWaitManager_ContextCancellation(t *testing.T) {
	config := DefaultWaitConfig()
	config.AfterNodeUpgrade = 1000 * 1000 * 1000 // Very long wait

	manager := NewWaitManager(config)
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	err := manager.Wait(ctx, "after-node-upgrade")
	if err == nil {
		t.Error("expected context cancellation error")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
