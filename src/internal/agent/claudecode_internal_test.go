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

	settings := string(hookSettings(cmd, nil))
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

// TestHookCommand_every_way_the_program_can_fail_blocks: an agent runs the hook
// through a shell, and treats an exit status other than 2 as "the hook broke,
// carry on". A program that is not there (127), that crashes or is killed (137)
// or that exits 1 must therefore end as 2. The controls: a program that succeeds
// stays 0, and one that blocks stays 2.
func TestHookCommand_every_way_the_program_can_fail_blocks(t *testing.T) {
	script := func(body string) string {
		bin := filepath.Join(t.TempDir(), "staircase")
		assert.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"+body+"\n"), 0o755))
		return bin
	}
	status := func(bin string) int {
		err := exec.Command("/bin/sh", "-c", HookCommand(bin, "claude-code", "--governed")).Run()
		if err == nil {
			return 0
		}
		var exitErr *exec.ExitError
		if assert.ErrorAs(t, err, &exitErr) {
			return exitErr.ExitCode()
		}
		return -1
	}
	assert.Equal(t, 0, status(script("exit 0")), "control: a program that succeeds")
	assert.Equal(t, 2, status(script("exit 2")), "control: a program that blocks")
	assert.Equal(t, 2, status(filepath.Join(t.TempDir(), "not-installed")), "the program is not there")
	assert.Equal(t, 2, status(script("exit 1")), "it fails")
	assert.Equal(t, 2, status(script("kill -9 $$")), "it is killed")
	assert.Equal(t, 2, status(script("exit 126")), "it cannot be executed")
}
