package orchestrator

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/staircase-core/src/internal/persistence"
)

// ReconcileResult describes what [Reconcile] found and cleaned up.
type ReconcileResult struct {
	// StalledRuns holds the IDs of runs that were RUNNING and got marked KILLED.
	StalledRuns []int64
	// OrphanBranches holds staircase/run-* branches requiring manual inspection.
	// A missing/failed run record does not prove that its branch is disposable.
	OrphanBranches []string
}

// Reconcile detects orphan staircase/run-* branches
// in sourcePath and stale RUNNING run records for caseID.
//
// A run is considered stale if it has been RUNNING for more than 2 hours — the
// same heuristic used by [persistence.Store.KillStaleRuns] in the normal path.
//
// Completed-run branches are delivery evidence, not orphans. Other inactive
// branches are reported but never automatically deleted: they may contain
// unmerged work or belong to another workspace with overlapping numeric IDs.
// The former pruneBranches argument is retained for callers but no longer
// authorizes deleting refs without an independent ownership/retention check.
func (r *Runner) Reconcile(_ context.Context, caseID int64, sourcePath string, _ bool) (ReconcileResult, error) {
	var result ReconcileResult

	// ── 1. Kill stale RUNNING DB records for this case ────────────────────────
	killed, err := r.killStaleRunRecords(caseID)
	if err != nil {
		return result, fmt.Errorf("reconcile stale runs: %w", err)
	}
	result.StalledRuns = killed

	// ── 2. Detect orphan git branches ─────────────────────────────────────────
	if sourcePath == "" {
		return result, nil
	}
	orphans, err := r.detectOrphanBranches(sourcePath)
	if err != nil {
		// Non-fatal: repo may not exist yet (first run) or git not installed.
		return result, nil
	}
	result.OrphanBranches = orphans

	return result, nil
}

// killStaleRunRecords finds RUNNING runs for caseID older than 2 hours and
// marks them as KILLED. Returns the IDs of affected runs.
func (r *Runner) killStaleRunRecords(caseID int64) ([]int64, error) {
	runs, err := r.store.ListRunsByCase(caseID)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-2 * time.Hour)
	var killed []int64
	for _, run := range runs {
		if run.Status != persistence.RunStatusRunning {
			continue
		}
		if run.StartTime.After(cutoff) {
			continue // started recently — may still be running legitimately
		}
		now := time.Now()
		if err := r.store.UpdateRunStatus(run.ID, persistence.RunStatusKilled, &now, ""); err != nil {
			return nil, fmt.Errorf("mark run %d killed: %w", run.ID, err)
		}
		killed = append(killed, run.ID)
	}
	return killed, nil
}

// detectOrphanBranches returns staircase/run-* branch names in repoPath whose
// corresponding DB run record is neither active nor successfully delivered.
func (r *Runner) detectOrphanBranches(repoPath string) ([]string, error) {
	gr, err := OpenGitRepo(repoPath)
	if err != nil {
		return nil, err
	}
	branches, err := gr.ListBranches("staircase/run-")
	if err != nil {
		return nil, err
	}

	var orphans []string
	for _, branch := range branches {
		suffix := strings.TrimPrefix(branch, "staircase/run-")
		runID, err := strconv.ParseInt(suffix, 10, 64)
		if err != nil {
			continue // unexpected branch name format; skip
		}
		run, err := r.store.GetRun(runID)
		if err != nil {
			return nil, fmt.Errorf("inspect run for branch %q: %w", branch, err)
		}
		if run == nil || (run.Status != persistence.RunStatusRunning && run.Status != persistence.RunStatusSuccess && run.GitCommitHash == "") {
			orphans = append(orphans, branch)
		}
	}
	return orphans, nil
}
