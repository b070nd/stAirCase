package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// scratchCheckout makes a throwaway checkout of commit of the repository at repo, in a new temporary directory named with prefix, and
// returns the directory and a function that removes it. It is where checks run: the final checks, the checks run when the agent says it
// is done, and the evidence a person's approval can rest on. It takes the repository's worktree lock, because git is not safe against two
// `worktree add` at once. It holds tracked files only: what the repository ignores is not in it.
func scratchCheckout(repo, commit, prefix string) (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", prefix)
	if err == nil {
		err = os.Remove(dir) // git worktree add creates it
	}
	if err != nil {
		return "", nil, err
	}
	err = withWorktreeLock(repo, func() error {
		out, err := exec.Command("git", "-C", repo, "-c", "core.hooksPath=/dev/null", "worktree", "add", "--detach", "--quiet", dir, commit).CombinedOutput()
		if err != nil {
			return fmt.Errorf("checkout for the checks: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return dir, func() {
		_ = withWorktreeLock(repo, func() error {
			return exec.Command("git", "-C", repo, "worktree", "remove", "--force", dir).Run()
		})
	}, nil
}
