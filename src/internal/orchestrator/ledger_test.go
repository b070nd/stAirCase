package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ledgerRun makes a certified run that creates, edits (LF and CRLF files),
// overwrites, and deletes, and returns what a verifier needs.
func ledgerRun(t *testing.T) (r runtest.Result, commit string, ledger []byte, digest string) {
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		propose := func(e domain.ProposedEdit) error {
			ap := env.ProposeEdit(ctx, "claude-code", "r", e)
			if !ap.Approved {
				return nil
			}
			return ap.Apply(env.Worktree)
		}
		for _, e := range []domain.ProposedEdit{
			{File: "new/hello.txt", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "hello\n"},
			{File: "lf.txt", SearchBlock: "b\n", ReplaceBlock: "B\n"},
			{File: "crlf.txt", SearchBlock: "y\n", ReplaceBlock: "Y\n"},
			{File: "run.sh", SearchBlock: "echo a", ReplaceBlock: "echo b"},
			{File: "old.txt", SearchBlock: orchestrator.MarkerDeleteFile},
			{File: "lf.txt", SearchBlock: "a\n", ReplaceBlock: "A\n"}, // the same file again
		} {
			if err := propose(e); err != nil {
				return err
			}
		}
		return nil
	}
	base := map[string]runtest.File{
		"lf.txt":   {Content: "a\nb\n", Mode: 0o644},
		"crlf.txt": {Content: "x\r\ny\r\n", Mode: 0o644},
		"run.sh":   {Content: "echo a\n", Mode: 0o755},
		"old.txt":  {Content: "bye\n", Mode: 0o644},
	}
	r, commit, s := certified(t, agent, orchestrator.RunOptions{Plan: &plan.Plan{Harness: "claude-code"}}, nil, base)
	require.NotEmpty(t, s.Predicate.Ledger, "the certificate names the ledger")
	require.NotEmpty(t, s.Predicate.Policy)
	b, err := os.ReadFile(filepath.Join(r.WsDir, "audit", "run-1.ledger.json"))
	require.NoError(t, err)
	fi, err := os.Stat(filepath.Join(r.WsDir, "audit", "run-1.ledger.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the ledger holds content")
	return r, commit, b, s.Predicate.Ledger
}

// TestRebuild_reproduces_the_commit: the ledger's digest is in the signed
// certificate, and replaying its approved proposals on the base commit gives
// exactly the commit's tree: created, edited (LF and CRLF), overwritten and
// deleted files, the exec bit and all.
func TestRebuild_reproduces_the_commit(t *testing.T) {
	r, commit, ledger, digest := ledgerRun(t)
	assert.Equal(t, sha256Hex(string(ledger)), digest)

	tree, l, err := orchestrator.RebuildTree(r.Repo, ledger)
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", commit+"^{tree}")), tree)
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", commit+"^")), l.Base)
	assert.Len(t, l.Proposals, 6)
}

// TestRebuild_a_changed_ledger_gives_another_tree: a ledger whose proposals
// were altered rebuilds a different tree, one that names a base that is not a
// commit is refused, and a proposal that no longer applies is refused.
func TestRebuild_a_changed_ledger_gives_another_tree(t *testing.T) {
	r, commit, ledger, _ := ledgerRun(t)
	want := strings.TrimSpace(git(t, r.Repo, "rev-parse", commit+"^{tree}"))

	altered := []byte(strings.Replace(string(ledger), `hello\n`, `HELLO\n`, 1))
	require.NotEqual(t, string(ledger), string(altered))
	tree, _, err := orchestrator.RebuildTree(r.Repo, altered)
	require.NoError(t, err)
	assert.NotEqual(t, want, tree)

	_, _, err = orchestrator.RebuildTree(r.Repo, []byte(strings.Replace(string(ledger), `"base":"`, `"base":"0000`, 1)))
	assert.Error(t, err)
	_, _, err = orchestrator.RebuildTree(r.Repo, []byte(strings.Replace(string(ledger), `"search_block":"b\n"`, `"search_block":"not there\n"`, 1)))
	assert.ErrorContains(t, err, "search_block not found")
	_, _, err = orchestrator.RebuildTree(r.Repo, []byte(`{"version":9}`))
	assert.ErrorContains(t, err, "version")
}
