package orchestrator_test

import (
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

// repoWith is a repository with a committed file and an ignored directory that holds a file.
func repoWith(t *testing.T) (repo, head string) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("vendor/\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("committed\n"), 0o644))
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "vendor"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "vendor", "autoload.php"), []byte("<?php\n"), 0o644))
	return repo, git("rev-parse", "HEAD")
}

// TestScratchCheckout (the one place checks get their directory): a checkout of a commit holds its tracked files and nothing the
// repository ignores; many can be made at once in one repository; cleaning removes the directory and git's record of it; a commit that
// does not exist is an error that says so.
func TestScratchCheckout_holds_tracked_files_only_and_many_can_be_made_at_once(t *testing.T) {
	repo, head := repoWith(t)
	var wg sync.WaitGroup
	dirs := make([]string, 8)
	cleans := make([]func(), 8)
	errs := make([]error, 8)
	for i := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dirs[i], cleans[i], errs[i] = orchestrator.ExportedScratchCheckout(repo, head, "staircase-check-")
		}()
	}
	wg.Wait()
	for i := range dirs {
		require.NoError(t, errs[i], "checkout %d", i)
		b, err := os.ReadFile(filepath.Join(dirs[i], "a.txt"))
		require.NoError(t, err)
		assert.Equal(t, "committed\n", string(b))
		assert.NoFileExists(t, filepath.Join(dirs[i], "vendor", "autoload.php"), "an ignored path is not in a checkout of a commit")
	}
	for i := range dirs {
		cleans[i]()
		assert.NoDirExists(t, dirs[i])
	}
	out, err := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain").Output()
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(out), "worktree "), "git keeps no record of the removed checkouts")

	_, _, err = orchestrator.ExportedScratchCheckout(repo, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "staircase-check-")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkout for the checks")
}
