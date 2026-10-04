package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cleanAggressive bool
	cleanDryRun     bool
	cleanKeepFailed bool

	// cleanKeepEventRows is how many audit event rows clean --aggressive keeps.
	cleanKeepEventRows int64 = 1_000_000
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove leftovers of older staircase versions from tmp/",
	Long: `staircase clean removes what older staircase versions left in tmp/: compiled
Python scripts (graph_exec_*) and stale IPC sockets. Compiled plans are kept.

--aggressive also removes the Python venv older versions installed and
orphaned staircase/run-* git branches older than 30 days.

--keep-failed preserves branches and logs for FAILED runs (forensic mode).

Aggressive cleaning never throws evidence away silently: a run branch that is
not merged into another branch is kept (it holds the only copy of the commit),
audit rows and flagged cases are written to archive/ before they are deleted (and
not deleted if that fails), and a file named legal-hold in the workspace stops
every such deletion.

--dry-run prints what would be removed without deleting anything.`,
	RunE: cleanHandler,
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanAggressive, "aggressive", false, "Also remove the old Python venv and stale git branches")
	cleanCmd.Flags().BoolVar(&cleanDryRun, "dry-run", false, "Print targets without deleting")
	cleanCmd.Flags().BoolVar(&cleanKeepFailed, "keep-failed", false, "Preserve branches/logs for FAILED runs")
	rootCmd.AddCommand(cleanCmd)
}

func cleanHandler(_ *cobra.Command, _ []string) error {
	wsDir := viper.GetString("STAIRCASE_DIR")
	tmpDir := filepath.Join(wsDir, "tmp")

	if cleanDryRun {
		fmt.Println("🔍 Dry-run mode - nothing will be deleted.")
	}

	// ── 1. Tmp: Python-era scripts and their sidecars, IPC sockets ────────────
	removed := 0
	for _, pat := range []string{"graph_exec_*", "*.sock"} {
		matches, _ := filepath.Glob(filepath.Join(tmpDir, pat))
		for _, m := range matches {
			removeTarget(m)
			removed++
		}
	}
	if removed == 0 {
		fmt.Println("   ✓ tmp/ already clean.")
	}

	if !cleanAggressive {
		return nil
	}

	fmt.Println("   🔬 Aggressive GC...")

	// ── 2. Preserve set: FAILED run IDs if --keep-failed ──────────────────────
	preservedRunIDs := map[int64]bool{}
	if cleanKeepFailed {
		db, err := persistence.InitDB(wsDir)
		if err == nil {
			store := persistence.NewStore(db)
			failedRuns, _ := store.ListRunsByStatus(persistence.RunStatusFailed)
			for _, r := range failedRuns {
				preservedRunIDs[r.ID] = true
			}
			_ = db.Close()
			fmt.Printf("   🔒 Preserving %d FAILED run(s) (--keep-failed).\n", len(preservedRunIDs))
		}
	}

	// ── 3. Remove the Python venv older versions installed ───────────────────
	venvPath := filepath.Join(wsDir, "venv")
	if info, err := os.Lstat(venvPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// Never follow a symlink - it could point outside the workspace.
			fmt.Printf("   ⚠️  Skipping venv removal: %s is a symlink.\n", venvPath)
		} else {
			removeTarget(venvPath)
		}
	}

	// ── 4. Prune stale staircase/run-* branches in project repos ──────────────
	if _, err := os.Stat(filepath.Join(wsDir, legalHoldFile)); err == nil {
		fmt.Printf("   🔒 Legal hold in effect (%s): no audit rows, cases or run branches are deleted. Remove the file to allow it.\n",
			filepath.Join(wsDir, legalHoldFile))
		return nil
	}
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	// Audit rows are evidence: archive what is about to go, then delete it.
	if !cleanDryRun {
		purgeAudit(store, wsDir)
	}

	sourcePaths, err := collectSourcePaths(store)
	if err != nil {
		return fmt.Errorf("collect source paths: %w", err)
	}

	cutoff := time.Now().AddDate(0, 0, -30)
	for _, repoPath := range sourcePaths {
		if err := pruneRepoBranches(repoPath, wsDir, cutoff, preservedRunIDs); err != nil {
			fmt.Printf("   ⚠️  %s: %v\n", repoPath, err)
		}
	}

	return nil
}

// legalHoldFile, present in the workspace, stops clean from deleting anything
// that is evidence: audit rows, cases and run branches.
const legalHoldFile = "legal-hold"

// archiveLine is one record of an archive: a case, a run or an event-log row.
type archiveLine struct {
	Kind string `json:"kind"`
	Data any    `json:"data"`
}

// purgeAudit deletes the cases flagged for deletion and the event-log rows of
// the oldest whole runs beyond cleanKeepEventRows (a run's chain is never cut
// in the middle), after writing them all to one file under
// archive/. If the archive cannot be written, nothing is deleted.
func purgeAudit(store *persistence.Store, wsDir string) {
	var lines []archiveLine
	seen := map[int64]bool{}
	flagged, err := store.ListFlaggedCases()
	if err != nil {
		fmt.Printf("   ⚠️  Listing flagged cases: %v\n", err)
		return
	}
	for _, c := range flagged {
		lines = append(lines, archiveLine{"case", c})
		runs, err := store.ListRunsByCase(c.ID)
		if err != nil {
			fmt.Printf("   ⚠️  Archiving case %d: %v\n", c.ID, err)
			return
		}
		for _, r := range runs {
			lines = append(lines, archiveLine{"run", r})
			events, err := store.ListEventLogs(r.ID)
			if err != nil {
				fmt.Printf("   ⚠️  Archiving run %d: %v\n", r.ID, err)
				return
			}
			for _, e := range events {
				seen[e.ID] = true
				lines = append(lines, archiveLine{"event", e})
			}
		}
	}
	old, err := store.PrunableEventLogs(cleanKeepEventRows) // whole runs only
	if err != nil {
		fmt.Printf("   ⚠️  Listing old event log rows: %v\n", err)
		return
	}
	for _, e := range old {
		if !seen[e.ID] {
			lines = append(lines, archiveLine{"event", e})
		}
	}
	if len(lines) == 0 {
		return
	}
	path, err := writeArchive(wsDir, lines)
	if err != nil {
		fmt.Printf("   ⚠️  The audit rows could not be archived, so none were deleted: %v\n", err)
		return
	}
	fmt.Printf("   📦 Archived %d record(s) to %s before deleting them.\n", len(lines), path)
	if n, err := store.DeleteFlaggedCases(); err != nil {
		fmt.Printf("   ⚠️  DeleteFlaggedCases: %v\n", err)
	} else if n > 0 {
		fmt.Printf("   🗑  Purged %d flagged case(s).\n", n)
	}
	if len(old) > 0 {
		var runIDs []int64
		for _, e := range old {
			if !slices.Contains(runIDs, e.RunID) {
				runIDs = append(runIDs, e.RunID)
			}
		}
		if n, err := store.PruneEventLogsOfRuns(runIDs); err != nil {
			fmt.Printf("   ⚠️  PruneEventLogs: %v\n", err)
		} else if n > 0 {
			fmt.Printf("   🗑  Pruned %d old event log row(s) of %d run(s).\n", n, len(runIDs))
		}
	}
}

// writeArchive writes lines as JSON Lines to archive/clean-<time>.jsonl (0600),
// flushed to disk, and returns its path.
func writeArchive(wsDir string, lines []archiveLine) (string, error) {
	dir := filepath.Join(wsDir, "archive")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".clean-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(f.Name()) }() // gone after the rename
	enc := json.NewEncoder(f)
	for _, l := range lines {
		if err := enc.Encode(l); err != nil {
			_ = f.Close()
			return "", err
		}
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	final := filepath.Join(dir, "clean-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".jsonl")
	if err := os.Rename(f.Name(), final); err != nil {
		return "", err
	}
	if d, err := os.Open(dir); err == nil { // make the new name durable too
		_ = d.Sync()
		_ = d.Close()
	}
	return final, nil
}

// delivered reports whether branch's tip is reachable from another branch
// (local or remote-tracking): then the branch is only a pointer to work that
// lives elsewhere. A run branch that is merged nowhere holds the only copy.
func delivered(repoPath, branch string) bool {
	out, err := exec.Command("git", "-C", repoPath, "for-each-ref", "--contains", branch,
		"--format=%(refname)", "refs/heads", "refs/remotes").Output()
	if err != nil {
		return false
	}
	for _, ref := range strings.Fields(string(out)) {
		if ref != "refs/heads/"+branch && !strings.HasPrefix(ref, "refs/heads/staircase/run-") {
			return true
		}
	}
	return false
}

// collectSourcePaths returns the source_path of every project that has one.
func collectSourcePaths(store *persistence.Store) ([]string, error) {
	all, err := store.ListAllProjects()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range all {
		if p.SourcePath != "" {
			paths = append(paths, p.SourcePath)
		}
	}
	return paths, nil
}

// pruneRepoBranches deletes staircase/run-* branches in repoPath that are
// older than cutoff and not in the preserve set, removing a run's kept
// worktree first (git refuses to delete a branch checked out in a worktree).
func pruneRepoBranches(repoPath, wsDir string, cutoff time.Time, preserve map[int64]bool) error {
	out, err := exec.Command(
		"git", "-C", repoPath,
		"for-each-ref", "--format=%(refname:short) %(creatordate:iso)", "refs/heads/staircase/run-*",
	).Output()
	if err != nil {
		return nil // not a git repo or no staircase branches
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		branch := parts[0]

		// Parse run ID from branch name "staircase/run-{ID}".
		suffix := strings.TrimPrefix(branch, "staircase/run-")
		runID, err := strconv.ParseInt(suffix, 10, 64)
		if err != nil || preserve[runID] {
			continue
		}

		// Parse branch creation date.
		t, err := time.Parse("2006-01-02 15:04:05 -0700", parts[1])
		if err != nil || t.After(cutoff) {
			continue
		}

		if !delivered(repoPath, branch) {
			fmt.Printf("   🔒 Kept %s: it is not merged anywhere else, so it holds the only copy of the run's commit.\n", branch)
			continue
		}

		wt := filepath.Join(wsDir, "worktrees", "run-"+suffix)
		_, wtErr := os.Stat(wt)
		if cleanDryRun {
			if wtErr == nil {
				fmt.Printf("   [dry-run] would remove worktree: %s\n", wt)
			}
			fmt.Printf("   [dry-run] would delete: %s in %s\n", branch, repoPath)
			continue
		}
		if wtErr == nil {
			if out, err := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", wt).CombinedOutput(); err != nil {
				fmt.Printf("   ⚠️  Remove worktree %s: %v: %s\n", wt, err, strings.TrimSpace(string(out)))
				continue
			}
			fmt.Printf("   🗑  Removed worktree: %s\n", wt)
		}
		if err := exec.Command("git", "-C", repoPath, "branch", "-D", branch).Run(); err != nil {
			fmt.Printf("   ⚠️  Delete %s: %v\n", branch, err)
		} else {
			fmt.Printf("   🗑  Deleted branch: %s\n", branch)
		}
	}
	if !cleanDryRun {
		_ = exec.Command("git", "-C", repoPath, "worktree", "prune").Run() // drop metadata of vanished worktrees
	}
	return nil
}

// removeTarget deletes a path (or logs it in dry-run mode).
func removeTarget(path string) {
	if cleanDryRun {
		fmt.Printf("   [dry-run] would remove: %s\n", path)
		return
	}
	if err := os.RemoveAll(path); err != nil {
		fmt.Printf("   ⚠️  Remove %s: %v\n", filepath.Base(path), err)
	} else {
		fmt.Printf("   🗑  Removed: %s\n", path)
	}
}
