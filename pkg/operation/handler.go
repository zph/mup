package operation

// HookResult and StateChange types for four-phase handler pattern
// REQ-PES-036, REQ-PES-047, REQ-PES-048

// HookResult contains the outcome of PreHook or PostHook execution
type HookResult struct {
	// Valid indicates whether the hook passed all checks
	// PreHook: If false, operation will not execute
	// PostHook: If false, operation is marked as failed
	Valid bool

	// Warnings are non-blocking issues that don't prevent execution
	// Example: "port already in use (may be from previous run)"
	Warnings []string

	// Errors are blocking issues that prevent execution (when Valid=false)
	// Example: "config file not found: /path/to/config"
	Errors []string

	// StateChanges describes divergence between expected and actual state
	// Used to inform user when world state has changed since plan generation
	StateChanges []StateChange

	// Metadata contains arbitrary data from hook execution
	// Example: verification attempt count, discovered values, etc.
	Metadata map[string]interface{}
}

// StateChange represents a divergence between planned and actual state
type StateChange struct {
	Resource string      // Resource identifier (e.g., "port:19101", "file:/path")
	Expected interface{} // Expected value from plan
	Actual   interface{} // Actual value discovered
	Impact   string      // Description of impact (e.g., "operation may fail")
}

// NewHookResult creates a HookResult with Valid=true
func NewHookResult() *HookResult {
	return &HookResult{
		Valid:        true,
		Warnings:     []string{},
		Errors:       []string{},
		StateChanges: []StateChange{},
		Metadata:     make(map[string]interface{}),
	}
}

// AddError adds an error and marks the result as invalid
func (hr *HookResult) AddError(err string) {
	hr.Valid = false
	hr.Errors = append(hr.Errors, err)
}

// AddWarning adds a non-blocking warning
func (hr *HookResult) AddWarning(warning string) {
	hr.Warnings = append(hr.Warnings, warning)
}

// AddStateChange adds a state divergence
func (hr *HookResult) AddStateChange(resource string, expected, actual interface{}, impact string) {
	hr.StateChanges = append(hr.StateChanges, StateChange{
		Resource: resource,
		Expected: expected,
		Actual:   actual,
		Impact:   impact,
	})
}
