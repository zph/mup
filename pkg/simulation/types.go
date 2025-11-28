package simulation

import (
	"time"
)

// REQ-SIM-002: Operation tracking for simulation reporting
type Operation struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`      // create_directory, upload_file, start_process, etc.
	Target    string                 `json:"target"`    // Path, command, or identifier
	Details   string                 `json:"details"`   // Additional information
	Result    string                 `json:"result"`    // success, failure
	Error     string                 `json:"error,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// REQ-SIM-006: In-memory filesystem state
type SimulatedFile struct {
	Path      string
	Content   []byte
	Mode      uint32
	CreatedAt time.Time
}

// REQ-SIM-006: In-memory directory state
type SimulatedDirectory struct {
	Path      string
	Mode      uint32
	CreatedAt time.Time
}

// REQ-SIM-006: In-memory symlink state
type SimulatedSymlink struct {
	Link      string
	Target    string
	CreatedAt time.Time
}

// REQ-SIM-009: Simulated process state
type SimulatedProcess struct {
	PID       int
	Command   string
	Args      []string
	State     ProcessState
	StartTime time.Time
	StopTime  *time.Time
}

// ProcessState represents the state of a simulated process
type ProcessState string

const (
	ProcessStateRunning ProcessState = "running"
	ProcessStateStopped ProcessState = "stopped"
	ProcessStateFailed  ProcessState = "failed"
)

// REQ-SIM-018, REQ-SIM-042: Configured failure for testing scenarios
type ConfiguredFailure struct {
	Operation string // upload_file, create_directory, etc.
	Target    string // Path or identifier that should fail
	Error     string // Error message to return
}

// REQ-SIM-002: Complete simulation state
type SimulationState struct {
	Operations []Operation
	StartTime  time.Time
	Files      map[string]*SimulatedFile
	Dirs       map[string]*SimulatedDirectory
	Symlinks   map[string]*SimulatedSymlink
	Processes  map[int]*SimulatedProcess
	NextPID    int
}

// NewSimulationState creates a new simulation state
func NewSimulationState() *SimulationState {
	return &SimulationState{
		Operations: make([]Operation, 0),
		StartTime:  time.Now(),
		Files:      make(map[string]*SimulatedFile),
		Dirs:       make(map[string]*SimulatedDirectory),
		Symlinks:   make(map[string]*SimulatedSymlink),
		Processes:  make(map[int]*SimulatedProcess),
		NextPID:    1000, // Start PIDs at 1000 to distinguish from real PIDs
	}
}

// RecordOperation adds an operation to the simulation state
func (s *SimulationState) RecordOperation(opType, target, details string, metadata map[string]interface{}) {
	op := Operation{
		ID:        generateOperationID(len(s.Operations)),
		Type:      opType,
		Target:    target,
		Details:   details,
		Result:    "success",
		Timestamp: time.Now(),
		Metadata:  metadata,
	}
	s.Operations = append(s.Operations, op)
}

// RecordFailure records a failed operation
func (s *SimulationState) RecordFailure(opType, target, details, errorMsg string) {
	op := Operation{
		ID:        generateOperationID(len(s.Operations)),
		Type:      opType,
		Target:    target,
		Details:   details,
		Result:    "failure",
		Error:     errorMsg,
		Timestamp: time.Now(),
	}
	s.Operations = append(s.Operations, op)
}

// AllocatePID returns the next available simulated PID
func (s *SimulationState) AllocatePID() int {
	pid := s.NextPID
	s.NextPID++
	return pid
}

func generateOperationID(index int) string {
	return "op-" + padLeft(index+1, 4)
}

func padLeft(n, width int) string {
	s := ""
	for i := 0; i < width; i++ {
		s = "0" + s
	}
	// Simple integer to string conversion
	result := ""
	if n == 0 {
		result = "0"
	} else {
		for n > 0 {
			result = string(rune('0'+(n%10))) + result
			n /= 10
		}
	}
	// Pad with leading zeros
	for len(result) < width {
		result = "0" + result
	}
	return result
}
