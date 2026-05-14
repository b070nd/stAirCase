// Package approvalhttp exposes a local HTTP API for out-of-process HITL approval.
//
// # Overview
//
// When an operator configures an approval webhook URL (project.WebhookURL), the
// old implementation POST-ed the yield synchronously and waited for the HTTP
// response — meaning the entire swarm stalled while the webhook provider
// processed the request.
//
// This package provides an inbound HTTP server that the operator's tooling can
// call back on an asyncronous basis:
//
//	GET  /v1/yields           → list all pending yields (JSON array)
//	GET  /v1/yields/{id}      → get a single pending yield
//	POST /v1/yields/{id}/approve  → approve the yield (body: {"feedback":"…"})
//	POST /v1/yields/{id}/reject   → reject  the yield (body: {"feedback":"…"})
//
// The swarm goroutine calls [Server.PendYield], receives a channel, then blocks
// on that channel.  When the operator approves/rejects via HTTP the channel
// receives the response and the swarm unblocks.
//
// # Lifecycle
//
//   - [NewServer] creates an idle server bound to the given address.
//   - [Server.Start] starts the HTTP listener and registers a shutdown hook on
//     the provided context.  All pending yields are auto-rejected on shutdown.
//   - [Server.ListenAddr] returns the actual bound address (useful when port 0
//     is passed for testing).
package approvalhttp

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/staircase-core/src/internal/domain"
)

// ─── types ────────────────────────────────────────────────────────────────────

// PendingYield is the server-side view of an in-flight yield waiting for an
// operator decision.
type PendingYield struct {
	ID      string              `json:"id"`
	Req     domain.YieldRequest `json:"request"`
	Created time.Time           `json:"created"`
	ch      chan domain.YieldResponse
}

// feedbackBody is the optional JSON body accepted by approve/reject endpoints.
type feedbackBody struct {
	Feedback string `json:"feedback"`
}

// MozillaTLSConfig returns a *tls.Config that matches the Mozilla TLS
// "intermediate" compatibility profile: TLS 1.2 minimum, AEAD-only cipher
// suites, X25519+P-256 key exchange.  Pass the result to [Server.StartTLS] to
// enable HTTPS on the approval endpoint (CHECK 8.5).
//
// Reference: https://wiki.mozilla.org/Security/Server_Side_TLS#Intermediate_compatibility_(recommended)
func MozillaTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		},
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
	}
}

// Server is the inbound approval HTTP server.
type Server struct {
	mu      sync.Mutex
	pending map[string]*PendingYield
	nextID  int

	httpSrv *http.Server
	addr    string // resolved listen address, set after Start
	token   string // bearer token; empty = no auth (dev/test mode only)
}

// ─── constructor ──────────────────────────────────────────────────────────────

// NewServer creates a server that will listen on addr (e.g. "127.0.0.1:0").
// token is the shared Bearer secret required on every request.  Pass an empty
// string to disable authentication — only appropriate for tests or isolated
// local dev environments where the listener is not reachable by other users.
// Call [Start] to begin accepting connections.
func NewServer(addr, token string) *Server {
	s := &Server{
		pending: make(map[string]*PendingYield),
		addr:    addr,
		token:   token,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/yields", s.requireAuth(s.handleList))
	mux.HandleFunc("/v1/yields/", s.requireAuth(s.handleYield))
	s.httpSrv = &http.Server{Handler: mux}
	return s
}

// requireAuth wraps h and enforces Bearer token authentication when s.token is
// non-empty.  The Authorization header must be exactly "Bearer <token>".
// Constant-time comparison prevents timing attacks.
func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		h(w, r)
	}
}

// Start binds the listener, serves in a goroutine, and registers a shutdown
// hook on ctx.  Returns an error if the listener cannot be bound.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("approvalhttp: listen %s: %w", s.addr, err)
	}
	s.addr = ln.Addr().String() // capture actual port when "0" was given

	go func() {
		_ = s.httpSrv.Serve(ln) // returns ErrServerClosed on shutdown
	}()
	go func() {
		<-ctx.Done()
		// Reject all pending yields so the swarm goroutines unblock cleanly.
		s.rejectAll("server shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
	}()
	return nil
}

// StartTLS starts the server with TLS using the provided certificate and key
// files and the supplied tls.Config.  Pass [MozillaTLSConfig]() as cfg to get
// Mozilla intermediate compatibility settings (CHECK 8.5).  If cfg is nil,
// the Go default TLS config is used.
func (s *Server) StartTLS(ctx context.Context, certFile, keyFile string, cfg *tls.Config) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("approvalhttp: listen %s: %w", s.addr, err)
	}
	s.addr = ln.Addr().String()

	if cfg == nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("approvalhttp: load TLS keypair: %w", err)
	}
	cfg.Certificates = []tls.Certificate{cert}
	tlsLn := tls.NewListener(ln, cfg)

	go func() {
		_ = s.httpSrv.Serve(tlsLn)
	}()
	go func() {
		<-ctx.Done()
		s.rejectAll("server shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
	}()
	return nil
}

// ListenAddr returns the actual TCP address the server is listening on.
// Only valid after [Start] or [StartTLS] has been called.
func (s *Server) ListenAddr() string {
	return s.addr
}

// ─── public API ───────────────────────────────────────────────────────────────

// PendYield registers the yield request as pending and returns its ID and a
// channel that will receive exactly one [domain.YieldResponse] when an operator
// calls the approve/reject endpoint (or when the server shuts down).
func (s *Server) PendYield(req domain.YieldRequest) (id string, ch <-chan domain.YieldResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	id = fmt.Sprintf("%d", s.nextID)
	py := &PendingYield{
		ID:      id,
		Req:     req,
		Created: time.Now(),
		ch:      make(chan domain.YieldResponse, 1),
	}
	s.pending[id] = py
	return id, py.ch
}

// ─── HTTP handlers ────────────────────────────────────────────────────────────

// GET /v1/yields — list all pending yields.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	out := make([]*PendingYield, 0, len(s.pending))
	for _, py := range s.pending {
		out = append(out, py)
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// handleYield routes:
//
//	GET  /v1/yields/{id}
//	POST /v1/yields/{id}/approve
//	POST /v1/yields/{id}/reject
func (s *Server) handleYield(w http.ResponseWriter, r *http.Request) {
	// Strip leading "/v1/yields/" prefix.
	rest := strings.TrimPrefix(r.URL.Path, "/v1/yields/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	switch {
	case r.Method == http.MethodGet && action == "":
		s.handleGet(w, r, id)
	case r.Method == http.MethodPost && action == "approve":
		s.handleDecision(w, r, id, true)
	case r.Method == http.MethodPost && action == "reject":
		s.handleDecision(w, r, id, false)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) handleGet(w http.ResponseWriter, _ *http.Request, id string) {
	s.mu.Lock()
	py, ok := s.pending[id]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "yield not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, py)
}

func (s *Server) handleDecision(w http.ResponseWriter, r *http.Request, id string, approved bool) {
	var body feedbackBody
	_ = json.NewDecoder(r.Body).Decode(&body) // feedback is optional

	s.mu.Lock()
	py, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.mu.Unlock()

	if !ok {
		http.Error(w, "yield not found", http.StatusNotFound)
		return
	}

	py.ch <- domain.YieldResponse{
		Type:     "yield_response",
		Approved: approved,
		Feedback: body.Feedback,
	}

	action := "rejected"
	if approved {
		action = "approved"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": action, "id": id})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func (s *Server) rejectAll(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, py := range s.pending {
		py.ch <- domain.YieldResponse{
			Type:     "yield_response",
			Approved: false,
			Feedback: reason,
		}
		delete(s.pending, id)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
