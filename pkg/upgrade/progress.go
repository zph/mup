package upgrade

import (
	"fmt"
	"strings"
	"time"
)

// ProgressTracker tracks progress of multi-step operations
type ProgressTracker struct {
	total   int
	current int
	label   string
	steps   []string
	start   time.Time
}

// NewProgressTracker creates a new progress tracker
func NewProgressTracker(label string, steps []string) *ProgressTracker {
	return &ProgressTracker{
		total: len(steps),
		label: label,
		steps: steps,
		start: time.Now(),
	}
}

// Step advances to the next step and displays progress
func (p *ProgressTracker) Step(stepIndex int) {
	if stepIndex >= len(p.steps) {
		return
	}

	p.current = stepIndex + 1
	elapsed := time.Since(p.start)

	// Clear line and show progress
	stepLabel := p.steps[stepIndex]
	progress := fmt.Sprintf("[%d/%d]", p.current, p.total)
	fmt.Printf("    %s %s (elapsed: %s)\n", progress, stepLabel, elapsed.Round(time.Second))
}

// Complete marks the operation as complete
func (p *ProgressTracker) Complete() {
	elapsed := time.Since(p.start)
	fmt.Printf("    ✓ Complete (total time: %s)\n", elapsed.Round(time.Second))
}

// Spinner displays a simple text spinner for indeterminate operations
type Spinner struct {
	message string
	frames  []string
	index   int
	done    chan bool
	start   time.Time
}

// NewSpinner creates a new spinner
func NewSpinner(message string) *Spinner {
	return &Spinner{
		message: message,
		frames:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		done:    make(chan bool),
		start:   time.Now(),
	}
}

// Start begins the spinner animation
func (s *Spinner) Start() {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				elapsed := time.Since(s.start)
				frame := s.frames[s.index%len(s.frames)]
				fmt.Printf("\r    %s %s (%.1fs)", frame, s.message, elapsed.Seconds())
				s.index++
			}
		}
	}()
}

// Stop stops the spinner and clears the line
func (s *Spinner) Stop() {
	close(s.done)
	time.Sleep(150 * time.Millisecond) // Allow final update
	fmt.Print("\r" + strings.Repeat(" ", 80) + "\r") // Clear line
}

// Success stops the spinner and shows success message
func (s *Spinner) Success(message string) {
	s.Stop()
	elapsed := time.Since(s.start)
	fmt.Printf("    ✓ %s (%.1fs)\n", message, elapsed.Seconds())
}

// Fail stops the spinner and shows failure message
func (s *Spinner) Fail(message string) {
	s.Stop()
	elapsed := time.Since(s.start)
	fmt.Printf("    ✗ %s (%.1fs)\n", message, elapsed.Seconds())
}

// NodeUpgradeProgress tracks progress for a single node upgrade
type NodeUpgradeProgress struct {
	node     string
	steps    []string
	tracker  *ProgressTracker
	spinner  *Spinner
	logLines []string
}

// NewNodeUpgradeProgress creates progress tracker for node upgrade
func NewNodeUpgradeProgress(node string) *NodeUpgradeProgress {
	steps := []string{
		"Stopping MongoDB process",
		"Backing up configuration",
		"Replacing binary",
		"Starting MongoDB with new version",
		"Waiting for node to be healthy",
		"Verifying new version",
	}

	return &NodeUpgradeProgress{
		node:     node,
		steps:    steps,
		tracker:  NewProgressTracker(node, steps),
		logLines: []string{},
	}
}

// StartStep begins a step with a spinner
func (n *NodeUpgradeProgress) StartStep(stepIndex int, message string) {
	n.tracker.Step(stepIndex)
	n.spinner = NewSpinner(message)
	n.spinner.Start()
}

// CompleteStep completes the current step
func (n *NodeUpgradeProgress) CompleteStep(successMsg string) {
	if n.spinner != nil {
		n.spinner.Success(successMsg)
		n.spinner = nil
	}
}

// FailStep marks current step as failed
func (n *NodeUpgradeProgress) FailStep(errorMsg string) {
	if n.spinner != nil {
		n.spinner.Fail(errorMsg)
		n.spinner = nil
	}
}

// AddLog adds a log line (for verbose output)
func (n *NodeUpgradeProgress) AddLog(line string) {
	n.logLines = append(n.logLines, line)
	// In verbose mode, could print immediately
	// fmt.Printf("      %s\n", line)
}

// Complete marks the entire node upgrade as complete
func (n *NodeUpgradeProgress) Complete() {
	n.tracker.Complete()
}

// MultiNodeProgress tracks progress across multiple nodes
type MultiNodeProgress struct {
	total     int
	completed int
	failed    int
	start     time.Time
}

// NewMultiNodeProgress creates a tracker for multiple nodes
func NewMultiNodeProgress(total int) *MultiNodeProgress {
	return &MultiNodeProgress{
		total: total,
		start: time.Now(),
	}
}

// NodeComplete marks a node as completed
func (m *MultiNodeProgress) NodeComplete(node string, success bool) {
	if success {
		m.completed++
		elapsed := time.Since(m.start)
		fmt.Printf("\n  ✓ Node %s upgraded (%d/%d complete, elapsed: %s)\n",
			node, m.completed, m.total, elapsed.Round(time.Second))
	} else {
		m.failed++
		fmt.Printf("\n  ✗ Node %s failed (%d/%d complete, %d failed)\n",
			node, m.completed, m.total, m.failed)
	}
}

// Summary prints final summary
func (m *MultiNodeProgress) Summary() {
	elapsed := time.Since(m.start)
	fmt.Printf("\n  Summary: %d/%d nodes upgraded, %d failed (total time: %s)\n",
		m.completed, m.total, m.failed, elapsed.Round(time.Second))
}

// ProgressBar displays a simple text-based progress bar
type ProgressBar struct {
	total   int
	current int
	width   int
	label   string
}

// NewProgressBar creates a new progress bar
func NewProgressBar(label string, total int) *ProgressBar {
	return &ProgressBar{
		label: label,
		total: total,
		width: 40,
	}
}

// Update updates the progress bar
func (p *ProgressBar) Update(current int) {
	p.current = current
	p.render()
}

// Increment increments progress by 1
func (p *ProgressBar) Increment() {
	p.current++
	p.render()
}

// render draws the progress bar
func (p *ProgressBar) render() {
	percent := float64(p.current) / float64(p.total)
	filled := int(percent * float64(p.width))

	bar := strings.Repeat("█", filled) + strings.Repeat("░", p.width-filled)
	fmt.Printf("\r  %s [%s] %d/%d (%.0f%%)",
		p.label, bar, p.current, p.total, percent*100)

	if p.current >= p.total {
		fmt.Println() // New line when complete
	}
}

// Complete completes the progress bar
func (p *ProgressBar) Complete() {
	p.current = p.total
	p.render()
}
