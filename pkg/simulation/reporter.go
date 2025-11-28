package simulation

import (
	"fmt"
	"time"
)

// Reporter provides formatted output for simulation results
// REQ-SIM-028, REQ-SIM-030: Output and reporting
type Reporter struct {
	executor *SimulationExecutor
}

// NewReporter creates a new simulation reporter
func NewReporter(executor *SimulationExecutor) *Reporter {
	return &Reporter{
		executor: executor,
	}
}

// PrintSummary outputs a concise summary of simulation results
// REQ-SIM-028: Summary report of what would have been performed
// REQ-SIM-037: Concise output for token efficiency
func (r *Reporter) PrintSummary() {
	state := r.executor.GetState()
	ops := r.executor.GetOperations()

	fmt.Println("\n" + r.separator())
	fmt.Println("[SIMULATION] Summary Report")
	fmt.Println(r.separator())

	// Operations by type
	opTypes := make(map[string]int)
	for _, op := range ops {
		opTypes[op.Type]++
	}

	fmt.Println("\n[SIMULATION] Operations Summary:")
	for opType, count := range opTypes {
		fmt.Printf("[SIMULATION]   %-20s: %d\n", opType, count)
	}
	fmt.Printf("[SIMULATION]   %-20s: %d\n", "Total", len(ops))

	// Resource changes
	fmt.Println("\n[SIMULATION] Resource Changes:")
	fmt.Printf("[SIMULATION]   Directories created : %d\n", len(state.Dirs))
	fmt.Printf("[SIMULATION]   Files created       : %d\n", len(state.Files))
	fmt.Printf("[SIMULATION]   Processes started   : %d\n", len(state.Processes))
	fmt.Printf("[SIMULATION]   Symlinks created    : %d\n", len(state.Symlinks))

	// Timing
	duration := time.Since(state.StartTime)
	fmt.Printf("\n[SIMULATION] Simulation Duration  : %s\n", duration.Round(time.Millisecond))
	fmt.Println("\n[SIMULATION] No actual changes were made to the system.")
	fmt.Println(r.separator())
}

// PrintDetailed outputs detailed operation log
// REQ-SIM-049: Verbose output with operation details
func (r *Reporter) PrintDetailed() {
	ops := r.executor.GetOperations()

	fmt.Println("\n" + r.separator())
	fmt.Println("[SIMULATION] Detailed Operation Log")
	fmt.Println(r.separator())

	for i, op := range ops {
		elapsed := op.Timestamp.Sub(r.executor.GetState().StartTime)
		fmt.Printf("\n[SIMULATION] [%03d] [%s] %s\n", i+1, elapsed.Round(time.Millisecond), op.Type)
		fmt.Printf("[SIMULATION]       Target: %s\n", op.Target)
		if op.Details != "" {
			fmt.Printf("[SIMULATION]       Details: %s\n", op.Details)
		}
		if op.Result != "success" {
			fmt.Printf("[SIMULATION]       Result: %s\n", op.Result)
			if op.Error != "" {
				fmt.Printf("[SIMULATION]       Error: %s\n", op.Error)
			}
		}
	}

	fmt.Println("\n" + r.separator())
}

// PrintOperationTypes prints a breakdown of operations by type
// REQ-SIM-030: Output simulated operation timing
func (r *Reporter) PrintOperationTypes() {
	ops := r.executor.GetOperations()
	opTypes := make(map[string][]Operation)

	for _, op := range ops {
		opTypes[op.Type] = append(opTypes[op.Type], op)
	}

	fmt.Println("\n[SIMULATION] Operations by Type:")
	for opType, typeOps := range opTypes {
		fmt.Printf("[SIMULATION]   %s: %d operations\n", opType, len(typeOps))
	}
}

func (r *Reporter) separator() string {
	return "================================================================"
}

// GetOperationCount returns the total number of operations
func (r *Reporter) GetOperationCount() int {
	return len(r.executor.GetOperations())
}

// GetResourceCounts returns counts of simulated resources
// REQ-SIM-028: Resource information in summary
func (r *Reporter) GetResourceCounts() (dirs, files, processes, symlinks int) {
	state := r.executor.GetState()
	return len(state.Dirs), len(state.Files), len(state.Processes), len(state.Symlinks)
}

// HasErrors returns true if any operations failed
// REQ-SIM-029: Report errors encountered during simulation
func (r *Reporter) HasErrors() bool {
	ops := r.executor.GetOperations()
	for _, op := range ops {
		if op.Result != "success" {
			return true
		}
	}
	return false
}

// GetErrors returns all failed operations
// REQ-SIM-029: Report errors encountered during simulation
func (r *Reporter) GetErrors() []Operation {
	ops := r.executor.GetOperations()
	errors := make([]Operation, 0)

	for _, op := range ops {
		if op.Result != "success" {
			errors = append(errors, op)
		}
	}

	return errors
}

// PrintErrors prints all errors encountered
// REQ-SIM-029: Report errors without exiting
func (r *Reporter) PrintErrors() {
	errors := r.GetErrors()
	if len(errors) == 0 {
		return
	}

	fmt.Println("\n[SIMULATION] Errors Encountered:")
	for i, op := range errors {
		fmt.Printf("[SIMULATION]   [%d] %s: %s\n", i+1, op.Type, op.Error)
		fmt.Printf("[SIMULATION]       Target: %s\n", op.Target)
	}
}
