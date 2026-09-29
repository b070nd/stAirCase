package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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

// TestShellSandbox_hides_credentials: a command in the sandbox cannot read
// credentials in the home folder or the stAirCase workspace (signing key,
// secrets), only the run's own worktree inside it, and still reads the
// rest of the system.
func TestShellSandbox_hides_credentials(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("checked on macOS (sandbox-exec); Linux uses bwrap when installed")
	}
	home, ws := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	for _, f := range []string{".ssh/id_ed25519", ".aws/credentials", ".netrc", ".config/gh/hosts.yml", ".notes"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(home, f)), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, f), []byte("x"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(ws, ".signing.key"), []byte("x"), 0o600))
	wt := filepath.Join(ws, "worktrees", "run-1")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wt, "main.go"), []byte("x"), 0o644))
	read := func(path string) bool {
		cmd, sandboxed, cleanup, err := Command(context.Background(), wt, wt, "cat "+path, Required, ws)
		require.NoError(t, err)
		defer cleanup()
		return cmd.Run() == nil && sandboxed
	}

	for _, f := range []string{".ssh/id_ed25519", ".aws/credentials", ".netrc", ".config/gh/hosts.yml"} {
		assert.False(t, read(filepath.Join(home, f)), f)
	}
	assert.False(t, read(filepath.Join(ws, ".signing.key")), "the workspace")
	assert.True(t, read("main.go"), "the run's worktree")
	assert.True(t, read(filepath.Join(home, ".notes")), "the rest of home")
	assert.True(t, read("/etc/hosts"), "the system")
}
