package orchestrator_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
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

// TestReviewAfter_binary_changes: a file that is not valid UTF-8 (an image, a
// Latin-1 source file) changed by a command or an editor is decided as a
// whole-file change shown by its size and digest, committed byte for byte, and
// the commit still rebuilds from its ledger, which then has version 2.
func TestReviewAfter_binary_changes(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), 0xff, 0xfe, 0x00, 0x80, 'x')
	latin1 := []byte("caf\xe9 au lait\n")
	var shown []domain.ProposedEdit
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		for name, b := range map[string][]byte{"logo.png": png, "menu.txt": latin1} {
			if err := os.WriteFile(filepath.Join(env.Worktree, name), b, 0o644); err != nil {
				return err
			}
		}
		got := env.ProposeWorktreeChanges(ctx, "cursor", "an editor wrote an image and a Latin-1 file")
		if !got.Approved {
			t.Errorf("not approved: %s", got.Feedback)
		}
		return nil
	}
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(agent),
		Setup: func(s *persistence.Store, ws string, p int64) {
			require.NoError(t, crypto.GenerateSigningKey(ws))
			scoped(op, `{"rules":[]}`)(s, ws, p) // a person decides
		}})
	require.NoError(t, r.Err)
	shown = op.seen[0].ProposedEdits
	require.Len(t, shown, 2)
	for _, e := range shown {
		assert.Empty(t, e.ReplaceBlock, "no unreadable text is put in front of a person")
		assert.NotEmpty(t, e.BinarySHA256, e.File)
		assert.NotZero(t, e.BinaryBytes, e.File)
	}

	for name, want := range map[string][]byte{"logo.png": png, "menu.txt": latin1} {
		got, err := exec.Command("git", "-C", r.Repo, "show", fmt.Sprintf("staircase/run-%d:%s", r.Run.ID, name)).Output()
		require.NoError(t, err)
		assert.Equal(t, want, got, "%s is committed byte for byte", name)
	}
	ledger, err := os.ReadFile(orchestrator.LedgerPath(r.WsDir, r.Run.ID))
	require.NoError(t, err)
	assert.Contains(t, string(ledger), `"version":2`)
	tree, _, err := orchestrator.RebuildTree(r.Repo, ledger)
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", fmt.Sprintf("staircase/run-%d^{tree}", r.Run.ID))), tree)
}

// TestReviewAfter_binary_changes_always_go_to_a_person: a file nobody can read
// is not approved by a rule, the agreed task or a model, whatever they say.
func TestReviewAfter_binary_changes_always_go_to_a_person(t *testing.T) {
	op := &operator{approve: true}
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		if err := os.WriteFile(filepath.Join(env.Worktree, "logo.png"), []byte("\x89PNG\xff\x00"), 0o644); err != nil {
			return err
		}
		env.ProposeWorktreeChanges(ctx, "cursor", "an editor wrote an image")
		return nil
	}
	r := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(agent),
		Setup: func(s *persistence.Store, ws string, p int64) {
			scoped(op, "")(s, ws, p) // the harness policy auto-approves file edits
		}})
	require.NoError(t, r.Err)
	assert.Equal(t, []string{"file_edit:operator"}, sources(t, r), "the policy rule did not decide it")
	require.Len(t, op.seen, 1)
	assert.Contains(t, op.seen[0].Guard, "not text and cannot be reviewed")
}

// TestBinary_limits_and_ledger_versions: a file that is not text is refused
// above its size limit, and a version 1 ledger (text only) that carries
// content_b64 is refused.
func TestBinary_limits_and_ledger_versions(t *testing.T) {
	r := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
		big := append([]byte{0xff}, make([]byte, 2<<20)...)
		ap := env.ProposeEdit(ctx, "cursor", "too big", domain.ProposedEdit{File: "big.bin", SearchBlock: orchestrator.MarkerNewFile,
			ContentB64: base64.StdEncoding.EncodeToString(big)})
		if ap.Approved || !strings.Contains(ap.Feedback, "at most") {
			t.Errorf("a file over the limit was not refused: %+v", ap)
		}
		return nil
	})})
	require.NoError(t, r.Err)

	repo := t.TempDir()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "T")
	run("commit", "-q", "--allow-empty", "-m", "base")
	ledger := fmt.Sprintf(`{"version":1,"base":%q,"proposals":[{"seq":1,"source":"operator","edits":[{"file":"x.bin","search_block":"(new file)","replace_block":"","content_b64":%q}]}]}`,
		run("rev-parse", "HEAD"), base64.StdEncoding.EncodeToString([]byte("\xff\x00")))
	_, _, err := orchestrator.RebuildTree(repo, []byte(ledger))
	assert.ErrorContains(t, err, "text only")
	_, _, err = orchestrator.RebuildTree(repo, []byte(strings.Replace(ledger, `"version":1`, `"version":2`, 1)))
	assert.NoError(t, err)
}

// TestReviewAfter_keeps_file_modes: what an editor or a command did to a file's
// executable bit is part of the change: a new script made executable, a file
// made executable without a content change, and an executable file made plain
// are committed as the worktree has them, and the commit rebuilds from its ledger.
func TestReviewAfter_keeps_file_modes(t *testing.T) {
	base := map[string]runtest.File{
		"plain.sh":   {Content: "echo plain\n", Mode: 0o644},
		"tool.sh":    {Content: "echo tool\n", Mode: 0o755},
		"changed.sh": {Content: "echo old\n", Mode: 0o755},
	}
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		wt := env.Worktree
		must := func(err error) {
			if err != nil {
				t.Error(err)
			}
		}
		must(os.WriteFile(filepath.Join(wt, "new.sh"), []byte("echo new\n"), 0o755))     // a new executable script
		must(os.Chmod(filepath.Join(wt, "plain.sh"), 0o755))                             // made executable, content unchanged
		must(os.Chmod(filepath.Join(wt, "tool.sh"), 0o644))                              // made plain, content unchanged
		must(os.WriteFile(filepath.Join(wt, "changed.sh"), []byte("echo new\n"), 0o755)) // edited, stays executable
		must(os.WriteFile(filepath.Join(wt, "new.txt"), []byte("text\n"), 0o644))
		got := env.ProposeWorktreeChanges(ctx, "cursor", "an editor changed modes")
		if !got.Approved {
			t.Errorf("not approved: %s", got.Feedback)
		}
		return nil
	}
	r, commit, _ := certified(t, agent, orchestrator.RunOptions{Plan: &plan.Plan{Harness: "claude-code"}}, nil, base)
	modes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(git(t, r.Repo, "ls-tree", "-r", commit)), "\n") {
		f := strings.Fields(line)
		modes[f[3]] = f[0]
	}
	assert.Equal(t, map[string]string{"new.sh": "100755", "plain.sh": "100755", "tool.sh": "100644", "changed.sh": "100755", "new.txt": "100644"}, modes)

	ledger, err := os.ReadFile(orchestrator.LedgerPath(r.WsDir, r.Run.ID))
	require.NoError(t, err)
	tree, _, err := orchestrator.RebuildTree(r.Repo, ledger)
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", commit+"^{tree}")), tree, "the ledger reproduces the modes too")
}

// TestReviewAfter_never_reads_through_a_symlink: a command (or an agent) that
// leaves a symlink to a file outside the worktree must not make stAirCase read
// that file: its content would be put in a proposal, the audit log and in front
// of reviewers. The symlink is refused, and the secret is nowhere in the record.
func TestReviewAfter_never_reads_through_a_symlink(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "id_secret")
	require.NoError(t, os.WriteFile(secret, []byte("TOP-SECRET-KEY-MATERIAL\n"), 0o600))
	var got orchestrator.Approval
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		if err := os.Symlink(secret, filepath.Join(env.Worktree, "innocent.txt")); err != nil {
			return err
		}
		got = env.ProposeWorktreeChanges(ctx, "cursor", "a command left a link")
		return nil
	}
	r := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(agent)})
	require.NoError(t, r.Err)
	assert.False(t, got.Approved, "a symlink is refused")
	for _, e := range r.Events {
		assert.NotContains(t, e.Payload, "TOP-SECRET-KEY-MATERIAL", "%s must not hold what the link points at", e.EventType)
	}
}
