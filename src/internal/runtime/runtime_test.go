//go:build !windows

package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// BootstrapToken
// ---------------------------------------------------------------------------

// TestBootstrapToken_length_and_hex verifies that BootstrapToken returns a
// 64-character lowercase hex string encoding 32 random bytes (CHECK 5.4.1).
func TestBootstrapToken_length_and_hex(t *testing.T) {
	tok, err := runtime.BootstrapToken()
	require.NoError(t, err)
	assert.Len(t, tok, 64, "token must be 64 hex characters (32 bytes)")

	decoded, err := hex.DecodeString(tok)
	require.NoError(t, err, "token must be valid hexadecimal")
	assert.Len(t, decoded, 32, "decoded token must be 32 bytes")
}

// TestBootstrapToken_unique_per_call verifies that two successive calls produce
// distinct tokens (the birthday probability of a collision is negligible).
func TestBootstrapToken_unique_per_call(t *testing.T) {
	tok1, err := runtime.BootstrapToken()
	require.NoError(t, err)
	tok2, err := runtime.BootstrapToken()
	require.NoError(t, err)
	assert.NotEqual(t, tok1, tok2, "successive tokens must differ")
}

// ---------------------------------------------------------------------------
// venvPythonBin (via export shim)
// ---------------------------------------------------------------------------

// TestVenvPythonBin_unix_path verifies that the venv Python binary path is
// formed correctly on non-Windows platforms.
func TestVenvPythonBin_unix_path(t *testing.T) {
	result := runtime.ExportedVenvPythonBin("/opt/venv")
	assert.Contains(t, result, "/opt/venv", "path must be rooted at venvPath")
	// On Unix the binary is venv/bin/python (no ".exe").
	assert.True(t,
		strings.Contains(result, "python"),
		"path must contain 'python', got: %s", result,
	)
	assert.False(t, strings.HasSuffix(result, ".exe"), "Unix path must not end in .exe")
}

// ---------------------------------------------------------------------------
// PythonProcess.PID
// ---------------------------------------------------------------------------

// TestPID_before_start returns -1 when the process has not been started.
func TestPID_before_start(t *testing.T) {
	// exec.Command returns a Cmd whose Process field is nil until Start().
	cmd := exec.Command("sleep", "1")
	done := make(chan error, 1)
	pp := runtime.NewPythonProcessForTest(cmd, done)
	assert.Equal(t, -1, pp.PID(), "PID must be -1 before process starts")
}

// TestPID_after_start returns the real OS PID once the process is running.
func TestPID_after_start(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available on this system")
	}

	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() }) // goroutine below owns cmd.Wait(); calling it here too races

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	pp := runtime.NewPythonProcessForTest(cmd, done)
	pid := pp.PID()
	assert.Positive(t, pid, "PID must be a positive integer after process starts")
	assert.Equal(t, cmd.Process.Pid, pid)
}

// ---------------------------------------------------------------------------
// PythonProcess.Kill
// ---------------------------------------------------------------------------

// TestKill_closes_done_channel verifies that Kill() terminates the subprocess
// promptly (via SIGTERM to the process group) using a real "sleep 60" process.
// Kill() internally drains the Done channel, so we verify it returns within
// a short timeout rather than trying to receive from Done a second time.
func TestKill_closes_done_channel(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available on this system")
	}

	cmd := exec.Command("sleep", "60")
	// Put the process in its own process group so Kill()'s negative-PID SIGTERM
	// reaches it correctly — mirrors what LaunchPython does via SysProcAttr
	// (CHECK 5.4.3).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start(), "failed to start sleep process")

	done := make(chan error, 1)
	// Feed the exit error into done when the process exits (mirrors LaunchPython).
	go func() { done <- cmd.Wait() }()

	pp := runtime.NewPythonProcessForTest(cmd, done)

	// Kill() blocks until the process exits (SIGTERM path) or 5-second SIGKILL
	// grace period. Run it in a goroutine so we can apply our own 3-second
	// deadline — SIGTERM to the process group should make it return in <<1 s.
	killDone := make(chan struct{})
	go func() {
		pp.Kill()
		close(killDone)
	}()

	select {
	case <-killDone:
		// Kill() returned — process was terminated successfully.
	case <-time.After(3 * time.Second):
		t.Fatal("Kill() did not return within 3 seconds; process may not have terminated")
	}
}

// TestKill_sigkill_path exercises the SIGKILL grace-period branch of Kill() by
// using a shell process that traps SIGTERM and ignores it. After 5 s Kill() sends
// SIGKILL to the process group and returns.
func TestKill_sigkill_path(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available on this system")
	}

	// A shell that ignores SIGTERM and sleeps forever — forces Kill() SIGKILL path.
	cmd := exec.Command("bash", "-c", "trap '' TERM; sleep 60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	pp := runtime.NewPythonProcessForTest(cmd, done)

	// Kill() will wait 5 s for SIGTERM grace period then send SIGKILL.
	// We allow 8 s so the test doesn't race with the grace period.
	killDone := make(chan struct{})
	go func() {
		pp.Kill()
		close(killDone)
	}()

	select {
	case <-killDone:
		// Kill() returned after SIGKILL — test passes.
	case <-time.After(8 * time.Second):
		t.Fatal("Kill() SIGKILL path did not complete within 8 seconds")
	}
}

// ---------------------------------------------------------------------------
// BootstrapVenv — fast path (no pip install needed)
// ---------------------------------------------------------------------------

// requirePython3 skips the test if python3 is not in PATH.
func requirePython3(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available on this system")
	}
	return py
}

// embeddedRequirementsHash computes the SHA-256 hex of the requirements.txt
// that is baked into the runtime package binary at compile time.  We replicate
// this computation so the test can pre-write the correct hash into the fake venv
// and exercise the BootstrapVenv fast path without running pip install.
func embeddedRequirementsHash(t *testing.T) string {
	t.Helper()
	// The embedded requirements.txt lives next to the source; read it here so the
	// hash computation matches exactly what the compiled binary uses.
	reqPath := filepath.Join(
		".", // test runs from package dir (src/internal/runtime)
		"requirements.txt",
	)
	data, err := os.ReadFile(reqPath)
	if err != nil {
		// Fallback: locate via the module root for editors that set a different cwd.
		abs, _ := filepath.Abs("requirements.txt")
		data, err = os.ReadFile(abs)
		require.NoError(t, err, "could not read requirements.txt to compute hash")
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// TestBootstrapVenv_fast_path creates a fake workspace whose venv already
// exists and whose hash file matches the embedded requirements. BootstrapVenv
// must return nil without running pip install.
func TestBootstrapVenv_fast_path(t *testing.T) {
	requirePython3(t)

	wsDir := t.TempDir()
	venvPath := filepath.Join(wsDir, "venv")
	pythonBin := filepath.Join(venvPath, "bin", "python")
	hashFile := filepath.Join(venvPath, ".requirements_hash")

	// Create the venv/bin directory and a fake python binary (just needs to exist
	// and be executable so os.Stat succeeds — BootstrapVenv fast path only stats
	// the binary, it does not execute it).
	require.NoError(t, os.MkdirAll(filepath.Dir(pythonBin), 0o755))
	require.NoError(t, os.WriteFile(pythonBin, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	// Write the correct requirements hash so the fast path short-circuits.
	hash := embeddedRequirementsHash(t)
	require.NoError(t, os.WriteFile(hashFile, []byte(hash), 0o644))

	err := runtime.BootstrapVenv(wsDir, "")
	assert.NoError(t, err, "BootstrapVenv fast path must return nil when hash matches")
}

// TestBootstrapVenv_recreates_broken_venv verifies that a .broken sentinel
// causes BootstrapVenv to remove the old venv and re-create it from scratch.
// An impossible offline-wheels directory forces pip to fail quickly so the test
// does not block on network traffic.
func TestBootstrapVenv_recreates_broken_venv(t *testing.T) {
	requirePython3(t)

	wsDir := t.TempDir()
	venvPath := filepath.Join(wsDir, "venv")
	hashFile := filepath.Join(venvPath, ".requirements_hash")
	brokenSentinel := hashFile + ".broken"

	// Leave a .broken sentinel so the code takes the "recreate from scratch" path.
	require.NoError(t, os.MkdirAll(venvPath, 0o755))
	require.NoError(t, os.WriteFile(brokenSentinel, []byte("previous error"), 0o644))

	// Use a non-existent offline wheels dir so pip fails fast with --no-index
	// (no packages can be found), avoiding any network download.
	offlineDir := filepath.Join(wsDir, "nonexistent-wheels")

	err := runtime.BootstrapVenv(wsDir, offlineDir)
	// Pip will fail because there are no wheels at offlineDir — we just verify
	// that BootstrapVenv ran to the pip-install step and returned a meaningful
	// error (not a panic or unrelated error).
	require.Error(t, err, "BootstrapVenv must return an error when pip install fails")
	assert.True(t,
		strings.Contains(err.Error(), "pip") ||
			strings.Contains(err.Error(), "install") ||
			strings.Contains(err.Error(), "dependencies") ||
			strings.Contains(err.Error(), "venv"),
		"error message should describe the pip failure, got: %v", err,
	)
}

// ---------------------------------------------------------------------------
// LaunchPython — end-to-end with a real python3 venv
// ---------------------------------------------------------------------------

// makeMinimalVenv creates a real virtualenv (without pip) in wsDir/venv.
// Returns the path to the Python binary.
func makeMinimalVenv(t *testing.T, wsDir string) string {
	t.Helper()
	py := requirePython3(t)

	venvPath := filepath.Join(wsDir, "venv")
	out, err := exec.Command(py, "-m", "venv", "--without-pip", venvPath).CombinedOutput()
	require.NoError(t, err, "failed to create test venv: %s", string(out))

	return filepath.Join(venvPath, "bin", "python")
}

// ---------------------------------------------------------------------------
// PythonProcess.Kill — nil-process path
// ---------------------------------------------------------------------------

// TestKill_nil_process_calls_cancel verifies that Kill() on a PythonProcess
// whose underlying cmd.Process is nil (never started) does not panic and does
// not block — it simply invokes the cancel function and returns.
func TestKill_nil_process_calls_cancel(t *testing.T) {
	cmd := exec.Command("sleep", "1")
	// cmd.Process is nil because Start() was never called.
	done := make(chan error, 1)
	pp := runtime.NewPythonProcessForTest(cmd, done)

	// Must return immediately without panicking.
	done2 := make(chan struct{})
	go func() {
		pp.Kill()
		close(done2)
	}()
	select {
	case <-done2:
		// Returned without blocking — test passes.
	case <-time.After(time.Second):
		t.Fatal("Kill() with nil Process did not return within 1 second")
	}
}

// ---------------------------------------------------------------------------
// LaunchPython — error paths and stderr goroutine
// ---------------------------------------------------------------------------

// TestLaunchPython_nonexistent_python_returns_error exercises the cmd.Start()
// failure path inside LaunchPython. When the workspace's venv Python binary
// does not exist, LaunchPython must return a "start python" error.
func TestLaunchPython_nonexistent_python_returns_error(t *testing.T) {
	wsDir := t.TempDir()
	// Create the venv/bin directory but leave the python binary absent so
	// cmd.Start() fails with "no such file or directory".
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, "venv", "bin"), 0o755))

	scriptPath := filepath.Join(wsDir, "agent.py")
	require.NoError(t, os.WriteFile(scriptPath, []byte(""), 0o644))
	sf, err := os.Open(scriptPath)
	require.NoError(t, err)
	defer sf.Close()

	ctx := context.Background()
	_, err = runtime.LaunchPython(ctx, wsDir, sf, "/tmp/test-nobin.sock", "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start python", "error must come from cmd.Start failure")
}

// TestLaunchPython_stderr_is_logged exercises the stderr-scanning goroutine
// inside LaunchPython. A Python script writes a line to stderr; the goroutine
// must scan it and forward it to obs.Log without panicking.
func TestLaunchPython_stderr_is_logged(t *testing.T) {
	requirePython3(t)

	wsDir := t.TempDir()
	makeMinimalVenv(t, wsDir)

	// Script reads the bootstrap line then prints to stderr before exiting.
	scriptPath := filepath.Join(wsDir, "agent_stderr.py")
	script := `import sys, json
_ = json.loads(sys.stdin.readline())
print("test stderr line from python", file=sys.stderr)
sys.exit(0)
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	tok, err := runtime.BootstrapToken()
	require.NoError(t, err)

	sf, err := os.Open(scriptPath)
	require.NoError(t, err)
	defer sf.Close()

	ctx := context.Background()
	pp, err := runtime.LaunchPython(ctx, wsDir, sf, "/tmp/test-stderr.sock", tok)
	require.NoError(t, err, "LaunchPython must start without error")
	require.NotNil(t, pp)

	select {
	case <-pp.Done:
		// Process exited — stderr goroutine had a chance to scan the output.
	case <-time.After(5 * time.Second):
		pp.Kill()
		t.Fatal("Python process did not exit within 5 seconds")
	}
}

// ---------------------------------------------------------------------------
// BootstrapVenv — requirements-changed paths
// ---------------------------------------------------------------------------

// TestBootstrapVenv_requirements_changed_pip_fails covers the path where the
// venv python exists but the stored hash doesn't match (requirements changed),
// and the subsequent pip install fails. It uses a fake python binary so no
// network access is needed.
func TestBootstrapVenv_requirements_changed_pip_fails(t *testing.T) {
	wsDir := t.TempDir()
	venvPath := filepath.Join(wsDir, "venv")
	pythonBin := filepath.Join(venvPath, "bin", "python")
	hashFile := filepath.Join(venvPath, ".requirements_hash")

	require.NoError(t, os.MkdirAll(filepath.Dir(pythonBin), 0o755))
	// Fake python: exits non-zero so pip install "fails".
	require.NoError(t, os.WriteFile(pythonBin, []byte("#!/bin/sh\nexit 1\n"), 0o755))
	// Write a deliberately wrong hash so the requirements-changed branch fires.
	require.NoError(t, os.WriteFile(hashFile, []byte("wronghash"), 0o644))

	err := runtime.BootstrapVenv(wsDir, "")
	require.Error(t, err, "BootstrapVenv must return an error when pip install fails")
	assert.Contains(t, err.Error(), "install", "error must mention install failure")

	// The .broken sentinel must have been written.
	_, statErr := os.Stat(hashFile + ".broken")
	assert.NoError(t, statErr, ".broken sentinel must be created after pip failure")
}

// TestBootstrapVenv_requirements_changed_pip_succeeds covers lines 51-53:
// venv python exists, hash is stale, but pip install succeeds (fake binary
// exits 0). BootstrapVenv must update the hash file and return nil.
func TestBootstrapVenv_requirements_changed_pip_succeeds(t *testing.T) {
	wsDir := t.TempDir()
	venvPath := filepath.Join(wsDir, "venv")
	pythonBin := filepath.Join(venvPath, "bin", "python")
	hashFile := filepath.Join(venvPath, ".requirements_hash")

	require.NoError(t, os.MkdirAll(filepath.Dir(pythonBin), 0o755))
	// Fake python: exits 0 — pretends pip install succeeded.
	require.NoError(t, os.WriteFile(pythonBin, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	// Write a deliberately wrong hash so the requirements-changed branch fires.
	require.NoError(t, os.WriteFile(hashFile, []byte("wronghash"), 0o644))

	err := runtime.BootstrapVenv(wsDir, "")
	assert.NoError(t, err, "BootstrapVenv must return nil when pip succeeds")

	// The hash file must now contain the correct embedded-requirements hash.
	stored, readErr := os.ReadFile(hashFile)
	require.NoError(t, readErr)
	assert.NotEqual(t, "wronghash", strings.TrimSpace(string(stored)),
		"hash file must be updated after successful pip install")
}

// TestBootstrapVenv_full_creation_success exercises the tail of the
// full-venv-creation path (lines that write the final hash file and print
// "Environment isolated successfully"). A fake python3 script is injected via
// PATH so no network access is needed and the test completes instantly.
func TestBootstrapVenv_full_creation_success(t *testing.T) {
	// Build a fake python3 in a temp directory and prepend it to PATH.
	fakeDir := t.TempDir()
	fakePy3 := filepath.Join(fakeDir, "python3")

	// The script handles two roles:
	//   python3 -m venv <path>  — creates minimal venv structure
	//   <venv>/bin/python …    — any other invocation (arch check, pip) → exits 0
	fakeScript := `#!/bin/sh
if [ "$2" = "venv" ]; then
    venvDir="$3"
    mkdir -p "$venvDir/bin"
    printf '#!/bin/sh\nexit 0\n' > "$venvDir/bin/python"
    chmod +x "$venvDir/bin/python"
    exit 0
fi
exit 0
`
	require.NoError(t, os.WriteFile(fakePy3, []byte(fakeScript), 0o755))

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+":"+origPath)

	wsDir := t.TempDir()
	err := runtime.BootstrapVenv(wsDir, "")
	assert.NoError(t, err, "BootstrapVenv must succeed with a venv that creates and installs cleanly")

	// The hash file must have been written by the success path.
	hashFile := filepath.Join(wsDir, "venv", ".requirements_hash")
	_, statErr := os.Stat(hashFile)
	assert.NoError(t, statErr, "hash file must exist after successful full venv creation")
}

// TestBootstrapVenv_pip_network_error covers the network/proxy error path
// inside runPipInstall. A fake python binary prints CERTIFICATE_VERIFY_FAILED
// to stderr and exits 1, triggering the specialised error message.
func TestBootstrapVenv_pip_network_error(t *testing.T) {
	wsDir := t.TempDir()
	venvPath := filepath.Join(wsDir, "venv")
	pythonBin := filepath.Join(venvPath, "bin", "python")
	hashFile := filepath.Join(venvPath, ".requirements_hash")

	require.NoError(t, os.MkdirAll(filepath.Dir(pythonBin), 0o755))
	// Fake python: outputs CERTIFICATE_VERIFY_FAILED to stderr, exits 1.
	require.NoError(t, os.WriteFile(pythonBin,
		[]byte("#!/bin/sh\necho 'CERTIFICATE_VERIFY_FAILED: ssl error' >&2\nexit 1\n"),
		0o755))
	require.NoError(t, os.WriteFile(hashFile, []byte("wronghash"), 0o644))

	err := runtime.BootstrapVenv(wsDir, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network/proxy",
		"error must mention network/proxy for SSL certificate failures")
}

// ---------------------------------------------------------------------------
// LaunchPython — starts and exits (existing test follows)
// ---------------------------------------------------------------------------

// TestLaunchPython_starts_and_exits verifies that LaunchPython can start a
// Python process, send the bootstrap message, and that the process exits
// cleanly when Kill() is called.
func TestLaunchPython_starts_and_exits(t *testing.T) {
	requirePython3(t)

	wsDir := t.TempDir()
	makeMinimalVenv(t, wsDir)

	// A minimal Python script: read the JSON bootstrap line from stdin, then exit.
	scriptPath := filepath.Join(wsDir, "agent.py")
	script := `import sys, json
data = json.loads(sys.stdin.readline())
sys.exit(0)
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))

	tok, err := runtime.BootstrapToken()
	require.NoError(t, err)

	sf, err := os.Open(scriptPath)
	require.NoError(t, err)
	defer sf.Close()

	ctx := context.Background()
	pp, err := runtime.LaunchPython(ctx, wsDir, sf, "/tmp/test.sock", tok)
	require.NoError(t, err, "LaunchPython must start without error")
	require.NotNil(t, pp)

	// The process should exit on its own (script reads one line and exits).
	select {
	case <-pp.Done:
		// Exited cleanly.
	case <-time.After(5 * time.Second):
		pp.Kill()
		t.Fatal("Python process did not exit within 5 seconds")
	}

	assert.Positive(t, pp.PID(), "PID must be a positive integer while process was running")
}
