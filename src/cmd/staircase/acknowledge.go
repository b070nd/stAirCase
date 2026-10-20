package main

import (
	"fmt"
	"io"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var acknowledgeCmd = &cobra.Command{
	Use:   "acknowledge <run-id>",
	Short: "Say you looked at a run whose agent was lost, without launching anything",
	Long: `A run whose agent's launch has no established end (its process was killed, crashed or lost
its machine, or the agent did not stop when asked) is parked: nobody knows what the agent did,
outside the worktree included, so it is not launched again blindly. acknowledge records, on the
run's audit chain, that a person looked: it writes the launch's end (unknown_after_restart) if the
launch is still open, then the acknowledgement, naming the exact launch (its attempt and
generation), so it cannot cover a later loss. It launches nothing and continues nothing.

Afterwards the run can be continued: with staircase resume, or by a hosted runner's own resume
(a hosted resume never acknowledges by itself, because the acknowledgement attests that a person
looked). staircase resume --ack-unknown does both in one step.

A run with no open or lost launch is refused, and calling it again on an acknowledged launch
changes nothing. Acknowledging does not lift the limit: a run whose launches were lost 3 times
is not launched again; recover what it approved with staircase recover.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runID, err := parseID("run-id", args[0])
		if err != nil {
			return err
		}
		return acknowledgeRun(cmd.OutOrStdout(), runID)
	},
}

func init() {
	rootCmd.AddCommand(acknowledgeCmd)
}

func acknowledgeRun(w io.Writer, runID int64) error {
	wsDir := viper.GetString("STAIRCASE_DIR")
	store, db, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	res, err := orchestrator.NewRunner(store, wsDir).Acknowledge(runID)
	if err != nil {
		if res.EndRecorded {
			err = fmt.Errorf("%w\n   Only the lost launch's outcome was recorded, as unknown_after_restart, and nothing else was changed", err)
		}
		return err
	}
	if !res.Acknowledged {
		fmt.Fprintf(w, "Launch %d of run #%d was acknowledged already. Nothing was changed.\n", res.Attempt, runID)
		return nil
	}
	if res.EndRecorded {
		fmt.Fprintf(w, "The end of launch %d was never recorded: it is now, as unknown_after_restart.\n", res.Attempt)
	}
	fmt.Fprintf(w, "Launch %d of run #%d is acknowledged. Nothing was launched. Continue the run with `staircase resume %d`, or resume its hosted session.\n", res.Attempt, runID, runID)
	return nil
}
