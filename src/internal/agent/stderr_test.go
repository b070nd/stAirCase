package agent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A harness that fails has its stderr tail kept as a diagnostic (the debug log, scrubbed of the credential the harness was
// given), and out of the error the run ends with, which a caller may log or a runner may keep (PROTOCOL.md: Cloudroom persisted
// stderr tails despite saying it did not). Every adapter does this.
func TestAdapters_keep_a_stderr_tail_out_of_the_error(t *testing.T) {
	const credential = "the-credential-0815"
	adapters := map[string]func(bin string) orchestrator.Agent{
		"claude-code": func(bin string) orchestrator.Agent {
			return &agent.ClaudeCode{Prompt: "p", Bin: bin, PassEnv: []string{"PROBE_CREDENTIAL"}}
		},
		"gemini": func(bin string) orchestrator.Agent {
			return &agent.Gemini{Prompt: "p", Bin: bin, PassEnv: []string{"PROBE_CREDENTIAL"}}
		},
		"codex":    func(bin string) orchestrator.Agent { return &agent.Codex{Prompt: "p", Bin: bin} },
		"opencode": func(bin string) orchestrator.Agent { return &agent.OpenCode{Prompt: "p", Bin: bin} },
	}
	for name, mk := range adapters {
		t.Run(name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), name)
			require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho \"TAIL-MARKER failed with $PROBE_CREDENTIAL\" >&2\nexit 1\n"), 0o755))
			t.Setenv("PROBE_CREDENTIAL", credential)
			r := runtest.Run(t, runtest.Options{Agent: mk(bin), Run: orchestrator.RunOptions{Debug: true}})
			require.Error(t, r.Err)
			assert.NotContains(t, r.Err.Error(), "TAIL-MARKER")
			assert.NotContains(t, r.Err.Error(), credential)
			for _, e := range r.Events {
				assert.NotContains(t, e.Payload, "TAIL-MARKER", "audit event %s", e.EventType)
			}

			entries, err := os.ReadDir(filepath.Join(r.WsDir, "log"))
			require.NoError(t, err)
			require.Len(t, entries, 1)
			b, err := os.ReadFile(filepath.Join(r.WsDir, "log", entries[0].Name()))
			require.NoError(t, err)
			assert.Contains(t, string(b), "agent_stderr")
			assert.Contains(t, string(b), "TAIL-MARKER failed with")
			assert.NotContains(t, string(b), credential, "the credential the harness was given is scrubbed from the tail")
		})
	}
}

// A tail that outgrew its bound says that its start was dropped.
func TestStderrTail_says_when_it_dropped_the_start(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\nawk 'BEGIN{for(i=0;i<2000;i++) print \"line \" i \" of an endless complaint\"}' >&2\nexit 1\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	r := runtest.Run(t, runtest.Options{Agent: &agent.ClaudeCode{Prompt: "p", Bin: bin}, Run: orchestrator.RunOptions{Debug: true}})
	require.Error(t, r.Err)
	entries, err := os.ReadDir(filepath.Join(r.WsDir, "log"))
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(r.WsDir, "log", entries[0].Name()))
	require.NoError(t, err)
	log := string(b)
	assert.Contains(t, log, "line 1999 of an endless complaint", "the end is kept")
	assert.NotContains(t, log, "line 0 of an endless complaint")
	assert.True(t, strings.Contains(log, "earlier output dropped"), "and the log says the start is gone: %.300s", log)
}
