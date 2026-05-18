package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
)

var replayCmd = &cobra.Command{
	Use:   "replay <run-id>",
	Short: "Replay yield decisions for a completed run",
	Long: `Verify the SOC2 audit chain for a run and print every yield decision in order.

Replay refuses to proceed if the hash chain is broken — this prevents
replaying a tampered run log (CHECK 9.3.2).`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		runID, err := parseID("run-id", args[0])
		if err != nil {
			return err
		}

		return runReplay(os.Stdout, store, runID)
	},
}

// runReplay verifies the audit chain for runID then writes yield decisions to
// out. Extracted for testability (CHECK 9.3.1, 9.3.2).
func runReplay(out io.Writer, store *persistence.Store, runID int64) error {
	// CHECK 9.3.2: refuse to replay a run whose audit chain has been tampered with.
	if err := store.VerifyChain(runID); err != nil {
		return fmt.Errorf("audit chain verification failed for run #%d: %w", runID, err)
	}

	run, err := store.GetRun(runID)
	if err != nil || run == nil {
		return fmt.Errorf("run #%d not found", runID)
	}

	fmt.Fprintf(out, "Run #%d  case=#%d  status=%s  branch=%s\n",
		run.ID, run.CaseID, run.Status, run.GitBranch)
	if run.GitCommitHash != "" {
		fmt.Fprintf(out, "         commit=%s\n", run.GitCommitHash)
	}
	fmt.Fprintln(out)

	logs, err := store.ListEventLogs(runID)
	if err != nil {
		return err
	}

	decisions := printYieldDecisions(out, logs)

	if decisions == 0 {
		fmt.Fprintln(out, "No yield decisions recorded for this run.")
	} else {
		fmt.Fprintf(out, "\n%d yield decision(s) replayed. Audit chain intact.\n", decisions)
	}
	return nil
}

// yieldDecidedPayload mirrors the JSON shape written by the orchestrator.
type yieldDecidedPayload struct {
	Source     string `json:"source"`
	Agent      string `json:"agent"`
	ActionType string `json:"action_type"`
	Approved   bool   `json:"approved"`
	Feedback   string `json:"feedback,omitempty"`
}

// printYieldDecisions writes yield_decided entries from logs to out.
// Returns the number of decisions printed.
func printYieldDecisions(out io.Writer, logs []domain.RunEventLog) int {
	var n int
	for _, entry := range logs {
		if entry.EventType != "yield_decided" {
			continue
		}
		var p yieldDecidedPayload
		if err := json.Unmarshal([]byte(entry.Payload), &p); err != nil {
			continue
		}
		verdict := "REJECTED"
		if p.Approved {
			verdict = "APPROVED"
		}
		feedback := ""
		if p.Feedback != "" {
			feedback = "  feedback: " + p.Feedback
		}
		fmt.Fprintf(out, "#%-4d  %-16s  %-12s  %-8s  [%s]%s\n",
			entry.ID,
			p.Agent,
			p.ActionType,
			verdict,
			p.Source,
			feedback,
		)
		n++
	}
	return n
}

func init() {
	rootCmd.AddCommand(replayCmd)
}
