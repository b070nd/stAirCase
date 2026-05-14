package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cleanAggressive bool
	cleanDryRun     bool
	cleanKeepFailed bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove tmp files and orphan IPC sockets",
	Long: `staircase clean removes graph_exec scripts and stale IPC sockets from tmp/.

--aggressive also prunes the Python venv and orphaned staircase/run-*
git branches older than 30 days.

--keep-failed preserves branches and logs for FAILED runs (forensic mode).

--dry-run prints what would be removed without deleting anything.`,
	RunE: cleanHandler,
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanAggressive, "aggressive", false, "Also prune venv and stale git branches")
	cleanCmd.Flags().BoolVar(&cleanDryRun, "dry-run", false, "Print targets without deleting")
	cleanCmd.Flags().BoolVar(&cleanKeepFailed, "keep-failed", false, "Preserve branches/logs for FAILED runs")
	rootCmd.AddCommand(cleanCmd)
}

func cleanHandler(_ *cobra.Command, _ []string) error {
	wsDir := viper.GetString("STAIRCASE_DIR")
	tmpDir := filepath.Join(wsDir, "tmp")

	if cleanDryRun {
		fmt.Println("🔍 Dry-run mode — nothing will be deleted.")
	}

	// ── 1. Tmp: graph_exec scripts + IPC sockets ──────────────────────────────
	removed := 0
	for _, pat := range []string{"*.py", "*.sock"} {
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

	// ── 3. Prune stale venv ────────────────────────────────────────────────────
	venvPath := filepath.Join(wsDir, "venv")
	if info, err := os.Lstat(venvPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// Never follow a symlink — it could point outside the workspace.
			fmt.Printf("   ⚠️  Skipping venv removal: %s is a symlink.\n", venvPath)
		} else {
			removeTarget(venvPath)
		}
	}

	// ── 4. Prune stale staircase/run-* branches in project repos ──────────────
	db, err := persistence.InitDB(wsDir)
	if err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	// Purge flagged cases.
	if !cleanDryRun {
		if n, err := store.DeleteFlaggedCases(); err != nil {
			fmt.Printf("   ⚠️  DeleteFlaggedCases: %v\n", err)
		} else if n > 0 {
			fmt.Printf("   🗑  Purged %d flagged case(s).\n", n)
		}
		// Prune event log if > 1M rows.
		if n, err := store.PruneEventLogs(1_000_000); err != nil {
			fmt.Printf("   ⚠️  PruneEventLogs: %v\n", err)
		} else if n > 0 {
			fmt.Printf("   🗑  Pruned %d old event log row(s).\n", n)
		}
	}

	sourcePaths, err := collectSourcePaths(store)
	if err != nil {
		return fmt.Errorf("collect source paths: %w", err)
	}

	cutoff := time.Now().AddDate(0, 0, -30)
	for _, repoPath := range sourcePaths {
		if err := pruneRepoBranches(repoPath, cutoff, preservedRunIDs); err != nil {
			fmt.Printf("   ⚠️  %s: %v\n", repoPath, err)
		}
	}

	return nil
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
// older than cutoff and not in the preserve set.
func pruneRepoBranches(repoPath string, cutoff time.Time, preserve map[int64]bool) error {
	out, err := exec.Command("git", "-C", repoPath,
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

		if cleanDryRun {
			fmt.Printf("   [dry-run] would delete: %s in %s\n", branch, repoPath)
			continue
		}
		if err := exec.Command("git", "-C", repoPath, "branch", "-D", branch).Run(); err != nil {
			fmt.Printf("   ⚠️  Delete %s: %v\n", branch, err)
		} else {
			fmt.Printf("   🗑  Deleted branch: %s\n", branch)
		}
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
