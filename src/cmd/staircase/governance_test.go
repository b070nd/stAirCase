package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const teamBlueprint = `name: hello
supervisor: supervisor
agents:
  - {name: supervisor, model: claude-sonnet-4-6, prompt: "You coordinate the work."}
  - {name: coder, model: claude-sonnet-4-6, prompt: "You write code."}
edges:
  - {from: supervisor, to: coder}
  - {from: coder, to: supervisor}
cases:
  - slug: greet
    prd: Create a greeting file
    stories:
      - text: Create GREETING.md
        scope: {allow: [GREETING.md], max_files: 1}
`

// TestGovernanceUse_imports_the_teams_blueprints: `governance use` brings the
// team's blueprints into the workspace with the pinned commit as their source,
// prints how to bind them, and `governance status` lists them.
func TestGovernanceUse_imports_the_teams_blueprints(t *testing.T) {
	ws := t.TempDir()
	viper.Set("STAIRCASE_DIR", ws)
	t.Cleanup(func() { viper.Set("STAIRCASE_DIR", ""); governanceYes = false })
	require.NoError(t, crypto.GenerateSigningKey(ws))
	src := t.TempDir()
	for f, content := range map[string]string{"policy.json": `{"rules":[]}`, "blueprints/hello/blueprint.yaml": teamBlueprint} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(src, f)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(src, f), []byte(content), 0o644))
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@t"}, {"config", "user.name", "T"}, {"add", "-A"}, {"commit", "-q", "-m", "team"}} {
		out, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	commit, _ := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()

	governanceYes, governanceRef = true, "main"
	out, err := captureStdout(t, func() error { return governanceUseCmd.RunE(governanceUseCmd, []string{src}) })
	require.NoError(t, err)
	assert.Contains(t, out, "blueprint hello (imported)")
	assert.Contains(t, out, "staircase project bind")

	store, db, err := openStore()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	all, err := store.ListBlueprints()
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, strings.TrimSpace(string(commit)), all[0].GitSHA)

	out, err = captureStdout(t, func() error { return governanceUseCmd.RunE(governanceUseCmd, []string{src}) })
	require.NoError(t, err)
	assert.Contains(t, out, "already imported")
	out, err = captureStdout(t, func() error { return governanceStatusCmd.RunE(governanceStatusCmd, nil) })
	require.NoError(t, err)
	assert.Contains(t, out, "blueprint hello")
}
