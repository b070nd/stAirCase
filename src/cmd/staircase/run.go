package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	runDryRun        bool
	runForce         bool
	runSkipGate      bool
	runAutoStash     bool
	runDebug         bool
	runReconcile     bool
	runApprovalPort  int
	runApprovalToken string
)

var runCmd = &cobra.Command{
	Use:   "run <case-id>",
	Short: "Execute a Case run with the active swarm topology",
	Args:  cobra.ExactArgs(1),
	RunE:  runCaseHandler,
}

func init() {
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Validate and print the execution plan without running")
	runCmd.Flags().BoolVar(&runForce, "force", false, "Skip dirty-tree pre-flight check")
	runCmd.Flags().BoolVar(&runSkipGate, "skip-gates", false, "Bypass quality gate pre-flight (use with care)")
	runCmd.Flags().BoolVar(&runAutoStash, "auto-stash", false, "Auto-stash dirty working tree instead of aborting")
	runCmd.Flags().BoolVar(&runDebug, "debug", false, "Write all IPC messages to $STAIRCASE_DIR/log/staircase-debug.log")
	runCmd.Flags().BoolVar(&runReconcile, "reconcile", false,
		"Clean up orphan staircase/run-* branches and stale RUNNING records before starting")
	runCmd.Flags().IntVar(&runApprovalPort, "approval-port", 0,
		"Start an inbound HTTP approval server on this port (0 = disabled). "+
			"Exposes GET /v1/yields and POST /v1/yields/{id}/approve|reject for async HITL.")
	runCmd.Flags().StringVar(&runApprovalToken, "approval-token", "",
		"Bearer token required by the approval HTTP server. "+
			"If empty and --approval-port is set, a random token is generated and printed at startup.")
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

	runner := orchestrator.NewRunner(store, wsDir)
	return runner.Run(ctx, caseID, orchestrator.RunOptions{
		DryRun:        runDryRun,
		Force:         runForce,
		SkipGates:     runSkipGate,
		AutoStash:     runAutoStash,
		Debug:         runDebug,
		Reconcile:     runReconcile,
		ApprovalPort:  runApprovalPort,
		ApprovalToken: runApprovalToken,
	})
}
