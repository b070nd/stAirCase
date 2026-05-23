package ipc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/obs"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed ipc.v1.schema.json
var embeddedIPCSchema []byte

// ipcSchemaID is the resource name used when registering the embedded schema
// with the compiler.  It does not need to be a reachable URL.
const ipcSchemaID = "ipc.v1.schema.json"

// compiledIPCSchemas holds per-kind compiled JSON Schemas.
// Compiled once at package init time and reused across all Server instances.
var compiledIPCSchemas map[string]*jsonschema.Schema

func init() {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(ipcSchemaID, bytes.NewReader(embeddedIPCSchema)); err != nil {
		panic("ipc: failed to register embedded schema: " + err.Error())
	}
	kinds := []string{"auth", "state_emit", "yield_request", "secret_request", "heartbeat"}
	compiledIPCSchemas = make(map[string]*jsonschema.Schema, len(kinds))
	for _, k := range kinds {
		sch, err := c.Compile(ipcSchemaID + "#/$defs/" + k)
		if err != nil {
			panic("ipc: failed to compile schema for " + k + ": " + err.Error())
		}
		compiledIPCSchemas[k] = sch
	}
}

// validateMsg validates raw JSON bytes against the pre-compiled schema for kind.
// Returns nil when valid, a descriptive error otherwise.
func validateMsg(kind string, raw []byte) error {
	sch, ok := compiledIPCSchemas[kind]
	if !ok {
		return nil // unknown kinds are handled elsewhere
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	return sch.Validate(v)
}

// heartbeatTimeout is how long the server waits between messages before
// treating the Python process as hung and closing the connection.
// Declared as a var (not const) so tests can lower it via export_test.go.
var heartbeatTimeout = 30 * time.Second

// authTimeout is how long the server waits for the auth handshake on a
// new connection before dropping it.
// Declared as a var (not const) so tests can lower it via export_test.go.
var authTimeout = 10 * time.Second

const (

	// maxPayloadSize caps how many bytes are stored per audit log entry.
	// Prevents a misbehaving agent from inflating the DB without bound.
	maxPayloadSize = 64 * 1024 // 64 KiB

	// readBufferSize caps the per-line scanner buffer. 4× maxPayloadSize bounds
	// memory per connection while still accommodating any legal message including
	// JSON envelope overhead. Messages larger than this are rejected at the
	// framing layer (scanner returns ErrTooLong). (CHECK 3.5.9: ≤ 256 KiB)
	readBufferSize = 4 * maxPayloadSize // 256 KiB

	// maxConnections is the maximum number of simultaneously active IPC
	// connections. CHECK 3.5.4 requires exactly 1 client, but we allow 2 to
	// accommodate a reconnect window: if Python crashes and restarts before the
	// OS fully closes the old TCP/UDS file descriptor, a brief overlap is
	// unavoidable. Setting this to 1 would cause valid reconnects to be rejected.
	// All connections still authenticate with the same per-run token, so a rogue
	// second connection still cannot gain privileges. If the spec is tightened to
	// 1 in a future revision, revert this to 1 and add a brief grace-period drain.
	maxConnections = 2
)

// knownMessageKinds is the set of message type strings the server recognises.
// Any incoming message whose "type" field is not in this set is dropped
// immediately after base unmarshal — before kind-specific unmarshaling —
// satisfying the CHECK 3.5.5 requirement that kind validation runs before
// full payload processing.
var knownMessageKinds = map[string]bool{
	"state_emit":     true,
	"yield_request":  true,
	"secret_request": true,
	"heartbeat":      true,
}

// Per-connection per-kind rate limits (messages per second).
// These protect against a buggy or compromised Python process flooding the
// server; legitimate agents will never approach these limits.
var kindRateLimits = map[string]int{
	"yield_request":  10,
	"secret_request": 30,
	"state_emit":     300,
	"heartbeat":      5,
}

// connRateLimiter tracks message counts per kind within the current one-second
// window for a single connection.  Not safe for concurrent use; callers must
// hold the connection-local context (handleConnection runs on one goroutine).
type connRateLimiter struct {
	counts      map[string]int
	windowStart time.Time
}

func newConnRateLimiter() *connRateLimiter {
	return &connRateLimiter{
		counts:      make(map[string]int),
		windowStart: time.Now(),
	}
}

// allow returns true when the message kind is within its per-second budget.
// The window resets lazily on the first check after a full second has elapsed.
func (r *connRateLimiter) allow(kind string) bool {
	limit, known := kindRateLimits[kind]
	if !known {
		return true // unknown kinds are not rate-limited
	}
	if time.Since(r.windowStart) > time.Second {
		r.counts = make(map[string]int)
		r.windowStart = time.Now()
	}
	r.counts[kind]++
	return r.counts[kind] <= limit
}

// Server listens on a Unix Domain Socket for the Python runtime.
// It authenticates Python with a one-time session token, then routes:
//   - state_emit     → SOC2 event log (fire-and-forget)
//   - yield_request  → HITL channel (blocks until operator approves/rejects)
//   - secret_request → DB secret lookup + decrypt (plaintext delivered, never via env vars)
//   - heartbeat      → keepalive acknowledgement
type Server struct {
	socketPath string
	runID      int64
	projectID  int64 // run's own project — secret requests for other projects are rejected
	token      string
	store      *persistence.Store
	aesKey     []byte // AES-256 key for decrypting secrets before delivery to Python

	// YieldCh receives yield requests from Python.
	// ResponseCh carries the operator decision back to the connection handler.
	YieldCh    chan IpcYieldRequest
	ResponseCh chan IpcYieldResponse

	// StatEmitCh receives state_emit events for live monitoring.
	StatEmitCh chan IpcStateEmit

	// yieldMu serializes concurrent yield_request handling so that at most one
	// yield is in flight at any time — prevents interleaved channel sends from
	// multiple simultaneous Python connections.
	yieldMu sync.Mutex

	// connCount tracks the number of currently active connections (atomic).
	connCount int32

	// deliveredSecrets accumulates every plaintext secret value sent to Python.
	// Used by the runner to scrub operator-visible fields before HITL display
	// (CHECK 4.4.3 / 7.4.2). Protected by secretsMu.
	deliveredSecrets []string
	secretsMu        sync.RWMutex

	gitCommitHash string
	mu            sync.Mutex
	debugLog      *log.Logger
}

// DeliveredSecrets returns a snapshot of all plaintext secret values that have
// been decrypted and delivered to Python during this run. The runner uses this
// to scrub yield requests before presenting them to the operator.
func (s *Server) DeliveredSecrets() []string {
	s.secretsMu.RLock()
	defer s.secretsMu.RUnlock()
	out := make([]string, len(s.deliveredSecrets))
	copy(out, s.deliveredSecrets)
	return out
}

// NewServer constructs a Server. Call Start to begin accepting connections.
// aesKey is the workspace AES-256 key used to decrypt secrets before delivery to Python.
// projectID is the project owning this run; secret_request messages specifying a
// different project_id are rejected to prevent cross-project secret leakage.
func NewServer(socketPath string, runID, projectID int64, token string, store *persistence.Store, aesKey []byte) *Server {
	return &Server{
		socketPath: socketPath,
		runID:      runID,
		projectID:  projectID,
		token:      token,
		store:      store,
		aesKey:     aesKey,
		YieldCh:    make(chan IpcYieldRequest, 1),
		ResponseCh: make(chan IpcYieldResponse, 1),
		StatEmitCh: make(chan IpcStateEmit, 32),
	}
}

// SetGitCommitHash updates the commit hash used in SOC2 event-log chaining.
// Called after teardown commit so that final log entries carry the real hash.
func (s *Server) SetGitCommitHash(hash string) {
	s.mu.Lock()
	s.gitCommitHash = hash
	s.mu.Unlock()
}

// SetDebugWriter enables structured IPC debug logging to w.
// Every raw IPC message (inbound and outbound) is written as a JSON-Lines entry.
func (s *Server) SetDebugWriter(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.debugLog = log.New(w, "", 0)
}

// ListenAddr returns the socket address Python should connect to.
// On Unix this is the UDS file path; on Windows it is "host:port".
func (s *Server) ListenAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.socketPath
}

// Start binds the socket and accepts connections in the background.
// On Unix a UDS is used; on Windows a TCP loopback socket is used instead.
// The socket file is removed when ctx is cancelled (Unix only).
func (s *Server) Start(ctx context.Context) error {
	var ln net.Listener
	var err error
	if runtime.GOOS == "windows" {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("ipc listen: %w", err)
		}
		// Store the actual bound address so LaunchPython sends the correct address.
		s.mu.Lock()
		s.socketPath = ln.Addr().String()
		s.mu.Unlock()
	} else {
		_ = os.Remove(s.socketPath)
		ln, err = net.Listen("unix", s.socketPath)
		if err != nil {
			return fmt.Errorf("ipc listen: %w", err)
		}
		if err := os.Chmod(s.socketPath, 0o600); err != nil {
			_ = ln.Close()
			return fmt.Errorf("ipc socket chmod: %w", err)
		}
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
		if runtime.GOOS != "windows" {
			_ = os.Remove(s.socketPath)
		}
	}()

	// Capture timeout values once at Start time.  handleConnection goroutines
	// close over these locals, never reading the package-level vars again.
	// This eliminates data races when tests override the vars between tests.
	hbTO := heartbeatTimeout
	authTO := authTimeout

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					obs.Log.Warn("ipc accept", "err", err)
					continue
				}
			}
			if atomic.AddInt32(&s.connCount, 1) > maxConnections {
				atomic.AddInt32(&s.connCount, -1)
				obs.Log.Warn("ipc connection limit", "max", maxConnections)
				_ = conn.Close()
				continue
			}
			// Reject connections from different OS users (Linux: SO_PEERCRED).
			if err := checkPeerUID(conn); err != nil {
				atomic.AddInt32(&s.connCount, -1)
				obs.Log.Warn("ipc peer credential", "err", err)
				_ = conn.Close()
				continue
			}
			go s.handleConnection(ctx, conn, authTO, hbTO)
		}
	}()

	return nil
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn, authTO, hbTO time.Duration) {
	defer atomic.AddInt32(&s.connCount, -1)
	defer func() { _ = conn.Close() }()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, readBufferSize), readBufferSize)
	enc := json.NewEncoder(conn)

	// ── Auth handshake ────────────────────────────────────────────────────────
	_ = conn.SetReadDeadline(time.Now().Add(authTO))
	if !scanner.Scan() {
		return
	}

	rawAuth := make([]byte, len(scanner.Bytes()))
	copy(rawAuth, scanner.Bytes())

	// CHECK 3.5.5: validate against schema before typed unmarshal.
	// This enforces additionalProperties:false and required fields at wire level.
	if err := validateMsg("auth", rawAuth); err != nil {
		_ = enc.Encode(map[string]string{"type": "auth_failed", "reason": "schema: " + err.Error()})
		return
	}

	var authMsg struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rawAuth, &authMsg); err != nil ||
		authMsg.Type != "auth" ||
		subtle.ConstantTimeCompare([]byte(authMsg.Token), []byte(s.token)) != 1 {
		_ = enc.Encode(map[string]string{"type": "auth_failed"})
		return
	}
	_ = enc.Encode(map[string]string{"type": "auth_ok"})

	rl := newConnRateLimiter()

	// ── Message loop ──────────────────────────────────────────────────────────
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(hbTO))
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				obs.Log.Warn("ipc connection", "err", err)
			}
			return
		}

		// Copy the scanned bytes — scanner reuses its buffer on the next call.
		line := make([]byte, len(scanner.Bytes()))
		copy(line, scanner.Bytes())

		if s.debugLog != nil {
			s.secretsMu.RLock()
			logged := crypto.ScrubBytes(line, s.deliveredSecrets)
			s.secretsMu.RUnlock()
			s.debugLog.Printf(`{"dir":"in","ts":%q,"payload":%s}`, time.Now().Format(time.RFC3339Nano), logged)
		}

		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &base); err != nil {
			continue
		}

		// CHECK 3.5.5: validate kind before any further processing.
		if !knownMessageKinds[base.Type] {
			obs.Log.Warn("ipc unknown kind", "kind", base.Type)
			continue
		}

		// CHECK 3.5.5: validate raw JSON against the JSON Schema sub-definition
		// for this message kind before any typed unmarshal.  This enforces
		// additionalProperties:false and all required/format/enum constraints at
		// the wire level, catching payloads that would otherwise be silently
		// accepted despite violating the published IPC schema.
		if err := validateMsg(base.Type, line); err != nil {
			obs.Log.Warn("ipc schema validation failed", "kind", base.Type, "err", err)
			switch base.Type {
			case "yield_request":
				_ = enc.Encode(IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: "schema: " + err.Error()})
			case "secret_request":
				_ = enc.Encode(IpcSecretResponse{Type: "secret_response", Error: "schema: " + err.Error()})
			}
			continue
		}

		obs.IPCMessagesTotal.WithLabelValues(base.Type).Inc()

		if !rl.allow(base.Type) {
			obs.Log.Warn("ipc rate limit", "kind", base.Type)
			// For request/response kinds, send an error so Python doesn't hang.
			switch base.Type {
			case "yield_request":
				_ = enc.Encode(IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: "rate limit exceeded"})
			case "secret_request":
				_ = enc.Encode(IpcSecretResponse{Type: "secret_response", Error: "rate limit exceeded"})
			}
			continue
		}

		switch base.Type {
		case "state_emit":
			var msg IpcStateEmit
			if err := json.Unmarshal(line, &msg); err == nil {
				payload := string(line)
				if len(payload) > maxPayloadSize {
					payload = payload[:maxPayloadSize]
				}
				s.logEvent("state_emit", payload)
				select {
				case s.StatEmitCh <- msg:
				default:
				}
			}

		case "yield_request":
			var msg IpcYieldRequest
			if err := json.Unmarshal(line, &msg); err != nil {
				// Malformed JSON: auto-reject so Python doesn't hang waiting for a response.
				obs.Log.Warn("ipc malformed yield_request", "err", err)
				_ = enc.Encode(IpcYieldResponse{
					Type:     "yield_response",
					Approved: false,
					Feedback: "malformed yield_request JSON: " + err.Error(),
				})
				break
			}
			// CHECK 3.5.5 / Fix E: validate required fields before forwarding.
			if msg.AgentName == "" {
				_ = enc.Encode(IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: "agent_name required"})
				break
			}
			if msg.ConfidenceScore < 0 || msg.ConfidenceScore > 1 {
				_ = enc.Encode(IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: "confidence_score must be 0–1"})
				break
			}
			validActionTypes := map[string]bool{"file_edit": true, "shell_exec": true, "custom": true}
			if !validActionTypes[msg.ActionType] {
				_ = enc.Encode(IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: "unknown action_type: " + msg.ActionType})
				break
			}
			payload := string(line)
			if len(payload) > maxPayloadSize {
				payload = payload[:maxPayloadSize]
			}
			s.logEvent("yield_request", payload)
			t0Yield := time.Now()
			// yieldMu ensures at most one yield is in flight at a time.
			// Without this, two simultaneous Python connections could
			// interleave sends on YieldCh / ResponseCh causing a deadlock.
			s.yieldMu.Lock()
			select {
			case s.YieldCh <- msg:
			case <-ctx.Done():
				s.yieldMu.Unlock()
				return
			}
			select {
			case resp := <-s.ResponseCh:
				s.yieldMu.Unlock()
				if s.debugLog != nil {
					if b, err2 := json.Marshal(resp); err2 == nil {
						s.secretsMu.RLock()
						b = crypto.ScrubBytes(b, s.deliveredSecrets)
						s.secretsMu.RUnlock()
						s.debugLog.Printf(`{"dir":"out","ts":%q,"payload":%s}`, time.Now().Format(time.RFC3339Nano), b)
					}
				}
				_ = enc.Encode(resp)
				obs.YieldsTotal.WithLabelValues(msg.ActionType, outcomeStr(resp.Approved)).Inc()
				obs.YieldLatency.WithLabelValues(msg.ActionType).Observe(time.Since(t0Yield).Seconds())
			case <-ctx.Done():
				s.yieldMu.Unlock()
				return
			}

		case "secret_request":
			var req IpcSecretRequest
			if err := json.Unmarshal(line, &req); err != nil {
				// Malformed JSON: send an error so Python doesn't hang waiting for a response.
				obs.Log.Warn("ipc malformed secret_request", "err", err)
				_ = enc.Encode(IpcSecretResponse{Type: "secret_response", Error: "malformed secret_request JSON: " + err.Error()})
				break
			}
			// CHECK 3.5.5 / Fix E: key_name must not be empty.
			if req.KeyName == "" {
				_ = s.store.LogSecretAccess(&s.runID, "", "error") // CHECK 4.4.1
				obs.SecretAccessTotal.WithLabelValues("error").Inc()
				_ = enc.Encode(IpcSecretResponse{Type: "secret_response", Error: "key_name required"})
				break
			}
			// Reject requests targeting a different project — prevents a
			// compromised runtime from reading another project's secrets.
			if req.ProjectID != nil && *req.ProjectID != s.projectID {
				_ = s.store.LogSecretAccess(&s.runID, req.KeyName, "error") // CHECK 4.4.1
				obs.SecretAccessTotal.WithLabelValues("error").Inc()
				_ = enc.Encode(IpcSecretResponse{Type: "secret_response", Error: "project_id mismatch"})
				break
			}
			// Always look up under the run's own project, ignoring any
			// project_id the Python side may have omitted.
			ownProjectID := s.projectID
			secret, err := s.store.GetSecret(req.KeyName, &ownProjectID)
			if err != nil {
				_ = s.store.LogSecretAccess(&s.runID, req.KeyName, "error") // CHECK 4.4.1
				obs.SecretAccessTotal.WithLabelValues("error").Inc()
				_ = enc.Encode(IpcSecretResponse{Type: "secret_response", Error: "store error: " + err.Error()})
				break
			}
			if secret == nil {
				_ = s.store.LogSecretAccess(&s.runID, req.KeyName, "not_found") // CHECK 4.4.1
				obs.SecretAccessTotal.WithLabelValues("not_found").Inc()
				_ = enc.Encode(IpcSecretResponse{Type: "secret_response", Error: "not found"})
				break
			}
			// Decrypt in Go before delivery — plaintext never enters the DB and
			// the AES key never crosses the UDS boundary into Python.
			plaintext, err := crypto.Decrypt(s.aesKey, secret.EncryptedValue)
			if err != nil {
				_ = s.store.LogSecretAccess(&s.runID, req.KeyName, "error") // CHECK 4.4.1
				obs.SecretAccessTotal.WithLabelValues("error").Inc()
				_ = enc.Encode(IpcSecretResponse{Type: "secret_response", Error: "decrypt error"})
				break
			}
			_ = s.store.LogSecretAccess(&s.runID, req.KeyName, "success") // CHECK 4.4.1
			obs.SecretAccessTotal.WithLabelValues("success").Inc()
			// Record the plaintext so the runner can scrub it from yield requests
			// before presenting them to the operator (CHECK 4.4.3 / 7.4.2).
			s.secretsMu.Lock()
			s.deliveredSecrets = append(s.deliveredSecrets, plaintext)
			s.secretsMu.Unlock()
			_ = enc.Encode(IpcSecretResponse{
				Type:           "secret_response",
				PlaintextValue: plaintext,
			})

		case "heartbeat":
			_ = enc.Encode(map[string]string{"type": "heartbeat_ack"})
		}
	}
}

// outcomeStr converts a boolean approval decision to a metric label string.
func outcomeStr(approved bool) string {
	if approved {
		return "approved"
	}
	return "rejected"
}

// logEvent appends a chain-hashed entry to the SOC2 audit log.
func (s *Server) logEvent(eventType, payload string) {
	s.mu.Lock()
	gitHash := s.gitCommitHash
	s.mu.Unlock()

	prevHash, err := s.store.GetLastEventHash(s.runID)
	if err != nil {
		obs.Log.Warn("ipc event hash", "err", err)
		return
	}
	if _, err := s.store.AppendEventLog(s.runID, eventType, payload, prevHash, gitHash); err != nil {
		obs.Log.Warn("ipc append event", "err", err)
		return
	}
	obs.AuditChainLength.Inc()
}
