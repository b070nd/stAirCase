package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doctorWorkspace is a healthy workspace and a PATH of stand-in tools: the ones
// given, and git.
func doctorWorkspace(t *testing.T, tools map[string]string) (ws string) {
	ws = t.TempDir()
	viper.Set("STAIRCASE_DIR", ws)
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", "") })
	require.NoError(t, crypto.GenerateKey(ws))
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	bin := t.TempDir()
	for name, script := range tools {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755))
	}
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(git, filepath.Join(bin, "git"))) // the PATH is these tools and git, so what is installed here does not show
	t.Setenv("PATH", bin)
	return ws
}

// TestDoctor_reports_the_optional_tools: what stAirCase works without but gains
// something from (signing, a sandbox, the agents) is listed with what it was
// found to be, and a missing one is information, not a failure.
func TestDoctor_reports_the_optional_tools(t *testing.T) {
	doctorWorkspace(t, map[string]string{
		"ssh":        `echo "OpenSSH_9.6p1, OpenSSL 3.0.13" >&2`,
		"ssh-keygen": `exit 0`,
		"claude":     `case "$1" in --version) echo "2.1.236 (Claude Code)";; auth) echo '{"loggedIn": true, "authMethod": "oauth"}';; esac`,
		"codex":      `case "$1" in --version) echo "codex-cli 0.159.0";; login) echo "Not logged in" >&2; exit 1;; esac`,
	})
	out, err := captureStdout(t, func() error { return doctorHandler(nil, nil) })
	require.NoError(t, err, "a missing optional tool is not a failed check:\n%s", out)
	for _, want := range []string{
		"Optional tools",
		"ssh-keygen (OpenSSH 9.6)",
		"Claude Code 2.1.236 (logged in)",
		"Codex codex-cli 0.159.0 (not logged in",
		"Gemini CLI: not installed",
		"OpenCode: not installed",
		"sandbox",
	} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "oauth", "what a login status prints is not repeated")
}

// TestDoctor_ssh_keygen: signatures need OpenSSH 8.0, and without ssh-keygen
// there are none.
func TestDoctor_ssh_keygen(t *testing.T) {
	doctorWorkspace(t, map[string]string{"ssh": `echo "OpenSSH_7.9p1" >&2`, "ssh-keygen": `exit 0`})
	out, err := captureStdout(t, func() error { return doctorHandler(nil, nil) })
	require.NoError(t, err)
	assert.Contains(t, out, "needs 8.0")

	doctorWorkspace(t, nil)
	out, err = captureStdout(t, func() error { return doctorHandler(nil, nil) })
	require.NoError(t, err)
	assert.Contains(t, out, "ssh-keygen: not found")
	assert.Contains(t, out, "--sign-approvals")
}

// TestDoctor_finds_interrupted_runs: a run that was interrupted before it
// committed and holds approved changes in its journal is listed with the
// command that recovers it; finished runs and runs that already have a commit are not.
func TestDoctor_finds_interrupted_runs(t *testing.T) {
	ws := doctorWorkspace(t, nil)
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	store := persistence.NewStore(db)
	v, _ := store.CreateVendor("v")
	p, _ := store.CreateProject(v.ID, "p", t.TempDir())
	_, err = store.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	require.NoError(t, err)
	c, _ := store.CreateCase(p.ID)
	mk := func(status, commit string, journaled int) int64 {
		r, err := store.CreateRun(c.ID, 1, "staircase/run-x")
		require.NoError(t, err)
		require.NoError(t, store.UpdateRunStatus(r.ID, status, nil, commit))
		if journaled > 0 {
			require.NoError(t, os.MkdirAll(filepath.Join(ws, "journal"), 0o700))
			line := `{"seq":1,"source":"operator","request":{}}` + "\n"
			var b []byte
			for i := 0; i < journaled; i++ {
				b = append(b, line...)
			}
			require.NoError(t, os.WriteFile(filepath.Join(ws, "journal", "run-"+strconv.FormatInt(r.ID, 10)+".approved.jsonl"), b, 0o600))
		}
		return r.ID
	}
	running := mk(persistence.RunStatusRunning, "", 2)
	killed := mk(persistence.RunStatusKilled, "", 1)
	mk(persistence.RunStatusKilled, "abc123", 3)  // already has its commit
	mk(persistence.RunStatusFailed, "", 0)        // nothing approved
	mk(persistence.RunStatusSuccess, "def456", 1) // finished
	require.NoError(t, db.Close())

	out, err := captureStdout(t, func() error { return doctorHandler(nil, nil) })
	require.NoError(t, err, "an interrupted run is a warning, not a failed check")
	assert.Contains(t, out, "run #"+strconv.FormatInt(running, 10)+" was interrupted with 2 approved change(s)")
	assert.Contains(t, out, "staircase recover "+strconv.FormatInt(running, 10)+" --force")
	assert.Contains(t, out, "run #"+strconv.FormatInt(killed, 10)+" was interrupted with 1 approved change(s): staircase recover "+strconv.FormatInt(killed, 10))
	assert.Equal(t, 2, strings.Count(out, "was interrupted"), "only the two that can be recovered")
}

// TestDoctor_legal_hold: a legal hold in the workspace is said out loud.
func TestDoctor_legal_hold(t *testing.T) {
	ws := doctorWorkspace(t, nil)
	require.NoError(t, os.WriteFile(filepath.Join(ws, "legal-hold"), []byte("matter 42\n"), 0o600))
	out, err := captureStdout(t, func() error { return doctorHandler(nil, nil) })
	require.NoError(t, err)
	assert.Contains(t, out, "legal hold")
}
