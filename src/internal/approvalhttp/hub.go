package approvalhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/wslock"
)

// Session is a running session's approval server, as it registers itself so
// the hub can find it. Its file is readable by the user only: it holds the
// session's key.
type Session struct {
	Name  string `json:"name"`
	URL   string `json:"url"` // http://127.0.0.1:port
	Token string `json:"token"`
	// Instance names this one process's session, new every time one starts. Everything the hub hands out is keyed
	// by it, so a replacement session that reuses the same port and key is never taken for the one it replaced.
	Instance string `json:"instance"`
	PID      int    `json:"pid"`
	Started  string `json:"started"` // RFC 3339
}

func sessionsDir(wsDir string) string { return filepath.Join(wsDir, "sessions") }

func sessionFile(wsDir, instance string) string {
	return filepath.Join(sessionsDir(wsDir), instance+".json")
}

func sessionLock(wsDir, instance string) string {
	return filepath.Join(sessionsDir(wsDir), instance+".lock")
}

// Registration is a session's claim in the workspace: its file, and a lock on its lock file that the process
// holds for as long as it lives. A file whose lock is free belongs to a process that is gone, however it ended.
type Registration struct {
	wsDir string
	S     Session
	lock  *os.File
}

// Register writes the session's file in the workspace and takes its liveness lock. The session gets a new
// Instance (any it came with is replaced). Call Release when the session ends.
func Register(wsDir string, s Session) (*Registration, error) {
	if err := os.MkdirAll(sessionsDir(wsDir), 0o700); err != nil {
		return nil, err
	}
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	s.Instance, s.PID, s.Started = hex.EncodeToString(id), os.Getpid(), time.Now().UTC().Format(time.RFC3339)
	lock, err := os.OpenFile(sessionLock(wsDir, s.Instance), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := wslock.LockExclusive(lock.Fd()); err != nil {
		_ = lock.Close()
		return nil, err
	}
	b, _ := json.Marshal(s)
	if err := os.WriteFile(sessionFile(wsDir, s.Instance), b, 0o600); err != nil {
		_ = wslock.Unlock(lock.Fd())
		_ = lock.Close()
		return nil, err
	}
	return &Registration{wsDir: wsDir, S: s, lock: lock}, nil
}

// Abandon drops the lock without removing the files: what a process that is killed leaves behind. For tests.
func (r *Registration) Abandon() {
	_ = wslock.Unlock(r.lock.Fd())
	_ = r.lock.Close()
}

// Release removes the session's files and its lock.
func (r *Registration) Release() {
	if r == nil {
		return
	}
	_ = os.Remove(sessionFile(r.wsDir, r.S.Instance))
	_ = os.Remove(sessionLock(r.wsDir, r.S.Instance))
	_ = wslock.Unlock(r.lock.Fd())
	_ = r.lock.Close()
}

// alive reports whether the process that registered the session still lives. Where locks exclude nothing it
// says yes: the hub's own failed call is then what removes a session.
func alive(wsDir, instance string) bool {
	if !wslock.Enforced {
		return true
	}
	f, err := os.OpenFile(sessionLock(wsDir, instance), os.O_RDWR, 0)
	if err != nil {
		return false // no lock file: not a session of this version, or already released
	}
	defer func() { _ = f.Close() }()
	if err := wslock.LockExclusive(f.Fd()); err != nil {
		return true
	}
	_ = wslock.Unlock(f.Fd())
	return false
}

// Reconcile removes the registrations of sessions whose process is gone (a kill -9 leaves them behind) and
// returns them. It touches nothing but the workspace's sessions directory: no repository, no database.
func Reconcile(wsDir string) []Session {
	var gone []Session
	files, _ := filepath.Glob(filepath.Join(sessionsDir(wsDir), "*.json"))
	for _, f := range files {
		instance := strings.TrimSuffix(filepath.Base(f), ".json")
		if instance == "hub" || alive(wsDir, instance) {
			continue
		}
		var s Session
		if b, err := os.ReadFile(f); err == nil {
			_ = json.Unmarshal(b, &s)
		}
		_ = os.Remove(f)
		_ = os.Remove(sessionLock(wsDir, instance))
		gone = append(gone, s)
	}
	return gone
}

// Hub is one review page for every running session of a workspace: it lists
// their waiting proposals and forwards each decision to its session. The
// browser knows only the hub's key; the sessions' keys stay here.
type Hub struct {
	wsDir   string
	token   string
	srv     *Server // serves the page and listens; its API is replaced by the hub's
	http    *http.Client
	removed []Session
}

// NewHub creates a hub for the sessions registered in wsDir.
func NewHub(wsDir, token string) *Hub {
	h := &Hub{wsDir: wsDir, token: token, http: &http.Client{Timeout: 5 * time.Second}}
	h.srv = newServer(token, func(mux *http.ServeMux) {
		mux.HandleFunc("/v1/yields", h.auth(h.list))
		mux.HandleFunc("/v1/yields/", h.auth(h.yield))
	})
	return h
}

// HubRunningError says another hub already serves this workspace: there is one coordinator at a time.
type HubRunningError struct{ URL string }

func (e *HubRunningError) Error() string {
	if e.URL == "" {
		return "another `staircase serve` is already serving this workspace"
	}
	return "another `staircase serve` is already serving this workspace at " + e.URL
}

// Start listens on addr until ctx ends. Only one hub serves a workspace: it takes the workspace's hub lock first
// (held by the process for its life, so a hub that was killed leaves it free) and a second Start returns a
// *HubRunningError. Registrations whose process is gone are removed on the way in.
func (h *Hub) Start(ctx context.Context, addr string) error {
	if err := os.MkdirAll(sessionsDir(h.wsDir), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(sessionsDir(h.wsDir), "hub.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := wslock.LockExclusive(lock.Fd()); err != nil {
		_ = lock.Close()
		var url struct{ URL string }
		if b, rerr := os.ReadFile(filepath.Join(sessionsDir(h.wsDir), "hub.json")); rerr == nil {
			_ = json.Unmarshal(b, &url)
		}
		return &HubRunningError{URL: url.URL}
	}
	h.removed = Reconcile(h.wsDir)
	h.srv.addr = addr
	if err := h.srv.Start(ctx); err != nil {
		_ = wslock.Unlock(lock.Fd())
		_ = lock.Close()
		return err
	}
	b, _ := json.Marshal(map[string]any{"url": "http://" + h.srv.ListenAddr(), "pid": os.Getpid()})
	_ = os.WriteFile(filepath.Join(sessionsDir(h.wsDir), "hub.json"), b, 0o600)
	go func() { // the hub's claim ends with the hub
		<-ctx.Done()
		_ = os.Remove(filepath.Join(sessionsDir(h.wsDir), "hub.json"))
		_ = wslock.Unlock(lock.Fd())
		_ = lock.Close()
	}()
	return nil
}

// Removed is the registrations of dead sessions that Start cleaned up.
func (h *Hub) Removed() []Session { return h.removed }

// ListenAddr is the bound address.
func (h *Hub) ListenAddr() string { return h.srv.ListenAddr() }

// ReviewURL is the page's link, the key in the fragment.
func (h *Hub) ReviewURL() string { return h.srv.ReviewURL() }

func (h *Hub) auth(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !bearer(r, h.token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		f(w, r)
	}
}

// sessions reads the registered sessions; a file that is unreadable or names
// an address off this machine is ignored: its key is never sent anywhere.
func (h *Hub) sessions() map[string]Session {
	out := map[string]Session{}
	files, _ := filepath.Glob(filepath.Join(sessionsDir(h.wsDir), "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		var s Session
		if err != nil || json.Unmarshal(b, &s) != nil || !loopback(s.URL) || s.Instance == "" || s.Instance == "hub" ||
			s.Instance != strings.TrimSuffix(filepath.Base(f), ".json") || !alive(h.wsDir, s.Instance) {
			continue // unreadable, off this machine, not of this version, or its process is gone: its key is never sent anywhere
		}
		out[s.Instance] = s
	}
	return out
}

func loopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
}

// call sends a request to a session with its key.
func (h *Hub) call(s Session, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(method, s.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	return h.http.Do(req)
}

func (h *Hub) list(w http.ResponseWriter, _ *http.Request) {
	out := []map[string]any{}
	for key, s := range h.sessions() {
		resp, err := h.call(s, http.MethodGet, "/v1/yields", nil)
		if err != nil {
			var ne net.Error
			if !errors.As(err, &ne) || !ne.Timeout() { // gone, not slow
				_ = os.Remove(sessionFile(h.wsDir, key))
			}
			continue
		}
		var ys []map[string]any
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&ys)
		_ = resp.Body.Close()
		if err != nil {
			continue
		}
		for _, y := range ys {
			id, _ := y["id"].(string)
			y["id"], y["session"] = key+"~"+id, s.Name
			out = append(out, y)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// yield forwards GET /v1/yields/{session~id}[/approve|reject].
func (h *Hub) yield(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/yields/")
	id, action, _ := strings.Cut(rest, "/")
	key, sid, ok := strings.Cut(id, "~")
	s, known := h.sessions()[key]
	if !ok || !known || strings.ContainsAny(sid, "/?#") {
		http.Error(w, "yield not found", http.StatusNotFound)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	resp, err := h.call(s, r.Method, "/v1/yields/"+sid+"/"+action, body)
	if err != nil {
		http.Error(w, "the session is not reachable", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 8<<20))
}
