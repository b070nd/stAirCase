package agent_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain lets the test binary stand in for `claude`: with FAKE_CLAUDE naming a script
// file it plays those tool calls through the real hook commands
// from --settings, the way Claude Code does, and logs each decision. Called
// as `<binary> hook …` it stands in for `staircase hook`, which the adapter's
// hooks call (the adapter names the running program). Hooks inherit the
// fake's environment, so the hook check comes first.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		os.Exit(agent.RunHook(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if os.Getenv("FAKE_CLAUDE") != "" {
		os.Exit(fakeClaude())
	}
	if os.Getenv("FAKE_CODEX") != "" {
		os.Exit(fakeCodex())
	}
	if os.Getenv("FAKE_GEMINI") != "" {
		os.Exit(fakeGemini())
	}
	os.Exit(m.Run())
}

type fakeCall struct {
	Tool   string         `json:"tool"`
	Input  map[string]any `json:"input"`
	Mangle bool           `json:"mangle"` // write other bytes than approved, as a CRLF-preserving editor would
}

func fakeClaude() int {
	var settings string
	for i, a := range os.Args {
		if a == "--settings" {
			settings = os.Args[i+1]
		}
	}
	b, err := os.ReadFile(settings)
	if err != nil {
		return 1
	}
	if f := os.Getenv("FAKE_CLAUDE_SETTINGS"); f != "" {
		_ = os.WriteFile(f, b, 0o600)
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &s) != nil {
		return 1
	}
	hook := func(event string, id int, c fakeCall) (int, string) {
		in, _ := json.Marshal(map[string]any{"hook_event_name": event, "tool_name": c.Tool,
			"tool_input": c.Input, "tool_use_id": fmt.Sprint("t", id), "cwd": "."})
		run := func() (int, string) {
			ctx := context.Background()
			// the host gives up on a slow hook: on the tool hook only (the drill's subject), not on the session
			// handshake, which under a loaded CI machine can take longer than the drill's short limit
			if ms, _ := strconv.Atoi(os.Getenv("FAKE_CLAUDE_HOOK_TIMEOUT_MS")); ms > 0 && event == "PreToolUse" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
				defer cancel()
			}
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", s.Hooks[event][0].Hooks[0].Command)
			cmd.Stdin = bytes.NewReader(in)
			ownGroup(cmd)
			cmd.WaitDelay = time.Second
			out, _ := cmd.Output()
			if ctx.Err() != nil {
				return -2, "" // timed out
			}
			code := 0
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
			return code, string(out)
		}
		if os.Getenv("FAKE_CLAUDE_TWICE") != "" { // a company's managed hook and the session's own, in parallel
			done := make(chan struct{})
			go func() { run(); close(done) }()
			defer func() { <-done }()
		}
		return run()
	}
	if f := os.Getenv("FAKE_CLAUDE_ARGS"); f != "" {
		b, _ := json.Marshal(os.Args[1:])
		_ = os.WriteFile(f, b, 0o600)
	}
	wd, _ := os.Getwd()
	var calls []fakeCall
	script, _ := os.ReadFile(os.Getenv("FAKE_CLAUDE"))
	_ = json.Unmarshal([]byte(strings.ReplaceAll(string(script), "$WT", wd)), &calls)
	log, _ := os.Create(os.Getenv("FAKE_CLAUDE_LOG"))
	defer log.Close()
	if len(s.Hooks["SessionStart"]) > 0 && os.Getenv("FAKE_CLAUDE_NO_SESSION_START") == "" {
		hook("SessionStart", 0, fakeCall{}) // what Claude Code does when a session begins
	}
	for i, c := range calls {
		if c.Tool == "Stop" { // the agent wants to end the session
			_, out := hook("Stop", i, c)
			var d struct{ Decision, Reason string }
			_ = json.Unmarshal([]byte(out), &d)
			fmt.Fprintf(log, "Stop %s %s\n", orDefault(d.Decision, "allow"), strings.ReplaceAll(d.Reason, "\n", " "))
			continue
		}
		code, out := hook("PreToolUse", i, c)
		var d struct {
			Out struct {
				Decision string `json:"permissionDecision"`
				Reason   string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		_ = json.Unmarshal([]byte(out), &d)
		if code == 2 {
			d.Out.Decision = "blocked"
		}
		if os.Getenv("FAKE_CLAUDE_HOST") == "documented" {
			// Claude Code's documented behaviour: only exit status 2 or a deny blocks the tool;
			// any other failure of the hook (it is missing, it crashes, it times out, it prints
			// nothing) is a non-blocking error and the tool runs.
			if code != 2 && d.Out.Decision != "deny" {
				if d.Out.Decision != "allow" {
					d.Out.Decision = "proceeded-after-hook-failure"
				}
			} else {
				d.Out.Decision = "blocked"
			}
			fmt.Fprintf(log, "%s %s %s\n", c.Tool, d.Out.Decision, d.Out.Reason)
			if d.Out.Decision == "blocked" {
				continue
			}
		} else {
			fmt.Fprintf(log, "%s %s %s\n", c.Tool, d.Out.Decision, d.Out.Reason)
			if code != 0 || d.Out.Decision != "allow" {
				continue
			}
		}
		path, _ := c.Input["file_path"].(string)
		switch c.Tool {
		case "Write":
			content := c.Input["content"].(string)
			if c.Mangle {
				content = strings.ReplaceAll(content, "\n", "\r\n")
			}
			_ = os.WriteFile(path, []byte(content), 0o644)
		case "Edit":
			cur, _ := os.ReadFile(path)
			_ = os.WriteFile(path, []byte(strings.Replace(string(cur), c.Input["old_string"].(string), c.Input["new_string"].(string), 1)), 0o644)
		}
		if c.Tool == "Bash" { // Claude Code runs it (in its sandbox), then reports it
			_ = exec.Command("/bin/sh", "-c", c.Input["command"].(string)).Run()
		}
		if c.Tool == "Write" || c.Tool == "Edit" || c.Tool == "Bash" {
			_, out := hook("PostToolUse", i, c)
			var d struct{ Decision, Reason string }
			if json.Unmarshal([]byte(out), &d) == nil && d.Decision == "block" {
				fmt.Fprintf(log, "PostToolUse block %s\n", d.Reason)
			}
		}
	}
	fmt.Println(`{"type":"result","is_error":false,"result":"done","usage":{"input_tokens":10,"output_tokens":5}}`)
	return 0
}

// TestClaudeCode_every_tool_call_is_governed drives a fake Claude Code
// through the real hooks: approved edits land byte for byte (even when the
// editor writes other bytes), everything else is denied before it runs.
func TestClaudeCode_every_tool_call_is_governed(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "decisions.log")
	abs := func(p string) string { return "$WT/" + p } // the fake runs in the worktree
	script, err := json.Marshal([]fakeCall{
		{Tool: "Write", Input: map[string]any{"file_path": abs("new.txt"), "content": "hello\n"}, Mangle: true},
		{Tool: "Edit", Input: map[string]any{"file_path": abs("f.txt"), "old_string": "b\n", "new_string": "B\n"}},
		{Tool: "Edit", Input: map[string]any{"file_path": abs("f.txt"), "old_string": "a", "new_string": "X"}}, // ambiguous
		{Tool: "Edit", Input: map[string]any{"file_path": abs("f.txt"), "old_string": "b", "new_string": "c", "replace_all": true}},
		{Tool: "Read", Input: map[string]any{"file_path": abs("f.txt")}},
		{Tool: "Read", Input: map[string]any{"file_path": "/etc/hosts"}},
		{Tool: "Glob", Input: map[string]any{"pattern": "/etc/*"}},
		{Tool: "Write", Input: map[string]any{"file_path": abs("../escape.txt"), "content": "x"}},
		{Tool: "Bash", Input: map[string]any{"command": "touch shell.txt"}},
		{Tool: "WebFetch", Input: map[string]any{"url": "https://example.com"}},
	})
	require.NoError(t, err)
	// The adapter passes Claude Code only allowlisted variables, so a wrapper
	// script tells the re-executed test binary to play the fake.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.json"), script, 0o600))
	bin := filepath.Join(dir, "claude")
	argsFile := filepath.Join(dir, "args.json")
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\nFAKE_CLAUDE=%s FAKE_CLAUDE_LOG=%s FAKE_CLAUDE_ARGS=%s exec %s \"$@\"\n",
		filepath.Join(dir, "script.json"), logFile, argsFile, os.Args[0]), 0o755))
	r := runtest.Run(t, runtest.Options{
		Base:  map[string]runtest.File{"f.txt": {Content: "a\nb\na2\n", Mode: 0o644}},
		Agent: &agent.ClaudeCode{Prompt: "p", Bin: bin},
	})
	require.NoError(t, r.Err)
	log, err := os.ReadFile(logFile)
	require.NoError(t, err)
	decisions := strings.Split(strings.TrimSpace(string(log)), "\n")
	want := []string{"Write allow", "Edit allow", "Edit deny", "Edit deny", "Read allow", "Read deny", "Glob deny", "Write deny", "Bash deny", "WebFetch deny"}
	require.Len(t, decisions, len(want), string(log))
	for i, w := range want {
		assert.True(t, strings.HasPrefix(decisions[i], w), "call %d: %s", i, decisions[i])
	}
	assert.Contains(t, decisions[8], "--allow-shell-exec")

	got, err := r.OnBranch("new.txt")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", got, "the approved bytes are committed, not what the editor wrote")
	got, err = r.OnBranch("f.txt")
	require.NoError(t, err)
	assert.Equal(t, "a\nB\na2\n", got)
	assert.Contains(t, r.Types(), "state_emit")

	// Only staircase's settings load: the user's and the repository's own
	// hooks and MCP servers would act outside governance (F82).
	var args []string
	b, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &args))
	assert.Contains(t, strings.Join(args, "\x00"), "--setting-sources\x00\x00", "no user, project or local settings")
	assert.Contains(t, args, "--strict-mcp-config")
}

// TestClaudeCode_failure_reports_why: Claude Code prints its error (such as
// an expired login) as the JSON result on stdout, not on stderr.
func TestClaudeCode_failure_reports_why(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "claude")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho '{\"is_error\":true,\"result\":\"Failed to authenticate\"}'\nexit 1\n"), 0o755))
	r := runtest.Run(t, runtest.Options{Agent: &agent.ClaudeCode{Prompt: "p", Bin: bin}})
	require.Error(t, r.Err)
	assert.Equal(t, persistence.RunStatusFailed, r.Run.Status)
	assert.Contains(t, r.Err.Error(), "Failed to authenticate")
}

// TestClaudeCode_two_hooks_decide_once: with a company's managed hook and the
// session's own hook both installed, Claude Code calls both for every tool
// call; each call is still proposed and decided once.
func TestClaudeCode_two_hooks_decide_once(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "decisions.log")
	script, err := json.Marshal([]fakeCall{
		{Tool: "Write", Input: map[string]any{"file_path": "$WT/a.txt", "content": "a\n"}},
		{Tool: "Write", Input: map[string]any{"file_path": "$WT/b.txt", "content": "b\n"}},
	})
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.json"), script, 0o600))
	bin := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\nFAKE_CLAUDE=%s FAKE_CLAUDE_LOG=%s FAKE_CLAUDE_TWICE=1 exec %s \"$@\"\n",
		filepath.Join(dir, "script.json"), logFile, os.Args[0]), 0o755))
	r := runtest.Run(t, runtest.Options{Agent: &agent.ClaudeCode{Prompt: "p", Bin: bin}})
	require.NoError(t, r.Err)
	decided := 0
	for _, typ := range r.Types() {
		if typ == "yield_decided" {
			decided++
		}
	}
	assert.Equal(t, 2, decided, "two tool calls, two decisions")
	for _, f := range []string{"a.txt", "b.txt"} {
		_, err := r.OnBranch(f)
		assert.NoError(t, err)
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// TestClaudeCode_cannot_stop_until_checks_pass: with --check, the session's
// Stop is refused while a check fails on the approved state, with the
// failure as the reason, and allowed once it passes. Checks run on a copy:
// what they write never reaches the worktree or the commit.
func TestClaudeCode_cannot_stop_until_checks_pass(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "decisions.log")
	script, err := json.Marshal([]fakeCall{
		{Tool: "Write", Input: map[string]any{"file_path": "$WT/health.txt", "content": "ok\n"}},
		{Tool: "Stop"},
		{Tool: "Edit", Input: map[string]any{"file_path": "$WT/health.txt", "old_string": "ok", "new_string": "done"}},
		{Tool: "Stop"},
	})
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.json"), script, 0o600))
	bin := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\nFAKE_CLAUDE=%s FAKE_CLAUDE_LOG=%s exec %s \"$@\"\n",
		filepath.Join(dir, "script.json"), logFile, os.Args[0]), 0o755))
	r := runtest.Run(t, runtest.Options{
		Agent: &agent.ClaudeCode{Prompt: "p", Bin: bin},
		Run:   orchestrator.RunOptions{Checks: []string{"echo junk > junk.txt && grep -q done health.txt"}},
	})
	require.NoError(t, r.Err)
	log, err := os.ReadFile(logFile)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	require.Len(t, lines, 4, string(log))
	assert.True(t, strings.HasPrefix(lines[1], "Stop block"), lines[1])
	assert.Contains(t, lines[1], "grep -q done health.txt")
	assert.True(t, strings.HasPrefix(lines[3], "Stop allow"), lines[3])
	got, err := r.OnBranch("health.txt")
	require.NoError(t, err)
	assert.Equal(t, "done\n", got)
	_, err = r.OnBranch("junk.txt")
	assert.Error(t, err, "what a check writes stays in its copy")
	assert.Contains(t, r.Types(), "done_checked")
}

// TestClaudeCode_commands_run_in_its_sandbox: a session turns on Claude Code's
// own sandbox, strictly (no unsandboxed retry, no start without it, no
// network), hiding the workspace and credentials like staircase's sandbox.
// An approved command then counts as sandboxed: the files it wrote are
// decided after it ran and the run keeps CAL 3. A command asking to leave
// the sandbox is refused.
func TestClaudeCode_commands_run_in_its_sandbox(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "decisions.log")
	script, err := json.Marshal([]fakeCall{
		{Tool: "Bash", Input: map[string]any{"command": "echo gen > gen.txt"}},
		{Tool: "Bash", Input: map[string]any{"command": "curl example.com", "dangerouslyDisableSandbox": true}},
	})
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.json"), script, 0o600))
	bin, settingsFile := filepath.Join(dir, "claude"), filepath.Join(dir, "settings.json")
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\nFAKE_CLAUDE=%s FAKE_CLAUDE_LOG=%s FAKE_CLAUDE_SETTINGS=%s exec %s \"$@\"\n",
		filepath.Join(dir, "script.json"), logFile, settingsFile, os.Args[0]), 0o755))
	r := runtest.Run(t, runtest.Options{
		Agent: &agent.ClaudeCode{Prompt: "p", Bin: bin},
		Run:   orchestrator.RunOptions{AllowShellExec: true},
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			require.NoError(t, crypto.GenerateSigningKey(wsDir))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"type":"yield_response","approved":true}`))
			}))
			t.Cleanup(srv.Close)
			require.NoError(t, s.UpdateProjectWebhook(projectID, srv.URL))
		},
	})
	require.NoError(t, r.Err)
	log, err := os.ReadFile(logFile)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	require.Len(t, lines, 2, string(log))
	assert.True(t, strings.HasPrefix(lines[0], "Bash allow"), lines[0])
	assert.True(t, strings.HasPrefix(lines[1], "Bash deny"), lines[1])
	got, err := r.OnBranch("gen.txt")
	require.NoError(t, err)
	assert.Equal(t, "gen\n", got)
	assert.Contains(t, r.Types(), "shell_ran")

	var s struct {
		Sandbox struct {
			Enabled, FailIfUnavailable bool
			AllowUnsandboxedCommands   *bool
			Filesystem                 struct{ DenyRead, AllowRead []string }
			Network                    struct {
				AllowedDomains  []string
				StrictAllowlist bool
			}
		}
	}
	b, err := os.ReadFile(settingsFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &s))
	sb := s.Sandbox
	assert.True(t, sb.Enabled && sb.FailIfUnavailable && sb.Network.StrictAllowlist, string(b))
	require.NotNil(t, sb.AllowUnsandboxedCommands)
	assert.False(t, *sb.AllowUnsandboxedCommands)
	assert.Empty(t, sb.Network.AllowedDomains)
	assert.Contains(t, sb.Filesystem.DenyRead, "~/.ssh")
	ws, _ := filepath.EvalSymlinks(r.WsDir)
	assert.Contains(t, sb.Filesystem.DenyRead, ws, "the workspace")
	assert.NotEmpty(t, sb.Filesystem.AllowRead, "the worktree inside the workspace")

	b, err = os.ReadFile(filepath.Join(r.WsDir, "audit", "run-1.certificate.json"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "payload") // signed
	certs, err := exec.Command("git", "-C", r.Repo, "notes", "--ref=staircase", "show", "staircase/run-1").Output()
	require.NoError(t, err)
	var env struct{ Payload string }
	require.NoError(t, json.Unmarshal(certs, &env))
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"cal":3`, "a command in Claude Code's strict sandbox keeps CAL 3")
}
