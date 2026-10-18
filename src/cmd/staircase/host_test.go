package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaudeStream is a Claude Code in stream-json mode, as a runner such as Cloudroom drives it: it reads one user message from
// stdin, reports what it was given (arguments, the settings file, the message) as a stream-json line, writes HEALTH.md through
// the hooks the settings name (only when allowed), and ends with a result line.
func fakeClaudeStream() int {
	var settings string
	for i, a := range os.Args {
		if a == "--settings" && i+1 < len(os.Args) {
			settings = os.Args[i+1]
		}
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	raw, _ := os.ReadFile(settings)
	var s map[string]any
	_ = json.Unmarshal(raw, &s)
	report, _ := json.Marshal(map[string]any{"type": "system", "subtype": "init", "args": os.Args[1:], "settings": s, "stdin": strings.TrimSpace(line)})
	fmt.Println(string(report))
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &hooks) != nil || len(hooks.Hooks["PreToolUse"]) == 0 {
		fmt.Println(`{"type":"result","is_error":true,"result":"no hooks"}`)
		return 1
	}
	call := func(event string) string {
		in, _ := json.Marshal(map[string]any{"hook_event_name": event, "tool_name": "Write", "tool_use_id": "t1", "cwd": ".",
			"tool_input": map[string]any{"file_path": "HEALTH.md", "content": "ok\n"}})
		cmd := exec.Command("/bin/sh", "-c", hooks.Hooks[event][0].Hooks[0].Command)
		cmd.Stdin = bytes.NewReader(in)
		out, _ := cmd.Output()
		return string(out)
	}
	call("SessionStart")
	if strings.Contains(call("PreToolUse"), `"allow"`) {
		_ = os.WriteFile("HEALTH.md", []byte("ok\n"), 0o644)
		call("PostToolUse")
	}
	fmt.Println(`{"type":"result","is_error":false,"result":"done"}`)
	return 0
}

// cloudroomArgs are the arguments Cloudroom 0.1.57 gives Claude Code (src/runtime/claude.rs), for a new session.
func cloudroomArgs(session string) []string {
	return []string{"--settings", `{"fastMode":false,"enableWorkflows":false}`,
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--replay-user-messages", "--dangerously-skip-permissions", "--permission-prompts", "none", "--no-chrome",
		"--model", "haiku", "--append-system-prompt", "RUNNER INSTRUCTIONS",
		"--disallowedTools", "AskUserQuestion,EnterPlanMode,ExitPlanMode,Agent,Task,Workflow,SendMessage",
		"--mcp-config", `{"mcpServers":{"cloudroom":{"type":"sdk","name":"cloudroom"}}}`,
		"--settings", `{"attribution":{"commit":"","pr":""}}`,
		"--session-id", session}
}

// hostRepo is a session repository whose workspace is set up to host: scope and checks in host.json, and a stream-json fake claude.
func hostRepo(t *testing.T) (repo, ws string, git func(args ...string) string) {
	repo, ws, git = sessionRepo(t)
	require.NoError(t, os.Remove(filepath.Join(ws, "policy.json"))) // away: decided on evidence, not by a rule
	require.NoError(t, os.WriteFile(filepath.Join(ws, "host.json"), []byte(`{"allow":["HEALTH.md"],"checks":["test -f HEALTH.md"]}`), 0o600))
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"),
		fmt.Appendf(nil, "#!/bin/sh\nFAKE_CLAUDE_STREAM=1 exec %q \"$@\"\n", os.Args[0]), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		sessionAway, sessionOnEvidence, sessionInScope, sessionAllow, runChecks, runPassEnv = false, false, false, nil, nil, nil
	})
	return repo, ws, git
}

// TestHost_governs_a_session_a_runner_starts (ADR 0008): run with Cloudroom's own arguments, the stand-in keeps the runner's protocol
// (stdin and stdout pass through, nothing of stAirCase's own output in them) and its session id, and gives Claude Code stAirCase's
// hooks and nothing that widens them: its MCP tools behind the gate, no other settings source, the runner's harmless settings under stAirCase's,
// the Away brief after the runner's instructions. The session is an away run that ends waiting for the owner's review.
func TestHost_governs_a_session_a_runner_starts(t *testing.T) {
	repo, ws, git := hostRepo(t)
	var out bytes.Buffer
	msg := `{"type":"user","message":{"role":"user","content":"add a health file"}}`
	require.NoError(t, hostClaude(cloudroomArgs("11111111-2222-3333-4444-555555555555"), strings.NewReader(msg+"\n"), &out))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 2, "only the agent's stream-json reaches the runner: %q", out.String())
	var report struct {
		Args     []string       `json:"args"`
		Settings map[string]any `json:"settings"`
		Stdin    string         `json:"stdin"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &report))
	assert.Equal(t, msg, report.Stdin, "the runner's message passes through untouched")
	args := strings.Join(report.Args, " ")
	for _, want := range []string{"--input-format stream-json", "--output-format stream-json", "--dangerously-skip-permissions",
		"--model haiku", "--session-id 11111111-2222-3333-4444-555555555555", "--strict-mcp-config", "--disallowedTools"} {
		assert.Contains(t, args, want)
	}
	assert.Contains(t, args, `--mcp-config {"mcpServers":{"cloudroom"`, "the runner's MCP server is kept: its tools are decided like commands")
	for i, a := range report.Args {
		if a == "--setting-sources" {
			assert.Equal(t, "", report.Args[i+1], "no user or project settings")
		}
		if a == "--append-system-prompt" {
			assert.True(t, strings.HasPrefix(report.Args[i+1], "RUNNER INSTRUCTIONS"))
			assert.Contains(t, report.Args[i+1], orchestrator.AwayBrief)
		}
	}
	assert.Equal(t, 1, strings.Count(args, "--settings "), "one settings file")
	assert.Contains(t, report.Settings, "hooks")
	assert.Equal(t, false, report.Settings["fastMode"], "the runner's harmless settings are kept")
	assert.Contains(t, report.Settings, "attribution")

	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	defer db.Close()
	run, err := persistence.NewStore(db).GetRun(1)
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusAwaitingReview, run.Status)
	assert.NoFileExists(t, filepath.Join(repo, "HEALTH.md"), "the agent worked in the run's worktree, not the runner's checkout")
	decideAccept = true
	t.Cleanup(func() { decideAccept = false })
	require.NoError(t, decideHandler(nil, []string{"1"}))
	assert.Equal(t, "ok\n", git("show", "staircase/run-1:HEALTH.md"))
}

// TestHost_refuses_what_it_does_not_know: an argument the stand-in does not know could widen what the agent may do, so it is refused;
// so is a resumed or forked session in this slice, and a workspace that is not set up to host.
func TestHost_refuses_what_it_does_not_know(t *testing.T) {
	_, ws, _ := hostRepo(t)
	var out bytes.Buffer
	assert.ErrorContains(t, hostClaude(append(cloudroomArgs("s"), "--add-dir", "/"), strings.NewReader(""), &out), "--add-dir")
	resumed := cloudroomArgs("s")
	resumed[len(resumed)-2] = "--resume"
	assert.ErrorContains(t, hostClaude(resumed, strings.NewReader(""), &out), "resume")
	require.NoError(t, os.Remove(filepath.Join(ws, "host.json")))
	assert.ErrorContains(t, hostClaude(cloudroomArgs("s"), strings.NewReader(""), &out), "host setup")
	assert.Empty(t, out.String(), "nothing reaches the runner's stdout")
}

// TestHost_setup_refuses_terms_no_session_can_run_under: the profile is the terms of every hosted session, judged when it is
// set up rather than when a runner first starts a session; nothing is written for a refused one.
func TestHost_setup_refuses_terms_no_session_can_run_under(t *testing.T) {
	ws := t.TempDir()
	viper.Set("STAIRCASE_DIR", ws)
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", "") })
	hostSetupAllow, hostSetupChecks, hostSetupAwayTools = []string{"src/**"}, []string{"go test ./..."}, []string{"mcp__probe__*"}
	t.Cleanup(func() { hostSetupAllow, hostSetupChecks, hostSetupAwayTools = nil, nil, nil })
	err := hostSetupCmd.RunE(hostSetupCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly")
	assert.NoFileExists(t, filepath.Join(ws, "host.json"))
}
