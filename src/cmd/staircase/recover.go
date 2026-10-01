package main

import (
	"context"
	"fmt"
	"os"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var recoverForce bool

var recoverCmd = &cobra.Command{
	Use:   "recover <run-id>",
	Short: "Commit what an interrupted run had approved",
	Long: `A run keeps every approval it gives, before the agent is told the answer. If the
run was interrupted (a crash, a power cut, a killed terminal) before it committed,
recover makes the commit from exactly those approvals: it takes only what the run's
audit chain also records, derives the files again from the base commit, and moves
the run's branch only if it is still where the run started. The change certificate
says the run did not finish (CAL 2 at most), and its ledger is kept, so the commit
can be rebuilt and verified like any other.

When some of the change was approved as part of the agreed task, by evidence or by
reviewer models, you are shown the whole change and approve it once, as a finished
run would have asked, so recover needs a terminal then. The run's worktree is left
as it is. A run whose record still says RUNNING needs --force, when you know its
process is gone.`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		runID, err := parseID("run-id", args[0])
		if err != nil {
			return err
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		var confirm func(domain.YieldRequest) domain.YieldResponse
		if stat, _ := os.Stdin.Stat(); stat != nil && stat.Mode()&os.ModeCharDevice != 0 {
			confirm = tui.RunYieldTUI
		}
		res, err := orchestrator.NewRunner(store, viper.GetString("STAIRCASE_DIR")).Recover(context.Background(), runID, orchestrator.RecoverOptions{Force: recoverForce, Confirm: confirm})
		if err != nil {
			return err
		}
		fmt.Printf("✅ Run #%d recovered: %d approved proposal(s) committed as %.12s on its run branch\n", runID, res.Proposals, res.Commit)
		fmt.Println("   📜 The certificate says the run did not finish; verify it like any other: staircase verify staircase/run-" + args[0])
		return nil
	},
}

func init() {
	recoverCmd.Flags().BoolVar(&recoverForce, "force", false, "Recover a run whose record still says RUNNING (its process is known to be gone)")
	rootCmd.AddCommand(recoverCmd)
}
