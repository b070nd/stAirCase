package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHookCommand_is_stable_and_holds_no_secret: every run writes the same
// hook command - no token, no address, no curl (F81) - so an agent that
// trusts a hook by its hash trusts it once. The program path is quoted for
// the shell that runs the hook.
func TestHookCommand_is_stable_and_holds_no_secret(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "it's here")
	assert.NoError(t, os.MkdirAll(dir, 0o755))
	bin := filepath.Join(dir, "staircase")
	assert.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s ' \"$@\"\n"), 0o755))

	cmd := HookCommand(bin, "claude-code", "--governed")
	out, err := exec.Command("/bin/sh", "-c", cmd).Output()
	assert.NoError(t, err)
	assert.Equal(t, "hook claude-code --governed ", string(out))

	settings := string(hookSettings(cmd))
	assert.Contains(t, settings, "hook claude-code --governed")
	for _, secret := range []string{"curl", "Bearer", "127.0.0.1"} {
		assert.NotContains(t, settings, secret)
	}
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
