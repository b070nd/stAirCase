package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var recoverForce, recoverRequireEvidence bool

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
process is gone.

Recovery writes a record of what it is about to do before it touches git and names
itself in the commit it makes, so a recover that was interrupted, or whose ledger,
certificate, notes or records could not all be written, can be run again: it
finishes the missing evidence on the same commit and says what is still missing.
A commit on the run's branch that it did not make is never adopted. With
--require-evidence the command exits with an error while the evidence is incomplete.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runID, err := parseID("run-id", args[0])
		if err != nil {
			return err
		}
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		parent := cmd.Context()
		if parent == nil {
			parent = context.Background()
		}
		ctx, stop := signal.NotifyContext(parent, os.Interrupt) // Ctrl-C ends the recovery before it delivers
		defer stop()
		var confirm func(domain.YieldRequest) domain.YieldResponse
		if stat, _ := os.Stdin.Stat(); stat != nil && stat.Mode()&os.ModeCharDevice != 0 {
			confirm = func(req domain.YieldRequest) domain.YieldResponse { return tui.RunYieldTUI(ctx, req) }
		}
		res, err := orchestrator.NewRunner(store, viper.GetString("STAIRCASE_DIR")).Recover(ctx, runID,
			orchestrator.RecoverOptions{Force: recoverForce, Confirm: confirm, RequireEvidence: recoverRequireEvidence})
		if err != nil && res.Commit == "" {
			return err
		}
		reportRecovery(os.Stdout, runID, res)
		return err // the commit exists; an error now is --require-evidence
	},
}

// reportRecovery says what a recovery did: the commit, and honestly whether its
// evidence (ledger, certificate, notes, audit record, summary) is complete.
func reportRecovery(w io.Writer, runID int64, res orchestrator.RecoverResult) {
	verb := "recovered"
	if res.Repaired {
		verb = "was already committed; its evidence was finished"
	}
	_, _ = fmt.Fprintf(w, "✅ Run #%d %s: %d approved proposal(s) are commit %.12s on its run branch\n", runID, verb, res.Proposals, res.Commit)
	if len(res.EvidenceErrors) > 0 {
		_, _ = fmt.Fprintf(w, "   ⚠️  The commit exists but its evidence is incomplete:\n")
		for _, e := range res.EvidenceErrors {
			_, _ = fmt.Fprintf(w, "      - %s\n", e)
		}
		_, _ = fmt.Fprintf(w, "   Fix the cause and run: staircase recover %d   (it repairs the same commit)\n", runID)
		return
	}
	_, _ = fmt.Fprintf(w, "   📜 The certificate says the run did not finish; verify it like any other: staircase verify staircase/run-%d\n", runID)
}

func init() {
	recoverCmd.Flags().BoolVar(&recoverRequireEvidence, "require-evidence", false, "Fail (exit status 1) when the commit is made but its evidence is incomplete")
	recoverCmd.Flags().BoolVar(&recoverForce, "force", false, "Recover a run whose record still says RUNNING (its process is known to be gone)")
	rootCmd.AddCommand(recoverCmd)
}
