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

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGemini stands in for the Gemini CLI as its documentation describes it
// (geminicli.com/docs/hooks): settings from GEMINI_CLI_SYSTEM_SETTINGS_PATH,
// hooks named BeforeTool and AfterTool that get the tool call on stdin (with
// no tool_use_id), and a tool call is blocked by exit code 2 or by the JSON
// {"decision":"deny"}. FAKE_GEMINI=nohooks ignores the hooks.
func fakeGemini() int {
	if os.Getenv("FAKE_GEMINI") == "nohooks" {
		_ = os.WriteFile("hello.txt", []byte("ungoverned\n"), 0o644)
		return 0
	}
	if f := os.Getenv("FAKE_GEMINI_ARGS"); f != "" {
		b, _ := json.Marshal(os.Args[1:])
		_ = os.WriteFile(f, b, 0o600)
	}
	b, err := os.ReadFile(os.Getenv("GEMINI_CLI_SYSTEM_SETTINGS_PATH"))
	if err != nil {
		return 1
	}
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &s) != nil {
		return 1
	}
	hook := func(event string, extra map[string]any) (code int, out string) {
		groups := s.Hooks[event]
		if len(groups) == 0 {
			return 0, ""
		}
		in := map[string]any{"session_id": "s1", "transcript_path": "/tmp/t.json", "cwd": ".", "hook_event_name": event, "timestamp": "2026-09-29T10:00:00Z"}
		for k, v := range extra {
			in[k] = v
		}
		body, _ := json.Marshal(in)
		cmd := exec.Command("/bin/sh", "-c", groups[0].Hooks[0].Command)
		cmd.Stdin = bytes.NewReader(body)
		// Gemini sanitizes the environment of a hook: nothing of ours is inherited.
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GEMINI_SESSION_ID=s1"}
		o, _ := cmd.Output()
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return code, string(o)
	}
	hook("SessionStart", map[string]any{"source": "startup"})
	wd, _ := os.Getwd()
	var calls []fakeCall
	script, _ := os.ReadFile(os.Getenv("FAKE_GEMINI_SCRIPT"))
	_ = json.Unmarshal([]byte(strings.ReplaceAll(string(script), "$WT", wd)), &calls)
	log, _ := os.Create(os.Getenv("FAKE_GEMINI_LOG"))
	defer log.Close()
	for _, c := range calls {
		code, out := hook("BeforeTool", map[string]any{"tool_name": c.Tool, "tool_input": c.Input})
		var d struct{ Decision, Reason string }
		_ = json.Unmarshal([]byte(out), &d)
		decision := "allow"
		if code == 2 {
			decision = "blocked"
		} else if code != 0 { // "Other = Warning": Gemini lets the call run
			decision = "failed open"
		} else if d.Decision == "deny" || d.Decision == "block" {
			decision = "deny"
		}
		fmt.Fprintf(log, "%s %s %s\n", c.Tool, decision, strings.ReplaceAll(d.Reason, "\n", " "))
		if decision == "deny" || decision == "blocked" {
			continue
		}
		path, _ := c.Input["file_path"].(string)
		switch c.Tool {
		case "write_file":
			content := c.Input["content"].(string)
			if c.Mangle {
				content = strings.ReplaceAll(content, "\n", "\r\n")
			}
			_ = os.WriteFile(path, []byte(content), 0o644)
		case "replace":
			cur, _ := os.ReadFile(path)
			_ = os.WriteFile(path, []byte(strings.Replace(string(cur), c.Input["old_string"].(string), c.Input["new_string"].(string), 1)), 0o644)
		case "run_shell_command":
			_ = exec.Command("/bin/sh", "-c", c.Input["command"].(string)).Run()
		}
		if c.Tool == "write_file" || c.Tool == "replace" || c.Tool == "run_shell_command" {
			hook("AfterTool", map[string]any{"tool_name": c.Tool, "tool_input": c.Input, "tool_response": map[string]any{"llmContent": "ok"}})
		}
	}
	fmt.Println(`{"response":"done","stats":{}}`)
	return 0
}

func geminiBin(t *testing.T, mode string, calls []fakeCall) (bin, logFile, argsFile string) {
	script, err := json.Marshal(calls)
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.json"), script, 0o600))
	logFile, argsFile, bin = filepath.Join(dir, "decisions.log"), filepath.Join(dir, "args.json"), filepath.Join(dir, "gemini")
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\nFAKE_GEMINI=%s FAKE_GEMINI_SCRIPT=%s FAKE_GEMINI_LOG=%s FAKE_GEMINI_ARGS=%s exec %s \"$@\"\n",
		mode, filepath.Join(dir, "script.json"), logFile, argsFile, os.Args[0]), 0o755))
	return bin, logFile, argsFile
}

// TestGemini_every_tool_call_is_governed drives a fake Gemini CLI through the
// real hooks: approved edits land byte for byte, everything else is denied
// before it runs, the hooks find the run without any inherited variable, and
// Gemini's own approval prompts are off because stAirCase decides.
func TestGemini_every_tool_call_is_governed(t *testing.T) {
	abs := func(p string) string { return "$WT/" + p }
	bin, logFile, argsFile := geminiBin(t, "1", []fakeCall{
		{Tool: "write_file", Input: map[string]any{"file_path": abs("new.txt"), "content": "hello\n"}, Mangle: true},
		{Tool: "replace", Input: map[string]any{"file_path": abs("f.txt"), "old_string": "b\n", "new_string": "B\n"}},
		{Tool: "replace", Input: map[string]any{"file_path": abs("f.txt"), "old_string": "a", "new_string": "X"}}, // ambiguous
		{Tool: "replace", Input: map[string]any{"file_path": abs("f.txt"), "old_string": "b", "new_string": "c", "allow_multiple": true}},
		{Tool: "read_file", Input: map[string]any{"file_path": abs("f.txt")}},
		{Tool: "read_file", Input: map[string]any{"file_path": "/etc/hosts"}},
		{Tool: "list_directory", Input: map[string]any{"dir_path": "/etc"}},
		{Tool: "glob", Input: map[string]any{"pattern": "/etc/*"}},
		{Tool: "write_file", Input: map[string]any{"file_path": abs("../escape.txt"), "content": "x"}},
		{Tool: "run_shell_command", Input: map[string]any{"command": "touch shell.txt"}},
		{Tool: "web_fetch", Input: map[string]any{"prompt": "https://example.com"}},
		{Tool: "write_todos", Input: map[string]any{"todos": []string{"a"}}},
	})
	r := runtest.Run(t, runtest.Options{
		Base:  map[string]runtest.File{"f.txt": {Content: "a\nb\na2\n", Mode: 0o644}},
		Agent: &agent.Gemini{Prompt: "p", Bin: bin},
	})
	require.NoError(t, r.Err)
	log, err := os.ReadFile(logFile)
	require.NoError(t, err)
	decisions := strings.Split(strings.TrimSpace(string(log)), "\n")
	want := []string{"write_file allow", "replace allow", "replace deny", "replace deny", "read_file allow", "read_file deny",
		"list_directory deny", "glob deny", "write_file deny", "run_shell_command deny", "web_fetch deny", "write_todos allow"}
	require.Len(t, decisions, len(want), string(log))
	for i, w := range want {
		assert.True(t, strings.HasPrefix(decisions[i], w), "call %d: %s", i, decisions[i])
	}
	assert.Contains(t, decisions[9], "--allow-shell-exec")

	got, err := r.OnBranch("new.txt")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", got, "the approved bytes are committed, not what the editor wrote")
	got, err = r.OnBranch("f.txt")
	require.NoError(t, err)
	assert.Equal(t, "a\nB\na2\n", got)

	var args []string
	b, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &args))
	assert.Contains(t, args, "--approval-mode=yolo", "stAirCase's hooks decide; Gemini's own prompts would only stall a headless run")
	assert.Contains(t, args, "json")
}

// TestGemini_without_its_hooks_nothing_is_kept: a Gemini that never called
// the hooks was not governed; the run fails and commits nothing.
func TestGemini_without_its_hooks_nothing_is_kept(t *testing.T) {
	bin, _, _ := geminiBin(t, "nohooks", nil)
	r := runtest.Run(t, runtest.Options{Agent: &agent.Gemini{Prompt: "p", Bin: bin}})
	require.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "never ran staircase's hooks")
	assert.Empty(t, r.Run.GitCommitHash)
}
