package orchestrator_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── OpenGitRepo ─────────────────────────────────────────────────────────────

func TestGitRepo_open_nonexistent_returns_error(t *testing.T) {
	_, err := orchestrator.OpenGitRepo("/nonexistent/path/xyz")
	assert.Error(t, err)
}

func TestGitRepo_open_bare_repo_returns_error(t *testing.T) {
	dir := t.TempDir()
	out, err := exec.Command("git", "-C", dir, "init", "--bare").CombinedOutput()
	require.NoError(t, err, "git init --bare: %s", out)

	// A bare repository has no worktree, so OpenGitRepo must fail.
	_, err = orchestrator.OpenGitRepo(dir)
	assert.Error(t, err, "bare repo has no worktree — must return error")
}

// ─── CurrentBranch ───────────────────────────────────────────────────────────

func TestGitRepo_CurrentBranch_returns_main(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)
	branch, err := gr.CurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "main", branch)
}

// ─── BranchExists ────────────────────────────────────────────────────────────

func TestGitRepo_BranchExists_false_for_unknown(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)
	assert.False(t, gr.BranchExists("no-such-branch"))
}

func TestGitRepo_BranchExists_true_for_main(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)
	assert.True(t, gr.BranchExists("main"))
}

// ─── CreateBranch / DeleteBranch ─────────────────────────────────────────────

func TestGitRepo_CreateBranch_and_verify_exists(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	require.NoError(t, gr.CreateBranch("staircase/run-99"))
	assert.True(t, gr.BranchExists("staircase/run-99"))
}

func TestGitRepo_DeleteBranch_removes_branch(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	// Create then immediately check out main so we can delete the new branch.
	require.NoError(t, gr.CreateBranch("staircase/run-88"))
	require.NoError(t, gr.CheckoutBranch("main"))

	require.NoError(t, gr.DeleteBranch("staircase/run-88"))
	assert.False(t, gr.BranchExists("staircase/run-88"))
}

// ─── CurrentBranch — detached HEAD ───────────────────────────────────────────

func TestGitRepo_CurrentBranch_on_detached_HEAD(t *testing.T) {
	repoPath := initGitRepo(t)

	// Resolve HEAD SHA via system git, then detach HEAD at that commit.
	shaBytes, err := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	sha := strings.TrimSpace(string(shaBytes))
	require.Len(t, sha, 40, "SHA must be 40 hex chars")

	out, err := exec.Command("git", "-C", repoPath, "checkout", "--detach", sha).CombinedOutput()
	require.NoError(t, err, "detach HEAD: %s", out)

	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	branch, err := gr.CurrentBranch()
	require.NoError(t, err)
	// In detached-HEAD state CurrentBranch returns the full commit SHA.
	assert.Len(t, branch, 40, "detached HEAD must return 40-char SHA")
	assert.Equal(t, sha, branch)
}

// ─── CheckoutBranch ──────────────────────────────────────────────────────────

func TestGitRepo_CheckoutBranch_restores_main(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	require.NoError(t, gr.CreateBranch("staircase/run-77"))
	require.NoError(t, gr.CheckoutBranch("main"))

	branch, err := gr.CurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "main", branch)
}

func TestGitRepo_CheckoutBranch_by_sha_detaches_HEAD(t *testing.T) {
	repoPath := initGitRepo(t)

	shaBytes, err := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	sha := strings.TrimSpace(string(shaBytes))
	require.Len(t, sha, 40)

	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	// A 40-char string triggers the SHA checkout path in CheckoutBranch.
	require.NoError(t, gr.CheckoutBranch(sha))

	// HEAD is now detached at the given commit.
	current, err := gr.CurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, sha, current)
}

// ─── IsClean ─────────────────────────────────────────────────────────────────

func TestGitRepo_IsClean_on_clean_repo(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	clean, err := gr.IsClean()
	require.NoError(t, err)
	assert.True(t, clean)
}

func TestGitRepo_IsClean_false_with_untracked_file(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "dirty.txt"), []byte("x"), 0o644))

	clean, err := gr.IsClean()
	require.NoError(t, err)
	assert.False(t, clean)
}

// ─── ListBranches ────────────────────────────────────────────────────────────

func TestGitRepo_ListBranches_prefix_filter(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	require.NoError(t, gr.CreateBranch("staircase/run-1"))
	require.NoError(t, gr.CheckoutBranch("main"))
	require.NoError(t, gr.CreateBranch("staircase/run-2"))
	require.NoError(t, gr.CheckoutBranch("main"))

	branches, err := gr.ListBranches("staircase/run-")
	require.NoError(t, err)
	assert.Len(t, branches, 2)
	assert.NotContains(t, branches, "main")
}

func TestGitRepo_ListBranches_empty_when_no_match(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	branches, err := gr.ListBranches("staircase/run-")
	require.NoError(t, err)
	assert.Empty(t, branches)
}

// ─── Commit — default author fallback ────────────────────────────────────────

func TestGitRepo_Commit_falls_back_to_default_author_when_no_global_config(t *testing.T) {
	// Point HOME at an empty directory so go-git finds no ~/.gitconfig.
	// ConfigScoped(GlobalScope) returns an empty config, triggering the
	// "staircase" / "staircase@local" fallback inside Commit.
	emptyHome := t.TempDir()
	t.Setenv("HOME", emptyHome)

	dir := t.TempDir()
	// Initialize repo using per-command -c flags so there is no user config in
	// either the local repo config or the (now-empty) global config.
	cmds := [][]string{
		{"git", "-C", dir, "init", "-b", "main"},
		{"git", "-C", dir, "-c", "user.name=tmp", "-c", "user.email=tmp@tmp", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		require.NoError(t, err, "git setup: %s", out)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0o644))

	gr, err := orchestrator.OpenGitRepo(dir)
	require.NoError(t, err)
	require.NoError(t, gr.AddAll())

	// With no global git config, name and email should fall back to "staircase".
	sha, err := gr.Commit("fallback author test")
	require.NoError(t, err)
	assert.Len(t, sha, 40, "commit must succeed with default author")
}

// ─── AddAll / Commit ─────────────────────────────────────────────────────────

func TestGitRepo_AddAll_and_Commit_returns_sha(t *testing.T) {
	repoPath := initGitRepo(t)
	gr, err := orchestrator.OpenGitRepo(repoPath)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "new.txt"), []byte("hello"), 0o644))
	require.NoError(t, gr.AddAll())
	sha, err := gr.Commit("test: add new.txt")
	require.NoError(t, err)
	assert.Len(t, sha, 40, "commit SHA must be 40 hex chars")

	// Verify the commit is visible via system git.
	out, gitErr := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD").Output()
	require.NoError(t, gitErr)
	assert.Equal(t, sha, strings.TrimSpace(string(out)))
}
