package approvalhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/approvalhttp"
	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// startServer starts a server with no auth token (suitable for most tests).
func startServer(t *testing.T) (*approvalhttp.Server, context.CancelFunc) {
	t.Helper()
	return startServerWithToken(t, "")
}

// startServerWithToken starts a server with the given bearer token.
func startServerWithToken(t *testing.T, token string) (*approvalhttp.Server, context.CancelFunc) {
	t.Helper()
	srv := approvalhttp.NewServer("127.0.0.1:0", token)
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, srv.Start(ctx))
	t.Cleanup(cancel)
	return srv, cancel
}

func baseURL(srv *approvalhttp.Server) string {
	return "http://" + srv.ListenAddr()
}

func pendReq(t *testing.T, srv *approvalhttp.Server) (id string, ch <-chan domain.YieldResponse) {
	t.Helper()
	req := domain.YieldRequest{
		Type:            "yield_request",
		AgentName:       "coder",
		ActionType:      "file_edit",
		ConfidenceScore: 0.85,
	}
	return srv.PendYield(req)
}

func post(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	resp, err := http.Post(url, "application/json", r) //nolint:noctx
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// ─── ListenAddr ───────────────────────────────────────────────────────────────

func TestServer_listen_addr_not_empty_after_start(t *testing.T) {
	srv, _ := startServer(t)
	assert.NotEmpty(t, srv.ListenAddr())
}

// ─── GET /v1/yields ───────────────────────────────────────────────────────────

func TestServer_list_empty(t *testing.T) {
	srv, _ := startServer(t)
	resp := get(t, baseURL(srv)+"/v1/yields")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var out []any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Empty(t, out)
}

func TestServer_list_shows_pending_yield(t *testing.T) {
	srv, _ := startServer(t)
	pendReq(t, srv)

	resp := get(t, baseURL(srv)+"/v1/yields")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out []map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out, 1)
	assert.Equal(t, "1", out[0]["id"])
}

func TestServer_list_not_allowed_methods(t *testing.T) {
	srv, _ := startServer(t)
	resp, err := http.NewRequest(http.MethodDelete, baseURL(srv)+"/v1/yields", nil)
	require.NoError(t, err)
	httpResp, err := http.DefaultClient.Do(resp)
	require.NoError(t, err)
	defer httpResp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, httpResp.StatusCode)
}

// ─── GET /v1/yields/{id} ──────────────────────────────────────────────────────

func TestServer_get_existing_yield(t *testing.T) {
	srv, _ := startServer(t)
	id, _ := pendReq(t, srv)

	resp := get(t, fmt.Sprintf("%s/v1/yields/%s", baseURL(srv), id))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, id, out["id"])
}

func TestServer_get_unknown_yield_returns_404(t *testing.T) {
	srv, _ := startServer(t)
	resp := get(t, baseURL(srv)+"/v1/yields/999")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ─── POST /v1/yields/{id}/approve ────────────────────────────────────────────

func TestServer_approve_unblocks_channel(t *testing.T) {
	srv, _ := startServer(t)
	id, ch := pendReq(t, srv)

	resp := post(t, fmt.Sprintf("%s/v1/yields/%s/approve", baseURL(srv), id),
		map[string]string{"feedback": "looks good"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case decision := <-ch:
		assert.True(t, decision.Approved)
		assert.Equal(t, "looks good", decision.Feedback)
	case <-time.After(2 * time.Second):
		t.Fatal("channel not unblocked after approve")
	}
}

func TestServer_approve_removes_from_pending(t *testing.T) {
	srv, _ := startServer(t)
	id, _ := pendReq(t, srv)

	post(t, fmt.Sprintf("%s/v1/yields/%s/approve", baseURL(srv), id), nil)

	resp := get(t, baseURL(srv)+"/v1/yields")
	var out []any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Empty(t, out, "approved yield must be removed from pending list")
}

func TestServer_approve_unknown_yield_returns_404(t *testing.T) {
	srv, _ := startServer(t)
	resp := post(t, baseURL(srv)+"/v1/yields/999/approve", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ─── POST /v1/yields/{id}/reject ─────────────────────────────────────────────

func TestServer_reject_unblocks_channel_with_false(t *testing.T) {
	srv, _ := startServer(t)
	id, ch := pendReq(t, srv)

	resp := post(t, fmt.Sprintf("%s/v1/yields/%s/reject", baseURL(srv), id),
		map[string]string{"feedback": "too risky"})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case decision := <-ch:
		assert.False(t, decision.Approved)
		assert.Equal(t, "too risky", decision.Feedback)
	case <-time.After(2 * time.Second):
		t.Fatal("channel not unblocked after reject")
	}
}

func TestServer_reject_unknown_yield_returns_404(t *testing.T) {
	srv, _ := startServer(t)
	resp := post(t, baseURL(srv)+"/v1/yields/999/reject", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ─── shutdown auto-rejects pending yields ─────────────────────────────────────

func TestServer_shutdown_rejects_pending_yields(t *testing.T) {
	srv, cancel := startServer(t)
	_, ch := pendReq(t, srv)

	cancel() // trigger shutdown

	select {
	case decision := <-ch:
		assert.False(t, decision.Approved)
		assert.Contains(t, decision.Feedback, "shutting down")
	case <-time.After(2 * time.Second):
		t.Fatal("pending yield was not rejected on shutdown")
	}
}

// ─── multiple concurrent yields ───────────────────────────────────────────────

func TestServer_multiple_yields_independent(t *testing.T) {
	srv, _ := startServer(t)
	id1, ch1 := pendReq(t, srv)
	id2, ch2 := pendReq(t, srv)

	// Approve id2 first, then reject id1.
	post(t, fmt.Sprintf("%s/v1/yields/%s/approve", baseURL(srv), id2), nil)
	post(t, fmt.Sprintf("%s/v1/yields/%s/reject", baseURL(srv), id1), nil)

	dec1 := <-ch1
	dec2 := <-ch2
	assert.False(t, dec1.Approved)
	assert.True(t, dec2.Approved)
}

// ─── approve without body still works ─────────────────────────────────────────

func TestServer_approve_without_feedback_body(t *testing.T) {
	srv, _ := startServer(t)
	id, ch := pendReq(t, srv)

	post(t, fmt.Sprintf("%s/v1/yields/%s/approve", baseURL(srv), id), nil)

	select {
	case decision := <-ch:
		assert.True(t, decision.Approved)
		assert.Empty(t, decision.Feedback)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

// ─── bearer token authentication ──────────────────────────────────────────────

func postWithToken(t *testing.T, url string, body any, token string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(http.MethodPost, url, r)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:noctx
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func getWithToken(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:noctx
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestServer_auth_missing_token_returns_401(t *testing.T) {
	srv, _ := startServerWithToken(t, "secret-token")
	pendReq(t, srv)

	// No Authorization header → 401 on GET and POST.
	resp := get(t, baseURL(srv)+"/v1/yields")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestServer_auth_wrong_token_returns_401(t *testing.T) {
	srv, _ := startServerWithToken(t, "correct-token")
	id, _ := pendReq(t, srv)

	resp := postWithToken(t, fmt.Sprintf("%s/v1/yields/%s/approve", baseURL(srv), id), nil, "wrong-token")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestServer_auth_correct_token_allows_request(t *testing.T) {
	const tok = "correct-token"
	srv, _ := startServerWithToken(t, tok)
	id, ch := pendReq(t, srv)

	resp := postWithToken(t, fmt.Sprintf("%s/v1/yields/%s/approve", baseURL(srv), id), nil, tok)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case dec := <-ch:
		assert.True(t, dec.Approved)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestServer_auth_list_requires_token(t *testing.T) {
	const tok = "list-token"
	srv, _ := startServerWithToken(t, tok)
	pendReq(t, srv)

	// Without token: 401.
	assert.Equal(t, http.StatusUnauthorized, get(t, baseURL(srv)+"/v1/yields").StatusCode)
	// With correct token: 200.
	assert.Equal(t, http.StatusOK, getWithToken(t, baseURL(srv)+"/v1/yields", tok).StatusCode)
}

// ─── concurrent decision race ──────────────────────────────────────────────────

// TestServer_decision_race_first_wins verifies that when two goroutines race to
// approve the same yield, exactly one gets HTTP 200 and the other gets 404 (the
// yield is already gone).  The channel receives exactly one response.
func TestServer_decision_race_first_wins(t *testing.T) {
	srv, _ := startServer(t)
	id, ch := pendReq(t, srv)

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp := post(t, fmt.Sprintf("%s/v1/yields/%s/approve", baseURL(srv), id), nil)
			codes[idx] = resp.StatusCode
		}(i)
	}
	wg.Wait()

	sort.Ints(codes)
	assert.Equal(t, []int{http.StatusOK, http.StatusNotFound}, codes, "exactly one winner, one loser")

	select {
	case dec := <-ch:
		assert.True(t, dec.Approved)
	case <-time.After(2 * time.Second):
		t.Fatal("channel should receive exactly one response")
	}
}
