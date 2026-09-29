package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/b070nd/stAirCase/src/internal/llm"
	"github.com/b070nd/stAirCase/src/internal/obs"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
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
	runSandbox        string
	runChecks         []string
	runAgent          string
	runModel          string
	runAgreedBy       string // who agreed to the task before a session started
	runAckDrift       bool
	runValidator      []string
	runSignal         string
)

var runCmd = &cobra.Command{
	Use:   "run <case-id>",
	Short: "Run a compiled case: agents work, you approve, the approved change is committed",
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
	runCmd.Flags().BoolVar(&runForce, "force", false, "No effect: runs use a separate worktree")
	runCmd.Flags().BoolVar(&runSkipGate, "skip-gates", false, "Bypass quality gate pre-flight (use with care)")
	runCmd.Flags().BoolVar(&runAutoStash, "auto-stash", false, "No effect: runs use a separate worktree")
	for _, f := range []string{"force", "auto-stash"} {
		_ = runCmd.Flags().MarkDeprecated(f, "runs execute in a separate git worktree; your checkout is never modified")
	}
	runCmd.Flags().BoolVar(&runDebug, "debug", false, "Log every agent message (proposals, usage) to $STAIRCASE_DIR/log/")
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
		"OTLP/gRPC endpoint for OpenTelemetry traces (e.g. localhost:4317). Empty = disabled.")
	runCmd.Flags().StringVar(&runRecordLLM, "record-llm", "",
		"Record every model exchange of this run to this file (JSON lines) for offline replay.")
	runCmd.Flags().StringVar(&runReplayLLM, "replay-llm", "",
		"File path to replay recorded LLM exchanges instead of calling the real API.")
	runCmd.Flags().BoolVar(&runAllowShellExec, "allow-shell-exec", false,
		"Enable run_shell for this run - agents may request OS-level shell execution subject to HITL approval. "+
			"Shell execution is disabled by default; pass this flag to opt in.")
	runCmd.Flags().StringVar(&runSandbox, "sandbox", sandbox.Auto,
		"Where approved shell commands run: auto (in the sandbox when this machine has one: macOS sandbox-exec, Linux bwrap or Landlock), "+
			"required (refuse commands that cannot be sandboxed) or off")
	checkFlag(runCmd)
	runCmd.Flags().BoolVar(&runAckDrift, "ack-drift", false,
		"Run a case whose previous run was halted for drift, after reviewing it (recorded on the audit chain)")
	validatorFlag(runCmd)
	runCmd.Flags().StringVar(&runModel, "model", "", "Model for an agent harness (claude-code, codex); default: the harness's own")
	runCmd.Flags().StringVar(&runAgent, "agent", "built-in",
		"Agent to run: built-in (the compiled topology), claude-code or codex (governed through their hooks; experimental)")
	rootCmd.AddCommand(runCmd)
}

func runCaseHandler(_ *cobra.Command, args []string) error {
	caseID, err := parseID("case-id", args[0])
	if err != nil {
		return err
	}
	switch runSandbox {
	case sandbox.Auto, sandbox.Required, sandbox.Off:
	default:
		return fmt.Errorf("--sandbox must be auto, required or off, not %q", runSandbox)
	}
	return runCase(caseID)
}

// runCase runs a compiled case with the run flags.
func runCase(caseID int64) error {
	wsDir := viper.GetString("STAIRCASE_DIR")

	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Trap SIGINT/SIGTERM so the run is stopped and its outcome recorded. The
	// handler stays registered until the command returns: a second Ctrl-C is
	// absorbed instead of killing the process mid-cleanup (run left RUNNING).
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

	if runMetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		go func() { _ = http.ListenAndServe(runMetricsAddr, mux) }() // #nosec G114 -- metrics addr is operator-controlled, not user input
	}

	// Configure OpenTelemetry when --otel-endpoint is provided (CHECK 10.3.1).
	otelShutdown := obs.InitOTel(runOTelEndpoint)
	defer otelShutdown()

	// The case runs exactly as compiled: its plan, checked against its checksum.
	pl, err := plan.Load(filepath.Join(wsDir, "tmp", fmt.Sprintf("plan_case%d.json", caseID)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("case #%d is not compiled - run 'staircase compile %d' first", caseID, caseID)
	case err != nil:
		return fmt.Errorf("plan for case #%d: %w", caseID, err)
	case pl.CaseID != caseID:
		return fmt.Errorf("the plan was compiled for case #%d - run 'staircase compile %d --force'", pl.CaseID, caseID)
	}

	// The plan says who runs the case; --agent can only choose a harness for a
	// plan compiled for the built-in agents.
	who := runAgent
	if pl.Harness != "" {
		if runAgent != "built-in" && runAgent != pl.Harness {
			return fmt.Errorf("case #%d is compiled to run %s, not %s - recompile with 'staircase compile %d --force --agent %s'",
				caseID, pl.Harness, runAgent, caseID, runAgent)
		}
		who = pl.Harness
	}
	var ag orchestrator.Agent = &agent.Graph{Plan: pl, Record: runRecordLLM, Replay: runReplayLLM}
	switch who {
	case "built-in":
	case "claude-code":
		ag = &agent.ClaudeCode{Prompt: pl.Brief(), Model: runModel}
	case "codex":
		ag = &agent.Codex{Prompt: pl.Brief(), Model: runModel}
	case "review":
		ag = &agent.Review{Commit: pl.Review.Commit, By: pl.Review.By}
	default:
		return fmt.Errorf("unknown --agent %q: use built-in, claude-code or codex", who)
	}

	var validator *orchestrator.Validator
	for _, m := range runValidator {
		if llm.SecretFor(m) == "" {
			return fmt.Errorf("--validator %q: no provider serves this model", m)
		}
	}
	if len(runValidator) > 0 {
		validator = &orchestrator.Validator{Models: runValidator}
	}
	var sig *orchestrator.Signal
	if runSignal != "" {
		sig = &orchestrator.Signal{Model: runSignal}
	}

	runner := orchestrator.NewRunner(store, wsDir)
	return runner.Run(ctx, caseID, orchestrator.RunOptions{
		DryRun:         runDryRun,
		SkipGates:      runSkipGate,
		Debug:          runDebug,
		Reconcile:      runReconcile,
		ApprovalPort:   runApprovalPort,
		ApprovalToken:  runApprovalToken,
		AllowShellExec: runAllowShellExec,
		Sandbox:        runSandbox,
		ApproveInScope: sessionInScope,
		Signal:         sig,
		Checks:         runChecks,
		AckDrift:       runAckDrift,
		Validator:      validator,
		Agreed:         runAgreedBy,
		Agent:          ag,
		Plan:           &pl,
	})
}

// checkFlag adds --check to cmd.
func checkFlag(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&runChecks, "check", nil,
		"A command (such as your tests) to run on the commit once it is made, in the sandbox; "+
			"its result goes into the change certificate, and verify fails a failed check. Repeat for more")
}

// validatorFlag adds --validator and --signal to cmd.
func validatorFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&runSignal, "signal", "",
		"Evaluation model (e.g. typesafe-ai/jev via the LLM gateway) asked about every change approved without you; "+
			"it can only send a change to you (risky, off the stories, or no answer), never approve one")
	cmd.Flags().StringArrayVar(&runValidator, "validator", nil,
		"Model that reviews in-scope file edits the policy leaves open (e.g. openai/gpt-6-astra via the LLM gateway); "+
			"a human approves the run's final change once. Repeat for a panel: the models must agree, otherwise a human decides")
}
