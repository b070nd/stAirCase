package ipc_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/ipc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clientScript drives the embedded Python client (runtime/ipc_embed.py): idle
// for longer than the server's idle deadline, then request an approval.
const clientScript = `import sys, time
sys.path.insert(0, sys.argv[1])
import ipc_embed as ipc
ipc.REPLY_TIMEOUT = 0.5
c = ipc.IPCClient(sys.argv[2], sys.argv[3], heartbeat_secs=float(sys.argv[4]))
time.sleep(2.5)  # a long LLM call: no requests on the wire
c.emit_state("agent", {"phase": "after-idle"})
r = c.yield_request("agent", "file_edit", [{"file": "a", "search_block": "x", "replace_block": "y"}], "why")
c.emit_state("agent", {"approved": r.get("approved")})
c.close()
`

func startRealClient(t *testing.T, heartbeatSecs string) (*ipc.Server, *exec.Cmd, *bytes.Buffer) {
	t.Helper()
	if testing.Short() {
		t.Skip("spawns python3 — skipped in -short mode")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	t.Cleanup(ipc.SetHeartbeatTimeoutForTest(1 * time.Second)) // before Start captures it
	srv, _, _, token, _ := newIPCEnv(t)
	runtimeDir, err := filepath.Abs(filepath.Join("..", "runtime"))
	require.NoError(t, err)
	script := filepath.Join(t.TempDir(), "client.py")
	require.NoError(t, os.WriteFile(script, []byte(clientScript), 0o600))
	var stderr bytes.Buffer
	cmd := exec.Command(py, script, runtimeDir, srv.ListenAddr(), token, heartbeatSecs)
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return srv, cmd, &stderr
}

func TestRealClient_survives_long_llm_call_and_slow_operator(t *testing.T) {
	srv, cmd, stderr := startRealClient(t, "0.3")

	select {
	case e := <-srv.StatEmitCh:
		assert.Equal(t, "after-idle", e.State["phase"], "heartbeats must keep the connection open")
	case <-time.After(10 * time.Second):
		t.Fatalf("no state after idle period; client stderr: %s", stderr)
	}
	select {
	case <-srv.YieldCh:
	case <-time.After(10 * time.Second):
		t.Fatalf("no yield request; client stderr: %s", stderr)
	}
	time.Sleep(1500 * time.Millisecond) // operator slower than the client's 0.5 s reply timeout
	srv.ResponseCh <- ipc.IpcYieldResponse{Type: "yield_response", Approved: true}
	select {
	case e := <-srv.StatEmitCh:
		assert.Equal(t, true, e.State["approved"])
	case <-time.After(10 * time.Second):
		t.Fatalf("client never received the slow approval; stderr: %s", stderr)
	}
	require.NoError(t, cmd.Wait(), "client must exit 0; stderr: %s", stderr)
}

func TestRealClient_without_heartbeats_is_dropped_by_idle_deadline(t *testing.T) {
	srv, cmd, stderr := startRealClient(t, "0")
	err := cmd.Wait()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "a dropped connection must end the client; stderr: %s", stderr)
	assert.Equal(t, 70, exitErr.ExitCode(), "stderr: %s", stderr)
	select {
	case e := <-srv.StatEmitCh:
		t.Fatalf("server should have dropped the silent client, got %v", e)
	default:
	}
}
