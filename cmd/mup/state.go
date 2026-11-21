package main

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/paths"
)

var (
	stateID       string
	stateLogLevel string
	stateLogTail  int
)

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Inspect the state of plan applies",
	Long: `Inspect what happened when a plan was applied to a cluster: its status,
per-phase progress, execution log and checkpoints. The most recent apply is
shown unless --state-id selects another.`,
}

var stateShowCmd = &cobra.Command{
	Use:   "show <cluster-name>",
	Short: "Show the status and progress of an apply",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		state, err := loadApplyState(args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Cluster:    %s\n", state.ClusterName)
		fmt.Printf("State ID:   %s\n", state.StateID)
		fmt.Printf("Plan ID:    %s\n", state.PlanID)
		fmt.Printf("Operation:  %s\n", state.Operation)
		fmt.Printf("Status:     %s\n", state.Status)
		fmt.Printf("Started:    %s\n", state.StartedAt.Format(time.RFC3339))
		if state.CompletedAt != nil {
			fmt.Printf("Completed:  %s\n", state.CompletedAt.Format(time.RFC3339))
		}
		fmt.Printf("Phase:      %s\n", state.CurrentPhase)

		counts := make(map[apply.ApplyStatus]int)
		for _, op := range state.OperationStates {
			counts[op.Status]++
		}
		fmt.Printf("Operations: %d completed, %d failed, %d running\n",
			counts[apply.StatusCompleted], counts[apply.StatusFailed], counts[apply.StatusRunning])

		if len(state.PhaseStates) > 0 {
			phases := make([]*apply.PhaseState, 0, len(state.PhaseStates))
			for _, phase := range state.PhaseStates {
				phases = append(phases, phase)
			}
			sort.Slice(phases, func(i, j int) bool { return startedBefore(phases[i].StartedAt, phases[j].StartedAt) })

			fmt.Println("\nPhases:")
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, phase := range phases {
				fmt.Fprintf(w, "  %s\t%s\t%s\n", phase.Name, phase.Status, phase.Error)
			}
			if err := w.Flush(); err != nil {
				return err
			}
		}

		if len(state.Errors) > 0 {
			fmt.Println("\nErrors:")
			for _, e := range state.Errors {
				fmt.Printf("  %s  %s/%s: %s\n", e.Timestamp.Format(time.RFC3339), e.Phase, e.Operation, e.Error)
			}
		}

		if state.CanResume() {
			fmt.Printf("\nResume with: mup cluster resume %s\n", state.ClusterName)
		}
		return nil
	},
}

var stateLogsCmd = &cobra.Command{
	Use:   "logs <cluster-name>",
	Short: "Show the execution log of an apply",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		state, err := loadApplyState(args[0])
		if err != nil {
			return err
		}

		entries := state.ExecutionLog
		if stateLogLevel != "" {
			filtered := entries[:0:0]
			for _, entry := range entries {
				if entry.Level == stateLogLevel {
					filtered = append(filtered, entry)
				}
			}
			entries = filtered
		}
		if stateLogTail > 0 && len(entries) > stateLogTail {
			entries = entries[len(entries)-stateLogTail:]
		}

		if len(entries) == 0 {
			fmt.Println("No log entries.")
			return nil
		}
		for _, entry := range entries {
			where := entry.Phase
			if entry.Operation != "" {
				where += "/" + entry.Operation
			}
			fmt.Printf("%s  %-5s  %-28s  %s\n", entry.Timestamp.Format(time.RFC3339), entry.Level, where, entry.Message)
		}
		return nil
	},
}

var stateCheckpointsCmd = &cobra.Command{
	Use:   "checkpoints <cluster-name>",
	Short: "List the checkpoints of an apply",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		state, err := loadApplyState(args[0])
		if err != nil {
			return err
		}

		if len(state.Checkpoints) == 0 {
			fmt.Println("No checkpoints.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TIME\tPHASE\tDESCRIPTION\tID")
		for _, checkpoint := range state.Checkpoints {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				checkpoint.Timestamp.Format(time.RFC3339), checkpoint.Phase, checkpoint.Description, checkpoint.ID)
		}
		return w.Flush()
	},
}

// loadApplyState returns the apply selected by --state-id, or the most recent
// apply for the cluster.
func loadApplyState(clusterName string) (*apply.ApplyState, error) {
	clusterDir, err := paths.ClusterDir(clusterName)
	if err != nil {
		return nil, err
	}
	stateManager := apply.NewStateManager(clusterDir)

	if stateID != "" {
		state, err := stateManager.LoadState(stateID)
		if err != nil {
			return nil, fmt.Errorf("failed to load state %s for cluster %s: %w", stateID, clusterName, err)
		}
		return state, nil
	}

	state, err := stateManager.GetCurrentState()
	if err != nil {
		return nil, fmt.Errorf("no apply state for cluster %s: %w", clusterName, err)
	}
	return state, nil
}

// startedBefore orders optional start times, unstarted last.
func startedBefore(a, b *time.Time) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	return a.Before(*b)
}

func init() {
	stateCmd.PersistentFlags().StringVar(&stateID, "state-id", "", "Apply to inspect (default: most recent)")
	stateLogsCmd.Flags().StringVar(&stateLogLevel, "level", "", "Only show entries of this level (info, warn, error)")
	stateLogsCmd.Flags().IntVar(&stateLogTail, "tail", 0, "Only show the last N entries")

	stateCmd.AddCommand(stateShowCmd)
	stateCmd.AddCommand(stateLogsCmd)
	stateCmd.AddCommand(stateCheckpointsCmd)
	rootCmd.AddCommand(stateCmd)
}
