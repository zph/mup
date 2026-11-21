package main

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/zph/mup/pkg/apply"
	"github.com/zph/mup/pkg/paths"
)

var lockReleaseYes bool

var lockCmd = &cobra.Command{
	Use:   "lock",
	Short: "Inspect and clear cluster locks",
	Long: `Inspect and clear the locks mup takes while changing a cluster.

A lock whose owning process has exited is taken over automatically by the
next operation. Use these commands to see what is locked, or to clear a lock
mup cannot clear on its own.`,
}

var lockListCmd = &cobra.Command{
	Use:   "list",
	Short: "List cluster locks",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		lockMgr, err := newLockManager()
		if err != nil {
			return err
		}
		clusterNames, err := storedClusterNames()
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		var found int
		for _, clusterName := range clusterNames {
			lock, err := lockMgr.GetLock(clusterName)
			if errors.Is(err, apply.ErrNoLock) {
				continue
			}
			if found == 0 {
				fmt.Fprintln(w, "CLUSTER\tOPERATION\tLOCKED BY\tEXPIRES\tSTATE")
			}
			found++
			if err != nil {
				fmt.Fprintf(w, "%s\t-\t-\t-\tunreadable (%v)\n", clusterName, err)
				continue
			}
			state := "held"
			if lock.IsStale() {
				state = "stale"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				clusterName, lock.Operation, lock.LockedBy, lock.ExpiresAt.Format(time.RFC3339), state)
		}

		if found == 0 {
			fmt.Println("No cluster locks.")
			return nil
		}
		return w.Flush()
	},
}

var lockCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove expired cluster locks",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		lockMgr, err := newLockManager()
		if err != nil {
			return err
		}
		if err := lockMgr.CleanupExpiredLocks(); err != nil {
			return err
		}
		fmt.Println("✓ Expired locks cleaned up")
		return nil
	},
}

var lockShowCmd = &cobra.Command{
	Use:   "show <cluster-name>",
	Short: "Show the lock on a cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		lockMgr, err := newLockManager()
		if err != nil {
			return err
		}

		lock, err := lockMgr.GetLock(clusterName)
		if errors.Is(err, apply.ErrNoLock) {
			fmt.Printf("Cluster '%s' is not locked.\n", clusterName)
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock file for cluster %s is unreadable: %w", clusterName, err)
		}

		state := "held"
		if lock.IsStale() {
			state = "stale (expired, or the owning process has exited)"
		}
		fmt.Printf("Cluster:    %s\n", lock.ClusterName)
		fmt.Printf("Operation:  %s\n", lock.Operation)
		fmt.Printf("Plan:       %s\n", lock.PlanID)
		fmt.Printf("Locked by:  %s\n", lock.LockedBy)
		fmt.Printf("Locked at:  %s\n", lock.LockedAt.Format(time.RFC3339))
		fmt.Printf("Expires:    %s\n", lock.ExpiresAt.Format(time.RFC3339))
		fmt.Printf("State:      %s\n", state)
		return nil
	},
}

var lockForceUnlockCmd = &cobra.Command{
	Use:   "force-unlock <cluster-name>",
	Short: "Remove a cluster lock without asking, whoever holds it",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return releaseClusterLock(args[0], true)
	},
}

var lockReleaseCmd = &cobra.Command{
	Use:   "release <cluster-name>",
	Short: "Remove a cluster lock left behind by an interrupted operation",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return releaseClusterLock(args[0], lockReleaseYes)
	},
}

func newLockManager() (*apply.LockManager, error) {
	storageDir, err := paths.StorageDir()
	if err != nil {
		return nil, err
	}
	lockMgr, err := apply.NewLockManager(storageDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create lock manager: %w", err)
	}
	return lockMgr, nil
}

// releaseClusterLock shows a cluster's lock and removes it after confirmation.
// LOCAL-018
func releaseClusterLock(clusterName string, assumeYes bool) error {
	lockMgr, err := newLockManager()
	if err != nil {
		return err
	}

	lock, err := lockMgr.GetLock(clusterName)
	switch {
	case errors.Is(err, apply.ErrNoLock):
		fmt.Printf("Cluster '%s' is not locked.\n", clusterName)
		return nil
	case err != nil:
		fmt.Printf("Lock file for cluster '%s' is unreadable: %v\n", clusterName, err)
	default:
		fmt.Printf("Cluster '%s' is locked by %s (operation: %s, plan: %s, expires: %s)\n",
			clusterName, lock.LockedBy, lock.Operation, lock.PlanID, lock.ExpiresAt.Format(time.RFC3339))
	}

	if !assumeYes {
		fmt.Printf("Remove this lock? Only do this if no mup operation is running. (yes/no): ")
		var response string
		if _, err := fmt.Scanln(&response); err != nil || (response != "yes" && response != "y") {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	if err := lockMgr.ForceUnlock(clusterName); err != nil {
		return err
	}
	fmt.Printf("✓ Lock removed for cluster '%s'\n", clusterName)
	return nil
}

func init() {
	lockReleaseCmd.Flags().BoolVar(&lockReleaseYes, "yes", false, "Skip confirmation prompt")

	lockCmd.AddCommand(lockListCmd)
	lockCmd.AddCommand(lockShowCmd)
	lockCmd.AddCommand(lockCleanupCmd)
	lockCmd.AddCommand(lockReleaseCmd)
	lockCmd.AddCommand(lockForceUnlockCmd)
	rootCmd.AddCommand(lockCmd)
}
