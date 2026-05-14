package main

import (
	"crypto/sha256"
	"fmt"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect runs, event logs, and execution history",
}

// ── inspect runs ──────────────────────────────────────────────────────────────

var inspectRunsCaseID int64

var inspectRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List runs, optionally filtered by case",
	RunE: func(_ *cobra.Command, _ []string) error {
		store, db, err := openStore()
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()

		var runs []domain.Run
		if inspectRunsCaseID != 0 {
			runs, err = store.ListRunsByCase(inspectRunsCaseID)
			if err != nil {
				return err
			}
		} else {
			for _, status := range []string{persistence.RunStatusRunning, persistence.RunStatusSuccess, persistence.RunStatusFailed, persistence.RunStatusKilled} {
				batch, _ := store.ListRunsByStatus(status)
				runs = append(runs, batch...)
			}
		}

		if len(runs) == 0 {
			fmt.Println("No runs found.")
			return nil
		}

		rows := make([][]string, len(runs))
		for i, r := range runs {
			dur := "—"
			if r.EndTime != nil {
				dur = r.EndTime.Sub(r.StartTime).Round(1e9).String()
			}
			rows[i] = []string{
				fmt.Sprint(r.ID),
				fmt.Sprint(r.CaseID),
				r.Status,
				r.GitBranch,
				fmt.Sprint(r.TopologyVersion),
				r.StartTime.Format("2006-01-02 15:04"),
				dur,
			}
		}
		table([]string{"ID", "Case", "Status", "Branch", "Topo", "Started", "Duration"}, rows)
		return nil
	},
}

// ── inspect log ───────────────────────────────────────────────────────────────

const inspectPayloadSnip = 120 // default truncation length for payload preview

var inspectLogFull bool

var inspectLogCmd = &cobra.Command{
	Use:   "log <run-id>",
	Short: "Show the SOC2 event log for a run and verify the hash chain",
	Args:  cobra.ExactArgs(1),
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

		run, err := store.GetRun(runID)
		if err != nil || run == nil {
			return fmt.Errorf("run #%d not found", runID)
		}

		fmt.Printf("Run #%d  case=#%d  status=%s  branch=%s\n",
			run.ID, run.CaseID, run.Status, run.GitBranch)
		if run.GitCommitHash != "" {
			fmt.Printf("         commit=%s\n", run.GitCommitHash)
		}
		fmt.Println()

		logs, err := store.ListEventLogs(runID)
		if err != nil {
			return err
		}
		if len(logs) == 0 {
			fmt.Println("No event log entries.")
			return nil
		}

		// Re-derive hash chain and flag any breaks.
		// Each entry carries its own git_commit_hash — the value that was
		// current when the entry was logged. Entries written before the
		// teardown commit have "" here; entries written after have the real
		// hash. Using the per-entry value is the only way to verify correctly.
		prevHash := ""
		chainOK := true
		for _, l := range logs {
			expected := fmt.Sprintf("%x", sha256.Sum256(
				[]byte(l.Payload+prevHash+l.GitCommitHash),
			))
			intact := l.EventHash == expected
			if !intact {
				chainOK = false
			}
			mark := "✅"
			if !intact {
				mark = "❌ TAMPERED"
			}
			hashSnip := l.EventHash
			if len(hashSnip) > 16 {
				hashSnip = hashSnip[:16] + "…"
			}
			fmt.Printf("%s #%-4d  %-14s  %s  %s\n",
				mark, l.ID, l.EventType, l.Timestamp.Format("15:04:05"), hashSnip)
			// Payload preview — truncated by default to avoid terminal flooding (C-4).
			if inspectLogFull {
				fmt.Printf("     %s\n", l.Payload)
			} else if len(l.Payload) > 0 {
				snip := l.Payload
				if len(snip) > inspectPayloadSnip {
					snip = snip[:inspectPayloadSnip] + "… (--full to see complete payload)"
				}
				fmt.Printf("     %s\n", snip)
			}
			prevHash = l.EventHash
		}

		fmt.Println()
		if chainOK {
			fmt.Printf("✅ Hash chain intact (%d entries).\n", len(logs))
		} else {
			fmt.Printf("❌ Hash chain BROKEN — log may have been tampered with!\n")
		}
		return nil
	},
}

func init() {
	inspectRunsCmd.Flags().Int64Var(&inspectRunsCaseID, "case", 0, "Filter by case ID")
	inspectLogCmd.Flags().BoolVar(&inspectLogFull, "full", false, "Print complete payloads without truncation")
	inspectCmd.AddCommand(inspectRunsCmd, inspectLogCmd)
	rootCmd.AddCommand(inspectCmd)
}
