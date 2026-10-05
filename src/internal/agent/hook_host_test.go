package agent_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hostRun plays one approved Write through a fake Claude Code that follows the
// behaviour Claude Code documents for hooks: only exit status 2 or a "deny" stops
// the tool; a hook that is missing, crashes, times out or prints nothing is a
// non-blocking error and the tool runs. hookBin is the program the settings call
// ("" for the real one); hostEnv adds variables to the fake host.
func hostRun(t *testing.T, hookBin, hostEnv string) (runtest.Result, string) {
	script, err := json.Marshal([]fakeCall{{Tool: "Write", Input: map[string]any{"file_path": "$WT/new.txt", "content": "hello\n"}}})
	require.NoError(t, err)
	dir := t.TempDir()
	logFile := filepath.Join(dir, "decisions.log")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.json"), script, 0o600))
	bin := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\nFAKE_CLAUDE=%s FAKE_CLAUDE_LOG=%s FAKE_CLAUDE_HOST=documented %s exec %s \"$@\"\n",
		filepath.Join(dir, "script.json"), logFile, hostEnv, os.Args[0]), 0o755))
	r := runtest.Run(t, runtest.Options{Agent: &agent.ClaudeCode{Prompt: "p", Bin: bin, HookBin: hookBin}})
	b, _ := os.ReadFile(logFile)
	return r, strings.TrimSpace(string(b))
}

func scriptBin(t *testing.T, body string) string {
	bin := filepath.Join(t.TempDir(), "staircase")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	return bin
}

// TestClaudeCode_host_hook_failures: a hook that cannot answer must not become "no
// objection" in a host that carries on after a failed hook. Each case says what is
// shown, and what is not: a program that is missing, crashes or is killed makes the
// hook block, so the tool does not run; a host that gives up on a slow hook runs the
// tool anyway, and what holds then is the end-of-run check: nothing unapproved is
// committed. (That is not the same as the tool not running.)
func TestClaudeCode_host_hook_failures(t *testing.T) {
	t.Run("control: a working hook, an approved write", func(t *testing.T) {
		r, log := hostRun(t, "", "")
		require.NoError(t, r.Err)
		assert.Equal(t, "Write allow", strings.TrimSpace(strings.SplitN(log, "\n", 2)[0]))
		got, err := r.OnBranch("new.txt")
		require.NoError(t, err)
		assert.Equal(t, "hello\n", got)
	})

	for name, hookBin := range map[string]func(*testing.T) string{
		"the hook program is missing": func(t *testing.T) string { return filepath.Join(t.TempDir(), "not-installed") },
		"the hook program crashes":    func(t *testing.T) string { return scriptBin(t, "exit 1") },
		"the hook program is killed":  func(t *testing.T) string { return scriptBin(t, "kill -9 $$") },
		"the hook program cannot run": func(t *testing.T) string { return scriptBin(t, "exit 126") },
	} {
		t.Run(name+": the tool does not run", func(t *testing.T) {
			r, log := hostRun(t, hookBin(t), "")
			assert.Contains(t, log, "Write blocked", "the hook ended as a block")
			assert.NotContains(t, log, "proceeded")
			_, err := r.OnBranch("new.txt")
			assert.Error(t, err, "nothing was written, so nothing was committed")
		})
	}

	t.Run("the host gives up on a slow hook: the tool runs, nothing unapproved is committed", func(t *testing.T) {
		real := os.Args[0]
		// only the tool hook is slow (the session handshake answers), so the host gives up on that one call
		slow := scriptBin(t, "in=$(cat)\ncase \"$in\" in *PreToolUse*) sleep 2;; esac\nprintf '%s' \"$in\" | exec '"+real+"' \"$@\"")
		r, log := hostRun(t, slow, "FAKE_CLAUDE_HOOK_TIMEOUT_MS=300")
		assert.Contains(t, log, "Write proceeded-after-hook-failure", "the host ran the tool without an answer (this is the host's choice, not stAirCase's)")
		require.Error(t, r.Err, "what the host wrote was never approved")
		assert.Empty(t, r.Run.GitCommitHash, "so nothing was committed")
		assert.Contains(t, r.Types(), "unapproved_worktree_change", "the ungated write is on the audit chain")
		_, err := r.OnBranch("new.txt")
		assert.Error(t, err)
	})
}

// TestClaudeCode_a_session_whose_hooks_never_ran_is_not_governed: Claude Code calls the
// SessionStart hook when a session begins (probed on 2.1.236 in headless mode). When it never
// arrives, the hooks did not run, so nothing the session did was governed: the run fails and
// nothing is kept. The control is the same session with the hooks running.
func TestClaudeCode_a_session_whose_hooks_never_ran_is_not_governed(t *testing.T) {
	r, log := hostRun(t, "", "")
	require.NoError(t, r.Err, "control: the hooks ran")
	assert.Contains(t, log, "Write allow")

	r, _ = hostRun(t, "", "FAKE_CLAUDE_NO_SESSION_START=1")
	require.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "never ran staircase's hooks")
	assert.Empty(t, r.Run.GitCommitHash, "nothing it did is kept")
	_, err := r.OnBranch("new.txt")
	assert.Error(t, err)
}
