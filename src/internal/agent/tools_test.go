package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// call runs one of the agent's built-in tools with args as its JSON arguments.
func call(ctx context.Context, env *orchestrator.AgentEnv, name string, args any) string {
	for _, tl := range agent.Tools(env, "coder") {
		if tl.Name == name {
			b, _ := json.Marshal(args)
			return tl.Call(ctx, b)
		}
	}
	return "no such tool: " + name
}

// runTools runs one in-process run whose agent is script; outputs collects
// what the tools returned.
func runTools(t *testing.T, base map[string]runtest.File, opts orchestrator.RunOptions, setup func(*persistence.Store, string, int64), script func(ctx context.Context, env *orchestrator.AgentEnv) []string) (runtest.Result, []string) {
	var out []string
	r := runtest.Run(t, runtest.Options{Base: base, Setup: setup, Run: opts,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			out = script(ctx, env)
			return nil
		})})
	return r, out
}

// TestTools_write_exactly_what_was_approved catches a tool writing anything
// but the orchestrator's derived bytes: the full created content (not a
// preview), edits under Python-compatible newline rules, the exec bit kept,
// and approved deletions.
func TestTools_write_exactly_what_was_approved(t *testing.T) {
	big := strings.Repeat("x", 50_000) // well past the old 10,000-char preview
	r, out := runTools(t, map[string]runtest.File{
		"f.txt":   {Content: "a\r\nb\r\n", Mode: 0o644},
		"run.sh":  {Content: "echo a\n", Mode: 0o755},
		"old.txt": {Content: "bye\n", Mode: 0o644},
	}, orchestrator.RunOptions{}, nil, func(ctx context.Context, env *orchestrator.AgentEnv) []string {
		return []string{
			call(ctx, env, "create_file", map[string]string{"path": "big.txt", "content": big, "reasoning": "r"}),
			call(ctx, env, "request_edit", map[string]string{"file": "f.txt", "search_block": "b\n", "replace_block": "B\n", "reasoning": "r"}),
			call(ctx, env, "request_edit", map[string]string{"file": "run.sh", "search_block": "echo a", "replace_block": "echo b", "reasoning": "r"}),
			call(ctx, env, "delete_file", map[string]string{"path": "old.txt", "reasoning": "r"}),
		}
	})
	require.Equal(t, []string{"created big.txt", "applied", "applied", "deleted old.txt"}, out)
	require.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	got, err := r.OnBranch("big.txt")
	require.NoError(t, err)
	assert.Equal(t, big, got)
	got, err = r.OnBranch("f.txt")
	require.NoError(t, err)
	assert.Equal(t, "a\nB\n", got)
	mode, err := exec.Command("git", "-C", r.Repo, "ls-tree", fmt.Sprintf("staircase/run-%d", r.Run.ID), "run.sh").CombinedOutput()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(mode), "100755"), "exec bit kept: %s", mode)
	_, err = r.OnBranch("old.txt")
	assert.Error(t, err, "old.txt deleted on the run branch")
}

// TestTools_refusals_reach_the_model catches a refused proposal being written
// anyway or failing silently: the model must learn why, and nothing changes.
func TestTools_refusals_reach_the_model(t *testing.T) {
	r, out := runTools(t, map[string]runtest.File{"f.txt": {Content: "hello\n", Mode: 0o644}},
		orchestrator.RunOptions{}, nil, func(ctx context.Context, env *orchestrator.AgentEnv) []string {
			return []string{
				call(ctx, env, "create_file", map[string]string{"path": "huge.txt", "content": strings.Repeat("x", 200*1024+1), "reasoning": "r"}),
				call(ctx, env, "request_edit", map[string]string{"file": "f.txt", "search_block": "absent", "replace_block": "x", "reasoning": "r"}),
				call(ctx, env, "delete_file", map[string]string{"path": "missing.txt", "reasoning": "r"}),
				call(ctx, env, "create_file", map[string]string{"path": "../escape.txt", "content": "x", "reasoning": "r"}),
				call(ctx, env, "create_file", map[string]any{"path": 42}),
			}
		})
	require.Len(t, out, 5)
	for i, want := range []string{"at most", "search_block not found", "does not exist", "outside the project root", "invalid arguments"} {
		assert.Contains(t, out[i], want, "tool call %d", i)
	}
	for _, o := range out[:4] {
		assert.True(t, strings.HasPrefix(o, "rejected: "), o)
	}
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status, "refusals alone do not fail a run")
	assert.Empty(t, r.Run.GitCommitHash)
}

// TestTools_read_and_list_stay_inside_the_project catches reads escaping the
// worktree - by "..", or by a symlink pointing outside it.
func TestTools_read_and_list_stay_inside_the_project(t *testing.T) {
	root, outside := t.TempDir(), filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("A\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "big.bin"), make([]byte, 100*1024+1), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	env := &orchestrator.AgentEnv{Worktree: root}
	ctx := context.Background()
	read := func(p string) string { return call(ctx, env, "read_file", map[string]string{"path": p}) }

	assert.Equal(t, "A\n", read("a.txt"))
	assert.Contains(t, read("../"+filepath.Base(filepath.Dir(outside))+"/secret.txt"), "escapes project root")
	assert.Contains(t, read("link"), "escapes project root")
	assert.Contains(t, read("missing.txt"), "file not found")
	assert.Contains(t, read("dir"), "is a directory")
	assert.Contains(t, read("big.bin"), "too large")
	assert.Equal(t, "dir/\na.txt\nbig.bin\nlink", call(ctx, env, "list_dir", map[string]string{"path": ""}))
	assert.Equal(t, "(empty directory)", call(ctx, env, "list_dir", map[string]string{"path": "dir"}))
	assert.Contains(t, call(ctx, env, "list_dir", map[string]string{"path": "a.txt"}), "not a directory")
	assert.Contains(t, call(ctx, env, "list_dir", map[string]string{"path": ".."}), "escapes project root")
}

// TestTools_run_shell catches shell access in a run that does not allow it, a
// command running before its approval, and credentials in its environment.
func TestTools_run_shell(t *testing.T) {
	for _, tl := range agent.Tools(&orchestrator.AgentEnv{Worktree: t.TempDir()}, "coder") {
		assert.NotEqual(t, "run_shell", tl.Name, "offered without --allow-shell-exec")
	}
	t.Setenv("STAIRCASE_TEST_CREDENTIAL", "leaked")
	approver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"type":"yield_response","approved":true}`))
	}))
	defer approver.Close()
	r, out := runTools(t, nil, orchestrator.RunOptions{AllowShellExec: true},
		func(s *persistence.Store, _ string, projectID int64) {
			require.NoError(t, s.UpdateProjectWebhook(projectID, approver.URL))
		}, func(ctx context.Context, env *orchestrator.AgentEnv) []string {
			return []string{
				call(ctx, env, "run_shell", map[string]string{"command": "echo out; echo err >&2; exit 3", "reasoning": "r"}),
				call(ctx, env, "run_shell", map[string]string{"command": "echo ${STAIRCASE_TEST_CREDENTIAL:-absent}", "reasoning": "r"}),
				call(ctx, env, "run_shell", map[string]string{"command": "pwd", "reasoning": "r", "working_dir": ".."}),
			}
		})
	assert.Equal(t, []string{"exit=3\nout\nSTDERR: err\n", "exit=0\nabsent\n", "error: working_dir escapes project root"}, out)
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	assert.Contains(t, r.Types(), "yield_decided", "every command was decided before it ran")
}
