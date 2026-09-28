package main

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
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain lets the test binary stand in for what a session starts: called
// as `<binary> hook …` it is `staircase hook` (the adapter's hooks call the
// running program); with FAKE_CLAUDE set it is a Claude Code that writes one
// file through those hooks. Hooks inherit the fake's environment, so the
// hook check comes first.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		os.Exit(agent.RunHook(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if os.Getenv("FAKE_CLAUDE") != "" {
		os.Exit(fakeClaude())
	}
	os.Exit(m.Run())
}

// fakeClaude writes HEALTH.md the way Claude Code does: PreToolUse hook,
// the write itself when allowed, PostToolUse hook.
func fakeClaude() int {
	var settings string
	for i, a := range os.Args {
		if a == "--settings" && i+1 < len(os.Args) {
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
	call := func(event string) string {
		in, _ := json.Marshal(map[string]any{"hook_event_name": event, "tool_name": "Write", "tool_use_id": "t1", "cwd": ".",
			"tool_input": map[string]any{"file_path": "HEALTH.md", "content": "ok\n"}})
		cmd := exec.Command("/bin/sh", "-c", s.Hooks[event][0].Hooks[0].Command)
		cmd.Stdin = bytes.NewReader(in)
		out, _ := cmd.Output()
		return string(out)
	}
	if !strings.Contains(call("PreToolUse"), `"allow"`) {
		fmt.Println(`{"is_error":true,"result":"the write was not allowed"}`)
		return 1
	}
	if os.WriteFile("HEALTH.md", []byte("ok\n"), 0o644) != nil {
		return 1
	}
	call("PostToolUse")
	fmt.Println(`{"is_error":false,"result":"done","usage":{"input_tokens":1,"output_tokens":1}}`)
	return 0
}

// TestClaudeSession_needs_no_setup: in any git repository, `staircase claude
// "task"` creates the workspace, the project and a case, runs Claude Code
// under governance and leaves exactly the approved change on a run branch. A
// second session in the same repository reuses the project.
func TestClaudeSession_needs_no_setup(t *testing.T) {
	repo, ws, git := sessionRepo(t)
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	assert.Equal(t, "ok\n", git("show", "staircase/run-1:HEALTH.md"))
	assert.NoFileExists(t, filepath.Join(repo, "HEALTH.md"), "the checkout is not touched")

	require.NoError(t, claudeSession(nil, []string{"again"}))
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	defer db.Close()
	projects, err := persistence.NewStore(db).ListAllProjects()
	require.NoError(t, err)
	require.Len(t, projects, 1, "the second session reuses the project")
	assert.Equal(t, "shop", projects[0].Name)
	cases, err := persistence.NewStore(db).ListCasesByProject(projects[0].ID)
	require.NoError(t, err)
	assert.Len(t, cases, 2)
}

// sessionRepo is a git repository "shop" with one commit, the current
// directory, with a fake claude on PATH and a workspace (not yet created)
// whose policy approves file edits: no terminal is needed to decide.
func sessionRepo(t *testing.T) (repo, ws string, git func(args ...string) string) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test - skipped in -short mode")
	}
	repo = filepath.Join(t.TempDir(), "shop")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	git = func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(out)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "T")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("shop\n"), 0o644))
	git("add", "-A")
	git("commit", "-q", "-m", "init")

	ws = filepath.Join(t.TempDir(), "workspace") // does not exist yet
	viper.Set("STAIRCASE_DIR", ws)
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", "") })
	require.NoError(t, os.MkdirAll(ws, 0o700)) // only for the policy: no terminal to approve from
	require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"),
		[]byte(`{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`), 0o600))
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"),
		fmt.Appendf(nil, "#!/bin/sh\nFAKE_CLAUDE=1 exec %q \"$@\"\n", os.Args[0]), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(repo)
	sessionYes = true // no terminal in tests: agree up front
	t.Cleanup(func() { sessionYes = false })
	return repo, ws, git
}

// TestClaudeSession_outside_a_repository fails with a clear message.
func TestClaudeSession_outside_a_repository(t *testing.T) {
	viper.Set("STAIRCASE_DIR", filepath.Join(t.TempDir(), "ws"))
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", "") })
	t.Chdir(t.TempDir())
	assert.ErrorContains(t, claudeSession(nil, []string{"task"}), "not inside a git repository")
	assert.ErrorContains(t, claudeSession(nil, nil), "what should Claude Code do")
}

// TestAgreement_says_what_will_happen: before an agent starts, the person
// sees the task, where it may change files and how edits and commands are
// governed for this agent.
func TestAgreement_says_what_will_happen(t *testing.T) {
	text := agreement(sessionSetup{harness: "codex", name: "Codex", task: "fix the date test", root: "/r/shop",
		base: "0123456789abcdef", allow: []string{"src/**"}, model: "gpt-6-luna"})
	for _, want := range []string{"Codex", "fix the date test", "/r/shop", "0123456789ab", "src/**",
		"before", "sandbox", "afterwards", "gpt-6-luna", "no budget cap"} {
		assert.Contains(t, text, want)
	}
	claude := agreement(sessionSetup{harness: "claude-code", name: "Claude Code", task: "t", root: "/r", base: "abc"})
	assert.Contains(t, claude, "the whole repository")
	assert.Contains(t, claude, "not allowed")
}

// TestClaudeSession_needs_agreement: without a terminal to ask, a session
// starts only with --yes, and the chain records who agreed to which plan.
func TestClaudeSession_needs_agreement(t *testing.T) {
	_, ws, _ := sessionRepo(t)
	sessionYes = false
	assert.ErrorContains(t, claudeSession(nil, []string{"task"}), "--yes")
	sessionYes = true
	require.NoError(t, claudeSession(nil, []string{"add", "a", "health", "file"}))
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	defer db.Close()
	events, err := persistence.NewStore(db).ListEventLogs(1)
	require.NoError(t, err)
	var agreed map[string]any
	for _, e := range events {
		if e.EventType == "task_agreed" {
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &agreed))
		}
	}
	require.NotNil(t, agreed, "task_agreed recorded")
	assert.Equal(t, "--yes", agreed["by"])
	assert.NotEmpty(t, agreed["plan_digest"])
}
