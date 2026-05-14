package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	gateJSONOut bool
	gateOutFile string
)

var gateCmd = &cobra.Command{
	Use:   "gate <case-id>",
	Short: "Run pre-flight quality gates for a case and generate a report",
	Long: `Executes all registered quality gates against a case before running.

Gates are grouped by category (structural, security, runtime, dependency).
BLOCK gates must pass for 'staircase run' to proceed.
WARN  gates are advisory — they surface issues but do not block execution.

Exit codes:
  0  all BLOCK gates passed (overall PASS or WARN)
  1  one or more BLOCK gates failed (overall FAIL)
  2  usage error`,
	Args: cobra.ExactArgs(1),
	RunE: runGateHandler,
}

func init() {
	gateCmd.Flags().BoolVar(&gateJSONOut, "json", false, "Emit machine-readable JSON report")
	gateCmd.Flags().StringVar(&gateOutFile, "out", "", "Write report to file (default: stdout)")
	rootCmd.AddCommand(gateCmd)
}

func runGateHandler(_ *cobra.Command, args []string) error {
	caseID, err := parseID("case-id", args[0])
	if err != nil {
		return err
	}

	store, db, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	report := gate.RunAll(gate.Context{
		CaseID: caseID,
		WsDir:  viper.GetString("STAIRCASE_DIR"),
		Store:  store,
	})

	out := os.Stdout
	if gateOutFile != "" {
		out, err = os.Create(gateOutFile)
		if err != nil {
			return fmt.Errorf("open output file: %w", err)
		}
		defer func() { _ = out.Close() }()
	}

	if gateJSONOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printGateReport(out, report)
	}

	if report.Blocking() {
		os.Exit(1)
	}
	return nil
}

// printGateReport renders a human-readable gate report to w.
//
//nolint:errcheck // fmt.Fprint* to an io.Writer; errors are not actionable in a CLI reporter.
func printGateReport(w io.Writer, r gate.Report) {
	// ── Header ───────────────────────────────────────────────────────────────
	fmt.Fprintf(w, "\nQuality Gate Report — case #%d\n", r.CaseID)
	fmt.Fprintf(w, "Run at: %s\n\n", r.RunAt.Format(time.RFC3339))

	// ── Column widths ────────────────────────────────────────────────────────
	const (
		wCat  = 12
		wName = 36
		wSev  = 9
		wStat = 6
	)
	hdr := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %s",
		wCat, "CATEGORY", wName, "GATE", wSev, "SEVERITY", wStat, "STATUS", "MESSAGE")
	sep := strings.Repeat("─", len(hdr))
	fmt.Fprintln(w, hdr)
	fmt.Fprintln(w, sep)

	prevCat := ""
	for _, res := range r.Gates {
		if res.Category != prevCat {
			if prevCat != "" {
				fmt.Fprintln(w)
			}
			prevCat = res.Category
		}
		statusIcon := statusIcon(res.Status)
		fmt.Fprintf(
			w, "%-*s  %-*s  %-*s  %s%-*s  %s\n",
			wCat, res.Category,
			wName, res.Name,
			wSev, string(res.Severity),
			statusIcon, wStat, string(res.Status),
			res.Message,
		)
	}

	// ── Footer ────────────────────────────────────────────────────────────────
	fmt.Fprintln(w)
	fmt.Fprintln(w, strings.Repeat("─", len(sep)))
	overallIcon := statusIcon(r.Overall)
	fmt.Fprintf(w, "Overall: %s%s\n", overallIcon, r.Overall)
	fmt.Fprintf(w, "Summary: %d pass, %d warn, %d fail, %d skip\n",
		r.Summary.Pass, r.Summary.Warn, r.Summary.Fail, r.Summary.Skip)

	if r.Blocking() {
		fmt.Fprintln(w, "\n❌  BLOCK failures must be resolved before running.")
	} else if r.Overall == gate.StatusWarn {
		fmt.Fprintln(w, "\n⚠   Warnings detected — review before running.")
	} else {
		fmt.Fprintln(w, "\n✅  All BLOCK gates passed. Safe to run.")
	}
}

func statusIcon(s gate.Status) string {
	switch s {
	case gate.StatusPass:
		return "✅ "
	case gate.StatusWarn:
		return "⚠  "
	case gate.StatusFail:
		return "❌ "
	default:
		return "⏭  "
	}
}
