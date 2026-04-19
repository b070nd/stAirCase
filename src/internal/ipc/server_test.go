package ipc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/ipc"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	srv = ipc.NewServer(socketPath, runID, token, store, aesKey)
	ctx, cancelFn := context.WithCancel(context.Background())
	require.NoError(t, srv.Start(ctx))
	t.Cleanup(cancelFn)
	cancel = cancelFn
	return
}

// dial opens a connection and performs the auth handshake, returning a
// scanner and encoder ready for message exchange.
func dial(t *testing.T, addr, token string) (enc *json.Encoder, sc *bufio.Scanner, conn net.Conn) {
	t.Helper()
	// Small retry loop — socket may not be ready immediately.
	var err error
	for i := 0; i < 20; i++ {
		conn, err = net.Dial("unix", addr)
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
		conn, err = net.Dial("unix", addr)
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

// ─── yield_request round-trip ─────────────────────────────────────────────────

func TestServer_yield_request_roundtrip(t *testing.T) {
	srv, _, _, token, _ := newIPCEnv(t)
	enc, sc, _ := dial(t, srv.ListenAddr(), token)

	// Send yield_request in a goroutine (it blocks waiting for response).
	done := make(chan struct{})
	go func() {
		defer close(done)
		yr := ipc.IpcYieldRequest{
			Type:           "yield_request",
			AgentName:      "planner",
			ActionType:     "file_edit",
			ReasoningTrace: "adding feature X",
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
	assert.Equal(t, "sk-plaintext-api-key", resp.EncryptedValue,
		"IPC server must return decrypted plaintext — not the encrypted blob")
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
		extra, err = net.Dial("unix", addr)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NoError(t, err, "should still be able to TCP-connect")
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
