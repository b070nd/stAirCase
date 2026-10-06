package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	resumeDiscard, resumeAckDrift, resumeFresh bool
)

var resumeCmd = &cobra.Command{
	Use:   "resume <run-id>",
	Short: "Continue a run that was interrupted before it committed",
	Long: `Continues a run that was killed, crashed or stopped before it committed: the same
run, branch and worktree, under the policy, plan and options it started with (it saved
them when it started), from the state its audit chain says it stood in. What it had
approved stays approved; proposal numbers, limits and the time it had used carry on
where they were; a new agent session takes over, told by the audit chain what is already
approved. The agent's own memory of the earlier part is not carried and never trusted.

Every check is made first, and any failure refuses with nothing changed: the run's
audit chain must verify, nobody may be running it, it must not have a commit or a
recovery begun, its branch must still be at its base, and its worktree must hold exactly
the approved state. A worktree that holds anything else (what the agent did after its
last approval) refuses and names the files; --discard-unapproved puts it back first. Nothing
is ever staged or adopted. A run halted for drift needs --ack-drift. A run whose time limit
is used up cannot be continued, only recovered ("staircase recover"), and neither can a
run that began before runs saved their terms.

A run that was started with an agent harness (claude-code, codex, gemini, opencode)
cannot have its own conversation continued yet: pass --fresh-context to continue it with a
new session grounded by the audit chain instead.`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		runID, err := parseID("run-id", args[0])
		if err != nil {
			return err
		}
		return resumeRun(runID)
	},
}

func init() {
	resumeCmd.Flags().BoolVar(&resumeDiscard, "discard-unapproved", false, "Put the worktree back to the approved state when it holds changes nobody approved")
	resumeCmd.Flags().BoolVar(&resumeAckDrift, "ack-drift", false, "Continue a run that was halted for drift, after reviewing it (recorded on the audit chain)")
	resumeCmd.Flags().BoolVar(&resumeFresh, "fresh-context", false, "Continue an agent-harness run with a new session grounded by the audit chain (its own conversation cannot be continued yet)")
	resumeCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0, "Decide from another terminal, a script or your browser through the local approval API on this port (0 = in this terminal)")
	resumeCmd.Flags().StringVar(&runApprovalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	resumeCmd.Flags().StringVar(&runModel, "model", "", "Model for an agent harness (default: its own)")
	resumeCmd.Flags().StringArrayVar(&runPassEnv, "pass-env", nil, "Name of an environment variable an agent harness may inherit, to give it its own credential; it, and any command it runs, can read the value")
	resumeCmd.Flags().BoolVar(&runDebug, "debug", false, "Write every audited message of this segment, scrubbed, to the run's debug log")
	rootCmd.AddCommand(resumeCmd)
}

func resumeRun(runID int64) error {
	wsDir := viper.GetString("STAIRCASE_DIR")
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	defer func() { _ = db.Close() }()
	runner := orchestrator.NewRunner(persistence.NewStore(db), wsDir)

	// Everything about the run that is read is read before the agent is built; the continuation checks it all again.
	pl, err := runner.PlanOf(runID)
	if err != nil {
		return err
	}
	who := "built-in"
	if pl != nil && pl.Harness != "" {
		who = pl.Harness
	}
	if who == "review" {
		return fmt.Errorf("run #%d reviews changes made elsewhere: there is no agent to continue; recover it with `staircase recover %d`", runID, runID)
	}
	if who != "built-in" && !resumeFresh {
		return fmt.Errorf("run #%d was made with %s: its own conversation cannot be continued yet, so it is not continued silently as a new one. "+
			"Pass --fresh-context to continue it with a new %s session grounded by the audit chain, or recover what it approved with `staircase recover %d`", runID, who, who, runID)
	}
	var ag orchestrator.Agent
	if pl != nil {
		if ag, err = agentFor(who, *pl); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("run #%d saved no plan: it cannot be continued, only recovered (`staircase recover %d`)", runID, runID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			fmt.Fprintln(os.Stderr, "\n⏹  Stopping the run and recording its outcome (further Ctrl-C are ignored)…")
			cancel()
		case <-ctx.Done():
		}
	}()

	err = runner.Resume(ctx, runID, orchestrator.ResumeOptions{Agent: ag, ApprovalPort: runApprovalPort, ApprovalToken: runApprovalToken,
		Debug: runDebug, AckDrift: resumeAckDrift, DiscardUnapproved: resumeDiscard,
		Context: map[bool]string{true: "fresh_grounded", false: ""}[resumeFresh]})
	if errors.Is(err, orchestrator.ErrNotContinuable) {
		return fmt.Errorf("%w\n   Nothing was changed. If its approvals matter: staircase recover %d", err, runID)
	}
	return err
}
