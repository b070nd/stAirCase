package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSeal_certifies_the_staged_changes_on_your_branch: seal reviews what is
// staged, one file at a time, and moves the current branch to a certified
// commit with your message that holds exactly the approved files. A rejected
// change stays in your working files, and edits you did not stage are left
// alone.
func TestSeal_certifies_the_staged_changes_on_your_branch(t *testing.T) {
	repo, ws, git := sessionRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), []byte(`{"rules":[
		{"action_types":["file_edit"],"effect":"reject","allowed_extensions":[".env"]},
		{"action_types":["file_edit"],"effect":"approve"}]}`), 0o600))
	head := strings.TrimSpace(git("rev-parse", "HEAD"))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "GREETING.md"), []byte("hello\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".env"), []byte("TOKEN=abc\n"), 0o644))
	git("add", "GREETING.md", ".env")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("shop, edited by hand\n"), 0o644))

	sealBy, sealMessage = "Cursor", "Add a greeting"
	t.Cleanup(func() { sealBy, sealMessage = "", "" })
	require.NoError(t, sealHandler(nil, nil))

	sealed := strings.TrimSpace(git("rev-parse", "main"))
	assert.Equal(t, head, strings.TrimSpace(git("rev-parse", "main^")), "one new commit on your branch")
	msg := git("log", "-1", "--format=%B", sealed)
	assert.True(t, strings.HasPrefix(msg, "Add a greeting\n"), msg)
	assert.Contains(t, msg, "Assisted-by: Cursor\n")
	assert.Equal(t, "Cursor", strings.TrimSpace(git("log", "-1", "--format=%(trailers:key=Assisted-by,valueonly)", sealed)), "a trailer git reads")
	assert.Equal(t, "GREETING.md\n", git("diff-tree", "--no-commit-id", "--name-only", "-r", sealed))

	b, err := os.ReadFile(filepath.Join(repo, ".env"))
	require.NoError(t, err)
	assert.Equal(t, "TOKEN=abc\n", string(b), "the rejected change is still in your working files")
	b, err = os.ReadFile(filepath.Join(repo, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "shop, edited by hand\n", string(b), "unstaged edits are left alone")
	assert.Empty(t, git("diff", "--cached", "--name-only"), "nothing left staged")
	assert.NotContains(t, git("branch", "--list", "staircase/*"), "run-", "no leftover run branch")

	verifyMinCAL, verifyKey, verifyFile, verifyCheckAnchor, verifyAll, verifySigners = 2, "", "", false, false, ""
	require.NoError(t, verifyHandler(nil, []string{sealed}))

	assert.ErrorContains(t, sealHandler(nil, nil), "nothing staged")
}

// TestAttach_stops_plain_commits: in an attached checkout a plain git commit
// is refused with the way to seal it; --no-verify still gets through (it is a
// guard against accidents, not a wall).
func TestAttach_stops_plain_commits(t *testing.T) {
	repo, _, git := sessionRepo(t)
	require.NoError(t, attachHandler(nil, nil))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "x.txt"), []byte("x\n"), 0o644))
	git("add", "x.txt")
	out, err := exec.Command("git", "-C", repo, "commit", "-q", "-m", "plain").CombinedOutput()
	assert.Error(t, err)
	assert.Contains(t, string(out), "staircase seal")
	git("commit", "-q", "--no-verify", "-m", "on purpose")

	attachOff = true
	t.Cleanup(func() { attachOff = false })
	require.NoError(t, attachHandler(nil, nil))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "y.txt"), []byte("y\n"), 0o644))
	git("add", "y.txt")
	git("commit", "-q", "-m", "detached again")

	hooks := strings.TrimSpace(git("rev-parse", "--git-path", "hooks"))
	if !filepath.IsAbs(hooks) {
		hooks = filepath.Join(repo, hooks)
	}
	require.NoError(t, os.MkdirAll(hooks, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	attachOff = false
	assert.ErrorContains(t, attachHandler(nil, nil), "already has a pre-commit hook", "never overwrites your own hook")
}

// TestSeal_names_with_spaces_and_accents: a file whose name git would quote or
// that has spaces is sealed like any other, and the report does not say that an
// approved file was left out.
func TestSeal_names_with_spaces_and_accents(t *testing.T) {
	repo, ws, git := sessionRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), []byte(`{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`), 0o600))
	name := "docs/my notes é.md"
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte("hello\n"), 0o644))
	git("add", name)

	r, w, err := os.Pipe()
	require.NoError(t, err)
	stdout := os.Stdout
	os.Stdout = w
	sealErr := sealHandler(nil, nil)
	os.Stdout = stdout
	_ = w.Close()
	out, _ := io.ReadAll(r)
	require.NoError(t, sealErr)

	assert.Equal(t, name, strings.TrimSpace(git("-c", "core.quotepath=off", "diff-tree", "--no-commit-id", "--name-only", "-r", "main")))
	assert.NotContains(t, string(out), "left out, still", string(out))
}

// TestReportRecovery_is_honest_about_the_evidence: a recovery whose evidence is
// incomplete is not reported as a certified commit.
func TestReportRecovery_is_honest_about_the_evidence(t *testing.T) {
	var done strings.Builder
	reportRecovery(&done, 7, orchestrator.RecoverResult{Commit: strings.Repeat("a", 40), Proposals: 2})
	assert.Contains(t, done.String(), "Run #7 recovered")
	assert.Contains(t, done.String(), "The certificate says the run did not finish")
	assert.NotContains(t, done.String(), "incomplete")

	var partial strings.Builder
	reportRecovery(&partial, 7, orchestrator.RecoverResult{Commit: strings.Repeat("a", 40), Proposals: 2,
		EvidenceErrors: []string{"ledger: disk full", "certificate: no signing key"}})
	assert.Contains(t, partial.String(), "evidence is incomplete")
	assert.Contains(t, partial.String(), "ledger: disk full")
	assert.Contains(t, partial.String(), "staircase recover 7")
	assert.NotContains(t, partial.String(), "The certificate says", "no certificate was claimed")

	var repaired strings.Builder
	reportRecovery(&repaired, 7, orchestrator.RecoverResult{Commit: strings.Repeat("a", 40), Proposals: 2, Repaired: true})
	assert.Contains(t, repaired.String(), "already committed")
}
