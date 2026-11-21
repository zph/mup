package main

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/zph/mup/pkg/paths"
	"github.com/zph/mup/pkg/plan"
)

var planShowPlanID string

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
	planCmd.AddCommand(planShowCmd)
	rootCmd.AddCommand(planCmd)
}
