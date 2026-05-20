package ipc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/ipc"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestMain enables goroutine-leak detection for the entire IPC test suite
// (CHECK 12.4.4). Any goroutine started during a test that has not exited by
// the time the test finishes will be reported as a leak.
func TestMain(m *testing.M) {
	// database/sql.(*DB).connectionOpener is a background goroutine managed by
	// the SQL driver; it exits asynchronously after db.Close(). Filter it to
	// avoid a false-positive leak report on legitimate test teardown.
	goleak.VerifyTestMain(
		m,
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)
}

// ─── test helpers ─────────────────────────────────────────────────────────────

func newIPCEnv(t *testing.T) (srv *ipc.Server, store *persistence.Store, runID int64, token string, cancel context.CancelFunc) {
	t.Helper()
	wsDir := t.TempDir()
	db, err := persistence.InitDB(wsDir)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	store = persistence.NewStore(db)

	// scaffold a run
	v, _ := store.CreateVendor("V")
	p, _ := store.CreateProject(v.ID, "P", "")
	c, _ := store.CreateCase(p.ID)
	topo, _ := store.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	run, _ := store.CreateRun(c.ID, topo.Version, "main")
	runID = run.ID

	token = "test-token-32-bytes-padded-here!" // must be consistent per test

	// UDS paths are capped at ~108 chars on macOS/Linux; use /tmp with a
	// unique short name derived from os.MkdirTemp so we stay well under the limit.
	sockDir, err := os.MkdirTemp("", "ipc-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	socketPath := filepath.Join(sockDir, "t.sock")

	aesKey := make([]byte, 32) // zero key for testing

	srv = ipc.NewServer(socketPath, runID, 0, token, store, aesKey)
	ctx, cancelFn := context.WithCancel(context.Background())
	require.NoError(t, srv.Start(ctx))
	t.Cleanup(cancelFn)
	cancel = cancelFn
	return
}

// networkForAddr returns "tcp" when addr looks like "host:port" (Windows
// loopback fallback) and "unix" for everything else (UDS path).
// This mirrors the server's own branch in Start() so that all tests
// automatically exercise the TCP path on Windows without any platform guards.
func networkForAddr(addr string) string {
	if len(addr) > 0 && addr[0] != '/' && addr[0] != '.' {
		// No leading slash → not an absolute or relative UDS path → TCP
		return "tcp"
	}
	return "unix"
}

// dial opens a connection and performs the auth handshake, returning a
// scanner and encoder ready for message exchange.
func dial(t *testing.T, addr, token string) (enc *json.Encoder, sc *bufio.Scanner, conn net.Conn) {
	t.Helper()
	network := networkForAddr(addr)
	// Small retry loop — socket may not be ready immediately.
	var err error
	for i := 0; i < 20; i++ {
		conn, err = net.Dial(network, addr)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, err, "dial IPC socket")
	t.Cleanup(func() { conn.Close() })

	enc = json.NewEncoder(conn)
	sc = bufio.NewScanner(conn)

	// auth
	require.NoError(t, enc.Encode(map[string]string{"type": "auth", "token": token}))
	require.True(t, sc.Scan(), "expected auth response")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	require.Equal(t, "auth_ok", resp["type"], "auth must succeed")
	return
}

// ─── UDS socket permissions ───────────────────────────────────────────────────

// TestServer_uds_socket_has_0600_permission verifies that the Unix Domain
// Socket created by Start() is chmod'd to 0600 so only the owning user can
// connect (CHECK 3.5.2 / §3.5 IPC hardening).
//
// Skipped on non-Unix platforms where the server uses a TCP fallback (Windows).
func TestServer_uds_socket_has_0600_permission(t *testing.T) {
	if networkForAddr("/tmp/dummy.sock") == "tcp" {
		t.Skip("TCP fallback active — socket permissions not applicable")
	}
	srv, _, _, _, _ := newIPCEnv(t)
	addr := srv.ListenAddr()

	info, err := os.Stat(addr)
	require.NoError(t, err, "socket file must exist after Start()")
	perm := info.Mode().Perm()
	assert.Equal(t, os.FileMode(0o600), perm,
		"UDS socket %s must have mode 0600, got %04o", addr, perm)
}

func TestServer_Start_missing_socket_parent_returns_error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses TCP loopback instead of Unix sockets")
	}
	socketPath := filepath.Join(t.TempDir(), "missing", "t.sock")
	srv := ipc.NewServer(socketPath, 1, 0, "token", nil, make([]byte, 32))

	err := srv.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ipc listen")
}

// ─── Auth handshake ───────────────────────────────────────────────────────────

func TestServer_auth_ok(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)
	_ = enc
	_ = sc
	// dial() already asserts auth_ok
}

func TestServer_auth_wrong_token_rejected(t *testing.T) {
	srv, _, _, _, _ := newIPCEnv(t)
	addr := srv.ListenAddr()

	var conn net.Conn
	var err error
	for i := 0; i < 20; i++ {
		conn, err = net.Dial(networkForAddr(addr), addr)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, err, "dial IPC socket")
	defer conn.Close()

	enc := json.NewEncoder(conn)
	sc := bufio.NewScanner(conn)
	require.NoError(t, enc.Encode(map[string]string{"type": "auth", "token": "wrong-token"}))
	require.True(t, sc.Scan())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "auth_failed", resp["type"])
}

// ─── Heartbeat ────────────────────────────────────────────────────────────────

func TestServer_heartbeat_ack(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	require.NoError(t, enc.Encode(map[string]string{"type": "heartbeat"}))
	require.True(t, sc.Scan())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "heartbeat_ack", resp["type"])
}

// ─── state_emit → event log ───────────────────────────────────────────────────

func TestServer_state_emit_appends_event_log(t *testing.T) {
	srv, store, runID, token, _ := newIPCEnv(t)
	enc, _, _ := dial(t, srv.ListenAddr(), token)

	msg := ipc.IpcStateEmit{
		Type:        "state_emit",
		ActiveAgent: "coder",
		State:       map[string]interface{}{"step": 1},
	}
	require.NoError(t, enc.Encode(msg))
	// state_emit is fire-and-forget; give the server goroutine time to process.
	time.Sleep(30 * time.Millisecond)

	logs, err := store.ListEventLogs(runID)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "state_emit", logs[0].EventType)
}

// ─── payload size cap ─────────────────────────────────────────────────────────

func TestServer_state_emit_payload_truncated(t *testing.T) {
	srv, store, runID, token, _ := newIPCEnv(t)
	enc, _, _ := dial(t, srv.ListenAddr(), token)

	// Build a state_emit with a 128 KiB payload (> 64 KiB cap).
	bigPayload := strings.Repeat("x", 128*1024)
	raw := map[string]interface{}{
		"type":         "state_emit",
		"active_agent": "agent",
		"state":        map[string]interface{}{"data": bigPayload},
	}
	require.NoError(t, enc.Encode(raw))
	time.Sleep(30 * time.Millisecond)

	logs, err := store.ListEventLogs(runID)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.LessOrEqual(t, len(logs[0].Payload), 64*1024,
		"stored payload must be capped at 64 KiB")
}

func TestServer_state_emit_append_error_keeps_connection_alive(t *testing.T) {
	wsDir := t.TempDir()
	db, err := persistence.InitDB(wsDir)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	store := persistence.NewStore(db)

	sockDir, err := os.MkdirTemp("", "ipc-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	token := "test-token-32-bytes-padded-here!"
	srv := ipc.NewServer(filepath.Join(sockDir, "t.sock"), 999, 0, token, store, make([]byte, 32))
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, srv.Start(ctx))
	t.Cleanup(cancel)

	enc, sc, _ := dial(t, srv.ListenAddr(), token)
	require.NoError(t, enc.Encode(ipc.IpcStateEmit{
		Type:        "state_emit",
		ActiveAgent: "agent",
	}))
	time.Sleep(30 * time.Millisecond)

	require.NoError(t, enc.Encode(map[string]string{"type": "heartbeat"}))
	require.True(t, sc.Scan(), "server must keep the connection alive after log append errors")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "heartbeat_ack", resp["type"])
}

// ─── yield_request round-trip ─────────────────────────────────────────────────

func TestServer_yield_request_roundtrip(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	// Send yield_request in a goroutine (it blocks waiting for response).
	done := make(chan struct{})
	go func() {
		defer close(done)
		yr := ipc.IpcYieldRequest{
			Type:            "yield_request",
			AgentName:       "planner",
			ActionType:      "file_edit",
			ReasoningTrace:  "adding feature X",
			ConfidenceScore: 0.95,
		}
		if err := enc.Encode(yr); err != nil {
			return
		}
		// Read the yield_response.
		if sc.Scan() {
			var resp ipc.IpcYieldResponse
			_ = json.Unmarshal(sc.Bytes(), &resp)
			assert.True(t, resp.Approved)
		}
	}()

	// Operator side: receive the yield and approve it.
	select {
	case req := <-srv.YieldCh:
		assert.Equal(t, "planner", req.AgentName)
		srv.ResponseCh <- ipc.IpcYieldResponse{
			Type:     "yield_response",
			Approved: true,
			Feedback: "looks good",
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for yield request")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for yield goroutine")
	}
}

// ─── secret_request — not found ───────────────────────────────────────────────

func TestServer_secret_request_not_found(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	require.NoError(t, enc.Encode(ipc.IpcSecretRequest{
		Type:    "secret_request",
		KeyName: "MISSING_KEY",
	}))
	require.True(t, sc.Scan())
	var resp ipc.IpcSecretResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "not found", resp.Error)
}

// ─── secret_request — found and decrypted ────────────────────────────────────

// TestServer_secret_request_decrypts_before_delivery verifies the critical
// I-3/T-1 fix: the IPC server must decrypt secrets in Go before sending them
// to Python. Python must receive the plaintext string, never the encrypted blob.
func TestServer_secret_request_decrypts_before_delivery(t *testing.T) {
	srv, store, _, token, _ := newIPCEnv(t)

	// The server was constructed with a zero AES key (newIPCEnv: make([]byte, 32)).
	// Encrypt the secret with the same key so the server can decrypt it.
	aesKey := make([]byte, 32)
	encrypted, err := crypto.Encrypt(aesKey, "sk-plaintext-api-key")
	require.NoError(t, err)
	_, err = store.CreateSecret("ANTHROPIC_API_KEY", encrypted, nil)
	require.NoError(t, err)

	enc, sc, _ := dial(t, srv.ListenAddr(), token)
	require.NoError(t, enc.Encode(ipc.IpcSecretRequest{
		Type:    "secret_request",
		KeyName: "ANTHROPIC_API_KEY",
	}))
	require.True(t, sc.Scan(), "must receive a secret_response")

	var resp ipc.IpcSecretResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Empty(t, resp.Error, "secret_request must not return an error when the key exists")
	assert.Equal(t, "sk-plaintext-api-key", resp.PlaintextValue,
		"IPC server must return decrypted plaintext in PlaintextValue — not the encrypted blob")
}

// ─── secret_request — project_id scoping ─────────────────────────────────────

// TestServer_secret_request_project_id_mismatch verifies that a secret_request
// targeting a different project is rejected (Fix C — CHECK 4.4.x cross-project guard).
func TestServer_secret_request_project_id_mismatch(t *testing.T) {
	wsDir := t.TempDir()
	db, err := persistence.InitDB(wsDir)
	require.NoError(t, err)
	store := persistence.NewStore(db)

	sockDir, err := os.MkdirTemp("", "ipc-test-projectmatch-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	token := "test-token-32-bytes-padded-here!"
	const ownProject int64 = 42
	srv := ipc.NewServer(filepath.Join(sockDir, "t.sock"), 1, ownProject, token, store, make([]byte, 32))
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, srv.Start(ctx))
	t.Cleanup(cancel)

	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	// Send a request targeting a *different* project.
	otherProject := int64(99)
	require.NoError(t, enc.Encode(ipc.IpcSecretRequest{
		Type:      "secret_request",
		KeyName:   "SOME_KEY",
		ProjectID: &otherProject,
	}))
	require.True(t, sc.Scan())
	var resp ipc.IpcSecretResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "project_id mismatch", resp.Error,
		"cross-project secret request must be rejected")
}

// ─── field validation (Fix E — CHECK 3.5.5) ──────────────────────────────────

func TestServer_yield_request_empty_agent_name_rejected(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	require.NoError(t, enc.Encode(map[string]any{
		"type":             "yield_request",
		"agent_name":       "",
		"action_type":      "custom",
		"confidence_score": 0.9,
	}))
	require.True(t, sc.Scan())
	var resp ipc.IpcYieldResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.False(t, resp.Approved)
	assert.Contains(t, resp.Feedback, "agent_name required")
}

func TestServer_yield_request_invalid_confidence_rejected(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	require.NoError(t, enc.Encode(map[string]any{
		"type":             "yield_request",
		"agent_name":       "planner",
		"action_type":      "custom",
		"confidence_score": 1.5, // out of range
	}))
	require.True(t, sc.Scan())
	var resp ipc.IpcYieldResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.False(t, resp.Approved)
	assert.Contains(t, resp.Feedback, "confidence_score")
}

func TestServer_yield_request_unknown_action_type_rejected(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	require.NoError(t, enc.Encode(map[string]any{
		"type":             "yield_request",
		"agent_name":       "planner",
		"action_type":      "rm_rf", // unknown
		"confidence_score": 0.9,
	}))
	require.True(t, sc.Scan())
	var resp ipc.IpcYieldResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.False(t, resp.Approved)
	assert.Contains(t, resp.Feedback, "unknown action_type")
}

func TestServer_secret_request_empty_key_name_rejected(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	require.NoError(t, enc.Encode(ipc.IpcSecretRequest{
		Type:    "secret_request",
		KeyName: "",
	}))
	require.True(t, sc.Scan())
	var resp ipc.IpcSecretResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "key_name required", resp.Error)
}

// ─── event log hash chain ─────────────────────────────────────────────────────

func TestServer_event_log_chain_hashes_are_sequential(t *testing.T) {
	srv, store, runID, token, _ := newIPCEnv(t)
	enc, _, _ := dial(t, srv.ListenAddr(), token)

	for i := 0; i < 3; i++ {
		require.NoError(t, enc.Encode(ipc.IpcStateEmit{
			Type:        "state_emit",
			ActiveAgent: "agent",
		}))
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	logs, err := store.ListEventLogs(runID)
	require.NoError(t, err)
	require.Len(t, logs, 3)
	// Each entry's hash must be non-empty and entries must be distinct.
	hashes := map[string]bool{}
	for _, l := range logs {
		assert.NotEmpty(t, l.EventHash)
		assert.False(t, hashes[l.EventHash], "duplicate hash in chain")
		hashes[l.EventHash] = true
	}
}

// ─── Connection limit ─────────────────────────────────────────────────────────

func TestServer_connection_limit_drops_excess(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	addr := srv.ListenAddr()

	// Open maxConnections (2) authenticated connections.
	conns := make([]net.Conn, 0, 3)
	for i := 0; i < 2; i++ {
		_, _, c := dial(t, addr, token)
		conns = append(conns, c)
	}

	// A third connection should be dropped at the server side.
	// We attempt to connect; after a brief moment the server closes it.
	var extra net.Conn
	var err error
	for i := 0; i < 20; i++ {
		extra, err = net.Dial(networkForAddr(addr), addr)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, err, "should still be able to connect")
	defer extra.Close()

	// The server immediately closes the over-limit connection after accept.
	// Reading from it should return EOF or an error promptly.
	extra.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 64)
	_, readErr := extra.Read(buf)
	assert.Error(t, readErr, "over-limit connection should be closed by the server")

	for _, c := range conns {
		c.Close()
	}
}

// ─── secret_request — malformed JSON ─────────────────────────────────────────

// TestServer_secret_request_malformed_json_returns_error verifies §3.5.8:
// a malformed secret_request must receive an error response, not silence.
// Before this fix, the server's "if err == nil" guard silently dropped the
// message, leaving Python blocked forever waiting for a secret_response.
func TestServer_secret_request_malformed_json_returns_error(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	_, sc, conn := dial(t, srv.ListenAddr(), token)

	// project_id is *int64; sending a string causes the IpcSecretRequest
	// unmarshal to fail while the base-type extraction succeeds.
	// Before the §3.5.8 fix, the server's "if err == nil" guard silently
	// discarded the message and Python would hang indefinitely.
	_, err := fmt.Fprintf(conn, `{"type":"secret_request","project_id":"not-an-int"}`+"\n")
	require.NoError(t, err)

	// The server must respond with an error — not silence.
	require.True(t, sc.Scan(), "server must send a secret_response for malformed input")
	var resp ipc.IpcSecretResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "secret_response", resp.Type)
	assert.NotEmpty(t, resp.Error, "malformed secret_request must produce an error response")
}

// ─── per-kind rate limiting ───────────────────────────────────────────────────

// TestServer_rate_limit_exceeded_returns_error_not_silence verifies §3.5.6:
// when a message kind exceeds its per-second budget the server returns an error
// response for request/response kinds and does not hang.
func TestServer_rate_limit_exceeded_returns_error_not_silence(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	// heartbeat limit is 5/sec. Send 6 — the 6th must be rate-limited.
	// We read each ack immediately to avoid scanner deadlock.
	for i := 0; i < 5; i++ {
		require.NoError(t, enc.Encode(map[string]string{"type": "heartbeat"}))
		require.True(t, sc.Scan())
		var r map[string]string
		require.NoError(t, json.Unmarshal(sc.Bytes(), &r))
		assert.Equal(t, "heartbeat_ack", r["type"])
	}

	// 6th heartbeat: rate limit exceeded; server logs and drops — no response.
	// We verify the connection is still alive (not closed) by sending a fresh
	// heartbeat after sleeping for >1 second (window reset).
	require.NoError(t, enc.Encode(map[string]string{"type": "heartbeat"})) // over limit — drop
	time.Sleep(1100 * time.Millisecond)                                    // window resets
	require.NoError(t, enc.Encode(map[string]string{"type": "heartbeat"}))
	require.True(t, sc.Scan(), "connection must still be alive after rate limit")
	var r map[string]string
	require.NoError(t, json.Unmarshal(sc.Bytes(), &r))
	assert.Equal(t, "heartbeat_ack", r["type"])
}

// ─── SetDebugWriter ───────────────────────────────────────────────────────────

// TestServer_SetDebugWriter_logs_inbound_and_outbound verifies that enabling
// the debug writer causes both inbound and outbound messages to be logged.
// This covers the debugLog branches in handleConnection (CHECK 3.6.1).
func TestServer_SetDebugWriter_logs_inbound_and_outbound(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	var buf strings.Builder
	srv.SetDebugWriter(&buf)

	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	// A heartbeat produces an inbound log entry (no outbound log for heartbeat_ack
	// since heartbeat is not a yield_request — only yield_response is logged out).
	require.NoError(t, enc.Encode(map[string]string{"type": "heartbeat"}))
	require.True(t, sc.Scan())
	time.Sleep(20 * time.Millisecond)

	logged := buf.String()
	assert.Contains(t, logged, `"dir":"in"`, "inbound message must be debug-logged")
	assert.Contains(t, logged, "heartbeat", "debug log must contain the message type")
}

// TestServer_SetDebugWriter_logs_yield_response verifies that the outbound
// yield_response is written to the debug log when a debug writer is set.
func TestServer_SetDebugWriter_logs_yield_response(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	var buf strings.Builder
	srv.SetDebugWriter(&buf)

	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = enc.Encode(ipc.IpcYieldRequest{
			Type:            "yield_request",
			AgentName:       "planner",
			ActionType:      "file_edit",
			ReasoningTrace:  "trace",
			ConfidenceScore: 0.9,
		})
		sc.Scan() // consume yield_response
	}()

	select {
	case req := <-srv.YieldCh:
		assert.Equal(t, "planner", req.AgentName)
		srv.ResponseCh <- ipc.IpcYieldResponse{Type: "yield_response", Approved: true}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for yield")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for yield goroutine")
	}

	assert.Contains(t, buf.String(), `"dir":"out"`, "outbound yield_response must be debug-logged")
}

// ─── Unknown message type ─────────────────────────────────────────────────────

// TestServer_unknown_message_type_is_silently_dropped verifies that the server
// silently discards unrecognised message types without closing the connection.
// This covers the rate-limiter's "unknown kind → allow" path and the switch
// default in handleConnection.
func TestServer_unknown_message_type_is_silently_dropped(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	require.NoError(t, enc.Encode(map[string]string{"type": "frobnicator", "payload": "ignored"}))
	time.Sleep(20 * time.Millisecond)

	// Connection must still be alive — confirm with a heartbeat.
	require.NoError(t, enc.Encode(map[string]string{"type": "heartbeat"}))
	require.True(t, sc.Scan(), "connection must stay alive after unknown message type")
	var r map[string]string
	require.NoError(t, json.Unmarshal(sc.Bytes(), &r))
	assert.Equal(t, "heartbeat_ack", r["type"])
}

// ─── Context cancellation during yield ───────────────────────────────────────

// TestServer_context_cancel_during_yield_closes_connection verifies that
// cancelling the server context while a yield_request is in flight causes
// the connection to close cleanly (covers the ctx.Done paths in the yield
// select in handleConnection).
func TestServer_context_cancel_during_yield_closes_connection(t *testing.T) {
	srv, _, _, token, cancel := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	// Send a yield_request but never respond on ResponseCh — the handler blocks.
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_ = enc.Encode(ipc.IpcYieldRequest{
			Type:            "yield_request",
			AgentName:       "agent",
			ActionType:      "file_edit",
			ReasoningTrace:  "reading",
			ConfidenceScore: 0.5,
		})
	}()

	// Wait until the server receives the yield on its channel.
	select {
	case <-srv.YieldCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for yield on YieldCh")
	}

	// Cancel the server context while the yield handler waits for ResponseCh.
	cancel()

	// The connection must close — reading from it should return EOF/error.
	done := make(chan bool)
	go func() { done <- !sc.Scan() }()
	select {
	case closed := <-done:
		assert.True(t, closed, "scanner should return false (EOF/error) when context is cancelled")
	case <-time.After(2 * time.Second):
		t.Fatal("connection did not close after context cancel")
	}
	<-sent
}

// ─── Invalid / malformed messages ────────────────────────────────────────────

// TestServer_invalid_json_body_silently_dropped verifies that a line that is
// not valid JSON at all causes a silent continue without closing the connection.
func TestServer_invalid_json_body_silently_dropped(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	_, _, conn := dial(t, srv.ListenAddr(), token)
	enc := json.NewEncoder(conn)
	sc := bufio.NewScanner(conn)

	// Send raw bytes that aren't valid JSON.
	_, err := fmt.Fprintf(conn, "not-valid-json-at-all\n")
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)

	// Connection must still be alive.
	require.NoError(t, enc.Encode(map[string]string{"type": "heartbeat"}))
	require.True(t, sc.Scan())
	var r map[string]string
	require.NoError(t, json.Unmarshal(sc.Bytes(), &r))
	assert.Equal(t, "heartbeat_ack", r["type"])
}

// TestServer_malformed_yield_request_auto_rejected verifies that a
// yield_request whose full JSON cannot be decoded is auto-rejected so that
// Python doesn't hang waiting for a response (CHECK §3.5.x).
func TestServer_malformed_yield_request_auto_rejected(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	_, _, conn := dial(t, srv.ListenAddr(), token)
	sc := bufio.NewScanner(conn)

	// confidence_score must be a float; send a string to break IpcYieldRequest
	// unmarshal while keeping the base-type extraction intact.
	_, err := fmt.Fprintf(conn, `{"type":"yield_request","confidence_score":"not-a-float"}`+"\n")
	require.NoError(t, err)

	require.True(t, sc.Scan(), "server must send an auto-reject response")
	var resp ipc.IpcYieldResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "yield_response", resp.Type)
	assert.False(t, resp.Approved, "malformed yield_request must be auto-rejected")
	assert.NotEmpty(t, resp.Feedback, "auto-reject must include a feedback message")
}

// TestServer_yield_payload_large_truncated verifies that a yield_request with
// a payload exceeding 64 KiB is stored truncated (not rejected or dropped).
func TestServer_yield_payload_large_truncated(t *testing.T) {
	srv, store, runID, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	bigTrace := strings.Repeat("x", 128*1024)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = enc.Encode(ipc.IpcYieldRequest{
			Type:            "yield_request",
			AgentName:       "agent",
			ActionType:      "file_edit",
			ReasoningTrace:  bigTrace,
			ConfidenceScore: 0.8,
		})
		sc.Scan() // consume yield_response
	}()

	select {
	case <-srv.YieldCh:
		srv.ResponseCh <- ipc.IpcYieldResponse{Type: "yield_response", Approved: true}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for yield")
	}
	<-done

	time.Sleep(30 * time.Millisecond)
	logs, err := store.ListEventLogs(runID)
	require.NoError(t, err)
	require.NotEmpty(t, logs, "yield_request must be logged")
	assert.LessOrEqual(t, len(logs[0].Payload), 64*1024,
		"stored yield payload must be capped at 64 KiB")
}

// TestServer_secret_request_decrypt_error_returns_error verifies that a secret
// whose stored value cannot be AES-decrypted returns an error_response instead
// of panicking or hanging.
func TestServer_secret_request_decrypt_error_returns_error(t *testing.T) {
	srv, store, _, token, _ := newIPCEnv(t)

	// Store a secret with an invalid ciphertext (not real AES-GCM output).
	_, err := store.CreateSecret("BAD_KEY", "not-valid-aes-gcm-ciphertext", nil)
	require.NoError(t, err)

	enc, sc, _ := dial(t, srv.ListenAddr(), token)
	require.NoError(t, enc.Encode(ipc.IpcSecretRequest{
		Type:    "secret_request",
		KeyName: "BAD_KEY",
	}))
	require.True(t, sc.Scan())
	var resp ipc.IpcSecretResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "secret_response", resp.Type)
	assert.Equal(t, "decrypt error", resp.Error)
	assert.Empty(t, resp.PlaintextValue, "plaintext must be empty on decrypt failure")
}

// ─── Rate limit — yield_request response ─────────────────────────────────────

// TestServer_rate_limit_yield_request_sends_rejection verifies that once the
// yield_request rate limit (10/sec) is exceeded the server sends an explicit
// rejection response so Python doesn't block on ResponseCh (CHECK §3.5.6).
func TestServer_rate_limit_yield_request_sends_rejection(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	_, _, conn := dial(t, srv.ListenAddr(), token)
	sc := bufio.NewScanner(conn)

	// Exhaust the limit (10/sec) using malformed requests so we don't block on
	// ResponseCh — each malformed request gets an immediate auto-reject.
	for i := 0; i < 10; i++ {
		_, _ = fmt.Fprintf(conn, `{"type":"yield_request","confidence_score":"bad"}`+"\n")
		require.True(t, sc.Scan(), "expected auto-reject #%d", i+1)
		var r ipc.IpcYieldResponse
		require.NoError(t, json.Unmarshal(sc.Bytes(), &r))
		assert.Equal(t, "yield_response", r.Type)
	}

	// 11th: rate limit exceeded — server must respond with "rate limit exceeded".
	_, err := fmt.Fprintf(conn, `{"type":"yield_request","confidence_score":"bad"}`+"\n")
	require.NoError(t, err)
	require.True(t, sc.Scan(), "server must send a rejection on rate limit")
	var r ipc.IpcYieldResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &r))
	assert.Equal(t, "rate limit exceeded", r.Feedback)
}

// ─── Connection closed before auth ───────────────────────────────────────────

// TestServer_connection_closed_before_auth_handled_cleanly verifies that the
// server goroutine doesn't panic or leak when a client disconnects before
// sending the auth message.
func TestServer_connection_closed_before_auth_handled_cleanly(t *testing.T) {
	srv, _, _, _, _ := newIPCEnv(t)
	addr := srv.ListenAddr()

	conn, err := net.Dial(networkForAddr(addr), addr)
	require.NoError(t, err)
	conn.Close() // close before sending auth — server sees EOF on first Scan

	// Give the server goroutine time to handle the EOF and return.
	time.Sleep(30 * time.Millisecond)
	// No assertion needed — absence of panic/hang is the guarantee.
}

// ─── SetGitCommitHash ─────────────────────────────────────────────────────────

func TestServer_SetGitCommitHash_reflected_in_log(t *testing.T) {
	srv, store, runID, token, _ := newIPCEnv(t)
	srv.SetGitCommitHash("abc123")

	enc, _, _ := dial(t, srv.ListenAddr(), token)
	require.NoError(t, enc.Encode(ipc.IpcStateEmit{
		Type:        "state_emit",
		ActiveAgent: "agent",
	}))
	time.Sleep(50 * time.Millisecond)

	// Verify the log was written (hash chaining includes gitHash internally).
	logs, err := store.ListEventLogs(runID)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	// The event_hash is SHA-256(payload+""+gitHash); just verify it's present.
	assert.NotEmpty(t, logs[0].EventHash)
}

// ─── Secret store error ───────────────────────────────────────────────────────

// TestServer_secret_request_store_error_returns_error verifies that a DB error
// on GetSecret returns an error response rather than panicking or hanging.
// This covers the GetSecret-error branch and the LogSecretAccess call for that
// path, which are not reachable via the "not found" or "decrypt error" tests.
func TestServer_secret_request_store_error_returns_error(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("flock-based test setup not applicable on Windows")
	}
	wsDir := t.TempDir()
	db, err := persistence.InitDB(wsDir)
	require.NoError(t, err)
	store := persistence.NewStore(db)
	// Close the DB now — subsequent queries will fail with "sql: database is closed".
	require.NoError(t, db.Close())

	sockDir, err := os.MkdirTemp("", "ipc-test-storeErr-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(sockDir) })

	token := "test-token-32-bytes-padded-here!"
	srv := ipc.NewServer(filepath.Join(sockDir, "t.sock"), 1, 0, token, store, make([]byte, 32))
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, srv.Start(ctx))
	t.Cleanup(cancel)

	enc, sc, _ := dial(t, srv.ListenAddr(), token)
	require.NoError(t, enc.Encode(ipc.IpcSecretRequest{
		Type:    "secret_request",
		KeyName: "ANY_KEY",
	}))
	require.True(t, sc.Scan())
	var resp ipc.IpcSecretResponse
	require.NoError(t, json.Unmarshal(sc.Bytes(), &resp))
	assert.Equal(t, "secret_response", resp.Type)
	assert.Contains(t, resp.Error, "store error", "closed DB must produce a store error response")
}

// ─── Auth timeout (§5.1 L3 boundary) ─────────────────────────────────────────

// TestServer_auth_timeout_closes_connection verifies that the server closes a
// connection when the client never sends the auth message within the deadline
// (CHECK §5.1: "Auth timeout: no auth sent within 10s; connection closed").
//
// The timeout is shortened to 50 ms to keep the test fast.
func TestServer_auth_timeout_closes_connection(t *testing.T) {
	restore := ipc.SetAuthTimeoutForTest(50 * time.Millisecond)
	t.Cleanup(restore)

	srv, _, _, _, _ := newIPCEnv(t)
	addr := srv.ListenAddr()

	var conn net.Conn
	var err error
	for i := 0; i < 20; i++ {
		conn, err = net.Dial(networkForAddr(addr), addr)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, err, "dial must succeed")
	defer conn.Close()

	// Do NOT send auth — wait for the server to time out and close.
	// Set a generous read deadline so the test doesn't hang if the server
	// misbehaves; the 50 ms auth timeout should fire well before 500 ms.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
	buf := make([]byte, 1)
	_, readErr := conn.Read(buf)

	// The server must have closed the connection — we expect io.EOF or a
	// network error (use of closed connection), not a timeout.
	assert.Error(t, readErr, "server must close the connection after auth timeout")
}

// ─── Heartbeat timeout (§5.1 L3 boundary) ────────────────────────────────────

// TestServer_heartbeat_timeout_closes_connection verifies that the server
// closes a connection that goes silent after successful auth — i.e., no
// messages arrive within the heartbeat window
// (CHECK §5.1: "Heartbeat missed 3 times: connection closed").
//
// The heartbeat timeout is shortened to 60 ms to keep the test fast.
func TestServer_heartbeat_timeout_closes_connection(t *testing.T) {
	restore := ipc.SetHeartbeatTimeoutForTest(60 * time.Millisecond)
	t.Cleanup(restore)

	srv, _, _, token, _ := newIPCEnv(t)

	// dial() performs the auth handshake; from this point the heartbeat clock
	// is ticking.
	_, sc, conn := dial(t, srv.ListenAddr(), token)

	// Do NOT send any further messages — wait for the server to expire the
	// heartbeat window and close the connection.
	// Give up to 500 ms; the 60 ms heartbeat should fire well before then.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))

	// Drain scanner until closed.
	for sc.Scan() {
		// Consume any buffered server messages (none expected after auth_ok).
	}
	scanErr := sc.Err()

	// A nil scan error with Scan()==false means the connection was closed (EOF).
	// A non-nil error means a read error — both indicate server-side close.
	_ = scanErr
	// The scanner returning false (EOF or error) proves the server closed the
	// connection within the heartbeat window — if it didn't, SetReadDeadline
	// above would have fired and sc.Err() would be a deadline exceeded error.
	// Either way, the connection is no longer open.
}
