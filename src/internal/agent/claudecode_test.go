package agent_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/agent"
	"github.com/b070nd/staircase-core/src/internal/orchestrator/runtest"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain lets the test binary stand in for `claude`: with FAKE_CLAUDE naming a script
// file it plays those tool calls through the real hook commands
// from --settings, the way Claude Code does, and logs each decision.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CLAUDE") != "" {
		os.Exit(fakeClaude())
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
		cmd := exec.Command("/bin/sh", "-c", s.Hooks[event][0].Hooks[0].Command)
		cmd.Stdin = bytes.NewReader(in)
		out, _ := cmd.Output()
		code := 0
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return code, string(out)
	}
	wd, _ := os.Getwd()
	var calls []fakeCall
	script, _ := os.ReadFile(os.Getenv("FAKE_CLAUDE"))
	_ = json.Unmarshal([]byte(strings.ReplaceAll(string(script), "$WT", wd)), &calls)
	log, _ := os.Create(os.Getenv("FAKE_CLAUDE_LOG"))
	defer log.Close()
	for i, c := range calls {
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
		fmt.Fprintf(log, "%s %s %s\n", c.Tool, d.Out.Decision, d.Out.Reason)
		if code != 0 || d.Out.Decision != "allow" {
			continue
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
		if c.Tool == "Write" || c.Tool == "Edit" {
			hook("PostToolUse", i, c)
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
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\nFAKE_CLAUDE=%s FAKE_CLAUDE_LOG=%s exec %s \"$@\"\n",
		filepath.Join(dir, "script.json"), logFile, os.Args[0]), 0o755))
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
