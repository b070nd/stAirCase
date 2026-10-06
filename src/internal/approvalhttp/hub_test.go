package approvalhttp_test

import (
	"context"
	"encoding/json"
	"github.com/b070nd/stAirCase/src/internal/wslock"
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
		reg, regErr := approvalhttp.Register(dir, approvalhttp.Session{Name: name, URL: "http://" + srv.ListenAddr(), Token: token})
		require.NoError(t, regErr)
		t.Cleanup(reg.Release)
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
	files, _ := filepath.Glob(filepath.Join(dir, "sessions", "[0-9a-f]*[0-9a-f].json")) // the sessions, not hub.json
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
		reg, regErr := approvalhttp.Register(dir, approvalhttp.Session{Name: "evil", URL: "http://" + ln.Addr().String(), Token: "stolen"})
		require.NoError(t, regErr)
		t.Cleanup(reg.Release)
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

// TestHub_a_replaced_session_does_not_inherit_old_cards: a session that ends
// and is replaced at the same address must not be decided by a card the page
// still shows for the old one (F91).
func TestHub_a_replaced_session_does_not_inherit_old_cards(t *testing.T) {
	dir := t.TempDir()
	old, cancelOld := startServerWithToken(t, "key")
	addr := old.ListenAddr()
	reg, regErr := approvalhttp.Register(dir, approvalhttp.Session{Name: "run", URL: "http://" + addr, Token: "key"})
	require.NoError(t, regErr)
	t.Cleanup(reg.Release)
	old.PendYield(domain.YieldRequest{AgentName: "coder", ActionType: "file_edit", ReasoningTrace: "the old proposal"})

	hub := approvalhttp.NewHub(dir, "hub-key")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx, "127.0.0.1:0"))
	base := "http://" + hub.ListenAddr()
	var list []struct {
		ID string `json:"id"`
	}
	_, body := hubGet(t, base+"/v1/yields", "hub-key")
	require.NoError(t, json.Unmarshal(body, &list))
	require.Len(t, list, 1)
	oldCard := list[0].ID // still on someone's screen

	cancelOld()
	require.Eventually(t, func() bool {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return false
		}
		_ = ln.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond)
	fresh := approvalhttp.NewServer(addr, "key")
	fctx, fcancel := context.WithCancel(context.Background())
	defer fcancel()
	require.NoError(t, fresh.Start(fctx))
	reg2, regErr := approvalhttp.Register(dir, approvalhttp.Session{Name: "run", URL: "http://" + addr, Token: "key"})
	require.NoError(t, regErr)
	t.Cleanup(reg2.Release)
	_, ch := fresh.PendYield(domain.YieldRequest{AgentName: "coder", ActionType: "file_edit", ReasoningTrace: "a different proposal"})

	req, _ := http.NewRequest(http.MethodPost, base+"/v1/yields/"+oldCard+"/approve", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer hub-key")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	select {
	case r := <-ch:
		t.Fatalf("the old card decided the new proposal: %+v", r)
	default:
	}
}

// TestHub_only_one_coordinator_serves_a_workspace: a second hub on the same workspace is refused, naming the
// first; when the first ends (or is killed: its lock is the process's) another can start; and the registrations
// of sessions that died meanwhile are cleaned up on the way in.
func TestHub_only_one_coordinator_serves_a_workspace(t *testing.T) {
	if !wslock.Enforced {
		t.Skip("locks exclude nothing on this platform")
	}
	dir := t.TempDir()
	first := approvalhttp.NewHub(dir, "k1")
	ctx1, cancel1 := context.WithCancel(context.Background())
	require.NoError(t, first.Start(ctx1, "127.0.0.1:0"))

	second := approvalhttp.NewHub(dir, "k2")
	err := second.Start(context.Background(), "127.0.0.1:0")
	var running *approvalhttp.HubRunningError
	require.ErrorAs(t, err, &running)
	assert.Equal(t, "http://"+first.ListenAddr(), running.URL, "the refusal names the hub that is serving")

	cancel1()
	require.Eventually(t, func() bool {
		third := approvalhttp.NewHub(dir, "k3")
		ctx3, cancel3 := context.WithCancel(context.Background())
		t.Cleanup(cancel3)
		return third.Start(ctx3, "127.0.0.1:0") == nil
	}, 5*time.Second, 50*time.Millisecond, "a new hub can start once the first has ended")
}

// TestHub_a_card_of_a_dead_session_cannot_decide_its_replacement: a session is killed with a proposal pending
// and is started again at the same address with the same key (a fixed --approval-token). The old card, and the
// old proposal's id sent straight to the new session, decide nothing; the new session's own proposal can still
// be decided through the hub.
func TestHub_a_card_of_a_dead_session_cannot_decide_its_replacement(t *testing.T) {
	dir := t.TempDir()
	old, cancelOld := startServerWithToken(t, "fixed")
	addr := old.ListenAddr()
	oldReg, err := approvalhttp.Register(dir, approvalhttp.Session{Name: "run", URL: "http://" + addr, Token: "fixed"})
	require.NoError(t, err)
	oldID, _ := old.PendYield(domain.YieldRequest{AgentName: "coder", ActionType: "file_edit", ReasoningTrace: "the same words"})

	hub := approvalhttp.NewHub(dir, "hub-key")
	hctx, hcancel := context.WithCancel(context.Background())
	defer hcancel()
	require.NoError(t, hub.Start(hctx, "127.0.0.1:0"))
	base := "http://" + hub.ListenAddr()
	var list []struct {
		ID string `json:"id"`
	}
	_, body := hubGet(t, base+"/v1/yields", "hub-key")
	require.NoError(t, json.Unmarshal(body, &list))
	require.Len(t, list, 1)
	oldCard := list[0].ID

	// the process is killed: nothing is cleaned up
	cancelOld()
	oldReg.Abandon()
	require.Eventually(t, func() bool {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return false
		}
		_ = ln.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond)

	// the replacement: same address, same key, the same proposal
	fresh := approvalhttp.NewServer(addr, "fixed")
	fctx, fcancel := context.WithCancel(context.Background())
	defer fcancel()
	require.NoError(t, fresh.Start(fctx))
	newReg, err := approvalhttp.Register(dir, approvalhttp.Session{Name: "run", URL: "http://" + addr, Token: "fixed"})
	require.NoError(t, err)
	t.Cleanup(newReg.Release)
	newID, ch := fresh.PendYield(domain.YieldRequest{AgentName: "coder", ActionType: "file_edit", ReasoningTrace: "the same words"})
	require.NotEqual(t, oldID, newID, "a proposal's id is new for every proposal")

	post := func(url string) int {
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer hub-key")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusNotFound, post(base+"/v1/yields/"+oldCard+"/approve"), "the old card decides nothing")
	// the old proposal's id sent straight to the replacement, with the (fixed) key it shares
	direct, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/yields/"+oldID+"/approve", strings.NewReader(`{}`))
	direct.Header.Set("Authorization", "Bearer fixed")
	dr, err := http.DefaultClient.Do(direct)
	require.NoError(t, err)
	_ = dr.Body.Close()
	assert.Equal(t, http.StatusNotFound, dr.StatusCode, "the old proposal's id is unknown to the replacement")
	select {
	case <-ch:
		t.Fatal("the replacement's proposal was decided by an old identity")
	case <-time.After(200 * time.Millisecond):
	}

	// the replacement's own proposal is listed under its own card and can be decided
	var after []struct {
		ID string `json:"id"`
	}
	_, body = hubGet(t, base+"/v1/yields", "hub-key")
	require.NoError(t, json.Unmarshal(body, &after))
	require.Len(t, after, 1)
	assert.NotEqual(t, oldCard, after[0].ID)
	assert.Equal(t, http.StatusOK, post(base+"/v1/yields/"+after[0].ID+"/approve"))
	select {
	case resp := <-ch:
		assert.True(t, resp.Approved)
	case <-time.After(2 * time.Second):
		t.Fatal("the replacement's own proposal was not decided")
	}
}
