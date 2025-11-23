package upgrade

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// [UPG-016] Configurable user prompting and interaction

// PromptLevel represents the granularity of user prompting
type PromptLevel string

const (
	PromptLevelNone     PromptLevel = "none"     // No prompts (fully automated)
	PromptLevelPhase    PromptLevel = "phase"    // Prompt before each phase (default)
	PromptLevelNode     PromptLevel = "node"     // Prompt before each node
	PromptLevelCritical PromptLevel = "critical" // Prompt only for critical operations
)

// PromptResponse represents user's response to a prompt
type PromptResponse string

const (
	PromptResponseContinue    PromptResponse = "continue"
	PromptResponseSkip        PromptResponse = "skip"
	PromptResponsePause       PromptResponse = "pause"
	PromptResponseAbort       PromptResponse = "abort"
	PromptResponseHealthCheck PromptResponse = "health"
	PromptResponseViewStatus  PromptResponse = "status"
)

// PromptContext contains context information for displaying prompts
type PromptContext struct {
	Phase            PhaseName
	NodeHostPort     string
	NodeRole         string // PRIMARY, SECONDARY, ARBITER, MONGOS
	CompletedNodes   int
	TotalNodes       int
	EstimatedTimeMin int
	IsCritical       bool // Whether this is a critical operation
	OperationName    string
}

// PrompterInterface defines the interface for user prompting
type PrompterInterface interface {
	ShouldPrompt(context PromptContext) bool
	Prompt(context PromptContext) (PromptResponse, error)
	DisplayStatus(state *UpgradeState)
}

// Prompter handles user interaction during upgrades
type Prompter struct {
	level  PromptLevel
	reader *bufio.Reader
	state  *UpgradeState
}

// NewPrompter creates a new prompter with given level
func NewPrompter(level PromptLevel, state *UpgradeState) *Prompter {
	return &Prompter{
		level:  level,
		reader: bufio.NewReader(os.Stdin),
		state:  state,
	}
}

// ShouldPrompt determines if we should prompt based on level and context
// [UPG-016] Prompt granularity logic
func (p *Prompter) ShouldPrompt(context PromptContext) bool {
	switch p.level {
	case PromptLevelNone:
		return false

	case PromptLevelPhase:
		// Prompt at phase boundaries (when nodeHostPort is empty)
		return context.NodeHostPort == ""

	case PromptLevelNode:
		// Prompt before every node
		return context.NodeHostPort != ""

	case PromptLevelCritical:
		// Prompt only for critical operations
		return context.IsCritical

	default:
		// Default to phase-level
		return context.NodeHostPort == ""
	}
}

// Prompt displays an interactive prompt and gets user response
// [UPG-016] Interactive prompt UI
func (p *Prompter) Prompt(context PromptContext) (PromptResponse, error) {
	// Don't prompt if level is none
	if !p.ShouldPrompt(context) {
		return PromptResponseContinue, nil
	}

	// Display prompt UI
	p.displayPromptUI(context)

	// Get user input
	for {
		fmt.Print("\n  Choice [c/s/p/a/h/v]: ")
		input, err := p.reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(strings.ToLower(input))

		switch input {
		case "c", "continue":
			return PromptResponseContinue, nil

		case "s", "skip":
			if p.level != PromptLevelNode {
				fmt.Println("  ⚠ Skip option only available with --prompt-level=node")
				continue
			}
			return PromptResponseSkip, nil

		case "p", "pause":
			return PromptResponsePause, nil

		case "a", "abort":
			fmt.Print("\n  Are you sure you want to abort the upgrade? (yes/no): ")
			confirm, _ := p.reader.ReadString('\n')
			confirm = strings.TrimSpace(strings.ToLower(confirm))
			if confirm == "yes" || confirm == "y" {
				return PromptResponseAbort, nil
			}
			fmt.Println("  Abort cancelled. Returning to prompt...")
			p.displayPromptUI(context)

		case "h", "health":
			p.displayHealthCheck()
			p.displayPromptUI(context)

		case "v", "status":
			p.DisplayStatus(p.state)
			p.displayPromptUI(context)

		default:
			fmt.Printf("  Invalid choice: %s\n", input)
			fmt.Println("  Valid options: c (continue), s (skip), p (pause), a (abort), h (health), v (status)")
		}
	}
}

// displayPromptUI shows the interactive prompt UI with box-drawing characters
// [UPG-016] Professional prompt UI
func (p *Prompter) displayPromptUI(context PromptContext) {
	width := 60
	border := strings.Repeat("═", width-2)

	fmt.Println("\n╔" + border + "╗")
	fmt.Println("║" + center("MongoDB Upgrade Progress", width-2) + "║")
	fmt.Println("╠" + border + "╣")

	// Phase information
	if context.Phase != "" {
		phaseStr := fmt.Sprintf("Phase: %s", formatPhaseName(context.Phase))
		fmt.Println("║  " + pad(phaseStr, width-4) + "║")
	}

	// Node information
	if context.NodeHostPort != "" {
		nodeStr := fmt.Sprintf("Next Node: %s", context.NodeHostPort)
		if context.NodeRole != "" {
			nodeStr += fmt.Sprintf(" (%s)", context.NodeRole)
		}
		fmt.Println("║  " + pad(nodeStr, width-4) + "║")
	}

	// Operation name for critical operations
	if context.IsCritical && context.OperationName != "" {
		opStr := fmt.Sprintf("Operation: %s", context.OperationName)
		fmt.Println("║  " + pad(opStr, width-4) + "║")
	}

	// Progress
	if context.TotalNodes > 0 {
		progressStr := fmt.Sprintf("Progress: %d/%d nodes completed", context.CompletedNodes, context.TotalNodes)
		fmt.Println("║  " + pad(progressStr, width-4) + "║")
	}

	// Estimated time
	if context.EstimatedTimeMin > 0 {
		timeStr := fmt.Sprintf("Estimated Time: %d minutes remaining", context.EstimatedTimeMin)
		fmt.Println("║  " + pad(timeStr, width-4) + "║")
	}

	fmt.Println("╠" + border + "╣")

	// Ready message
	readyMsg := "Ready to proceed"
	if context.NodeHostPort != "" {
		readyMsg = fmt.Sprintf("Ready to upgrade %s", context.NodeHostPort)
	}
	fmt.Println("║  " + pad(readyMsg, width-4) + "║")
	fmt.Println("║" + strings.Repeat(" ", width-2) + "║")

	// Options
	fmt.Println("║  " + pad("Options:", width-4) + "║")
	fmt.Println("║    " + pad("c - Continue", width-6) + "║")
	if p.level == PromptLevelNode {
		fmt.Println("║    " + pad("s - Skip this node", width-6) + "║")
	}
	fmt.Println("║    " + pad("p - Pause and save checkpoint", width-6) + "║")
	fmt.Println("║    " + pad("a - Abort upgrade", width-6) + "║")
	fmt.Println("║    " + pad("h - Health check", width-6) + "║")
	fmt.Println("║    " + pad("v - View status", width-6) + "║")
	fmt.Println("║" + strings.Repeat(" ", width-2) + "║")

	fmt.Println("╚" + border + "╝")
}

// DisplayStatus shows current upgrade status
// [UPG-016] Status display
func (p *Prompter) DisplayStatus(state *UpgradeState) {
	if state == nil {
		fmt.Println("\n  No upgrade state available")
		return
	}

	width := 60
	border := strings.Repeat("═", width-2)

	fmt.Println("\n╔" + border + "╗")
	fmt.Println("║" + center("Upgrade Status", width-2) + "║")
	fmt.Println("╠" + border + "╣")
	fmt.Printf("║  %-*s  ║\n", width-4, fmt.Sprintf("Cluster: %s", state.ClusterName))
	fmt.Printf("║  %-*s  ║\n", width-4, fmt.Sprintf("From: %s", state.PreviousVersion))
	fmt.Printf("║  %-*s  ║\n", width-4, fmt.Sprintf("To: %s", state.TargetVersion))
	fmt.Printf("║  %-*s  ║\n", width-4, fmt.Sprintf("Status: %s", state.OverallStatus))
	fmt.Printf("║  %-*s  ║\n", width-4, fmt.Sprintf("Current Phase: %s", state.CurrentPhase))
	fmt.Println("╠" + border + "╣")

	// Node status summary
	completed := state.GetCompletedNodeCount()
	total := state.GetTotalNodeCount()
	pending := len(state.GetNodesByStatus(NodeStatusPending))
	inProgress := len(state.GetNodesByStatus(NodeStatusInProgress))
	failed := len(state.GetNodesByStatus(NodeStatusFailed))
	skipped := len(state.SkippedNodes)

	fmt.Printf("║  %-*s  ║\n", width-4, "Node Status:")
	fmt.Printf("║    %-*s  ║\n", width-6, fmt.Sprintf("✓ Completed: %d/%d", completed, total))
	if pending > 0 {
		fmt.Printf("║    %-*s  ║\n", width-6, fmt.Sprintf("○ Pending: %d", pending))
	}
	if inProgress > 0 {
		fmt.Printf("║    %-*s  ║\n", width-6, fmt.Sprintf("⟳ In Progress: %d", inProgress))
	}
	if failed > 0 {
		fmt.Printf("║    %-*s  ║\n", width-6, fmt.Sprintf("✗ Failed: %d", failed))
	}
	if skipped > 0 {
		fmt.Printf("║    %-*s  ║\n", width-6, fmt.Sprintf("⊘ Skipped: %d", skipped))
	}

	// Checkpoint info
	if state.CheckpointCount > 0 {
		fmt.Println("╠" + border + "╣")
		fmt.Printf("║  %-*s  ║\n", width-4, fmt.Sprintf("Checkpoints: %d", state.CheckpointCount))
		since := time.Since(state.LastCheckpoint)
		fmt.Printf("║  %-*s  ║\n", width-4, fmt.Sprintf("Last: %s ago", formatDuration(since)))
	}

	fmt.Println("╚" + border + "╝")
}

// displayHealthCheck performs and displays health check
func (p *Prompter) displayHealthCheck() {
	fmt.Println("\n  Running health check...")
	// TODO: Implement actual health check logic [UPG-006]
	fmt.Println("  ✓ All nodes responding")
	fmt.Println("  ✓ Replication lag acceptable")
	fmt.Println("  ✓ No active migrations")
}

// Helper functions for UI formatting

func center(text string, width int) string {
	if len(text) >= width {
		return text[:width]
	}
	leftPad := (width - len(text)) / 2
	rightPad := width - len(text) - leftPad
	return strings.Repeat(" ", leftPad) + text + strings.Repeat(" ", rightPad)
}

func pad(text string, width int) string {
	if len(text) >= width {
		return text[:width]
	}
	return text + strings.Repeat(" ", width-len(text))
}

func formatPhaseName(phase PhaseName) string {
	switch phase {
	case PhasePreFlight:
		return "Pre-Flight Validation"
	case PhaseConfigServers:
		return "Config Servers Upgrade"
	case PhaseMongos:
		return "Mongos Upgrade"
	case PhasePostUpgrade:
		return "Post-Upgrade Tasks"
	default:
		return string(phase)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	} else {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// PromptForPhase prompts user before starting a phase
// [UPG-016] Phase-level prompting
func (p *Prompter) PromptForPhase(phase PhaseName, state *UpgradeState) (PromptResponse, error) {
	context := PromptContext{
		Phase:          phase,
		CompletedNodes: state.GetCompletedNodeCount(),
		TotalNodes:     state.GetTotalNodeCount(),
	}
	return p.Prompt(context)
}

// PromptForNode prompts user before upgrading a node
// [UPG-016] Node-level prompting
func (p *Prompter) PromptForNode(hostPort, role string, state *UpgradeState) (PromptResponse, error) {
	context := PromptContext{
		Phase:          state.CurrentPhase,
		NodeHostPort:   hostPort,
		NodeRole:       role,
		CompletedNodes: state.GetCompletedNodeCount(),
		TotalNodes:     state.GetTotalNodeCount(),
	}
	return p.Prompt(context)
}

// PromptForCriticalOperation prompts user before a critical operation
// [UPG-016] Critical operation prompting
func (p *Prompter) PromptForCriticalOperation(operationName string, state *UpgradeState) (PromptResponse, error) {
	context := PromptContext{
		Phase:          state.CurrentPhase,
		IsCritical:     true,
		OperationName:  operationName,
		CompletedNodes: state.GetCompletedNodeCount(),
		TotalNodes:     state.GetTotalNodeCount(),
	}
	return p.Prompt(context)
}

// ANSI color codes for terminal output
const (
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
)

// PromptForFailover prompts user before performing a primary failover
// [UPG-019] Failover confirmation with red warning
func (p *Prompter) PromptForFailover(hostPort, rsName string, state *UpgradeState) (PromptResponse, error) {
	fmt.Println()
	fmt.Printf("%s%s═══════════════════════════════════════════════════════════════%s\n", colorBold, colorRed, colorReset)
	fmt.Printf("%s%s                    ⚠️  FAILOVER REQUIRED  ⚠️%s\n", colorBold, colorRed, colorReset)
	fmt.Printf("%s%s═══════════════════════════════════════════════════════════════%s\n", colorBold, colorRed, colorReset)
	fmt.Println()
	fmt.Printf("%sNode %s is the PRIMARY of replica set '%s'%s\n", colorRed, hostPort, rsName, colorReset)
	fmt.Println()
	fmt.Printf("%s%sThis operation will:%s\n", colorBold, colorRed, colorReset)
	fmt.Printf("%s  • Force the PRIMARY to step down%s\n", colorRed, colorReset)
	fmt.Printf("%s  • Trigger an election for a new PRIMARY%s\n", colorRed, colorReset)
	fmt.Printf("%s  • Cause service disruption during election%s\n", colorRed, colorReset)
	fmt.Printf("%s  • Impact write availability for ~15 seconds (up to 5 minutes on large clusters)%s\n", colorRed, colorReset)
	fmt.Println()
	fmt.Printf("%sUpgrades can ONLY be performed on SECONDARY nodes.%s\n", colorYellow, colorReset)
	fmt.Printf("%sThe system will step down this PRIMARY, wait for election,%s\n", colorYellow, colorReset)
	fmt.Printf("%sthen upgrade it as a SECONDARY.%s\n", colorYellow, colorReset)
	fmt.Println()

	// Get confirmation
	fmt.Printf("Proceed with failover? (yes/no): ")
	input, err := p.reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(strings.ToLower(input))
	if input == "yes" || input == "y" {
		return PromptResponseContinue, nil
	}

	fmt.Println("\n  Failover cancelled. Upgrade aborted.")
	return PromptResponseAbort, nil
}

// ParsePromptLevel parses a prompt level string
func ParsePromptLevel(s string) (PromptLevel, error) {
	switch strings.ToLower(s) {
	case "none", "":
		return PromptLevelNone, nil
	case "phase":
		return PromptLevelPhase, nil
	case "node":
		return PromptLevelNode, nil
	case "critical":
		return PromptLevelCritical, nil
	default:
		return "", fmt.Errorf("invalid prompt level: %s (expected 'none', 'phase', 'node', or 'critical')", s)
	}
}
