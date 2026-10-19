package agent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCapabilities_each_adapter_declares_what_it_supports (V11-C01 scope 8): what each adapter supports is declared in one table,
// read from what the adapters do. The table describes; it grants nothing.
func TestCapabilities_each_adapter_declares_what_it_supports(t *testing.T) {
	assert.Equal(t, []string{"claude-code", "codex", "gemini", "opencode"}, agent.AdapterNames())
	want := map[string]agent.Capabilities{
		"claude-code": {NativeResume: true, Sandbox: true, MCPGate: true, Hooks: true, HostMode: true},
		"codex":       {NativeResume: true, Sandbox: true, Hooks: true},
		"gemini":      {NativeResume: true, Hooks: true},
		"opencode":    {},
	}
	for name, w := range want {
		got, ok := agent.CapabilitiesOf(name)
		require.True(t, ok, name)
		got.Note = "" // the note is prose
		assert.Equal(t, w, got, name)
	}
	_, ok := agent.CapabilitiesOf("a-stand-in")
	assert.False(t, ok, "an adapter that declares nothing is not assumed to support anything")
}

// TestCapabilities_native_resume_is_what_the_adapter_does: the declared resume support and the adapter's own answer to the
// orchestrator are one value, so a run is never told one thing and shown another.
func TestCapabilities_native_resume_is_what_the_adapter_does(t *testing.T) {
	for name, a := range map[string]orchestrator.Agent{
		"claude-code": &agent.ClaudeCode{}, "codex": &agent.Codex{}, "gemini": &agent.Gemini{}, "opencode": &agent.OpenCode{},
	} {
		caps, _ := agent.CapabilitiesOf(name)
		r, ok := a.(orchestrator.SessionResumer)
		assert.Equal(t, caps.NativeResume, ok && r.NativeResume(), name)
	}
}

func TestCapabilities_are_shown_in_words(t *testing.T) {
	caps, _ := agent.CapabilitiesOf("gemini")
	yes, no := caps.Supports()
	assert.Equal(t, []string{"native resume", "hooks"}, yes)
	assert.Equal(t, []string{"agent sandbox", "MCP gate", "host mode"}, no)
}

// launchEnd is the one launch_ended a stand-in agent's run left on its chain.
func launchEnd(t *testing.T, r runtest.Result) history.LaunchEnded {
	t.Helper()
	var ends []history.LaunchEnded
	for _, e := range r.Events {
		if e.EventType == "launch_ended" {
			var p history.LaunchEnded
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &p))
			ends = append(ends, p)
		}
	}
	require.Len(t, ends, 1)
	return ends[0]
}

// failingBin is a program that ends with the given status without running a hook.
func failingBin(t *testing.T, name string, status string) string {
	bin := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexit "+status+"\n"), 0o755))
	return bin
}

// TestAdapters_record_a_lifecycle_failure_as_its_class (V11-C01 scope 8): an adapter whose session was not governed or never
// started is rejected, one whose process ends with an error is a host exit. Either way the chain says which, the run commits
// nothing, and no check ran: a lifecycle failure is never a check result and never acceptance.
func TestAdapters_record_a_lifecycle_failure_as_its_class(t *testing.T) {
	type run func(t *testing.T) runtest.Result
	hooks := map[string]run{
		"claude-code": func(t *testing.T) runtest.Result {
			r, _ := hostRun(t, "", "FAKE_CLAUDE_NO_SESSION_START=1")
			return r
		},
		"codex": func(t *testing.T) runtest.Result {
			bin, _ := codexBin(t, "nohooks")
			return runtest.Run(t, runtest.Options{Agent: &agent.Codex{Prompt: "p", Bin: bin}})
		},
		"gemini": func(t *testing.T) runtest.Result {
			bin, _, _ := geminiBin(t, "nohooks", nil)
			return runtest.Run(t, runtest.Options{Agent: &agent.Gemini{Prompt: "p", Bin: bin}})
		},
		"opencode": func(t *testing.T) runtest.Result {
			bin, _, _ := openCodeBin(t, "nohooks", nil)
			return runtest.Run(t, runtest.Options{Agent: &agent.OpenCode{Prompt: "p", Bin: bin}})
		},
	}
	for name, play := range hooks {
		t.Run(name+": hooks that never ran are rejected", func(t *testing.T) {
			r := play(t)
			require.Error(t, r.Err)
			end := launchEnd(t, r)
			assert.Equal(t, history.LaunchFailed, end.Outcome)
			assert.Equal(t, history.ClassRejected, end.Class)
			assert.Equal(t, persistence.RunStatusFailed, r.Run.Status)
			assert.Empty(t, r.Run.GitCommitHash)
			assert.NotContains(t, r.Types(), "phase_checked", "no check ran, so none is blamed")
		})
	}

	agents := map[string]func(bin string) orchestrator.Agent{
		"claude-code": func(bin string) orchestrator.Agent { return &agent.ClaudeCode{Prompt: "p", Bin: bin} },
		"codex":       func(bin string) orchestrator.Agent { return &agent.Codex{Prompt: "p", Bin: bin} },
		"gemini":      func(bin string) orchestrator.Agent { return &agent.Gemini{Prompt: "p", Bin: bin} },
		"opencode":    func(bin string) orchestrator.Agent { return &agent.OpenCode{Prompt: "p", Bin: bin} },
	}
	for name, mk := range agents {
		t.Run(name+": a process that ends with an error is a host exit", func(t *testing.T) {
			r := runtest.Run(t, runtest.Options{Agent: mk(failingBin(t, name, "3"))})
			require.Error(t, r.Err)
			end := launchEnd(t, r)
			assert.Equal(t, history.LaunchFailed, end.Outcome)
			assert.Equal(t, history.ClassHostExit, end.Class)
			assert.Empty(t, r.Run.GitCommitHash)
		})
		t.Run(name+": a program that cannot start is rejected", func(t *testing.T) {
			r := runtest.Run(t, runtest.Options{Agent: mk(filepath.Join(t.TempDir(), "not-installed"))})
			require.Error(t, r.Err)
			end := launchEnd(t, r)
			assert.Equal(t, history.LaunchFailed, end.Outcome)
			assert.Equal(t, history.ClassRejected, end.Class)
			assert.Empty(t, r.Run.GitCommitHash)
		})
	}
}
