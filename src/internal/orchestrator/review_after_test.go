package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commandChanges plays an agent whose command (a formatter, a code
// generator) changes files directly, then asks for the changes to be
// reviewed, as the Codex adapter does after each shell command.
func commandChanges(got *orchestrator.Approval) orchestrator.AgentFunc {
	return func(ctx context.Context, env *orchestrator.AgentEnv) error {
		wt := env.Worktree
		if err := os.WriteFile(filepath.Join(wt, "gen.txt"), []byte("generated\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("reformatted\n"), 0o644); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(wt, "old.txt")); err != nil {
			return err
		}
		*got = env.ProposeWorktreeChanges(ctx, "codex", "the command `make fmt` changed these files")
		return nil
	}
}

var reviewBase = map[string]runtest.File{
	"f.txt":   {Content: "original\n", Mode: 0o644},
	"old.txt": {Content: "bye\n", Mode: 0o644},
}

// TestReviewAfter_approved_changes_are_committed: files a command changed
// come to a decision as one proposal; approved, they are committed like any
// approved change, and the certificate says they were reviewed afterwards.
func TestReviewAfter_approved_changes_are_committed(t *testing.T) {
	var got orchestrator.Approval
	r, _, s := certified(t, commandChanges(&got), orchestrator.RunOptions{}, nil, reviewBase)
	require.True(t, got.Approved, got.Feedback)
	for path, want := range map[string]string{"gen.txt": "generated\n", "f.txt": "reformatted\n"} {
		content, err := r.OnBranch(path)
		require.NoError(t, err)
		assert.Equal(t, want, content)
	}
	_, err := r.OnBranch("old.txt")
	assert.Error(t, err, "the deletion is committed")
	assert.Equal(t, 2, s.Predicate.CAL, "changes reviewed after they happened")
	assert.Contains(t, s.Predicate.Notes, "commands changed files that were reviewed after the fact")
}

// TestReviewAfter_rejected_changes_are_reverted: rejected, the worktree is put
// back to its approved state, so the run commits nothing and still succeeds.
func TestReviewAfter_rejected_changes_are_reverted(t *testing.T) {
	var got orchestrator.Approval
	var wt string
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		wt = env.Worktree
		if err := commandChanges(&got)(ctx, env); err != nil {
			return err
		}
		for path, want := range map[string]string{"f.txt": "original\n", "old.txt": "bye\n"} {
			b, err := os.ReadFile(filepath.Join(wt, path))
			if err != nil || string(b) != want {
				t.Errorf("%s not reverted: %q, %v", path, b, err)
			}
		}
		if _, err := os.Stat(filepath.Join(wt, "gen.txt")); err == nil {
			t.Error("gen.txt not removed")
		}
		return nil
	}
	r := runtest.Run(t, runtest.Options{Base: reviewBase, Agent: orchestrator.AgentFunc(agent),
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			// no policy rule: a person decides, and rejects
			require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(`{"rules":[]}`), 0o600))
			webhook(t, s, projectID, &operator{approve: false})
		}})
	require.NoError(t, r.Err)
	assert.False(t, got.Approved)
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	assert.Empty(t, r.Run.GitCommitHash, "nothing was approved")
}

// TestReviewAfter_nothing_changed: a command that changed nothing needs no
// decision.
func TestReviewAfter_nothing_changed(t *testing.T) {
	var got orchestrator.Approval
	r := runtest.Run(t, runtest.Options{Base: reviewBase, Agent: orchestrator.AgentFunc(
		func(ctx context.Context, env *orchestrator.AgentEnv) error {
			got = env.ProposeWorktreeChanges(ctx, "codex", "ls")
			return nil
		})})
	require.NoError(t, r.Err)
	assert.True(t, got.Approved)
	assert.NotContains(t, r.Types(), "yield_decided")
}
