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
	resumeDiscard, resumeAckDrift, resumeFresh, resumeAckUnknown bool
)

var resumeCmd = &cobra.Command{
	Use:   "resume <run-id>",
	Short: "Continue a run that was interrupted before it committed",
	Long: `Continues a run that was killed, crashed or stopped before it committed: the same
run, branch and worktree, under the policy, plan and options it started with (it saved
them when it started), from the state its audit chain says it stood in. What it had
approved stays approved; proposal numbers, limits and the time it had used carry on
where they were. What is approved is decided by the audit chain, never by what the agent
remembers: an agent harness continues its own session when it can (see below), and
otherwise a new session takes over, told by the chain what is already approved.

Every check is made first, and any failure refuses with nothing changed: the run's
audit chain must verify, nobody may be running it, it must not have a commit or a
recovery begun, its branch must still be at its base, and its worktree must hold exactly
the approved state. A worktree that holds anything else (what the agent did after its
last approval) refuses and names the files; --discard-unapproved puts it back first. Nothing
is ever staged or adopted. A run halted for drift needs --ack-drift. A run whose time limit
is used up cannot be continued, only recovered ("staircase recover"), and neither can a
run that began before runs saved their terms.

Every launch of the agent is claimed on the audit chain before its process starts, and
its outcome is recorded when it stops. A run whose last launch has no outcome (its
process was killed, crashed or lost its machine) is not launched again blindly: resume
records the outcome as unknown_after_restart, parks the run and says why. Check what the
agent may have done, then say you looked with "staircase acknowledge" (it launches nothing,
and a hosted runner's own resume then proceeds) or continue at once with --ack-unknown,
which does the same acknowledgement first. A run whose launches ended this way 3 times is
not launched again at all, acknowledged or not; recover what it approved instead.

A run that was started with an agent harness continues the harness's own session when it
can: Claude Code, Gemini CLI and Codex have their session id on the audit chain and are
resumed with it, so the agent remembers its conversation. When that cannot be done (the
chain names no session, or the harness cannot resume one: OpenCode) the run is refused,
never continued silently as a new conversation. --fresh-context continues it with a new
session grounded by the audit chain instead, and the chain says so.`,
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
	resumeCmd.Flags().BoolVar(&resumeAckUnknown, "ack-unknown", false, "Continue a run parked because its last launch has no known outcome, after checking what its agent may have done: acknowledges the launch like staircase acknowledge, then continues (recorded on the audit chain)")
	resumeCmd.Flags().BoolVar(&resumeFresh, "fresh-context", false, "Continue an agent-harness run in a new session grounded by the audit chain, instead of resuming its own session (the only way to continue one whose session cannot be resumed)")
	resumeCmd.Flags().IntVar(&cli.approvalPort, "approval-port", 0, "Decide from another terminal, a script or your browser through the local approval API on this port (0 = in this terminal)")
	resumeCmd.Flags().StringVar(&cli.approvalToken, "approval-token", "", "Token for the approval API (default: a new one, printed)")
	resumeCmd.Flags().StringVar(&cli.model, "model", "", "Model for an agent harness (default: its own)")
	resumeCmd.Flags().StringArrayVar(&cli.passEnv, "pass-env", nil, "Name of an environment variable an agent harness may inherit, to give it its own credential; it, and any command it runs, can read the value")
	resumeCmd.Flags().BoolVar(&cli.debug, "debug", false, "Write every audited message of this segment, scrubbed, to the run's debug log")
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
	pl, err := runner.PlanOf(runID, false)
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
	var ag orchestrator.Agent
	if pl != nil {
		if ag, err = agentFor(cli, who, *pl); err != nil {
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

	opts := orchestrator.ResumeOptions{Agent: ag, ApprovalPort: cli.approvalPort, ApprovalToken: cli.approvalToken,
		Debug: cli.debug, AckDrift: resumeAckDrift, DiscardUnapproved: resumeDiscard, AckUnknown: resumeAckUnknown}
	if resumeFresh {
		opts.Context = "fresh_grounded"
	}
	return resumeRefusal(runner.Resume(ctx, runID, opts), runID)
}

// resumeRefusal says what a refused continuation changed: nothing, or only the outcome of a lost launch, when this very
// attempt recorded it (a run that was parked before has it on its chain already, and this attempt wrote nothing).
func resumeRefusal(err error, runID int64) error {
	if !errors.Is(err, orchestrator.ErrNotContinuable) {
		return err
	}
	changed := "Nothing was changed."
	if errors.Is(err, orchestrator.ErrLaunchRecorded) {
		changed = "Only the lost launch's outcome was recorded, as unknown_after_restart, and nothing else was changed."
	}
	return fmt.Errorf("%w\n   %s If its approvals matter: staircase recover %d", err, changed, runID)
}
