package agent_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionFile writes the file a governed run gives its agent: where the run's
// hook endpoint is and its token.
func sessionFile(t *testing.T, url string) string {
	b, err := json.Marshal(map[string]string{"url": url, "token": "tok"})
	require.NoError(t, err)
	f := filepath.Join(t.TempDir(), "hook.json")
	require.NoError(t, os.WriteFile(f, b, 0o600))
	return f
}

func serve(t *testing.T, h http.HandlerFunc) string {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL + "/hook"
}

func runHook(stdin string, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = agent.RunHook(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestRunHook_forwards_the_call_and_prints_the_answer: the hook input goes
// to the run with the run's token, and the run's answer is printed as is.
func TestRunHook_forwards_the_call_and_prints_the_answer(t *testing.T) {
	var auth, body string
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"hookSpecificOutput":{"permissionDecision":"allow"}}`))
	})
	t.Setenv(agent.HookFileEnv, sessionFile(t, url))

	code, out, _ := runHook(`{"tool_name":"Edit"}`, "claude-code", "--governed")
	assert.Equal(t, 0, code)
	assert.Equal(t, `{"hookSpecificOutput":{"permissionDecision":"allow"}}`, out)
	assert.Equal(t, "Bearer tok", auth)
	assert.Equal(t, `{"tool_name":"Edit"}`, body)
}

// TestRunHook_every_failure_blocks: agents let a tool call run when its hook
// fails any other way, so every failure must exit 2.
func TestRunHook_every_failure_blocks(t *testing.T) {
	answer := func(status int) string {
		return sessionFile(t, serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	}
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	garbled := filepath.Join(t.TempDir(), "hook.json")
	require.NoError(t, os.WriteFile(garbled, []byte("not json"), 0o600))

	for _, c := range []struct {
		name, file string
		args       []string
	}{
		{"governed but no session", "", []string{"claude-code", "--governed"}},
		{"session file missing", filepath.Join(t.TempDir(), "gone.json"), []string{"claude-code"}},
		{"session file garbled", garbled, []string{"claude-code"}},
		{"token refused", answer(http.StatusUnauthorized), []string{"claude-code", "--governed"}},
		{"run failed", answer(http.StatusInternalServerError), []string{"claude-code", "--governed"}},
		{"run gone", sessionFile(t, down.URL+"/hook"), []string{"claude-code", "--governed"}},
		{"unknown agent", answer(http.StatusOK), []string{"no-such-agent", "--governed"}},
		{"no agent", answer(http.StatusOK), nil},
		{"unknown flag", answer(http.StatusOK), []string{"claude-code", "--fail-open"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(agent.HookFileEnv, c.file)
			code, out, errOut := runHook("{}", c.args...)
			assert.Equal(t, 2, code)
			assert.Empty(t, out, "nothing the agent could read as a decision")
			assert.Contains(t, errOut, "staircase hook:")
		})
	}
}

// TestRunHook_outside_a_session_passes_through: a hook installed once for a
// user or a company (no --governed) lets calls through when no governed
// session runs.
func TestRunHook_outside_a_session_passes_through(t *testing.T) {
	t.Setenv(agent.HookFileEnv, "")
	code, out, errOut := runHook("{}", "claude-code")
	assert.Equal(t, 0, code)
	assert.Empty(t, out)
	assert.Empty(t, errOut)
}

// TestRunHook_require: a company's managed hook (--require) blocks every tool
// call outside a governed session; inside one it passes the call to the
// session like the session's own hook (the session decides each call once).
func TestRunHook_require(t *testing.T) {
	var posts int
	live := serve(t, func(w http.ResponseWriter, r *http.Request) {
		posts++
		_, _ = w.Write([]byte(`{"hookSpecificOutput":{"permissionDecision":"allow"}}`))
	})
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	t.Setenv(agent.HookFileEnv, "")
	code, out, errOut := runHook(`{"tool_name":"Bash"}`, "claude-code", "--require")
	assert.Equal(t, 2, code, "no session: blocked")
	assert.Empty(t, out)
	assert.Contains(t, errOut, "staircase claude")

	t.Setenv(agent.HookFileEnv, sessionFile(t, gone.URL+"/hook"))
	code, _, _ = runHook(`{}`, "claude-code", "--require")
	assert.Equal(t, 2, code, "a session file without a live session does not count")

	t.Setenv(agent.HookFileEnv, sessionFile(t, live))
	code, out, _ = runHook(`{"tool_name":"Bash"}`, "claude-code", "--require")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, `"allow"`, "the session's answer")
	assert.Equal(t, 1, posts)
}
