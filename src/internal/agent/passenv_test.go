package agent_test

import (
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

// probe runs the Claude Code adapter against a program that records which of two variables
// it can see, and returns what it saw ("pass=<value|unset> other=<value|unset>").
func probe(t *testing.T, pass []string) (string, error) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen")
	bin := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\necho \"pass=${PROBE_CREDENTIAL-unset} other=${PROBE_OTHER-unset}\" >%s\nprintf '{\"is_error\":false,\"result\":\"ok\"}'\n", seen), 0o755))
	t.Setenv("PROBE_CREDENTIAL", "the-credential")
	t.Setenv("PROBE_OTHER", "not-asked-for")
	r := runtest.Run(t, runtest.Options{Agent: &agent.ClaudeCode{Prompt: "p", Bin: bin, PassEnv: pass}})
	b, _ := os.ReadFile(seen)
	if r.Err != nil && strings.Contains(r.Err.Error(), "never ran staircase's hooks") {
		r.Err = nil // this probe program is not a Claude Code and never calls a hook: only what it saw matters here
	}
	return strings.TrimSpace(string(b)), r.Err
}

// TestClaudeCode_gets_a_credential_only_by_name: the agent's environment holds nothing that could
// carry a credential unless the user names it (--pass-env); a name that is not set is not passed;
// something that is not a name (a pasted "NAME=secret") is refused without echoing it.
func TestClaudeCode_gets_a_credential_only_by_name(t *testing.T) {
	seen, err := probe(t, nil)
	require.NoError(t, err)
	assert.Equal(t, "pass=unset other=unset", seen, "by default the agent sees neither variable")

	seen, err = probe(t, []string{"PROBE_CREDENTIAL"})
	require.NoError(t, err)
	assert.Equal(t, "pass=the-credential other=unset", seen, "the named variable only")

	seen, err = probe(t, []string{"PROBE_NOT_SET_ANYWHERE"})
	require.NoError(t, err)
	assert.Equal(t, "pass=unset other=unset", seen)

	_, err = probe(t, []string{"PROBE_CREDENTIAL=the-credential"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "the-credential", "a pasted secret is not echoed")
	assert.Contains(t, err.Error(), "names of environment variables")
}

// TestGemini_gets_a_credential_only_by_name: the same rule for Gemini CLI, and its arguments
// carry --skip-trust (a headless Gemini refuses a folder it was not told to trust).
func TestGemini_gets_a_credential_only_by_name(t *testing.T) {
	probeGemini := func(pass []string) (string, string, error) {
		dir := t.TempDir()
		seen, args := filepath.Join(dir, "seen"), filepath.Join(dir, "args")
		bin := filepath.Join(dir, "gemini")
		require.NoError(t, os.WriteFile(bin, fmt.Appendf(nil, "#!/bin/sh\necho \"pass=${PROBE_CREDENTIAL-unset} other=${PROBE_OTHER-unset}\" >%s\necho \"$@\" >%s\nprintf '{\"response\":\"ok\"}'\n", seen, args), 0o755))
		t.Setenv("PROBE_CREDENTIAL", "the-credential")
		t.Setenv("PROBE_OTHER", "not-asked-for")
		r := runtest.Run(t, runtest.Options{Agent: &agent.Gemini{Prompt: "p", Bin: bin, PassEnv: pass}})
		b, _ := os.ReadFile(seen)
		a, _ := os.ReadFile(args)
		err := r.Err
		if err != nil && strings.Contains(err.Error(), "never ran staircase's hooks") {
			err = nil // this probe is not a Gemini and calls no hook: only what it saw matters here
		}
		return strings.TrimSpace(string(b)), string(a), err
	}
	seen, args, err := probeGemini(nil)
	require.NoError(t, err)
	assert.Equal(t, "pass=unset other=unset", seen)
	assert.Contains(t, args, "--skip-trust")
	seen, _, err = probeGemini([]string{"PROBE_CREDENTIAL"})
	require.NoError(t, err)
	assert.Equal(t, "pass=the-credential other=unset", seen)
	_, _, err = probeGemini([]string{"PROBE_CREDENTIAL=the-credential"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "the-credential")
}
