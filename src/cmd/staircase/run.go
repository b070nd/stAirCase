package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/b070nd/staircase-core/src/internal/obs"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	runDryRun         bool
	runForce          bool
	runSkipGate       bool
	runAutoStash      bool
	runDebug          bool
	runReconcile      bool
	runApprovalPort   int
	runApprovalToken  string
	runMetricsAddr    string
	runOTelEndpoint   string
	runRecordLLM      string
	runReplayLLM      string
	runAllowShellExec bool
)

var runCmd = &cobra.Command{
	Use:   "run <case-id>",
	Short: "Execute a Case run with the active swarm topology",
	Args:  cobra.ExactArgs(1),
	RunE:  runCaseHandler,
	// A FAILED/KILLED run returns an error so the process exits non-zero; the
	// run already printed its own status, so suppress cobra's usage/error dump
	// to keep that exit clean.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Validate and print the execution plan without running")
	runCmd.Flags().BoolVar(&runForce, "force", false, "Skip dirty-tree pre-flight check")
	runCmd.Flags().BoolVar(&runSkipGate, "skip-gates", false, "Bypass quality gate pre-flight (use with care)")
	runCmd.Flags().BoolVar(&runAutoStash, "auto-stash", false, "Auto-stash dirty working tree instead of aborting")
	runCmd.Flags().BoolVar(&runDebug, "debug", false, "Write all IPC messages to $STAIRCASE_DIR/log/staircase-debug.log")
	runCmd.Flags().BoolVar(&runReconcile, "reconcile", false,
		"Inspect orphan staircase/run-* branches (never delete them) and reconcile stale RUNNING records")
	runCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Start an inbound HTTP approval server on this port (0 = disabled). "+
			"Exposes GET /v1/yields and POST /v1/yields/{id}/approve|reject for async HITL.")
	runCmd.Flags().StringVar(&runApprovalToken, "approval-token", "",
		"Bearer token required by the approval HTTP server. "+
			"If empty and --approval-port is set, a random token is generated and printed at startup.")
	runCmd.Flags().StringVar(&runMetricsAddr, "metrics-addr", "",
		"Expose Prometheus metrics on this address (e.g. 127.0.0.1:9090). Empty = disabled.")
	runCmd.Flags().StringVar(&runOTelEndpoint, "otel-endpoint", "",
		"OTLP/gRPC endpoint for OpenTelemetry traces (e.g. localhost:4317). Empty = disabled (CHECK 10.3.1).")
	runCmd.Flags().StringVar(&runRecordLLM, "record-llm", "",
		"File path to record all LLM exchanges for deterministic replay in future test runs.")
	runCmd.Flags().StringVar(&runReplayLLM, "replay-llm", "",
		"File path to replay recorded LLM exchanges instead of calling the real API.")
	runCmd.Flags().BoolVar(&runAllowShellExec, "allow-shell-exec", false,
		"Enable run_shell for this run — agents may request OS-level shell execution subject to HITL approval. "+
			"Shell execution is disabled by default; pass this flag to opt in.")
	rootCmd.AddCommand(runCmd)
}

func runCaseHandler(_ *cobra.Command, args []string) error {
	caseID, err := parseID("case-id", args[0])
	if err != nil {
		return err
	}

	wsDir := viper.GetString("STAIRCASE_DIR")

	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Trap SIGINT/SIGTERM so deferred cleanup (branch restore, stash pop) runs.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()

	if runMetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		go func() { _ = http.ListenAndServe(runMetricsAddr, mux) }() // #nosec G114 -- metrics addr is operator-controlled, not user input
	}

	// Configure OpenTelemetry when --otel-endpoint is provided (CHECK 10.3.1).
	otelShutdown := obs.InitOTel(runOTelEndpoint)
	defer otelShutdown()

	runner := orchestrator.NewRunner(store, wsDir)
	return runner.Run(ctx, caseID, orchestrator.RunOptions{
		DryRun:         runDryRun,
		Force:          runForce,
		SkipGates:      runSkipGate,
		AutoStash:      runAutoStash,
		Debug:          runDebug,
		Reconcile:      runReconcile,
		ApprovalPort:   runApprovalPort,
		ApprovalToken:  runApprovalToken,
		RecordLLM:      runRecordLLM,
		ReplayLLM:      runReplayLLM,
		AllowShellExec: runAllowShellExec,
	})
}
