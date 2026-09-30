package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eachEngine runs test once for every sandbox engine of this machine,
// skipping those that cannot run here.
func eachEngine(t *testing.T, test func(t *testing.T)) {
	for _, e := range engines[runtime.GOOS] {
		t.Run(e.name, func(t *testing.T) {
			only = e.name
			t.Cleanup(func() { only = "" })
			test(t)
		})
	}
}

// sandboxRun runs command in wt with the sandbox on (required) and off: a
// check counts only if it passes without the sandbox, so a missing tool or
// path cannot make it pass. It skips when this machine has no sandbox.
func sandboxRun(t *testing.T, wt, command string, hide ...string) (on, off bool) {
	t.Helper()
	run := func(mode string) bool {
		cmd, sandboxed, cleanup, err := Command(context.Background(), wt, wt, command, mode, hide...)
		if err != nil && strings.Contains(err.Error(), "no sandbox") {
			if slices.Contains(strings.Fields(os.Getenv("STAIRCASE_REQUIRE_SANDBOX")), only) {
				t.Fatal(err) // CI: this engine must be tested, not skipped
			}
			t.Skip(err)
		}
		require.NoError(t, err)
		defer cleanup()
		out, err := cmd.CombinedOutput()
		t.Logf("%s (%s): %v %s", command, mode, err, out)
		return err == nil && sandboxed == (mode != Off)
	}
	return run(Required), run(Off)
}

// netClient is a command that fetches url, and fails if it cannot.
func netClient(t *testing.T, url string) string {
	if _, err := exec.LookPath("curl"); err == nil {
		return "curl -sf -m 3 " + url
	}
	if _, err := exec.LookPath("wget"); err == nil {
		return "wget -q -T 3 -O /dev/null " + url
	}
	t.Fatal("neither curl nor wget to check the network with")
	return ""
}

// TestShellSandbox: an approved command in the sandbox can write in the
// worktree and its own temp folder, and nowhere else, and cannot reach the
// network, not even this machine.
func TestShellSandbox(t *testing.T) { eachEngine(t, testShellSandbox) }

func testShellSandbox(t *testing.T) {
	wt, outside := t.TempDir(), t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	on, off := sandboxRun(t, wt, "echo in > inside.txt && cat inside.txt")
	assert.True(t, on && off, "writing in the worktree")
	on, off = sandboxRun(t, wt, `echo tmp > "$TMPDIR/scratch" && cat "$TMPDIR/scratch"`)
	assert.True(t, on && off, "writing in its temp folder")
	escape := filepath.Join(outside, "escape.txt")
	on, off = sandboxRun(t, wt, "echo out >> "+escape)
	assert.True(t, off)
	assert.False(t, on, "writing outside")
	b, _ := os.ReadFile(escape)
	assert.Equal(t, "out\n", string(b), "only the unsandboxed run wrote")
	on, off = sandboxRun(t, wt, netClient(t, srv.URL))
	assert.True(t, off)
	assert.False(t, on, "no network, not even this machine")
}

// TestShellSandbox_unavailable: without a sandbox tool, "required" refuses to
// run the command and "auto" runs it unsandboxed, saying so.
func TestShellSandbox_unavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no sandbox-exec or bwrap to find
	only = engines[runtime.GOOS][0].name
	t.Cleanup(func() { only = "" })
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
func TestShellSandbox_hides_credentials(t *testing.T) { eachEngine(t, testHidesCredentials) }

func testHidesCredentials(t *testing.T) {
	home, ws := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	// the agents' own login stores are credentials too (F103)
	logins := []string{".claude/.credentials.json", ".claude.json", ".codex/auth.json", ".gemini/oauth_creds.json",
		".local/share/opencode/auth.json", ".config/opencode/opencode.json"}
	for _, f := range append([]string{".ssh/id_ed25519", ".aws/credentials", ".netrc", ".config/gh/hosts.yml", ".notes"}, logins...) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(home, f)), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(home, f), []byte("x"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(ws, ".signing.key"), []byte("x"), 0o600))
	wt := filepath.Join(ws, "worktrees", "run-1")
	require.NoError(t, os.MkdirAll(wt, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wt, "main.go"), []byte("x"), 0o644))
	read := func(path string) (on, off bool) { return sandboxRun(t, wt, "cat "+path, ws) }

	for _, f := range append([]string{".ssh/id_ed25519", ".aws/credentials", ".netrc", ".config/gh/hosts.yml"}, logins...) {
		on, off := read(filepath.Join(home, f))
		assert.True(t, off, f)
		assert.False(t, on, f)
	}
	on, off := read(filepath.Join(ws, ".signing.key"))
	assert.True(t, off)
	assert.False(t, on, "the workspace")
	for _, f := range []string{"main.go", filepath.Join(home, ".notes"), "/etc/hosts"} {
		on, off := read(f)
		assert.True(t, on && off, f)
	}
}

// TestShellSandbox_bwrap_that_cannot_run: on Linux, a bwrap that is
// installed but not allowed to run (Ubuntu 24.04 restricts user namespaces)
// counts as no sandbox, so "auto" falls back instead of every command failing.
func TestShellSandbox_bwrap_that_cannot_run(t *testing.T) {
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "bwrap"), []byte("#!/bin/sh\necho 'bwrap: setting up uid map: Permission denied' >&2\nexit 1\n"), 0o755))
	assert.ErrorContains(t, bwrapUsable(filepath.Join(bin, "bwrap")), "Permission denied")
}

// TestReadable: hiding paths leaves every other entry next to them and their
// ancestors readable, and nothing inside them.
func TestReadable(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"home/.ssh", "home/.config/gh", "home/.config/nvim", "home/src", "ws/worktrees/run-1", "etc"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	in := func(p string) string { return filepath.Join(root, p) }
	got := readable([]string{in("home/.ssh"), in("home/.config/gh"), in("ws")})
	for _, want := range []string{in("etc"), in("home/src"), in("home/.config/nvim")} {
		assert.Contains(t, got, want)
	}
	for _, p := range got {
		for _, h := range []string{"home/.ssh", "home/.config/gh", "ws"} {
			assert.False(t, p == in(h) || within(in(h), p), "%s is readable", p)
		}
		assert.False(t, p == "/" || p == in("home") || p == in("home/.config"), "%s covers a hidden path", p)
	}
}
