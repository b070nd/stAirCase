package agent_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var tomlCommand = regexp.MustCompile(`command="((?:[^"\\]|\\.)*)"`)

// fakeCodex plays `codex exec` through the hooks given with -c, the way the
// real CLI does: SessionStart, an apply_patch that adds hello.txt, and a
// shell command that writes gen.txt. FAKE_CODEX=nohooks ignores the hooks.
func fakeCodex() int {
	args := os.Args[1:]
	if f := os.Getenv("FAKE_CODEX_ARGS"); f != "" {
		b, _ := json.Marshal(args)
		_ = os.WriteFile(f, b, 0o600)
	}
	if os.Getenv("FAKE_CODEX") == "nohooks" {
		_ = os.WriteFile("hello.txt", []byte("ungoverned\n"), 0o644)
		return 0
	}
	hooks := map[string]string{}
	for i, a := range args {
		if a == "-c" && i+1 < len(args) {
			if ev, val, ok := strings.Cut(args[i+1], "="); ok && strings.HasPrefix(ev, "hooks.") {
				if m := tomlCommand.FindStringSubmatch(val); m != nil {
					hooks[strings.TrimPrefix(ev, "hooks.")] = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(m[1])
				}
			}
		}
	}
	call := func(event, tool string, input map[string]any) string {
		in, _ := json.Marshal(map[string]any{"hook_event_name": event, "tool_name": tool, "tool_input": input,
			"tool_use_id": "t-" + tool, "cwd": "."})
		cmd := exec.Command("/bin/sh", "-c", hooks[event])
		cmd.Stdin = bytes.NewReader(in)
		out, _ := cmd.Output()
		return string(out)
	}
	call("SessionStart", "", nil)
	patch := map[string]any{"command": "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"}
	if !strings.Contains(call("PreToolUse", "apply_patch", patch), `"allow"`) {
		return 1
	}
	_ = os.WriteFile("hello.txt", []byte("hello\n"), 0o644)
	call("PostToolUse", "apply_patch", patch)
	shell := map[string]any{"command": "make gen"}
	if !strings.Contains(call("PreToolUse", "Bash", shell), `"allow"`) {
		return 1
	}
	_ = os.WriteFile("gen.txt", []byte("generated\n"), 0o644) // what the command did
	fmt.Fprint(os.Stderr, call("PostToolUse", "Bash", shell))
	fmt.Println("done")
	return 0
}

func codexBin(t *testing.T, mode string) (bin, argsFile string) {
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.json")
	bin = filepath.Join(dir, "codex")
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\nFAKE_CODEX=%s FAKE_CODEX_ARGS=%s exec %s \"$@\"\n",
		mode, argsFile, os.Args[0]), 0o755))
	return bin, argsFile
}

// TestCodex_edits_are_decided_first_and_command_changes_after: an
// apply_patch edit is a proposal decided before it happens; a shell command
// runs in Codex's sandbox and the files it changed are reviewed afterwards.
// Both land on the run branch once approved.
func TestCodex_edits_are_decided_first_and_command_changes_after(t *testing.T) {
	bin, argsFile := codexBin(t, "1")
	r := runtest.Run(t, runtest.Options{Agent: &agent.Codex{Prompt: "p", Bin: bin}})
	require.NoError(t, r.Err)
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	for path, want := range map[string]string{"hello.txt": "hello\n", "gen.txt": "generated\n"} {
		got, err := r.OnBranch(path)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	var args []string
	b, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &args))
	assert.True(t, slices.Contains(args, "workspace-write"), "shell commands run in Codex's sandbox: %v", args)
	assert.Contains(t, args, "--dangerously-bypass-hook-trust", "otherwise Codex skips the hooks silently")
	assert.Contains(t, args, `approval_policy="never"`)
	assert.True(t, slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "hooks.Stop=") }),
		"the definition of done: Codex asks before it ends")
}

// TestCodex_without_its_hooks_nothing_is_kept: a Codex that never ran the
// hooks was not governed; the run fails and commits nothing.
func TestCodex_without_its_hooks_nothing_is_kept(t *testing.T) {
	bin, _ := codexBin(t, "nohooks")
	r := runtest.Run(t, runtest.Options{Agent: &agent.Codex{Prompt: "p", Bin: bin}})
	require.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "never ran staircase's hooks")
	assert.Equal(t, persistence.RunStatusFailed, r.Run.Status)
	assert.Empty(t, r.Run.GitCommitHash)
}
