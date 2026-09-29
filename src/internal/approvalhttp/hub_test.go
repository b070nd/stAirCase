package approvalhttp_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/approvalhttp"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hubGet(t *testing.T, url, token string) (int, []byte) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// TestHub_one_page_for_every_session: the hub lists the proposals of every
// running session with its name, sends a decision to the session it belongs
// to, drops a session that is gone, never sends a session's key to an address
// off this machine, and asks for its own key.
func TestHub_one_page_for_every_session(t *testing.T) {
	dir := t.TempDir()
	start := func(name, token string) (*approvalhttp.Server, context.CancelFunc) {
		srv, cancel := startServerWithToken(t, token)
		require.NoError(t, approvalhttp.Register(dir, approvalhttp.Session{Name: name, URL: "http://" + srv.ListenAddr(), Token: token}))
		return srv, cancel
	}
	a, cancelA := start("claude: add health", "key-a")
	defer cancelA()
	b, cancelB := start("seal: greeting", "key-b")
	_, chA := a.PendYield(domain.YieldRequest{AgentName: "claude-code", ActionType: "file_edit"})
	_, chB := b.PendYield(domain.YieldRequest{AgentName: "Cursor", ActionType: "file_edit"})

	hub := approvalhttp.NewHub(dir, "hub-key")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx, "127.0.0.1:0"))
	base := "http://" + hub.ListenAddr()

	code, _ := hubGet(t, base+"/v1/yields", "")
	assert.Equal(t, http.StatusUnauthorized, code)
	code, _ = hubGet(t, base+"/v1/yields", "key-a")
	assert.Equal(t, http.StatusUnauthorized, code, "a session's key is not the hub's")
	code, body := hubGet(t, base+"/v1/yields", "hub-key")
	require.Equal(t, http.StatusOK, code)
	var list []struct {
		ID      string `json:"id"`
		Session string `json:"session"`
		Request struct {
			AgentName string `json:"agent_name"`
		} `json:"request"`
	}
	require.NoError(t, json.Unmarshal(body, &list))
	require.Len(t, list, 2)
	byAgent := map[string]string{}
	var idB string
	for _, y := range list {
		byAgent[y.Request.AgentName] = y.Session
		if y.Request.AgentName == "Cursor" {
			idB = y.ID
		}
	}
	assert.Equal(t, map[string]string{"claude-code": "claude: add health", "Cursor": "seal: greeting"}, byAgent)

	req, _ := http.NewRequest(http.MethodPost, base+"/v1/yields/"+idB+"/reject", strings.NewReader(`{"feedback":"no"}`))
	req.Header.Set("Authorization", "Bearer hub-key")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	select {
	case r := <-chB:
		assert.False(t, r.Approved)
		assert.Equal(t, "no", r.Feedback)
	case <-time.After(2 * time.Second):
		t.Fatal("the decision did not reach its session")
	}
	select {
	case <-chA:
		t.Fatal("the other session got a decision it did not receive")
	default:
	}

	cancelB() // session B ends
	_, body = hubGet(t, base+"/v1/yields", "hub-key")
	require.NoError(t, json.Unmarshal(body, &list))
	require.Len(t, list, 1)
	assert.Equal(t, "claude-code", list[0].Request.AgentName)
	files, _ := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
	assert.Len(t, files, 1, "the ended session's file is removed")

	// A file naming an address off this machine is ignored, and its key is never sent there.
	if ip := nonLoopbackIP(); ip != "" {
		contacted := make(chan string, 4)
		ln, err := net.Listen("tcp", ip+":0")
		require.NoError(t, err)
		evil := &http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			contacted <- r.Header.Get("Authorization")
		}), ReadHeaderTimeout: time.Second}
		go func() { _ = evil.Serve(ln) }()
		defer func() { _ = evil.Close() }()
		require.NoError(t, approvalhttp.Register(dir, approvalhttp.Session{Name: "evil", URL: "http://" + ln.Addr().String(), Token: "stolen"}))
		_, body = hubGet(t, base+"/v1/yields", "hub-key")
		require.NoError(t, json.Unmarshal(body, &list))
		assert.Len(t, list, 1)
		select {
		case got := <-contacted:
			t.Fatalf("the hub sent a session's key (%q) to an address off this machine", got)
		case <-time.After(300 * time.Millisecond):
		}
	} else {
		t.Log("no non-loopback address to check the key stays local")
	}

	code, page := hubGet(t, base+"/", "")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, string(page), "app.js")
	assert.Equal(t, base+"/#token=hub-key", hub.ReviewURL())
}

// nonLoopbackIP is one of this machine's own non-loopback IPv4 addresses.
func nonLoopbackIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return ""
}
