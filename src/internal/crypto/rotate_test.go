package crypto

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupWsDir creates a temp workspace dir with a fresh .key file.
func setupWsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, GenerateKey(dir))
	return dir
}

// noopReencrypt is a reencrypt callback that always succeeds.
func noopReencrypt(_, _ []byte) error { return nil }

// alwaysFalse is a tryDecryptAny callback that always returns false.
func alwaysFalse(_ []byte) bool { return false }

// TestRotateKey_happy_path verifies that a normal rotation replaces the key
// and leaves no journal or temp files behind.
func TestRotateKey_happy_path(t *testing.T) {
	dir := setupWsDir(t)
	oldKey, err := LoadKey(dir)
	require.NoError(t, err)

	require.NoError(t, RotateKey(dir, noopReencrypt, alwaysFalse))

	newKey, err := LoadKey(dir)
	require.NoError(t, err)
	assert.NotEqual(t, oldKey, newKey, "key must have changed")
	assert.Equal(t, 32, len(newKey), "new key must be 32 bytes")

	// No journal or leftover temp files.
	journalPath := filepath.Join(dir, rotateJournalFile)
	_, statErr := os.Stat(journalPath)
	assert.True(t, os.IsNotExist(statErr), "journal file must be cleaned up")

	entries, err := filepath.Glob(filepath.Join(dir, ".key-rotate-*"))
	require.NoError(t, err)
	assert.Empty(t, entries, "no temp key files should remain")
}

// TestRotateKey_resume_committed simulates a crash after the DB committed but
// before the key file was renamed (journal stage = "committed").
// Recovery must install the temp key and clean up.
func TestRotateKey_resume_committed(t *testing.T) {
	dir := setupWsDir(t)

	// Write a new key to a temp file (simulates the state after step 2).
	newKey := make([]byte, 32)
	for i := range newKey {
		newKey[i] = 0xAB
	}
	tmp, err := os.CreateTemp(dir, ".key-rotate-*")
	require.NoError(t, err)
	_, err = tmp.Write(newKey)
	require.NoError(t, err)
	require.NoError(t, tmp.Close())
	tmpPath := tmp.Name()

	// Write journal at "committed" stage.
	journalPath := filepath.Join(dir, rotateJournalFile)
	require.NoError(t, writeRotateJournal(journalPath, rotateJournal{
		Stage:      "committed",
		NewKeyPath: tmpPath,
	}))

	// RotateKey should resume and install the new key.
	require.NoError(t, RotateKey(dir, noopReencrypt, alwaysFalse))

	installed, err := LoadKey(dir)
	require.NoError(t, err)
	assert.Equal(t, newKey, installed, "resumed key must match the temp file")

	// Cleanup.
	_, statErr := os.Stat(journalPath)
	assert.True(t, os.IsNotExist(statErr))
	_, statErr = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(statErr), "temp file must be gone after resume")
}

// TestRotateKey_resume_pending_not_committed simulates a crash after the journal
// was written at "pending" but before the DB committed.
// tryDecryptAny returns false → recovery cleans up and starts a fresh rotation.
func TestRotateKey_resume_pending_not_committed(t *testing.T) {
	dir := setupWsDir(t)
	originalKey, err := LoadKey(dir)
	require.NoError(t, err)

	// Orphaned temp file from the aborted rotation.
	staleKey := make([]byte, 32)
	for i := range staleKey {
		staleKey[i] = 0xDE
	}
	tmp, err := os.CreateTemp(dir, ".key-rotate-*")
	require.NoError(t, err)
	_, err = tmp.Write(staleKey)
	require.NoError(t, err)
	require.NoError(t, tmp.Close())
	tmpPath := tmp.Name()

	journalPath := filepath.Join(dir, rotateJournalFile)
	require.NoError(t, writeRotateJournal(journalPath, rotateJournal{
		Stage:      "pending",
		NewKeyPath: tmpPath,
	}))

	// DB not committed: tryDecryptAny(staleKey) returns false.
	require.NoError(t, RotateKey(dir, noopReencrypt, func(key []byte) bool { return false }))

	// Key must differ from the original (fresh rotation ran successfully).
	newKey, err := LoadKey(dir)
	require.NoError(t, err)
	assert.NotEqual(t, originalKey, newKey, "fresh rotation must produce a new key")
	assert.NotEqual(t, staleKey, newKey, "stale key must not be installed")

	// Stale temp file must be cleaned up.
	_, statErr := os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(statErr), "stale temp file must be removed")
}

// TestRotateKey_resume_pending_already_committed simulates a crash after the DB
// committed but before the journal was advanced to "committed".
// tryDecryptAny(newKey) returns true → recovery installs the new key.
func TestRotateKey_resume_pending_already_committed(t *testing.T) {
	dir := setupWsDir(t)

	committedKey := make([]byte, 32)
	for i := range committedKey {
		committedKey[i] = 0xCC
	}
	tmp, err := os.CreateTemp(dir, ".key-rotate-*")
	require.NoError(t, err)
	_, err = tmp.Write(committedKey)
	require.NoError(t, err)
	require.NoError(t, tmp.Close())
	tmpPath := tmp.Name()

	journalPath := filepath.Join(dir, rotateJournalFile)
	require.NoError(t, writeRotateJournal(journalPath, rotateJournal{
		Stage:      "pending",
		NewKeyPath: tmpPath,
	}))

	// tryDecryptAny returns true for committedKey — DB already committed.
	require.NoError(t, RotateKey(dir, noopReencrypt, func(key []byte) bool {
		return len(key) == 32 && key[0] == 0xCC
	}))

	installed, err := LoadKey(dir)
	require.NoError(t, err)
	assert.Equal(t, committedKey, installed, "committed key must be installed")

	_, statErr := os.Stat(journalPath)
	assert.True(t, os.IsNotExist(statErr))
}

// TestRotateKey_reencrypt_error_cleans_up verifies that when reencrypt fails,
// the temp key file and journal are both removed and the error is propagated.
func TestRotateKey_reencrypt_error_cleans_up(t *testing.T) {
	dir := setupWsDir(t)
	originalKey, err := LoadKey(dir)
	require.NoError(t, err)

	reencryptFail := func(_, _ []byte) error {
		return errors.New("db transaction rolled back")
	}

	err = RotateKey(dir, reencryptFail, alwaysFalse)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reencrypt")

	// Original key must still be in place.
	currentKey, err := LoadKey(dir)
	require.NoError(t, err)
	assert.Equal(t, originalKey, currentKey, "key must not change on reencrypt error")

	// No journal.
	journalPath := filepath.Join(dir, rotateJournalFile)
	_, statErr := os.Stat(journalPath)
	assert.True(t, os.IsNotExist(statErr))

	// No temp files.
	entries, err := filepath.Glob(filepath.Join(dir, ".key-rotate-*"))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// ─── ResumeIfCommitted ────────────────────────────────────────────────────────

// TestResumeIfCommitted_no_journal_is_noop verifies that the function returns
// nil and leaves the workspace unchanged when no journal file exists.
func TestResumeIfCommitted_no_journal_is_noop(t *testing.T) {
	dir := setupWsDir(t)
	originalKey, err := LoadKey(dir)
	require.NoError(t, err)

	require.NoError(t, ResumeIfCommitted(dir))

	currentKey, err := LoadKey(dir)
	require.NoError(t, err)
	assert.Equal(t, originalKey, currentKey, "key must be unchanged when no journal exists")
}

// TestResumeIfCommitted_committed_journal_installs_key verifies that a
// "committed" journal causes the temp key to be renamed to .key and the
// journal to be removed.
func TestResumeIfCommitted_committed_journal_installs_key(t *testing.T) {
	dir := setupWsDir(t)

	newKey := make([]byte, 32)
	for i := range newKey {
		newKey[i] = 0xBB
	}
	tmp, err := os.CreateTemp(dir, ".key-rotate-*")
	require.NoError(t, err)
	_, err = tmp.Write(newKey)
	require.NoError(t, err)
	require.NoError(t, tmp.Close())
	tmpPath := tmp.Name()

	journalPath := filepath.Join(dir, rotateJournalFile)
	require.NoError(t, writeRotateJournal(journalPath, rotateJournal{
		Stage:      "committed",
		NewKeyPath: tmpPath,
	}))

	require.NoError(t, ResumeIfCommitted(dir))

	installed, err := LoadKey(dir)
	require.NoError(t, err)
	assert.Equal(t, newKey, installed, "committed key must be installed")

	_, statErr := os.Stat(journalPath)
	assert.True(t, os.IsNotExist(statErr), "journal must be removed after resume")
	_, statErr = os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(statErr), "temp key must be gone after rename")
}

// TestResumeIfCommitted_pending_journal_leaves_unchanged verifies that a
// "pending" journal is not touched by ResumeIfCommitted (it requires
// tryDecryptAny and is handled exclusively by RotateKey).
func TestResumeIfCommitted_pending_journal_leaves_unchanged(t *testing.T) {
	dir := setupWsDir(t)
	originalKey, err := LoadKey(dir)
	require.NoError(t, err)

	staleKey := make([]byte, 32)
	tmp, err := os.CreateTemp(dir, ".key-rotate-*")
	require.NoError(t, err)
	_, err = tmp.Write(staleKey)
	require.NoError(t, err)
	require.NoError(t, tmp.Close())
	tmpPath := tmp.Name()

	journalPath := filepath.Join(dir, rotateJournalFile)
	require.NoError(t, writeRotateJournal(journalPath, rotateJournal{
		Stage:      "pending",
		NewKeyPath: tmpPath,
	}))

	require.NoError(t, ResumeIfCommitted(dir))

	// Key must be unchanged.
	currentKey, err := LoadKey(dir)
	require.NoError(t, err)
	assert.Equal(t, originalKey, currentKey, "key must not change for pending journal")

	// Journal and temp file must still exist (RotateKey will handle them).
	_, statErr := os.Stat(journalPath)
	assert.False(t, os.IsNotExist(statErr), "pending journal must still exist")
	_, statErr = os.Stat(tmpPath)
	assert.False(t, os.IsNotExist(statErr), "temp key for pending journal must still exist")
}

// TestLoadKey_auto_resumes_committed_journal verifies that LoadKey itself
// self-heals a "committed" journal, so any command that loads the workspace
// key recovers from a crashed rotation automatically.
func TestLoadKey_auto_resumes_committed_journal(t *testing.T) {
	dir := setupWsDir(t)

	newKey := make([]byte, 32)
	for i := range newKey {
		newKey[i] = 0xCC
	}
	tmp, err := os.CreateTemp(dir, ".key-rotate-*")
	require.NoError(t, err)
	_, err = tmp.Write(newKey)
	require.NoError(t, err)
	require.NoError(t, tmp.Close())

	journalPath := filepath.Join(dir, rotateJournalFile)
	require.NoError(t, writeRotateJournal(journalPath, rotateJournal{
		Stage:      "committed",
		NewKeyPath: tmp.Name(),
	}))

	// LoadKey should resume and return the new key without any explicit
	// call to RotateKey or ResumeIfCommitted.
	loaded, err := LoadKey(dir)
	require.NoError(t, err)
	assert.Equal(t, newKey, loaded, "LoadKey must auto-resume a committed journal")

	_, statErr := os.Stat(journalPath)
	assert.True(t, os.IsNotExist(statErr), "journal must be cleaned up by LoadKey")
}

// TestWriteRotateJournal_is_atomic verifies that writeRotateJournal produces a
// valid JSON file and that re-reading it round-trips correctly.
func TestWriteRotateJournal_is_atomic(t *testing.T) {
	dir := t.TempDir()
	journalPath := filepath.Join(dir, rotateJournalFile)

	j := rotateJournal{Stage: "committed", NewKeyPath: "/tmp/foo"}
	require.NoError(t, writeRotateJournal(journalPath, j))

	data, err := os.ReadFile(journalPath)
	require.NoError(t, err)

	var got rotateJournal
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, j, got)
}
