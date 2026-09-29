package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShellSandbox: an approved command in the sandbox can write in the
// worktree and its own temp folder, and nowhere else, and cannot reach the
// network, not even this machine.
func TestShellSandbox(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("checked on macOS (sandbox-exec); Linux uses bwrap when installed")
	}
	wt, outside := t.TempDir(), t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	run := func(command string) (string, bool) {
		cmd, sandboxed, cleanup, err := Command(context.Background(), wt, wt, command, Required)
		require.NoError(t, err)
		defer cleanup()
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err == nil && sandboxed
	}

	_, ok := run("echo in > inside.txt && cat inside.txt")
	assert.True(t, ok, "writing in the worktree")
	_, ok = run(`echo tmp > "$TMPDIR/scratch" && cat "$TMPDIR/scratch"`)
	assert.True(t, ok, "writing in its temp folder")
	_, ok = run("echo out > " + filepath.Join(outside, "escape.txt"))
	assert.False(t, ok, "writing outside")
	assert.NoFileExists(t, filepath.Join(outside, "escape.txt"))
	_, ok = run("curl -sf -m 3 " + srv.URL)
	assert.False(t, ok, "no network, not even this machine")
}

// TestShellSandbox_unavailable: without a sandbox tool, "required" refuses to
// run the command and "auto" runs it unsandboxed, saying so.
func TestShellSandbox_unavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no sandbox-exec or bwrap to find
	wt := t.TempDir()
	_, _, _, err := Command(context.Background(), wt, wt, "true", Required)
	assert.ErrorContains(t, err, "no sandbox")
	_, sandboxed, cleanup, err := Command(context.Background(), wt, wt, "true", Auto)
	require.NoError(t, err)
	cleanup()
	assert.False(t, sandboxed)
	_, sandboxed, cleanup, err = Command(context.Background(), wt, wt, "true", Off)
	require.NoError(t, err)
	cleanup()
	assert.False(t, sandboxed)
}
