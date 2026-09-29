package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
