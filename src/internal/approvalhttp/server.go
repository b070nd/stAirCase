// Package approvalhttp exposes a local HTTP API for out-of-process HITL approval.
//
// # Overview
//
// When an operator configures an approval webhook URL (project.WebhookURL), the
// old implementation POST-ed the yield synchronously and waited for the HTTP
// response - meaning the entire swarm stalled while the webhook provider
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
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// ─── types ────────────────────────────────────────────────────────────────────

// PendingYield is the server-side view of an in-flight yield waiting for an
// operator decision.
type PendingYield struct {
	// ID belongs to this proposal alone: it is never reused by a later proposal
	// or a later server, so a page that still shows an old card cannot decide
	// a new request.
	ID  string              `json:"id"`
	Req domain.YieldRequest `json:"request"`
	// Challenge is the SHA-256 of the request exactly as shown. A client that
	// sends it back with its decision is refused unless it still names this request.
	Challenge string    `json:"request_sha256"`
	Created   time.Time `json:"created"`
	// Payload is what a person signs to sign the decision: this text followed
	// by "approve" or "reject" (SSH signature, namespace staircase-decision).
	Payload string `json:"decision_payload,omitempty"`
	ch      chan domain.YieldResponse
}

// feedbackBody is the optional JSON body accepted by approve/reject endpoints.
type feedbackBody struct {
	Feedback      string `json:"feedback"`
	RequestSHA256 string `json:"request_sha256"`
	Signer        string `json:"signer"`
	Signature     string `json:"signature"`
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
	decided map[string]struct{} // IDs already acted on; used to return 409 vs 404
	nextID  int

	httpSrv *http.Server
	addr    string // resolved listen address, set after Start
	token   string // bearer token; empty = no auth (dev/test mode only)
	routes  func(*http.ServeMux)
}

// ─── constructor ──────────────────────────────────────────────────────────────

// NewServer creates a server that will listen on addr (e.g. "127.0.0.1:0").
// token is the shared Bearer secret required on every request.  Pass an empty
// string to disable authentication - only appropriate for tests or isolated
// local dev environments where the listener is not reachable by other users.
// Call [Start] to begin accepting connections.
func NewServer(addr, token string) *Server {
	s := newServer(token, nil)
	s.addr = addr
	s.routes = func(mux *http.ServeMux) {
		mux.HandleFunc("/v1/yields", s.requireAuth(s.handleList))
		mux.HandleFunc("/v1/yields/", s.requireAuth(s.handleYield))
	}
	s.build()
	return s
}

// newServer is a server that serves the review page and the API routes.
func newServer(token string, routes func(*http.ServeMux)) *Server {
	s := &Server{
		pending: make(map[string]*PendingYield),
		decided: make(map[string]struct{}),
		token:   token,
		routes:  routes,
	}
	s.build()
	return s
}

func (s *Server) build() {
	mux := http.NewServeMux()
	if s.routes != nil {
		s.routes(mux)
	}
	mux.HandleFunc("/", servePage)
	s.httpSrv = &http.Server{Handler: localOnly(mux), ReadHeaderTimeout: 10 * time.Second}
}

// Token is the server's key.
func (s *Server) Token() string { return s.token }

// ReviewURL is the review page's link. The token is in the fragment, which
// browsers never send to a server.
func (s *Server) ReviewURL() string {
	u := "http://" + s.ListenAddr() + "/"
	if s.token != "" {
		u += "#token=" + s.token
	}
	return u
}

//go:embed ui
var ui embed.FS

// servePage serves the review page (index.html, app.js, style.css). It may
// load only itself and talk only to this server, and cannot be framed.
func servePage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		name = "index.html"
	}
	b, err := ui.ReadFile("ui/" + name)
	if err != nil || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; "+
		"img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	_, _ = w.Write(b)
}

// localOnly refuses requests addressed to any other host name than this
// machine's: a web page on another site cannot reach the API by pointing its
// own name at 127.0.0.1 (DNS rebinding).
func localOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if hh, _, err := net.SplitHostPort(host); err == nil {
			host = hh
		}
		switch strings.Trim(host, "[]") {
		case "127.0.0.1", "localhost", "::1":
			h.ServeHTTP(w, r)
		default:
			http.Error(w, "this server answers only to 127.0.0.1 and localhost", http.StatusMisdirectedRequest)
		}
	})
}

// requireAuth wraps h and enforces Bearer token authentication when s.token is
// non-empty.  The Authorization header must be exactly "Bearer <token>".
// Constant-time comparison prevents timing attacks.
func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && !bearer(r, s.token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// bearer reports whether r carries exactly "Authorization: Bearer <token>".
func bearer(r *http.Request, token string) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
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
	go func() { // #nosec G118 -- shutdown goroutine intentionally uses Background(); ctx is already done
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
	go func() { // #nosec G118 -- shutdown goroutine intentionally uses Background(); ctx is already done
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
	return s.PendSigned(req, "")
}

// PendSigned is PendYield for a decision a person can sign: payload is
// offered to the client as decision_payload.
func (s *Server) PendSigned(req domain.YieldRequest, payload string) (id string, ch <-chan domain.YieldResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	id = fmt.Sprintf("%d-%s", s.nextID, rand.Text()[:12])
	reqJSON, _ := json.Marshal(req)
	sum := sha256.Sum256(reqJSON)
	shown := req // the challenge is of the whole request; a person is not sent the bytes of a binary file, only its size and digest
	shown.ProposedEdits = slices.Clone(req.ProposedEdits)
	for i := range shown.ProposedEdits {
		shown.ProposedEdits[i].ContentB64 = ""
	}
	py := &PendingYield{
		ID:        id,
		Req:       shown,
		Challenge: hex.EncodeToString(sum[:]),
		Created:   time.Now(),
		Payload:   payload,
		ch:        make(chan domain.YieldResponse, 1),
	}
	s.pending[id] = py
	return id, py.ch
}

// Withdraw takes a proposal back because nobody can use an answer any more
// (the run ended): a later decision for it is refused as already decided.
func (s *Server) Withdraw(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
	s.decided[id] = struct{}{}
}

// ─── HTTP handlers ────────────────────────────────────────────────────────────

// GET /v1/yields - list all pending yields.
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) { // the body is optional, not a broken one
		http.Error(w, "the decision is not valid JSON", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	py, ok := s.pending[id]
	if ok && body.RequestSHA256 != "" && body.RequestSHA256 != py.Challenge {
		s.mu.Unlock() // the proposal stays pending: the client answered a different request
		http.Error(w, "the decision names a different request than the one pending under this id", http.StatusConflict)
		return
	}
	if ok {
		delete(s.pending, id)
		s.decided[id] = struct{}{}
	}
	alreadyDecided := !ok && func() bool { _, d := s.decided[id]; return d }()
	s.mu.Unlock()

	if !ok {
		if alreadyDecided {
			// 409 Conflict: another decision already won the race (CHECK 8.6).
			http.Error(w, "yield already decided", http.StatusConflict)
		} else {
			http.Error(w, "yield not found", http.StatusNotFound)
		}
		return
	}

	resp := domain.Decide(approved, body.Feedback)
	resp.Signer, resp.Signature = body.Signer, body.Signature
	py.ch <- resp

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
		py.ch <- domain.Decide(false, reason)
		delete(s.pending, id)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
