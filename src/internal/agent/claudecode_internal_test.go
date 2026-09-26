package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClaudeCode_hook_failure_blocks: when staircase cannot answer, the hook
// must exit 2 (Claude Code lets the tool run on any other failure).
func TestClaudeCode_hook_failure_blocks(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", hookCommand("http://127.0.0.1:1/hook", "tok"))
	cmd.Stdin = strings.NewReader("{}")
	_ = cmd.Run()
	assert.Equal(t, 2, cmd.ProcessState.ExitCode())
}

// TestReadInside_refuses_symlinks_out_of_the_worktree: the Edit pre-check
// reads through os.Root, so a symlink swapped in after the path check cannot
// point the read outside the worktree.
func TestReadInside_refuses_symlinks_out_of_the_worktree(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.txt")
	assert.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
	root := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(root, "ok.txt"), []byte("fine"), 0o600))
	assert.NoError(t, os.Symlink(outside, filepath.Join(root, "link.txt")))
	h := &hookServer{root: root}
	got, err := h.readInside("ok.txt")
	assert.NoError(t, err)
	assert.Equal(t, "fine", string(got))
	_, err = h.readInside("link.txt")
	assert.Error(t, err, "a symlink leaving the worktree must not be followed")
}
