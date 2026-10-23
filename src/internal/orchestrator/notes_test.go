package orchestrator_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notesRepo is a repository with n commits on top of the initial one, and the
// commits' names.
func notesRepo(t *testing.T, n int) (string, []string) {
	t.Helper()
	repo := initGitRepo(t)
	commits := make([]string, n)
	for i := range commits {
		git(t, repo, "commit", "--allow-empty", "-m", fmt.Sprintf("c%d", i))
		commits[i] = strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	}
	return repo, commits
}

// noteFile writes the bytes a run would attach and returns its path.
func noteFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

// noteOf is what a verifier reads: the note on commit under refs/notes/<ref>.
func noteOf(t *testing.T, repo, ref, commit string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "notes", "--ref="+ref, "show", commit).Output()
	require.NoError(t, err, "no %s note on %.12s", ref, commit)
	return string(out)
}

// TestAttachNote_parallel_writes_keep_every_note: git alone loses notes when
// they are added at the same moment (six parallel adds lost notes in 8 of 8
// trials); a delivery must never lose one.
func TestAttachNote_parallel_writes_keep_every_note(t *testing.T) {
	const n = 24
	t.Run("one ref", func(t *testing.T) {
		repo, commits := notesRepo(t, n)
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i, c := range commits {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = orchestrator.AttachNoteForTest(repo, orchestrator.NotesRef, noteFile(t, "c.json", fmt.Sprintf("{\"n\":%d}\n", i)), c)
			}()
		}
		wg.Wait()
		for i, c := range commits {
			require.NoError(t, errs[i])
			assert.Equal(t, fmt.Sprintf("{\"n\":%d}\n", i), noteOf(t, repo, orchestrator.NotesRef, c))
		}
	})
	t.Run("two refs", func(t *testing.T) {
		repo, commits := notesRepo(t, n)
		var wg sync.WaitGroup
		errs := make([]error, 2*n)
		for i, c := range commits {
			for j, ref := range []string{orchestrator.NotesRef, orchestrator.LedgerNotesRef} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs[2*i+j] = orchestrator.AttachNoteForTest(repo, ref, noteFile(t, "n.json", fmt.Sprintf("%s %d\n", ref, i)), c)
				}()
			}
		}
		wg.Wait()
		for i, c := range commits {
			require.NoError(t, errs[2*i])
			require.NoError(t, errs[2*i+1])
			assert.Equal(t, fmt.Sprintf("%s %d\n", orchestrator.NotesRef, i), noteOf(t, repo, orchestrator.NotesRef, c))
			assert.Equal(t, fmt.Sprintf("%s %d\n", orchestrator.LedgerNotesRef, i), noteOf(t, repo, orchestrator.LedgerNotesRef, c))
		}
	})
}

// TestAttachNote_two_deliveries_keep_certificate_and_ledger: two runs of one
// repository finish together; each leaves its certificate and its ledger.
func TestAttachNote_two_deliveries_keep_certificate_and_ledger(t *testing.T) {
	repo, commits := notesRepo(t, 2)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i, c := range commits {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[2*i] = orchestrator.AttachNoteForTest(repo, orchestrator.LedgerNotesRef, noteFile(t, "l.json", fmt.Sprintf("ledger %d\n", i)), c)
			errs[2*i+1] = orchestrator.AttachNoteForTest(repo, orchestrator.NotesRef, noteFile(t, "c.json", fmt.Sprintf("certificate %d\n", i)), c)
		}()
	}
	wg.Wait()
	require.NoError(t, errors.Join(errs...))
	for i, c := range commits {
		assert.Equal(t, fmt.Sprintf("ledger %d\n", i), noteOf(t, repo, orchestrator.LedgerNotesRef, c))
		assert.Equal(t, fmt.Sprintf("certificate %d\n", i), noteOf(t, repo, orchestrator.NotesRef, c))
	}
}

// TestAttachNote_stores_the_file_exactly: what a verifier compares is the
// file's bytes, not git's tidied copy of them.
func TestAttachNote_stores_the_file_exactly(t *testing.T) {
	repo, commits := notesRepo(t, 1)
	content := "no final newline\r\n\r\n\ttabbed   "
	require.NoError(t, orchestrator.AttachNoteForTest(repo, orchestrator.LedgerNotesRef, noteFile(t, "l.json", content), commits[0]))
	assert.Equal(t, content, noteOf(t, repo, orchestrator.LedgerNotesRef, commits[0]))
}

// swallowNoteWrites puts a git in front of the real one that reports success
// for the first n `git notes add` and writes nothing: a note lost without a
// word, as concurrent writers lose them.
func swallowNoteWrites(t *testing.T, n int) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	script := fmt.Sprintf(`#!/bin/sh
case " $* " in
*" notes "*" add "*)
	seen=$(cat %q 2>/dev/null || echo 0)
	if [ "$seen" -lt %d ]; then
		echo $((seen + 1)) > %q
		exit 0
	fi
	;;
esac
exec %q "$@"
`, count, n, count, gitPath)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestAttachNote_writes_again_a_note_that_vanished: the note is read back, and
// written again when it is not there.
func TestAttachNote_writes_again_a_note_that_vanished(t *testing.T) {
	repo, commits := notesRepo(t, 1)
	swallowNoteWrites(t, 2)
	require.NoError(t, orchestrator.AttachNoteForTest(repo, orchestrator.NotesRef, noteFile(t, "c.json", "certificate\n"), commits[0]))
	assert.Equal(t, "certificate\n", noteOf(t, repo, orchestrator.NotesRef, commits[0]))
}

// TestAttachNote_an_unconfirmable_note_is_an_error: after a bounded number of
// tries a note that cannot be read back is reported, never counted as written.
func TestAttachNote_an_unconfirmable_note_is_an_error(t *testing.T) {
	repo, commits := notesRepo(t, 1)
	swallowNoteWrites(t, 1000)
	err := orchestrator.AttachNoteForTest(repo, orchestrator.NotesRef, noteFile(t, "c.json", "certificate\n"), commits[0])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be confirmed")
}
