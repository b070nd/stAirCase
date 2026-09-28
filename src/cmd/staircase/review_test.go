package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReview_governs_changes_made_elsewhere: a branch an agent pushed
// elsewhere (a cloud agent's pull request) is reviewed file by file in a
// worktree of the current branch; the approved files land on a run branch
// with a certificate that names who made them and the exact commit reviewed.
func TestReview_governs_changes_made_elsewhere(t *testing.T) {
	repo, ws, git := sessionRepo(t)
	git("switch", "-q", "-c", "agent/health")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("shop\nhealth: GET /health\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "health.go"), []byte("package shop\n"), 0o644))
	git("add", "-A")
	git("commit", "-q", "-m", "add health")
	reviewed := strings.TrimSpace(git("rev-parse", "HEAD"))
	git("switch", "-q", "main")

	reviewBy = "Copilot coding agent"
	t.Cleanup(func() { reviewBy = "" })
	require.NoError(t, reviewHandler(nil, []string{"agent/health"}))

	assert.Equal(t, "shop\nhealth: GET /health\n", git("show", "staircase/run-1:README.md"))
	assert.Equal(t, "package shop\n", git("show", "staircase/run-1:health.go"))
	msg := git("log", "-1", "--format=%B", "staircase/run-1")
	assert.Contains(t, msg, "Assisted-by: Copilot coding agent\n")

	verifyMinCAL, verifyKey, verifyFile, verifyCheckAnchor, verifyAll, verifySigners = 2, "", "", false, false, ""
	require.NoError(t, verifyHandler(nil, []string{"staircase/run-1"}))
	pl, err := plan.Load(filepath.Join(ws, "tmp", "plan_case1.json"))
	require.NoError(t, err)
	require.NotNil(t, pl.Review, "the plan, covered by the certificate's digest, names what was reviewed")
	assert.Equal(t, reviewed, pl.Review.Commit)
	assert.Equal(t, "Copilot coding agent", pl.Review.By)
}

// TestReview_keeps_newer_work: a file the current branch changed since the
// reviewed branch split off, in a way the branch's change does not fit, is
// left out rather than overwritten; a branch already merged has nothing to
// review.
func TestReview_keeps_newer_work(t *testing.T) {
	repo, _, git := sessionRepo(t)
	git("switch", "-q", "-c", "agent/old")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("agent's readme\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o644))
	git("add", "-A")
	git("commit", "-q", "-m", "agent change")
	git("switch", "-q", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("newer readme on main\n"), 0o644))
	git("commit", "-q", "-am", "newer work")

	require.NoError(t, reviewHandler(nil, []string{"agent/old"}))
	assert.Equal(t, "newer readme on main\n", git("show", "staircase/run-1:README.md"), "newer work is not undone")
	assert.Equal(t, "new\n", git("show", "staircase/run-1:new.txt"))

	assert.ErrorContains(t, reviewHandler(nil, []string{"main~1"}), "nothing to review")
}
