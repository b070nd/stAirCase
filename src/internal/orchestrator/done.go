package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MaxDoneAttempts is how often an agent is sent back to work before it may
// end with failing checks (the certificate still records them).
const MaxDoneAttempts = 3

// Done runs the run's checks (--check) before the agent may end, on a copy of
// the worktree's current state, which holds only decided changes. It returns
// why the agent must go on, or "" when the checks pass, there are none, or
// the agent was sent back MaxDoneAttempts times already.
func (e *AgentEnv) Done(ctx context.Context) string {
	if len(e.Checks) == 0 {
		return ""
	}
	e.doneMu.Lock()
	defer e.doneMu.Unlock()
	if e.doneAttempts >= MaxDoneAttempts {
		return ""
	}
	dir, cleanup, err := snapshot(e.Worktree)
	if err != nil {
		e.host.audit("done_checked", map[string]any{"error": err.Error()})
		return "" // staircase's own failure: sending the agent back would not help
	}
	defer cleanup()
	e.doneAttempts++
	var failed, why []string
	for _, command := range e.Checks {
		c, tail := runCheck(ctx, dir, command, e.Sandbox, e.Workspace)
		if c.ExitCode != 0 {
			failed = append(failed, command)
			why = append(why, fmt.Sprintf("check %q failed (exit %d):\n%s", command, c.ExitCode, strings.TrimSpace(tail)))
		}
	}
	e.host.audit("done_checked", map[string]any{"attempt": e.doneAttempts, "failed": failed})
	if len(failed) == 0 {
		return ""
	}
	return fmt.Sprintf("staircase: the task is not done yet.\n%s\nFix this, then finish (attempt %d of %d).",
		strings.Join(why, "\n"), e.doneAttempts, MaxDoneAttempts)
}

// snapshot is a clean checkout of worktree's HEAD with its changed files
// copied over: the state a commit would have, where a check can write
// without touching the worktree.
func snapshot(worktree string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "staircase-check-")
	if err == nil {
		err = os.Remove(dir) // git worktree add creates it
	}
	if err != nil {
		return "", nil, err
	}
	git := func(args ...string) ([]byte, error) {
		return exec.Command("git", append([]string{"-C", worktree, "-c", "core.hooksPath=/dev/null"}, args...)...).Output()
	}
	if _, err := git("worktree", "add", "--detach", "--quiet", dir, "HEAD"); err != nil {
		return "", nil, fmt.Errorf("checkout for the checks: %w", err)
	}
	cleanup := func() { _, _ = git("worktree", "remove", "--force", dir) }
	status, err := git("status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	for _, entry := range strings.Split(strings.TrimRight(string(status), "\x00"), "\x00") {
		if len(entry) < 4 {
			continue
		}
		if err := copyEntry(filepath.Join(worktree, entry[3:]), filepath.Join(dir, entry[3:])); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return dir, cleanup, nil
}

// copyEntry makes dst what src is: a file with its mode, a symlink, or gone.
func copyEntry(src, dst string) error {
	info, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return os.RemoveAll(dst)
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, info.Mode().Perm())
}
