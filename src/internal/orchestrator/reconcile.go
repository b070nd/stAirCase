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
	// OrphanBranches holds staircase/run-* branch names that had no corresponding
	// RUNNING run in the database. If pruneBranches was true, these are branches
	// that could NOT be deleted (delete failed); successfully pruned branches are
	// not included.
	OrphanBranches []string
}

// Reconcile detects and optionally cleans up orphan staircase/run-* branches
// in sourcePath and stale RUNNING run records for caseID.
//
// A run is considered stale if it has been RUNNING for more than 2 hours — the
// same heuristic used by [persistence.Store.KillStaleRuns] in the normal path.
//
// An orphan branch is a staircase/run-{N} branch where run N is not currently
// RUNNING in the database (either no DB record exists, or the record is not RUNNING).
// This happens when the orchestrator crashes after creating the branch but before
// completing the run.
//
// If pruneBranches is true, orphan branches are deleted from the git repository.
// OrphanBranches in the returned result then only contains branches that failed
// to delete (i.e., those that still need attention).
func (r *Runner) Reconcile(_ context.Context, caseID int64, sourcePath string, pruneBranches bool) (ReconcileResult, error) {
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

	// ── 3. Prune orphan branches if requested ─────────────────────────────────
	if pruneBranches && len(orphans) > 0 {
		var stillOrphaned []string
		for _, branch := range orphans {
			if _, err := gitOutput(sourcePath, "branch", "-D", branch); err != nil {
				stillOrphaned = append(stillOrphaned, branch) // could not delete
			}
		}
		result.OrphanBranches = stillOrphaned
	}

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
// corresponding DB run record is NOT in RUNNING status (or does not exist).
func (r *Runner) detectOrphanBranches(repoPath string) ([]string, error) {
	out, err := gitOutput(repoPath, "for-each-ref",
		"--format=%(refname:short)", "refs/heads/staircase/run-*")
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}

	var orphans []string
	for _, branch := range strings.Split(out, "\n") {
		branch = strings.TrimSpace(branch)
		if branch == "" {
			continue
		}
		suffix := strings.TrimPrefix(branch, "staircase/run-")
		runID, err := strconv.ParseInt(suffix, 10, 64)
		if err != nil {
			continue // unexpected branch name format; skip
		}
		run, err := r.store.GetRun(runID)
		if err != nil || run == nil || run.Status != persistence.RunStatusRunning {
			orphans = append(orphans, branch)
		}
	}
	return orphans, nil
}
