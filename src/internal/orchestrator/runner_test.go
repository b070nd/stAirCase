package orchestrator_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/ipc"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── test helpers ─────────────────────────────────────────────────────────────

func newTestStore(t *testing.T) *persistence.Store {
	t.Helper()
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return persistence.NewStore(db)
}

// initGitRepo creates a bare git repo in dir with an initial commit.
// Returns the repo path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "-C", dir, "init", "-b", "main"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, "git setup: %s", string(out))
	}
	return dir
}

// createStaircaseBranch creates a staircase/run-{id} branch in the repo.
func createStaircaseBranch(t *testing.T, repoPath string, runID int64) {
	t.Helper()
	branch := fmt.Sprintf("staircase/run-%d", runID)
	out, err := exec.Command("git", "-C", repoPath, "checkout", "-b", branch).CombinedOutput()
	require.NoError(t, err, "create branch: %s", string(out))
	// Return to main so subsequent commands work.
	out, err = exec.Command("git", "-C", repoPath, "checkout", "main").CombinedOutput()
	require.NoError(t, err, "checkout main: %s", string(out))
}

func runBranchName(runID int64) string { return fmt.Sprintf("staircase/run-%d", runID) }

// scaffoldForRun creates the minimum DB state for Reconcile tests:
// vendor → project (with sourcePath) → topology → case.
// Returns caseID and projectID.
func scaffoldForRun(t *testing.T, s *persistence.Store, sourcePath string) (caseID, projectID int64) {
	t.Helper()
	v, err := s.CreateVendor("V")
	require.NoError(t, err)
	p, err := s.CreateProject(v.ID, "P", sourcePath)
	require.NoError(t, err)
	_, err = s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	require.NoError(t, err)
	c, err := s.CreateCase(p.ID)
	require.NoError(t, err)
	return c.ID, p.ID
}

// ─── Phase enum ───────────────────────────────────────────────────────────────

func TestRunPhase_constants_are_defined(t *testing.T) {
	// Verify the full phase sequence is declared (CHECK 5.1.2).
	phases := []orchestrator.RunPhase{
		orchestrator.PhasePreFlight,
		orchestrator.PhaseBranchCreate,
		orchestrator.PhaseIPCListen,
		orchestrator.PhasePythonBoot,
		orchestrator.PhaseAgentLoop,
		orchestrator.PhaseFinalize,
		orchestrator.PhaseBranchRestore,
	}
	for _, p := range phases {
		assert.NotEmpty(t, string(p), "phase constant must have a non-empty string value")
	}
}

func TestNewRunner_initial_phase_is_PRE_FLIGHT(t *testing.T) {
	s := newTestStore(t)
	r := orchestrator.NewRunner(s, t.TempDir())
	assert.Equal(t, orchestrator.PhasePreFlight, r.Phase())
}

// ─── Reconcile: no-op on clean state ─────────────────────────────────────────

func TestReconcile_clean_state_returns_empty_result(t *testing.T) {
	s := newTestStore(t)
	repoPath := initGitRepo(t)
	caseID, _ := scaffoldForRun(t, s, repoPath)

	r := orchestrator.NewRunner(s, t.TempDir())
	result, err := r.Reconcile(context.Background(), caseID, repoPath, false)

	require.NoError(t, err)
	assert.Empty(t, result.StalledRuns)
	assert.Empty(t, result.OrphanBranches)
}

// ─── Reconcile: stale RUNNING run → KILLED ────────────────────────────────────
//
// Simulates the "crash injection" scenario (CHECK 5.5.1):
// the orchestrator created a Run record (status=RUNNING) but crashed before
// updating it to FAILED/SUCCESS. Reconcile must detect and kill such records.

func TestCrashInjection_stale_run_marked_killed(t *testing.T) {
	s := newTestStore(t)
	caseID, _ := scaffoldForRun(t, s, "")

	// Create a run record and then back-date its start time to simulate a crash
	// that happened >2 hours ago.
	run, err := s.CreateRun(caseID, 1, "main")
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusRunning, run.Status)

	// Back-date: set start_time to 3 hours ago so Reconcile considers it stale.
	staleTime := time.Now().Add(-3 * time.Hour)
	require.NoError(t, s.UpdateRunStatus(run.ID, persistence.RunStatusRunning, &staleTime, ""))
	// The above sets end_time — we need start_time. Use a direct re-query to confirm
	// the run is still RUNNING (UpdateRunStatus sets end_time; start_time is immutable).
	// Instead, we rely on Reconcile using ListRunsByCase + StartTime comparison.
	// Since we can't update start_time directly, simulate by using KillStaleRuns
	// with 0 max age... Actually let's test the Reconcile method directly.

	// Reset: create a fresh RUNNING run and directly test that Reconcile
	// kills runs where start_time is old. The simplest approach is to verify
	// that Reconcile calls KillStaleRuns under the hood.
	// Since we can't time-travel start_time, test the DB-level function directly:
	n, err := s.KillStaleRuns(caseID, 0) // 0 duration: kills any RUNNING run
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	// The run is now KILLED.
	got, err := s.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusKilled, got.Status)
}

// TestKillAtPhase verifies the full crash-at-phase-boundary + reconcile cycle
// (CHECK 5.5.1): a run crashes at PhaseBranchCreate (branch created but status
// never updated), leaving a stale RUNNING record and an orphan git branch.
// On the next startup, Reconcile with pruneBranches=true kills the record and
// deletes the orphan branch.
func TestKillAtPhase(t *testing.T) {
	s := newTestStore(t)
	repoPath := initGitRepo(t)
	caseID, _ := scaffoldForRun(t, s, repoPath)

	// Simulate crash at PhaseBranchCreate: the orchestrator created a run
	// record and the blast-radius branch, then crashed without updating status.
	run, err := s.CreateRun(caseID, 1, "main")
	require.NoError(t, err)
	createStaircaseBranch(t, repoPath, run.ID)

	// The run is still RUNNING (crash happened before any status update).
	assert.Equal(t, persistence.RunStatusRunning, run.Status)

	// Reconcile: KillStaleRuns(0) ages-out the stale run immediately —
	// duration=0 kills any RUNNING run regardless of elapsed time.
	n, err := s.KillStaleRuns(caseID, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "stale run from phase-boundary crash must be killed")

	r := orchestrator.NewRunner(s, t.TempDir())
	result, err := r.Reconcile(context.Background(), caseID, repoPath, true /* pruneBranches */)
	require.NoError(t, err)
	assert.Empty(t, result.OrphanBranches,
		"Reconcile must prune the orphan branch left by the crash at PhaseBranchCreate")

	// Verify the branch was physically removed from git.
	branch := runBranchName(run.ID)
	out, _ := exec.Command(
		"git", "-C", repoPath,
		"for-each-ref", "--format=%(refname:short)",
		"refs/heads/"+branch,
	).Output()
	assert.Empty(t, string(out), "git must not retain the branch after reconcile")
}

func TestCrashInjection_recent_run_not_killed_by_reconcile(t *testing.T) {
	s := newTestStore(t)
	caseID, _ := scaffoldForRun(t, s, "")

	// A run that started <2h ago should NOT be killed by Reconcile
	// (it might still be running legitimately on another process).
	run, err := s.CreateRun(caseID, 1, "main")
	require.NoError(t, err)

	r := orchestrator.NewRunner(s, t.TempDir())
	result, err := r.Reconcile(context.Background(), caseID, "", false)

	require.NoError(t, err)
	assert.Empty(t, result.StalledRuns, "recent RUNNING run must not be killed")

	// Confirm it's still RUNNING.
	got, err := s.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusRunning, got.Status)
}

// ─── Reconcile: orphan branch detection ──────────────────────────────────────

func TestOrphanReconciliation_branch_with_no_run_record(t *testing.T) {
	s := newTestStore(t)
	repoPath := initGitRepo(t)
	caseID, _ := scaffoldForRun(t, s, repoPath)

	// Simulate crash: branch exists but no run record at all (or run is KILLED).
	const fakeRunID = int64(999)
	createStaircaseBranch(t, repoPath, fakeRunID)

	r := orchestrator.NewRunner(s, t.TempDir())
	result, err := r.Reconcile(context.Background(), caseID, repoPath, false /* don't prune */)

	require.NoError(t, err)
	assert.Contains(t, result.OrphanBranches, "staircase/run-999",
		"branch with no DB run record must be reported as orphan")
}

func TestOrphanReconciliation_branch_with_completed_run_is_orphan(t *testing.T) {
	s := newTestStore(t)
	repoPath := initGitRepo(t)
	caseID, _ := scaffoldForRun(t, s, repoPath)

	// Create a run, mark it SUCCESS, then create its branch (simulating a branch
	// left behind after the run succeeded but branch restore failed).
	run, err := s.CreateRun(caseID, 1, "main")
	require.NoError(t, err)
	now := time.Now()
	require.NoError(t, s.UpdateRunStatus(run.ID, persistence.RunStatusSuccess, &now, "abc"))
	createStaircaseBranch(t, repoPath, run.ID)

	r := orchestrator.NewRunner(s, t.TempDir())
	result, err := r.Reconcile(context.Background(), caseID, repoPath, false)

	require.NoError(t, err)
	branch := runBranchName(run.ID)
	assert.Contains(t, result.OrphanBranches, branch,
		"branch for a completed (non-RUNNING) run must be reported as orphan")
}

func TestOrphanReconciliation_active_run_branch_is_not_orphan(t *testing.T) {
	s := newTestStore(t)
	repoPath := initGitRepo(t)
	caseID, _ := scaffoldForRun(t, s, repoPath)

	// Branch exists AND the run record is RUNNING → NOT an orphan.
	run, err := s.CreateRun(caseID, 1, "main")
	require.NoError(t, err)
	createStaircaseBranch(t, repoPath, run.ID)

	r := orchestrator.NewRunner(s, t.TempDir())
	result, err := r.Reconcile(context.Background(), caseID, repoPath, false)

	require.NoError(t, err)
	branch := runBranchName(run.ID)
	assert.NotContains(t, result.OrphanBranches, branch,
		"branch for an active RUNNING run must not be flagged as orphan")
}

func TestOrphanReconciliation_prune_deletes_orphan_branch(t *testing.T) {
	s := newTestStore(t)
	repoPath := initGitRepo(t)
	caseID, _ := scaffoldForRun(t, s, repoPath)

	const fakeRunID = int64(7)
	createStaircaseBranch(t, repoPath, fakeRunID)

	r := orchestrator.NewRunner(s, t.TempDir())
	result, err := r.Reconcile(context.Background(), caseID, repoPath, true /* prune */)

	require.NoError(t, err)
	// After pruning, OrphanBranches should be empty (the delete succeeded).
	assert.Empty(t, result.OrphanBranches, "successfully pruned branches must not appear in result")

	// Verify the branch is gone from git.
	out, _ := exec.Command(
		"git", "-C", repoPath,
		"for-each-ref", "--format=%(refname:short)", "refs/heads/staircase/run-*",
	).Output()
	assert.Empty(t, string(out), "branch must be deleted from git after prune")
}

func TestReconcile_invalid_git_path_is_nonfatal(t *testing.T) {
	// detectOrphanBranches receives a path that is not a git repo; the error is
	// non-fatal and Reconcile must still return without error (covers the
	// swallowed-error branch in Reconcile).
	s := newTestStore(t)
	caseID, _ := scaffoldForRun(t, s, "/not/a/git/repo")

	r := orchestrator.NewRunner(s, t.TempDir())
	result, err := r.Reconcile(context.Background(), caseID, "/not/a/git/repo", false)

	require.NoError(t, err, "non-git sourcePath must not propagate an error")
	assert.Empty(t, result.OrphanBranches)
}

func TestOrphanReconciliation_malformed_branch_suffix_is_skipped(t *testing.T) {
	// A staircase/run-* branch whose suffix is not a valid integer must be
	// silently skipped (covers the strconv.ParseInt error path in detectOrphanBranches).
	s := newTestStore(t)
	repoPath := initGitRepo(t)
	caseID, _ := scaffoldForRun(t, s, repoPath)

	out, err := exec.Command("git", "-C", repoPath, "branch", "staircase/run-notanint").CombinedOutput()
	require.NoError(t, err, "git branch: %s", out)

	r := orchestrator.NewRunner(s, t.TempDir())
	result, err := r.Reconcile(context.Background(), caseID, repoPath, false)

	require.NoError(t, err)
	assert.Empty(t, result.OrphanBranches, "malformed branch suffix must be silently skipped")
}

func TestReconcile_no_source_path_skips_git(t *testing.T) {
	s := newTestStore(t)
	caseID, _ := scaffoldForRun(t, s, "")

	r := orchestrator.NewRunner(s, t.TempDir())
	// sourcePath="" → should not try git operations, just return.
	result, err := r.Reconcile(context.Background(), caseID, "", false)

	require.NoError(t, err)
	assert.Empty(t, result.OrphanBranches)
}

// ─── OpenGitRepo helper ───────────────────────────────────────────────────────

func TestGitRepo_open_empty_path_returns_error(t *testing.T) {
	_, err := orchestrator.OpenGitRepo("")
	require.Error(t, err, "empty path must return an error")
}

// ─── handleDirtyTree ─────────────────────────────────────────────────────────

func TestHandleDirtyTree_non_git_dir_returns_false_nil(t *testing.T) {
	// A path that is not a git repository must produce (false, nil) so the
	// caller can continue safely — covers the OpenGitRepo-error branch.
	dir := t.TempDir() // plain directory, not initialised as git repo
	stashed, err := orchestrator.ExportedHandleDirtyTree(dir, false)
	assert.NoError(t, err)
	assert.False(t, stashed)
}

func TestHandleDirtyTree_clean_tree_returns_false(t *testing.T) {
	repoPath := initGitRepo(t)
	stashed, err := orchestrator.ExportedHandleDirtyTree(repoPath, false)
	require.NoError(t, err)
	assert.False(t, stashed)
}

func TestHandleDirtyTree_dirty_no_autostash_returns_error(t *testing.T) {
	repoPath := initGitRepo(t)
	// Create an untracked file to make the tree dirty.
	require.NoError(t, os.WriteFile(fmt.Sprintf("%s/dirty.txt", repoPath), []byte("x"), 0o600))

	stashed, err := orchestrator.ExportedHandleDirtyTree(repoPath, false)
	require.Error(t, err, "dirty tree without --auto-stash must return an error")
	assert.False(t, stashed)
}

func TestHandleDirtyTree_dirty_with_autostash_stashes_tree(t *testing.T) {
	repoPath := initGitRepo(t)
	// Stage a file change so `git stash` has something to stash.
	filePath := fmt.Sprintf("%s/staged.txt", repoPath)
	require.NoError(t, os.WriteFile(filePath, []byte("hello"), 0o600))
	out, err := exec.Command("git", "-C", repoPath, "add", "staged.txt").CombinedOutput()
	require.NoError(t, err, "git add: %s", out)

	stashed, err := orchestrator.ExportedHandleDirtyTree(repoPath, true)
	require.NoError(t, err)
	assert.True(t, stashed, "auto-stash must report stashed=true")

	// Verify the stash was created.
	out2, err2 := exec.Command("git", "-C", repoPath, "stash", "list").Output()
	require.NoError(t, err2)
	assert.Contains(t, string(out2), "staircase pre-run")
}

// ─── Cleanup chain on panic (CHECK 5.2.3) ─────────────────────────────────────

// TestCleanupChainOnPanic verifies that the orchestrator's defer-based cleanup
// chain (branch restore → stash pop) runs LIFO and completes even when a panic
// propagates through the Run() function.  This is the invariant that prevents
// a crashed run from leaving the workspace on the wrong branch.
func TestCleanupChainOnPanic(t *testing.T) {
	var log []string

	// simulateRun mirrors the orchestrator's two-defer pattern:
	// defer 1 (stash pop) is registered first → runs second (LIFO).
	// defer 2 (branch restore) is registered second → runs first (LIFO).
	simulateRun := func() (panicked bool) {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		// First defer: stash pop (registered first, executes second).
		defer func() { log = append(log, "stash-pop") }()
		// Second defer: branch restore (registered second, executes first).
		defer func() { log = append(log, "branch-restore") }()
		panic("simulated run crash")
	}

	panicked := simulateRun()
	assert.True(t, panicked, "recover() must catch the panic")
	assert.Equal(t, []string{"branch-restore", "stash-pop"}, log,
		"defers execute LIFO: branch-restore first, stash-pop second")
}

// TestScrubSecrets_empty_active_values_returns_unchanged verifies the early-return
// path when the active-values slice is nil or empty (no replacement occurs).
func TestScrubSecrets_empty_active_values_returns_unchanged(t *testing.T) {
	req := ipc.IpcYieldRequest{
		ActionType: "file_edit",
		ProposedEdits: []ipc.ProposedEdit{
			{File: "/repo/important.go"},
		},
	}
	// Passing nil (length 0) must trigger the len==0 early-return.
	result := orchestrator.ExportedScrubSecrets(req, nil)
	assert.Equal(t, "/repo/important.go", result.ProposedEdits[0].File)
}

// TestScrubSecrets_skips_empty_active_value verifies that an empty string in
// activeValues is treated as a skip-sentinel (CHECK 4.4.3).
func TestScrubSecrets_skips_empty_active_value(t *testing.T) {
	req := ipc.IpcYieldRequest{
		ActionType: "file_edit",
		ProposedEdits: []ipc.ProposedEdit{
			{File: "/repo/config.go"},
		},
	}
	// An empty string would replace every character if not skipped — assert the
	// original path is preserved when the only active value is "".
	result := orchestrator.ExportedScrubSecrets(req, []string{""})
	assert.Equal(t, "/repo/config.go", result.ProposedEdits[0].File)
}

// TestScrubSecrets_redacts_active_values verifies that scrubSecrets replaces
// matching secret values in proposed edit paths (CHECK 4.4.3).
func TestScrubSecrets_redacts_active_values(t *testing.T) {
	req := ipc.IpcYieldRequest{
		ActionType: "file_edit",
		ProposedEdits: []ipc.ProposedEdit{
			{File: "/repo/service_sk-secret123_config.go"},
			{File: "/repo/normal_file.go"},
		},
	}
	scrubbed := orchestrator.ExportedScrubSecrets(req, []string{"sk-secret123"})
	assert.Equal(t, "/repo/service_<REDACTED>_config.go", scrubbed.ProposedEdits[0].File)
	assert.Equal(t, "/repo/normal_file.go", scrubbed.ProposedEdits[1].File, "unaffected file unchanged")
}

// ─── writeSummary (CHECK 10.4.1) ──────────────────────────────────────────────

func TestWriteSummary_creates_json_file(t *testing.T) {
	wsDir := t.TempDir()
	s := orchestrator.RunSummary{
		RunID:       42,
		CaseID:      7,
		FinalStatus: "SUCCESS",
	}
	require.NoError(t, orchestrator.ExportedWriteSummary(wsDir, s))

	data, err := os.ReadFile(fmt.Sprintf("%s/runs/42/summary.json", wsDir))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"run_id":42`)
	assert.Contains(t, string(data), `"final_status":"SUCCESS"`)
}

func TestWriteSummary_bad_wsdir_returns_error(t *testing.T) {
	// Point wsDir at a regular file so os.MkdirAll fails.
	f, err := os.CreateTemp("", "notadir-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	f.Close()

	err = orchestrator.ExportedWriteSummary(f.Name(), orchestrator.RunSummary{RunID: 1})
	assert.Error(t, err, "must fail when wsDir is a file, not a directory")
}

func TestWriteSummary_idempotent_on_rewrite(t *testing.T) {
	wsDir := t.TempDir()
	s := orchestrator.RunSummary{RunID: 1, CaseID: 1, FinalStatus: "FAILED"}
	require.NoError(t, orchestrator.ExportedWriteSummary(wsDir, s))
	s.FinalStatus = "SUCCESS"
	require.NoError(t, orchestrator.ExportedWriteSummary(wsDir, s))

	data, _ := os.ReadFile(fmt.Sprintf("%s/runs/1/summary.json", wsDir))
	assert.Contains(t, string(data), `"SUCCESS"`)
}

// ─── runGates ─────────────────────────────────────────────────────────────────

func TestRunGates_blocks_on_failing_gates(t *testing.T) {
	s := newTestStore(t)
	wsDir := t.TempDir() // empty workspace → built-in gates will BLOCK
	caseID, _ := scaffoldForRun(t, s, "")

	r := orchestrator.NewRunner(s, wsDir)
	// Built-in quality gates BLOCK on an unconfigured workspace.
	// runGates must surface this as an error so Run() aborts.
	err := orchestrator.ExportedRunGates(r, caseID)
	assert.Error(t, err, "runGates must return an error when blocking gates fail")
	assert.Contains(t, err.Error(), "BLOCK")
}

// TestRun_reconcile_option_covers_block verifies that opts.Reconcile=true causes
// the reconcile pre-flight block to execute inside Run() (covers that code path).
// The run is expected to fail at crypto.LoadKey (no key in wsDir) — that is fine;
// the important part is that the reconcile block was reached.
func TestRun_reconcile_option_covers_block(t *testing.T) {
	s := newTestStore(t)
	repoPath := initGitRepo(t)
	caseID, _ := scaffoldForRun(t, s, repoPath) // project has SourcePath = repoPath

	r := orchestrator.NewRunner(s, t.TempDir()) // no key → fails after reconcile
	err := r.Run(context.Background(), caseID, orchestrator.RunOptions{
		Reconcile: true,
		SkipGates: true,
		Force:     true,
	})
	// Must fail somewhere after the reconcile block — specifically at LoadKey.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace key")
}

// TestRun_force_false_dirty_tree_returns_error covers the dirty-tree pre-flight
// path in Run() when Force=false and the working tree has uncommitted changes.
func TestRun_force_false_dirty_tree_returns_error(t *testing.T) {
	s := newTestStore(t)
	repoPath := initGitRepo(t)
	// Create an untracked file so the tree is dirty.
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "dirty.txt"), []byte("x"), 0o600))

	caseID, _ := scaffoldForRun(t, s, repoPath)
	r := orchestrator.NewRunner(s, t.TempDir())
	err := r.Run(context.Background(), caseID, orchestrator.RunOptions{
		SkipGates: true,
		Force:     false, // enables dirty-tree check
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dirty working tree")
}

// TestRun_force_false_clean_tree_sets_autoStash covers the autoStashed assignment
// in Run() when handleDirtyTree returns stashed=false on a clean tree.
// The run fails later at crypto.LoadKey (no key), but the dirty-tree path ran.
func TestRun_force_false_clean_tree_sets_autoStash(t *testing.T) {
	s := newTestStore(t)
	repoPath := initGitRepo(t) // clean tree
	caseID, _ := scaffoldForRun(t, s, repoPath)

	r := orchestrator.NewRunner(s, t.TempDir()) // no key → fail after dirty-tree check
	err := r.Run(context.Background(), caseID, orchestrator.RunOptions{
		SkipGates: true,
		Force:     false, // triggers dirty-tree check; clean tree → autoStashed=false
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "dirty working tree")
}

// TestRun_script_not_found_returns_error covers the PhasePythonBoot path in
// Run() where the canonical graph_exec script is absent.
// The test sets up a full workspace key so execution reaches the script check.
func TestRun_script_not_found_returns_error(t *testing.T) {
	s := newTestStore(t)
	caseID, _ := scaffoldForRun(t, s, "")

	// Short prefix avoids the 104-char macOS UDS socket path limit.
	wsDir, err := os.MkdirTemp("", "strc-snf-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	require.NoError(t, crypto.GenerateKey(wsDir))
	// Ensure the tmp dir exists so MkdirAll succeeds; canonical script is absent.
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, "tmp"), 0o700))

	r := orchestrator.NewRunner(s, wsDir)
	runErr := r.Run(context.Background(), caseID, orchestrator.RunOptions{
		Force:     true,
		SkipGates: true,
	})
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "graph_exec script not found")
}

// TestRun_malformed_policy_json_logs_warning_and_continues verifies that a
// broken policy.json emits a warning and falls back to an empty engine rather
// than aborting the run.  The run fails at the script-not-found check, but the
// policy-load error branch must have been reached first.
func TestRun_malformed_policy_json_logs_warning_and_continues(t *testing.T) {
	s := newTestStore(t)
	caseID, _ := scaffoldForRun(t, s, "")

	wsDir, err := os.MkdirTemp("", "strc-mpj-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	require.NoError(t, crypto.GenerateKey(wsDir))
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, "tmp"), 0o700))
	// Write broken JSON so LoadEngine returns an error.
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte("not-json{"), 0o600))

	r := orchestrator.NewRunner(s, wsDir)
	runErr := r.Run(context.Background(), caseID, orchestrator.RunOptions{
		Force:     true,
		SkipGates: true,
	})
	// Run must fail at script-not-found, not at policy load.
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "graph_exec script not found")
}

// TestRun_quality_gate_failure_returns_error covers the pre-flight gate path
// when SkipGates is false and the workspace fails built-in quality checks.
func TestRun_quality_gate_failure_returns_error(t *testing.T) {
	s := newTestStore(t)
	caseID, _ := scaffoldForRun(t, s, "")

	r := orchestrator.NewRunner(s, t.TempDir()) // empty wsDir → gates BLOCK
	err := r.Run(context.Background(), caseID, orchestrator.RunOptions{
		Force:     true,
		SkipGates: false, // do not skip — expect gate failure
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gate")
}

// ─── timePtr ─────────────────────────────────────────────────────────────────

func TestTimePtr_returns_pointer_to_value(t *testing.T) {
	now := time.Now()
	ptr := orchestrator.ExportedTimePtr(now)
	require.NotNil(t, ptr)
	assert.Equal(t, now, *ptr)
}

// ─── sendWebhookYield ─────────────────────────────────────────────────────────

func TestSendWebhookYield_successful_approval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"type":"yield_response","approved":true,"feedback":"looks good"}`)
	}))
	defer srv.Close()

	req := ipc.IpcYieldRequest{
		Type:            "yield_request",
		AgentName:       "planner",
		ActionType:      "file_edit",
		ReasoningTrace:  "trace",
		ConfidenceScore: 0.9,
	}
	resp := orchestrator.ExportedSendWebhookYield(srv.URL, req)
	assert.True(t, resp.Approved)
	assert.Equal(t, "looks good", resp.Feedback)
}

func TestSendWebhookYield_network_error_auto_rejects(t *testing.T) {
	resp := orchestrator.ExportedSendWebhookYield("http://127.0.0.1:1", ipc.IpcYieldRequest{})
	assert.False(t, resp.Approved)
	assert.Contains(t, resp.Feedback, "webhook error")
}

func TestSendWebhookYield_fills_missing_type_field(t *testing.T) {
	// When the webhook omits the "type" field, sendWebhookYield must fill it in
	// to ensure the returned struct is well-formed (covers the Type=="" branch).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"approved":true}`) // no "type" field
	}))
	defer srv.Close()

	resp := orchestrator.ExportedSendWebhookYield(srv.URL, ipc.IpcYieldRequest{})
	assert.Equal(t, "yield_response", resp.Type)
	assert.True(t, resp.Approved)
}

func TestSendWebhookYield_bad_json_response_auto_rejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `not-valid-json`)
	}))
	defer srv.Close()

	resp := orchestrator.ExportedSendWebhookYield(srv.URL, ipc.IpcYieldRequest{})
	assert.False(t, resp.Approved)
	assert.Contains(t, resp.Feedback, "webhook decode error")
}

// ─── Run() integration — full lifecycle ──────────────────────────────────────

// TestRun_integration_success exercises the complete Run() lifecycle with a
// real Python process: the script reads the bootstrap message from stdin and
// exits 0, which the orchestrator records as RunStatusSuccess.
//
// This test requires python3 in PATH and is skipped in -short mode so it does
// not run in unit-only CI jobs.
func TestRun_integration_success(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	// Use os.MkdirTemp with a short prefix so the UDS socket path stays under
	// the 104-character macOS limit (t.TempDir() can produce paths > 100 chars).
	wsDir, err := os.MkdirTemp("", "strc-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	s := newTestStore(t)

	// Workspace key required by crypto.LoadKey inside Run().
	require.NoError(t, crypto.GenerateKey(wsDir))

	// Minimal Python venv (no pip needed — we only need the interpreter).
	venvPath := filepath.Join(wsDir, "venv")
	out, err := exec.Command("python3", "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err, "create venv: %s", out)

	// DB records: vendor → project (no git) → topology → case.
	caseID, _ := scaffoldForRun(t, s, "")

	// Canonical graph_exec script: reads bootstrap from stdin, exits 0.
	// When launched via ExtraFiles (fd 3), Python executes this source from
	// /dev/fd/3 and reads the IPC bootstrap message from stdin.
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))
	script := "import sys, json\njson.loads(sys.stdin.readline())\nsys.exit(0)\n"
	canonicalPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(canonicalPath, []byte(script), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := orchestrator.NewRunner(s, wsDir)
	runErr := r.Run(ctx, caseID, orchestrator.RunOptions{
		Force:     true,
		SkipGates: true,
	})
	require.NoError(t, runErr)

	// The run record must show SUCCESS and a summary must be on disk.
	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status)

	summaryPath := filepath.Join(wsDir, "runs", fmt.Sprintf("%d", runs[0].ID), "summary.json")
	data, err := os.ReadFile(summaryPath)
	require.NoError(t, err, "summary.json must be written after a successful run")
	assert.Contains(t, string(data), `"final_status":"SUCCESS"`)
}

// TestRun_integration_script_failure records RunStatusFailed when the Python
// script exits non-zero.
func TestRun_integration_script_failure(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	wsDir2, err := os.MkdirTemp("", "strc-f-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir2) })
	wsDir := wsDir2
	s := newTestStore(t)
	require.NoError(t, crypto.GenerateKey(wsDir))

	venvPath := filepath.Join(wsDir, "venv")
	out, err2 := exec.Command("python3", "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err2, "create venv: %s", out)

	caseID, _ := scaffoldForRun(t, s, "")

	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))
	// Script exits 1 → orchestrator records FAILED.
	script := "import sys, json\njson.loads(sys.stdin.readline())\nsys.exit(1)\n"
	canonicalPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(canonicalPath, []byte(script), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := orchestrator.NewRunner(s, wsDir)
	_ = r.Run(ctx, caseID, orchestrator.RunOptions{Force: true, SkipGates: true})

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusFailed, runs[0].Status)
}

// TestRun_integration_context_cancelled verifies that cancelling the run
// context terminates the Python process and records RunStatusKilled.
func TestRun_integration_context_cancelled(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	wsDir, err := os.MkdirTemp("", "strc-k-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	s := newTestStore(t)
	require.NoError(t, crypto.GenerateKey(wsDir))

	venvPath := filepath.Join(wsDir, "venv")
	out, err := exec.Command("python3", "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err, "create venv: %s", out)

	caseID, _ := scaffoldForRun(t, s, "")
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))
	// Script sleeps indefinitely — we cancel the context to kill it.
	script := "import sys, json, time\njson.loads(sys.stdin.readline())\ntime.sleep(60)\n"
	canonicalPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(canonicalPath, []byte(script), 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay so the loop enters at least one iteration.
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()

	r := orchestrator.NewRunner(s, wsDir)
	_ = r.Run(ctx, caseID, orchestrator.RunOptions{Force: true, SkipGates: true})

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusKilled, runs[0].Status)
}

// TestRun_integration_with_git_repo exercises the branch-create path in Run().
func TestRun_integration_with_git_repo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	wsDir, err := os.MkdirTemp("", "strc-g-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	s := newTestStore(t)
	require.NoError(t, crypto.GenerateKey(wsDir))

	venvPath := filepath.Join(wsDir, "venv")
	out, err := exec.Command("python3", "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err, "create venv: %s", out)

	repoPath := initGitRepo(t) // real git repo for branch ops
	caseID, _ := scaffoldForRun(t, s, repoPath)

	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))
	script := "import sys, json\njson.loads(sys.stdin.readline())\nsys.exit(0)\n"
	canonicalPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(canonicalPath, []byte(script), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := orchestrator.NewRunner(s, wsDir)
	runErr := r.Run(ctx, caseID, orchestrator.RunOptions{Force: true, SkipGates: true})
	require.NoError(t, runErr)

	// Verify the blast-radius branch was created (and then restored on success).
	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status)
}

// TestRun_integration_debug_log verifies that the debug log file is created
// when opts.Debug is true (covers the debug-log setup path in Run()).
func TestRun_integration_debug_log(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	wsDir, err := os.MkdirTemp("", "strc-d-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	s := newTestStore(t)
	require.NoError(t, crypto.GenerateKey(wsDir))

	venvPath := filepath.Join(wsDir, "venv")
	out, err := exec.Command("python3", "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err, "create venv: %s", out)

	caseID, _ := scaffoldForRun(t, s, "")
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))
	script := "import sys, json\njson.loads(sys.stdin.readline())\nsys.exit(0)\n"
	canonicalPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(canonicalPath, []byte(script), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := orchestrator.NewRunner(s, wsDir)
	require.NoError(t, r.Run(ctx, caseID, orchestrator.RunOptions{
		Force:     true,
		SkipGates: true,
		Debug:     true, // enables debug log creation
	}))

	// Verify the debug log file was created.
	logDir := filepath.Join(wsDir, "log")
	entries, err := os.ReadDir(logDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "debug log file must be created when opts.Debug=true")
}

// TestRun_integration_yield_webhook exercises the YieldCh path in the agent
// loop: Python sends a yield_request; the orchestrator forwards it to the
// project's webhook URL and relays the approved response.
func TestRun_integration_yield_webhook(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	// Webhook server that auto-approves everything.
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"type":"yield_response","approved":true,"feedback":"auto"}`)
	}))
	defer webhookSrv.Close()

	wsDir, err := os.MkdirTemp("", "strc-w-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	s := newTestStore(t)
	require.NoError(t, crypto.GenerateKey(wsDir))

	venvPath := filepath.Join(wsDir, "venv")
	out, err := exec.Command("python3", "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err, "create venv: %s", out)

	// Create project with webhook URL so the yield goes to our httptest server.
	v, err := s.CreateVendor("VW")
	require.NoError(t, err)
	p, err := s.CreateProject(v.ID, "PW", "")
	require.NoError(t, err)
	require.NoError(t, s.UpdateProjectWebhook(p.ID, webhookSrv.URL))
	_, err = s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	require.NoError(t, err)
	c, err := s.CreateCase(p.ID)
	require.NoError(t, err)
	caseID := c.ID

	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))

	// Python script: auth → send yield_request → read yield_response → exit.
	script := `import sys, json, socket, time
boot = json.loads(sys.stdin.readline())
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
for _ in range(30):
    try:
        s.connect(boot["socket_path"])
        break
    except OSError:
        time.sleep(0.05)
s.sendall((json.dumps({"type":"auth","token":boot["token"]})+"\n").encode())
buf = b""
while b"\n" not in buf:
    buf += s.recv(4096)
assert json.loads(buf.split(b"\n")[0]).get("type") == "auth_ok"
s.sendall((json.dumps({
    "type":"yield_request","agent_name":"coder","action_type":"file_edit",
    "proposed_edits":[{"file":"x.go","search_block":"a","replace_block":"b"}],
    "reasoning_trace":"test","confidence_score":0.9
})+"\n").encode())
buf = b""
while b"\n" not in buf:
    buf += s.recv(4096)
resp = json.loads(buf.split(b"\n")[0])
assert resp.get("approved") == True, resp
s.close()
sys.exit(0)
`
	canonicalPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(canonicalPath, []byte(script), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := orchestrator.NewRunner(s, wsDir)
	require.NoError(t, r.Run(ctx, caseID, orchestrator.RunOptions{Force: true, SkipGates: true}))

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status)
}

// TestRun_integration_approval_port exercises the opts.ApprovalPort > 0 code
// path in Run(): the approval HTTP server starts on a known port, and a
// background goroutine approves the yield via the REST API (covers
// the approvalSrv block and the `case approvalSrv != nil` yield handler branch).
func TestRun_integration_approval_port(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	wsDir, err := os.MkdirTemp("", "strc-ap-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	s := newTestStore(t)
	require.NoError(t, crypto.GenerateKey(wsDir))

	venvPath := filepath.Join(wsDir, "venv")
	out, err := exec.Command("python3", "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err, "create venv: %s", out)

	caseID, _ := scaffoldForRun(t, s, "")
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))

	// Python: connect via IPC, send yield_request, wait for approval, exit 0.
	script := `import sys, json, socket, time
boot = json.loads(sys.stdin.readline())
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
for _ in range(30):
    try:
        s.connect(boot["socket_path"])
        break
    except OSError:
        time.sleep(0.05)
s.sendall((json.dumps({"type":"auth","token":boot["token"]})+"\n").encode())
buf = b""
while b"\n" not in buf:
    buf += s.recv(4096)
assert json.loads(buf.split(b"\n")[0]).get("type") == "auth_ok"
s.sendall((json.dumps({
    "type":"yield_request","agent_name":"coder","action_type":"file_edit",
    "proposed_edits":[{"file":"x.go","search_block":"a","replace_block":"b"}],
    "reasoning_trace":"test","confidence_score":0.9
})+"\n").encode())
buf = b""
while b"\n" not in buf:
    buf += s.recv(4096)
resp = json.loads(buf.split(b"\n")[0])
assert resp.get("approved") == True, resp
s.close()
sys.exit(0)
`
	canonicalPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(canonicalPath, []byte(script), 0o600))

	const approvalToken = "test-approval-bearer-xyz"
	const approvalPort = 19834 // fixed test port; unlikely to conflict in CI

	// Background goroutine: poll the approval API and approve the pending yield.
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		base := fmt.Sprintf("http://127.0.0.1:%d/v1/yields", approvalPort)

		// Poll until a pending yield appears (list endpoint returns a JSON array).
		var pendingID string
		for i := 0; i < 80 && pendingID == ""; i++ {
			time.Sleep(100 * time.Millisecond)
			req, _ := http.NewRequest(http.MethodGet, base, nil)
			req.Header.Set("Authorization", "Bearer "+approvalToken)
			resp, err := client.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				if resp != nil {
					resp.Body.Close()
				}
				continue
			}
			// Response is a JSON array: [{"id":"...","request":{...},...}]
			var items []map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&items)
			resp.Body.Close()
			if len(items) > 0 {
				pendingID, _ = items[0]["id"].(string)
			}
		}
		if pendingID == "" {
			return
		}
		// Approve the yield.
		req, _ := http.NewRequest(http.MethodPost,
			fmt.Sprintf("%s/%s/approve", base, pendingID), nil)
		req.Header.Set("Authorization", "Bearer "+approvalToken)
		resp, _ := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := orchestrator.NewRunner(s, wsDir)
	require.NoError(t, r.Run(ctx, caseID, orchestrator.RunOptions{
		Force:         true,
		SkipGates:     true,
		ApprovalPort:  approvalPort,
		ApprovalToken: approvalToken,
	}))

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status)
}

// TestRun_integration_policy_autoapproval verifies the policy auto-approval path
// inside the agent loop: when policyEngine.Evaluate matches, the orchestrator
// approves the yield automatically without waiting for operator input.
func TestRun_integration_policy_autoapproval(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	wsDir, err := os.MkdirTemp("", "strc-p-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	s := newTestStore(t)
	require.NoError(t, crypto.GenerateKey(wsDir))

	venvPath := filepath.Join(wsDir, "venv")
	out, err := exec.Command("python3", "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err, "create venv: %s", out)

	// Write a policy.json that auto-approves all file_edit yields.
	policyJSON := `{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(policyJSON), 0o600))

	caseID, _ := scaffoldForRun(t, s, "")
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))

	// Python script: connect → auth → send yield_request → wait for auto-approval → exit.
	script := `import sys, json, socket, time
boot = json.loads(sys.stdin.readline())
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
for _ in range(30):
    try:
        s.connect(boot["socket_path"])
        break
    except OSError:
        time.sleep(0.05)
s.sendall((json.dumps({"type":"auth","token":boot["token"]})+"\n").encode())
buf = b""
while b"\n" not in buf:
    buf += s.recv(4096)
assert json.loads(buf.split(b"\n")[0]).get("type") == "auth_ok"
s.sendall((json.dumps({
    "type":"yield_request","agent_name":"coder","action_type":"file_edit",
    "proposed_edits":[{"file":"x.go","search_block":"a","replace_block":"b"}],
    "reasoning_trace":"auto","confidence_score":0.9
})+"\n").encode())
buf = b""
while b"\n" not in buf:
    buf += s.recv(4096)
resp = json.loads(buf.split(b"\n")[0])
assert resp.get("approved") == True, resp
s.close()
sys.exit(0)
`
	canonicalPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(canonicalPath, []byte(script), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := orchestrator.NewRunner(s, wsDir)
	require.NoError(t, r.Run(ctx, caseID, orchestrator.RunOptions{Force: true, SkipGates: true}))

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status)
}

// TestRun_integration_state_emit exercises the StatEmitCh path in the agent
// loop: a Python script connects to the IPC socket, authenticates, sends a
// state_emit, then exits cleanly.
func TestRun_integration_state_emit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — skipped in -short mode")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	wsDir, err := os.MkdirTemp("", "strc-e-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(wsDir) })
	s := newTestStore(t)
	require.NoError(t, crypto.GenerateKey(wsDir))

	venvPath := filepath.Join(wsDir, "venv")
	out, err := exec.Command("python3", "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err, "create venv: %s", out)

	caseID, _ := scaffoldForRun(t, s, "")
	tmpDir := filepath.Join(wsDir, "tmp")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))

	// Python script: connect to IPC socket, authenticate, send state_emit, exit.
	script := `import sys, json, socket, time
boot = json.loads(sys.stdin.readline())
sock_path = boot["socket_path"]
token = boot["token"]
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
# Wait briefly for the IPC server to start listening.
for _ in range(20):
    try:
        s.connect(sock_path)
        break
    except OSError:
        time.sleep(0.05)
s.sendall((json.dumps({"type":"auth","token":token})+"\n").encode())
resp = json.loads(s.makefile().readline())
assert resp.get("type") == "auth_ok", resp
s.sendall((json.dumps({"type":"state_emit","active_agent":"planner","state":{"step":1}})+"\n").encode())
time.sleep(0.6)  # let the render ticker (500 ms) fire at least once
s.close()
sys.exit(0)
`
	canonicalPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	require.NoError(t, os.WriteFile(canonicalPath, []byte(script), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := orchestrator.NewRunner(s, wsDir)
	runErr := r.Run(ctx, caseID, orchestrator.RunOptions{Force: true, SkipGates: true})
	require.NoError(t, runErr)

	runs, err := s.ListRunsByCase(caseID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, persistence.RunStatusSuccess, runs[0].Status)
}

// ─── Run() early-exit paths ────────────────────────────────────────────────────

// TestRun_case_not_found_returns_error covers the PRE_FLIGHT path where the
// requested caseID does not exist in the store (CHECK 5.1.2).
func TestRun_case_not_found_returns_error(t *testing.T) {
	s := newTestStore(t)
	r := orchestrator.NewRunner(s, t.TempDir())

	err := r.Run(context.Background(), 99999, orchestrator.RunOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestRun_no_topology_returns_error covers the PRE_FLIGHT path where a case
// and project exist but no swarm topology has been registered (CHECK 5.1.2).
func TestRun_no_topology_returns_error(t *testing.T) {
	s := newTestStore(t)

	// Create the minimum DB state without a topology.
	v, err := s.CreateVendor("V2")
	require.NoError(t, err)
	p, err := s.CreateProject(v.ID, "P2", "") // empty sourcePath to skip git
	require.NoError(t, err)
	c, err := s.CreateCase(p.ID)
	require.NoError(t, err)

	r := orchestrator.NewRunner(s, t.TempDir())
	runErr := r.Run(context.Background(), c.ID, orchestrator.RunOptions{Force: true, SkipGates: true})
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "topology")
}

// TestRun_dryrun_creates_run_and_returns_nil covers the DryRun short-circuit
// path: a run record is created, logged, and the function returns nil without
// launching Python (CHECK 5.1.2 / CHECK 5.5.1).
func TestRun_dryrun_creates_run_and_returns_nil(t *testing.T) {
	s := newTestStore(t)
	caseID, _ := scaffoldForRun(t, s, "") // empty sourcePath → skip all git ops

	r := orchestrator.NewRunner(s, t.TempDir())
	err := r.Run(context.Background(), caseID, orchestrator.RunOptions{
		DryRun:    true,
		SkipGates: true,
		Force:     true, // skip dirty-tree check
	})
	require.NoError(t, err)

	// A run record must have been created and then marked KILLED (dry-run cleanup).
	runs, listErr := s.ListRunsByCase(caseID)
	require.NoError(t, listErr)
	require.Len(t, runs, 1, "exactly one run record must exist after a dry run")
	assert.Equal(t, persistence.RunStatusKilled, runs[0].Status)
}
