package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureStdout(t *testing.T, f func() error) (string, error) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdout
	os.Stdout = w
	ferr := f()
	os.Stdout = old
	_ = w.Close()
	var b bytes.Buffer
	_, _ = io.Copy(&b, r)
	return b.String(), ferr
}

// TestHookTemplate: the settings a company deploys make every Claude Code or
// Codex session on its machines go through stAirCase (--require), with the
// long hook timeout a person's decision needs.
func TestHookTemplate(t *testing.T) {
	hookTemplateBin = "/opt/homebrew/bin/staircase"
	t.Cleanup(func() { hookTemplateBin = "" })

	out, err := captureStdout(t, func() error { return hookTemplateHandler(nil, []string{"claude-code"}) })
	require.NoError(t, err)
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
		ManagedOnly bool `json:"allowManagedHooksOnly"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &s), out)
	assert.True(t, s.ManagedOnly, "user and repository hooks do not load")
	pre := s.Hooks["PreToolUse"][0]
	assert.Equal(t, "*", pre.Matcher)
	assert.Equal(t, "'/opt/homebrew/bin/staircase' hook claude-code --require", pre.Hooks[0].Command)
	assert.Greater(t, pre.Hooks[0].Timeout, 3600, "a person may take long to decide")
	assert.NotEmpty(t, s.Hooks["PostToolUse"])

	out, err = captureStdout(t, func() error { return hookTemplateHandler(nil, []string{"codex"}) })
	require.NoError(t, err)
	for _, want := range []string{"[[hooks.PreToolUse]]", "[[hooks.PostToolUse]]", "[[hooks.SessionStart]]", "hook codex --require"} {
		assert.True(t, strings.Contains(out, want), "%q in:\n%s", want, out)
	}

	out, err = captureStdout(t, func() error { return hookTemplateHandler(nil, []string{"gemini"}) })
	require.NoError(t, err)
	var g struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &g), out)
	assert.Equal(t, "'/opt/homebrew/bin/staircase' hook gemini --require", g.Hooks["BeforeTool"][0].Hooks[0].Command)
	assert.Greater(t, g.Hooks["BeforeTool"][0].Hooks[0].Timeout, 3600*1000, "milliseconds: a person may take long to decide")
	assert.NotEmpty(t, g.Hooks["SessionStart"])
	assert.NotEmpty(t, g.Hooks["AfterTool"])

	assert.Error(t, hookTemplateHandler(nil, []string{"cursor"}))
}
