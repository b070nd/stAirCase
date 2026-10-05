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

	code, out, _ := runHook(`{"hook_event_name":"PreToolUse","tool_name":"Edit"}`, "claude-code", "--governed")
	assert.Equal(t, 0, code)
	assert.Equal(t, `{"hookSpecificOutput":{"permissionDecision":"allow"}}`, out)
	assert.Equal(t, "Bearer tok", auth)
	assert.Equal(t, `{"hook_event_name":"PreToolUse","tool_name":"Edit"}`, body)
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
	code, out, errOut := runHook(`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`, "claude-code", "--require")
	assert.Equal(t, 2, code, "no session: blocked")
	assert.Empty(t, out)
	assert.Contains(t, errOut, "staircase claude")

	t.Setenv(agent.HookFileEnv, sessionFile(t, gone.URL+"/hook"))
	code, _, _ = runHook(`{"hook_event_name":"PreToolUse"}`, "claude-code", "--require")
	assert.Equal(t, 2, code, "a session file without a live session does not count")

	t.Setenv(agent.HookFileEnv, sessionFile(t, live))
	code, out, _ = runHook(`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`, "claude-code", "--require")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, `"allow"`, "the session's answer")
	assert.Equal(t, 1, posts)
}

// TestRunHook_file: --file names the session, for an agent that does not pass
// our environment on to its hooks (Gemini CLI); it wins over the variable.
func TestRunHook_file(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"decision":"allow"}`)) })
	file := sessionFile(t, url)
	t.Setenv(agent.HookFileEnv, "")
	call := `{"hook_event_name":"BeforeTool","tool_name":"read_file"}`
	code, out, _ := runHook(call, "gemini", "--governed", "--file", file)
	assert.Equal(t, 0, code)
	assert.Equal(t, `{"decision":"allow"}`, out)

	code, _, errOut := runHook(call, "gemini", "--governed", "--file", filepath.Join(t.TempDir(), "missing.json"))
	assert.Equal(t, 2, code, "a session file that cannot be read blocks")
	assert.Contains(t, errOut, "hook file")
	code, _, _ = runHook(call, "gemini", "--governed", "--file")
	assert.Equal(t, 2, code, "--file needs a value")
}

// failingWriter is a stdout the agent has closed.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// askRun runs a hook for the agent against a run that answers with reply, and
// says how many times the run was asked.
func askRun(t *testing.T, agentName, call, reply string) (code int, stdout, stderr string, asked int) {
	url := serve(t, func(w http.ResponseWriter, _ *http.Request) { asked++; _, _ = w.Write([]byte(reply)) })
	t.Setenv(agent.HookFileEnv, sessionFile(t, url))
	code, stdout, stderr = runHook(call, agentName, "--governed")
	return
}

const (
	claudeAllow = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":""}}`
	claudeDeny  = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"no"}}`
)

// TestRunHook_an_answer_that_is_not_a_decision_blocks: agents read an empty,
// truncated or unrecognised answer to "may this tool run?" as no objection, so
// the hook turns each into a block. The paired controls: a real allow and a real
// deny are passed through, byte for byte.
func TestRunHook_an_answer_that_is_not_a_decision_blocks(t *testing.T) {
	bad := []string{"", "  \n", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDec`, `{}`, `[]`, `null`, `"allow"`, `42`,
		`{"hookSpecificOutput":{}}`, `{"hookSpecificOutput":{"permissionDecision":"ask"}}`, `{"hookSpecificOutput":{"permissionDecision":"ALLOW"}}`,
		`{"hookSpecificOutput":{"permissionDecision":true}}`, `{"decision":"allow"}` /* the other agent's format */}
	for _, name := range []string{"claude-code", "codex"} {
		for _, reply := range bad {
			code, out, errOut, asked := askRun(t, name, `{"hook_event_name":"PreToolUse","tool_name":"Write"}`, reply)
			assert.Equal(t, 1, asked)
			assert.Equal(t, 2, code, "%s: %q", name, reply)
			assert.Empty(t, out, "nothing the agent could read as a decision: %s %q", name, reply)
			assert.Contains(t, errOut, "PreToolUse")
		}
		for _, reply := range []string{claudeAllow, claudeDeny} {
			code, out, _, _ := askRun(t, name, `{"hook_event_name":"PreToolUse","tool_name":"Write"}`, reply)
			assert.Equal(t, 0, code)
			assert.Equal(t, reply, out, "%s passes a real decision through unchanged", name)
		}
	}
	geminiBad := []string{"", `{`, `{}`, `{"decision":"block"}`, `{"decision":"allowed"}`, `{"decision":true}`, claudeAllow /* the other agent's format */}
	for _, reply := range geminiBad {
		code, out, _, _ := askRun(t, "gemini", `{"hook_event_name":"BeforeTool","tool_name":"write_file"}`, reply)
		assert.Equal(t, 2, code, "gemini: %q", reply)
		assert.Empty(t, out)
	}
	for _, reply := range []string{`{"decision":"allow"}`, `{"decision":"deny","reason":"no"}`} {
		code, out, _, _ := askRun(t, "gemini", `{"hook_event_name":"BeforeTool","tool_name":"write_file"}`, reply)
		assert.Equal(t, 0, code)
		assert.Equal(t, reply, out)
	}
}

// TestRunHook_other_events_accept_an_acknowledgement: after a tool ran, or at the
// start or the stop, an empty answer or a JSON object is fine; one that was cut off
// is not (it may have been a block).
func TestRunHook_other_events_accept_an_acknowledgement(t *testing.T) {
	for _, c := range []struct{ agent, event string }{{"claude-code", "PostToolUse"}, {"claude-code", "SessionStart"}, {"claude-code", "Stop"},
		{"codex", "PostToolUse"}, {"gemini", "AfterTool"}, {"gemini", "SessionStart"}} {
		call := `{"hook_event_name":"` + c.event + `"}`
		for _, ok := range []string{"", "{}", `{"decision":"block","reason":"reverted"}`} {
			code, out, _, _ := askRun(t, c.agent, call, ok)
			assert.Equal(t, 0, code, "%s %s %q", c.agent, c.event, ok)
			assert.Equal(t, ok, out)
		}
		for _, cut := range []string{`{"decision":"blo`, `[`, `null`, `nope`} {
			code, out, _, _ := askRun(t, c.agent, call, cut)
			assert.Equal(t, 2, code, "%s %s %q", c.agent, c.event, cut)
			assert.Empty(t, out)
		}
	}
}

// TestRunHook_a_call_it_cannot_read_is_not_sent: a hook call that is not JSON,
// names no event, or names one the agent's hook is not registered for is blocked
// without asking the run.
func TestRunHook_a_call_it_cannot_read_is_not_sent(t *testing.T) {
	for _, c := range []struct{ agent, call string }{
		{"claude-code", `not json`}, {"claude-code", ``}, {"claude-code", `{"tool_name":"Write"}`}, {"claude-code", `{"hook_event_name":""}`},
		{"claude-code", `{"hook_event_name":"UserPromptSubmit"}`}, {"claude-code", `{"hook_event_name":"BeforeTool"}`},
		{"gemini", `{"hook_event_name":"PreToolUse"}`}, {"codex", `{"hook_event_name":"PreCompact"}`},
	} {
		code, out, errOut, asked := askRun(t, c.agent, c.call, claudeAllow)
		assert.Equal(t, 2, code, "%s %q", c.agent, c.call)
		assert.Empty(t, out)
		assert.Contains(t, errOut, "staircase hook:")
		assert.Zero(t, asked, "the run is not asked about a call that cannot be understood")
	}
}

// TestRunHook_an_answer_that_cannot_be_written_blocks: the agent closed its end.
func TestRunHook_an_answer_that_cannot_be_written_blocks(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(claudeAllow)) })
	t.Setenv(agent.HookFileEnv, sessionFile(t, url))
	var errOut bytes.Buffer
	code := agent.RunHook([]string{"claude-code", "--governed"}, strings.NewReader(`{"hook_event_name":"PreToolUse"}`), failingWriter{}, &errOut)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), "write the answer")
}
