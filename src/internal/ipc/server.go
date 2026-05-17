package ipc

import (
	"bufio"
	"context"
	"crypto/subtle"
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
)

const (
	// heartbeatTimeout is how long the server waits between messages before
	// treating the Python process as hung and closing the connection.
	heartbeatTimeout = 30 * time.Second

	// authTimeout is how long the server waits for the auth handshake on a
	// new connection before dropping it.
	authTimeout = 10 * time.Second

	// maxPayloadSize caps how many bytes are stored per audit log entry.
	// Prevents a misbehaving agent from inflating the DB without bound.
	maxPayloadSize = 64 * 1024 // 64 KiB

	// readBufferSize caps the per-line scanner buffer. 4× maxPayloadSize bounds
	// memory per connection while still accommodating any legal message including
	// JSON envelope overhead. Messages larger than this are rejected at the
	// framing layer (scanner returns ErrTooLong). (CHECK 3.5.9: ≤ 256 KiB)
	readBufferSize = 4 * maxPayloadSize // 256 KiB

	// maxConnections is the maximum number of simultaneously active IPC
	// connections. A single Python process only ever needs one; limiting to 2
	// accommodates a reconnect before the old connection fully tears down while
	// still guarding against runaway connection storms from a buggy agent.
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

	gitCommitHash string
	mu            sync.Mutex
	debugLog      *log.Logger
}

// NewServer constructs a Server. Call Start to begin accepting connections.
// aesKey is the workspace AES-256 key used to decrypt secrets before delivery to Python.
func NewServer(socketPath string, runID int64, token string, store *persistence.Store, aesKey []byte) *Server {
	return &Server{
		socketPath: socketPath,
		runID:      runID,
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
			go s.handleConnection(ctx, conn)
		}
	}()

	return nil
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer atomic.AddInt32(&s.connCount, -1)
	defer func() { _ = conn.Close() }()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, readBufferSize), readBufferSize)
	enc := json.NewEncoder(conn)

	// ── Auth handshake ────────────────────────────────────────────────────────
	_ = conn.SetReadDeadline(time.Now().Add(authTimeout))
	if !scanner.Scan() {
		return
	}

	var authMsg struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &authMsg); err != nil ||
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

		_ = conn.SetReadDeadline(time.Now().Add(heartbeatTimeout))
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
			s.debugLog.Printf(`{"dir":"in","ts":%q,"payload":%s}`, time.Now().Format(time.RFC3339Nano), line)
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
			secret, err := s.store.GetSecret(req.KeyName, req.ProjectID)
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
