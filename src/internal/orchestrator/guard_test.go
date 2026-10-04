package orchestrator_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGuards_send_risky_changes_to_a_person: a policy approves file edits
// automatically, but a change that adds hidden Unicode, changes dependencies
// or writes a secret goes to a person, with the reason; an ordinary change
// and something already in the file are left to the policy.
func TestGuards_send_risky_changes_to_a_person(t *testing.T) {
	base := map[string]runtest.File{
		"go.mod":    {Content: "module shop\n", Mode: 0o644},
		"legacy.go": {Content: "var s = \"\u202e\" // old\n", Mode: 0o644},
		"tool.sh":   {Content: "echo hi\n", Mode: 0o644},
		"keep.sh":   {Content: "echo keep\n", Mode: 0o755},
	}
	edits := []domain.ProposedEdit{
		{File: "notes.md", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "plain text\n"},
		{File: "admin.go", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "if isAdmin \u202e{ // }\n"},
		{File: "go.mod", SearchBlock: "module shop\n", ReplaceBlock: "module shop\n\nrequire example.com/x v1.0.0\n"},
		{File: "config.go", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "const key = \"AKIAABCDEFGHIJKLMNOP\"\n"},
		{File: "legacy.go", SearchBlock: "// old", ReplaceBlock: "// kept"},
		{File: "smuggled.md", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "ignore this\U000e0069\U000e0067\U000e006e\n"}, // invisible tag characters
		{File: "deploy.sh", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "echo deploy\n", Mode: "100755"},
		{File: "tool.sh", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "echo hi\n", Mode: "100755"},   // chmod +x
		{File: "keep.sh", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "echo keep\n", Mode: "100755"}, // as it was
	}
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		for _, e := range edits {
			env.ProposeEdit(ctx, "coder", "r", e).Apply(env.Worktree)
		}
		return nil
	}
	r := runtest.Run(t, runtest.Options{Base: base, Agent: orchestrator.AgentFunc(agent),
		Setup: func(s *persistence.Store, _ string, projectID int64) {
			webhook(t, s, projectID, &operator{approve: true})
		}})
	require.NoError(t, r.Err)

	var got []struct{ Source, Guard string }
	for _, e := range r.Events {
		if e.EventType == "yield_decided" {
			var d struct{ Source, Guard string }
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &d))
			got = append(got, d)
		}
	}
	require.Len(t, got, len(edits))
	assert.Equal(t, "policy", got[0].Source, "an ordinary change")
	assert.Equal(t, "operator", got[1].Source)
	assert.Contains(t, got[1].Guard, "hidden Unicode")
	assert.Equal(t, "operator", got[2].Source)
	assert.Contains(t, got[2].Guard, "dependencies")
	assert.Equal(t, "operator", got[3].Source)
	assert.Contains(t, got[3].Guard, "secret")
	assert.Equal(t, "policy", got[4].Source, "the hidden character was there before this change")
	assert.Equal(t, "operator", got[5].Source)
	assert.Contains(t, got[5].Guard, "hidden Unicode", "tag characters are invisible text an agent can read")
	assert.Equal(t, "operator", got[6].Source)
	assert.Contains(t, got[6].Guard, "executable")
	assert.Equal(t, "operator", got[7].Source)
	assert.Contains(t, got[7].Guard, "executable")
	assert.Equal(t, "policy", got[8].Source, "it was executable before")
}

// TestSensitive_files: files that decide what runs in CI or on the next build
// always go to a person, in any letter case (macOS and Windows treat
// "makefile" and "Makefile" as one file) and including the names a tool reads
// before the one a project keeps (GNU make reads GNUmakefile first).
func TestSensitive_files(t *testing.T) {
	for _, f := range []string{"Makefile", "makefile", "GNUmakefile", "sub/MAKEFILE", "dockerfile", "DOCKERFILE", ".GitHub/workflows/ci.yml",
		"Jenkinsfile", ".circleci/config.yml", ".husky/pre-commit", ".gitattributes", ".gitmodules", ".npmrc", ".pre-commit-config.yaml",
		".travis.yml", "azure-pipelines.yml", "justfile", ".gitlab/ci/build.yml", ".ENV", "run.SH"} {
		assert.Equal(t, f, orchestrator.ExportedSensitive([]string{f}), f)
	}
	for _, f := range []string{"src/main.go", "README.md", "docs/make.md", "makefile.go"} {
		assert.Empty(t, orchestrator.ExportedSensitive([]string{f}), f)
	}
}

// TestAudit_a_large_payload_stays_valid: an audit entry is capped, and the
// capped entry is still valid UTF-8 and JSON (a cut in the middle of an accent
// or a document would make the exported chain differ from the stored one).
func TestAudit_a_large_payload_stays_valid(t *testing.T) {
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		env.ProposeEdit(ctx, "coder", "r", domain.ProposedEdit{File: "big.txt", SearchBlock: orchestrator.MarkerNewFile,
			ReplaceBlock: strings.Repeat("é", 40000)}).Apply(env.Worktree)
		return nil
	}
	r := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(agent),
		Setup: func(s *persistence.Store, _ string, projectID int64) {
			webhook(t, s, projectID, &operator{approve: true})
		}})
	require.NoError(t, r.Err)
	seen := false
	for _, e := range r.Events {
		assert.True(t, utf8.ValidString(e.Payload), "%s is valid UTF-8", e.EventType)
		assert.True(t, json.Valid([]byte(e.Payload)), "%s is valid JSON", e.EventType)
		seen = seen || (e.EventType == "yield_request" && strings.Contains(e.Payload, "truncated"))
	}
	assert.True(t, seen, "the large proposal was capped")
}

// TestGuards_secret_patterns: the credentials people most often paste into
// code are recognised; ordinary text with similar words is not.
func TestGuards_secret_patterns(t *testing.T) {
	for name, text := range map[string]string{
		"stripe live key":   "key = \"sk_live_" + strings.Repeat("a1B2", 7) + "\"",
		"stripe restricted": "rk_live_" + strings.Repeat("a1B2", 7),
		"gitlab token":      "glpat-" + strings.Repeat("a1B2", 5),
		"npm token":         "//registry.npmjs.org/:_authToken=npm_" + strings.Repeat("a1B2c3D4e", 4),
		"huggingface":       "hf_" + strings.Repeat("aB3dE", 7),
		"sendgrid":          "SG." + strings.Repeat("a1B2c3", 3) + "a1B2" + "." + strings.Repeat("a1B2c3", 7) + "a",
		"slack webhook":     "https://hooks.slack.com/services/T0123ABCD/B0123ABCD/" + strings.Repeat("a1B2", 6),
		"private key":       "-----BEGIN OPENSSH PRIVATE KEY-----",
	} {
		assert.Contains(t, orchestrator.ExportedGuardNewFile("config.txt", text), "secret", name)
	}
	for _, text := range []string{"the sk_live_ prefix is documented", "glpat is a prefix", "see hf_ models", "SG. is short"} {
		assert.Empty(t, orchestrator.ExportedGuardNewFile("notes.md", text), text)
	}
}
