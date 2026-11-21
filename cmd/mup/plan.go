package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/operation"
	"github.com/zph/mup/pkg/paths"
	"github.com/zph/mup/pkg/plan"
)

var (
	planShowPlanID       string
	planValidatePlanID   string
	planApplyPlanID      string
	planApplyAutoApprove bool
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Inspect saved execution plans",
	Long: `Inspect the execution plans mup saves before applying a change.

Plans are created by commands such as 'mup cluster deploy' (use --plan-only to
stop after planning) and are stored under each cluster's directory.`,
}

var planListCmd = &cobra.Command{
	Use:   "list [cluster-name]",
	Short: "List saved plans, for one cluster or all",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		storageDir, err := paths.StorageDir()
		if err != nil {
			return err
		}
		planStore, err := plan.NewPlanStore(storageDir)
		if err != nil {
			return err
		}

		clusterNames := args
		if len(clusterNames) == 0 {
			clusterNames, err = storedClusterNames()
			if err != nil {
				return err
			}
		}

		var plans []plan.PlanMetadata
		for _, clusterName := range clusterNames {
			clusterPlans, err := planStore.ListPlans(clusterName)
			if err != nil {
				return fmt.Errorf("failed to list plans for %s: %w", clusterName, err)
			}
			plans = append(plans, clusterPlans...)
		}

		if len(plans) == 0 {
			fmt.Println("No plans found.")
			return nil
		}

		sort.Slice(plans, func(i, j int) bool { return plans[i].CreatedAt.After(plans[j].CreatedAt) })

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "CLUSTER\tPLAN ID\tOPERATION\tVERSION\tPHASES\tCREATED\tVALID")
		for _, p := range plans {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%t\n",
				p.ClusterName, p.PlanID, p.Operation, p.Version, p.PhaseCount, p.CreatedAt.Format(time.RFC3339), p.IsValid)
		}
		return w.Flush()
	},
}

var planShowCmd = &cobra.Command{
	Use:   "show <cluster-name>",
	Short: "Show a saved plan (the most recent one by default)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]

		storageDir, err := paths.StorageDir()
		if err != nil {
			return err
		}
		planStore, err := plan.NewPlanStore(storageDir)
		if err != nil {
			return err
		}

		planID := planShowPlanID
		if planID == "" {
			plans, err := planStore.ListPlans(clusterName)
			if err != nil {
				return fmt.Errorf("failed to list plans for %s: %w", clusterName, err)
			}
			if len(plans) == 0 {
				return fmt.Errorf("no plans found for cluster %s", clusterName)
			}
			sort.Slice(plans, func(i, j int) bool { return plans[i].CreatedAt.After(plans[j].CreatedAt) })
			planID = plans[0].PlanID
		}

		p, err := planStore.LoadPlan(clusterName, planID)
		if err != nil {
			return fmt.Errorf("failed to load plan %s: %w", planID, err)
		}

		fmt.Println(p.Summary())
		fmt.Printf("Path: %s\n", planStore.GetPlanPath(clusterName, planID))
		return nil
	},
}

// resolvePlanID returns planID, or the most recent plan for the cluster when
// planID is empty.
func resolvePlanID(planStore *plan.PlanStore, clusterName, planID string) (string, error) {
	if planID != "" {
		return planID, nil
	}
	plans, err := planStore.ListPlans(clusterName)
	if err != nil {
		return "", fmt.Errorf("failed to list plans for %s: %w", clusterName, err)
	}
	if len(plans) == 0 {
		return "", fmt.Errorf("no plans found for cluster %s", clusterName)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].CreatedAt.After(plans[j].CreatedAt) })
	return plans[0].PlanID, nil
}

var planValidateCmd = &cobra.Command{
	Use:   "validate <cluster-name>",
	Short: "Check that a saved plan is intact and can be applied",
	Long: `Check a saved plan (the most recent one by default) without applying it:

- it still matches the checksum recorded when it was saved
- it is well formed (has operations, every operation typed, unique operation IDs)
- every operation type has a handler in this build of mup
- the validation recorded when it was generated passed`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		clusterName := args[0]

		storageDir, err := paths.StorageDir()
		if err != nil {
			return err
		}
		planStore, err := plan.NewPlanStore(storageDir)
		if err != nil {
			return err
		}
		planID, err := resolvePlanID(planStore, clusterName, planValidatePlanID)
		if err != nil {
			return err
		}

		// LOCAL-032
		p, err := planStore.LoadVerifiedPlan(clusterName, planID)
		if err != nil {
			return err
		}
		fmt.Printf("Plan %s for cluster '%s'\n", planID, clusterName)
		fmt.Println("  ✓ Checksum matches")

		problems := planProblems(p)
		for _, problem := range problems {
			fmt.Printf("  ✗ %s\n", problem)
		}
		if len(problems) > 0 {
			return fmt.Errorf("plan %s is not valid: %d problem(s)", planID, len(problems))
		}

		fmt.Printf("  ✓ Well formed: %d operations in %d phases\n", p.TotalOperations(), len(p.Phases))
		fmt.Println("  ✓ Every operation type is supported")
		fmt.Println("  ✓ Recorded validation passed")
		fmt.Println("\nPlan is valid. Note: this does not re-check the machine (ports, disk space); apply does.")
		return nil
	},
}

// planProblems lists everything that makes a plan unfit to apply.
// LOCAL-033
func planProblems(p *plan.Plan) []string {
	problems := p.StructuralProblems()

	opExecutor := operation.NewExecutor(nil)
	unsupported := make(map[plan.OperationType]bool)
	for _, phase := range p.Phases {
		for _, op := range phase.Operations {
			if op.Type != "" && !opExecutor.Supports(op.Type) && !unsupported[op.Type] {
				unsupported[op.Type] = true
				problems = append(problems, fmt.Sprintf("operation type %q is not supported by this version of mup", op.Type))
			}
		}
	}

	if !p.Validation.Valid {
		problems = append(problems, "the validation recorded when the plan was generated did not pass")
	}
	return problems
}

var planApplyCmd = &cobra.Command{
	Use:   "apply <cluster-name>",
	Short: "Apply a saved plan (the most recent one by default)",
	Long: `Apply a plan saved earlier, for example by 'mup cluster deploy --plan-only'.

The plan must still match its checksum and pass 'mup plan validate'. A plan
that was already applied, or whose apply failed part-way, is refused: use
'mup cluster resume' to continue a failed apply.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		clusterName := args[0]
		ctx, cancel := context.WithTimeout(context.Background(), planApplyTimeout)
		defer cancel()

		storageDir, err := paths.StorageDir()
		if err != nil {
			return err
		}
		clusterDir, err := paths.ClusterDir(clusterName)
		if err != nil {
			return err
		}
		planStore, err := plan.NewPlanStore(storageDir)
		if err != nil {
			return err
		}
		planID, err := resolvePlanID(planStore, clusterName, planApplyPlanID)
		if err != nil {
			return err
		}

		p, err := planStore.LoadVerifiedPlan(clusterName, planID)
		if err != nil {
			return err
		}
		if problems := planProblems(p); len(problems) > 0 {
			for _, problem := range problems {
				fmt.Printf("  ✗ %s\n", problem)
			}
			return fmt.Errorf("plan %s is not valid: %d problem(s)", planID, len(problems))
		}

		stateManager := apply.NewStateManager(clusterDir)
		if err := ensurePlanNotApplied(stateManager, planID, clusterName); err != nil {
			return err
		}

		fmt.Println(p.Summary())
		if !planApplyAutoApprove {
			fmt.Printf("\nApply plan %s to cluster %s? (yes/no): ", planID, clusterName)
			var response string
			if _, err := fmt.Scanln(&response); err != nil || (response != "yes" && response != "y") {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		return withClusterLock(ctx, clusterName, planID, p.Operation, func() error {
			applier := apply.NewDefaultApplier(operation.NewExecutor(localExecutorsForPlan(p)), stateManager)

			fmt.Printf("\n🚀 Applying plan %s...\n\n", planID)
			state, err := applier.Apply(ctx, p)
			if err != nil {
				fmt.Printf("\n❌ Apply failed: %v\n", err)
				fmt.Printf("\nState ID: %s\n", state.StateID)
				fmt.Printf("Resume with: mup cluster resume %s\n", clusterName)
				return err
			}

			fmt.Printf("\n✅ Plan applied successfully!\n")
			fmt.Printf("State ID: %s\n", state.StateID)
			return nil
		})
	},
}

// planApplyTimeout bounds one 'mup plan apply' run.
const planApplyTimeout = 30 * time.Minute

// ensurePlanNotApplied refuses a plan that already has an apply recorded:
// a completed one must not run twice, and an unfinished one is resumed.
func ensurePlanNotApplied(stateManager *apply.StateManager, planID, clusterName string) error {
	states, err := stateManager.ListStates()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read apply history: %w", err)
	}
	for _, state := range states {
		if state.PlanID != planID {
			continue
		}
		if state.Status == apply.StatusCompleted {
			return fmt.Errorf("plan %s was already applied (state %s)", planID, state.StateID)
		}
		return fmt.Errorf("plan %s has an unfinished apply (state %s, %s); continue it with: mup cluster resume %s",
			planID, state.StateID, state.Status, clusterName)
	}
	return nil
}

// storedClusterNames returns every cluster directory under the storage root,
// including clusters that only have a saved plan and no metadata yet.
func storedClusterNames() ([]string, error) {
	clustersDir, err := paths.ClustersDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(clustersDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", clustersDir, err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func init() {
	planShowCmd.Flags().StringVar(&planShowPlanID, "plan-id", "", "Plan to show (default: most recent)")

	planCmd.AddCommand(planListCmd)
	planValidateCmd.Flags().StringVar(&planValidatePlanID, "plan-id", "", "Plan to validate (default: most recent)")
	planApplyCmd.Flags().StringVar(&planApplyPlanID, "plan-id", "", "Plan to apply (default: most recent)")
	planApplyCmd.Flags().BoolVar(&planApplyAutoApprove, "auto-approve", false, "Skip confirmation and apply the plan")

	planCmd.AddCommand(planShowCmd)
	planCmd.AddCommand(planValidateCmd)
	planCmd.AddCommand(planApplyCmd)
	rootCmd.AddCommand(planCmd)
}
