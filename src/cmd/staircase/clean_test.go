package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cleanFlags(t *testing.T, aggressive bool) {
	cleanAggressive, cleanDryRun, cleanKeepFailed = aggressive, false, false
	t.Cleanup(func() {
		cleanAggressive, cleanDryRun, cleanKeepFailed, cleanKeepEventRows = false, false, false, 1_000_000
	})
}

func allCases(t *testing.T, store *persistence.Store) []domain.Case {
	ps, err := store.ListAllProjects()
	require.NoError(t, err)
	var out []domain.Case
	for _, p := range ps {
		cs, err := store.ListCasesByProject(p.ID)
		require.NoError(t, err)
		out = append(out, cs...)
	}
	return out
}

func branchExists(git func(...string) string, branch string) bool {
	return strings.TrimSpace(git("branch", "--list", branch)) != ""
}

// TestClean_keeps_run_work_that_was_not_delivered: an old staircase/run-N
// branch that is not merged anywhere is the only place its commit lives, so
// clean keeps it; once it is merged, the branch is only a pointer and goes (F102).
func TestClean_keeps_run_work_that_was_not_delivered(t *testing.T) {
	repo, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	future := time.Now().Add(time.Hour) // every branch counts as old

	require.NoError(t, pruneRepoBranches(repo, ws, future, nil))
	assert.True(t, branchExists(git, "staircase/run-1"), "unmerged work is kept")

	git("merge", "-q", "--no-ff", "-m", "merge the run", "staircase/run-1")
	require.NoError(t, pruneRepoBranches(repo, ws, future, nil))
	assert.False(t, branchExists(git, "staircase/run-1"), "merged, so only a pointer")
}

// TestClean_archives_audit_rows_before_deleting_them: flagged cases and old
// event-log rows are written to archive/ first; nothing is deleted when the
// archive cannot be written; and a legal hold stops every deletion (F102).
func TestClean_archives_audit_rows_before_deleting_them(t *testing.T) {
	_, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	store, db, err := openStore()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	runs, err := store.ListRunsByStatus("SUCCESS")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	events, err := store.ListEventLogs(runs[0].ID)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	cases := allCases(t, store)
	require.NotEmpty(t, cases)
	require.NoError(t, store.FlagCaseDeleted(cases[0].ID))
	cleanFlags(t, true)

	// A legal hold: nothing is deleted, and it says why.
	hold := filepath.Join(ws, "legal-hold")
	require.NoError(t, os.WriteFile(hold, []byte("matter 42\n"), 0o600))
	out, err := captureStdout(t, func() error { return cleanHandler(nil, nil) })
	require.NoError(t, err)
	assert.Contains(t, out, "Legal hold")
	after, _ := store.ListEventLogs(runs[0].ID)
	assert.Len(t, after, len(events), "audit rows are kept under a legal hold")
	cases = allCases(t, store)
	assert.NotEmpty(t, cases)
	require.NoError(t, os.Remove(hold))

	// An unwritable archive: nothing is deleted.
	require.NoError(t, os.WriteFile(filepath.Join(ws, "archive"), []byte("a file, not a directory"), 0o600))
	_, err = captureStdout(t, func() error { return cleanHandler(nil, nil) })
	require.NoError(t, err)
	after, _ = store.ListEventLogs(runs[0].ID)
	assert.Len(t, after, len(events), "no archive, no deletion")
	require.NoError(t, os.Remove(filepath.Join(ws, "archive")))

	// With an archive, the rows are written first and then removed.
	_, err = captureStdout(t, func() error { return cleanHandler(nil, nil) })
	require.NoError(t, err)
	files, _ := filepath.Glob(filepath.Join(ws, "archive", "*.jsonl"))
	require.Len(t, files, 1)
	b, err := os.ReadFile(files[0])
	require.NoError(t, err)
	assert.Contains(t, string(b), events[0].EventHash, "the event rows are in the archive")
	assert.Contains(t, string(b), `"kind":"case"`)
	fi, _ := os.Stat(files[0])
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	after, _ = store.ListEventLogs(runs[0].ID)
	assert.Empty(t, after, "then the flagged case, its runs and their events are gone")
	_ = git
	viper.Set("STAIRCASE_DIR", ws)
}

// TestClean_archives_pruned_event_rows: rows beyond the keep limit are
// archived before the oldest are pruned.
func TestClean_archives_pruned_event_rows(t *testing.T) {
	_, ws, _ := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	store, db, err := openStore()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	runs, _ := store.ListRunsByStatus("SUCCESS")
	events, _ := store.ListEventLogs(runs[0].ID)
	require.Greater(t, len(events), 2)
	cleanFlags(t, true)
	cleanKeepEventRows = int64(len(events) - 2)

	_, err = captureStdout(t, func() error { return cleanHandler(nil, nil) })
	require.NoError(t, err)
	left, _ := store.ListEventLogs(runs[0].ID)
	assert.Len(t, left, len(events)-2)
	files, _ := filepath.Glob(filepath.Join(ws, "archive", "*.jsonl"))
	require.Len(t, files, 1)
	b, _ := os.ReadFile(files[0])
	assert.Contains(t, string(b), events[0].EventHash)
	assert.Contains(t, string(b), events[1].EventHash)
	assert.NotContains(t, string(b), events[2].EventHash, "only what was pruned")
}

// copyDir copies a directory tree (files only, modes kept), as a backup would.
func copyDir(t *testing.T, from, to string) {
	t.Helper()
	require.NoError(t, filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, info.Mode().Perm())
	}))
}

// TestWorkspace_backup_and_restore: a plain copy of the workspace directory
// taken after a run (the database, its write-ahead log, the keys, audit/) is a
// complete backup: restored elsewhere, the run's audit chain verifies, its
// secrets decrypt and its commit still verifies against the restored key (F104).
func TestWorkspace_backup_and_restore(t *testing.T) {
	_, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	require.NoError(t, setSecretFromStdin(t, "API_KEY", "s3cret"))
	commit := strings.TrimSpace(git("rev-parse", "staircase/run-1"))

	backup := filepath.Join(t.TempDir(), "restored")
	require.NoError(t, os.MkdirAll(backup, 0o700))
	copyDir(t, ws, backup)
	viper.Set("STAIRCASE_DIR", backup)

	restored, rdb, err := openStore()
	require.NoError(t, err)
	defer func() { _ = rdb.Close() }()
	require.NoError(t, restored.VerifyChain(1), "the audit chain survives a copy of the directory")
	key, err := crypto.LoadKey(backup)
	require.NoError(t, err)
	sec, err := restored.GetSecret("API_KEY", nil)
	require.NoError(t, err)
	require.NotNil(t, sec)
	v, err := crypto.Decrypt(key, sec.EncryptedValue)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", v)
	verifyMinCAL, verifyKey, verifyFile, verifyAll, verifyRebuild = 3, "", "", false, false
	t.Cleanup(func() { verifyMinCAL = 0 })
	require.NoError(t, verifyHandler(nil, []string{commit}), "the restored signing key verifies the commit")
	viper.Set("STAIRCASE_DIR", ws)
}

// TestRecover_command: a run that finished has nothing to recover, and the
// command says so.
func TestRecover_command(t *testing.T) {
	sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	assert.ErrorContains(t, recoverCmd.RunE(recoverCmd, []string{"1"}), "already has a commit")
	assert.ErrorContains(t, recoverCmd.RunE(recoverCmd, []string{"999"}), "not found")
	assert.Error(t, recoverCmd.RunE(recoverCmd, []string{"x"}))
}
