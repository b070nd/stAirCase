package approvalhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
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
)

// Session is a running session's approval server, as it registers itself so
// the hub can find it. Its file is readable by the user only: it holds the
// session's key.
type Session struct {
	Name  string `json:"name"`
	URL   string `json:"url"` // http://127.0.0.1:port
	Token string `json:"token"`
}

func sessionsDir(wsDir string) string { return filepath.Join(wsDir, "sessions") }

func sessionFile(wsDir string, s Session) string {
	sum := sha256.Sum256([]byte(s.URL))
	return filepath.Join(sessionsDir(wsDir), hex.EncodeToString(sum[:6])+".json")
}

// Register writes the session's file in the workspace.
func Register(wsDir string, s Session) error {
	if err := os.MkdirAll(sessionsDir(wsDir), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(s)
	return os.WriteFile(sessionFile(wsDir, s), b, 0o600)
}

// Unregister removes the session's file.
func Unregister(wsDir string, s Session) { _ = os.Remove(sessionFile(wsDir, s)) }

// Hub is one review page for every running session of a workspace: it lists
// their waiting proposals and forwards each decision to its session. The
// browser knows only the hub's key; the sessions' keys stay here.
type Hub struct {
	wsDir string
	token string
	srv   *Server // serves the page and listens; its API is replaced by the hub's
	http  *http.Client
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

// Start listens on addr until ctx ends.
func (h *Hub) Start(ctx context.Context, addr string) error {
	h.srv.addr = addr
	return h.srv.Start(ctx)
}

// ListenAddr is the bound address.
func (h *Hub) ListenAddr() string { return h.srv.ListenAddr() }

// ReviewURL is the page's link, the key in the fragment.
func (h *Hub) ReviewURL() string { return h.srv.ReviewURL() }

func (h *Hub) auth(f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
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
		if err != nil || json.Unmarshal(b, &s) != nil || !loopback(s.URL) {
			continue
		}
		out[strings.TrimSuffix(filepath.Base(f), ".json")] = s
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
				_ = os.Remove(filepath.Join(sessionsDir(h.wsDir), key+".json"))
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
