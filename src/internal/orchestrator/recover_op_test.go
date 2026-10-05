package orchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func approveAll(domain.YieldRequest) domain.YieldResponse { return domain.Decide(true, "ok") }

// TestRecover_a_commit_that_looks_like_its_own_but_is_not: ownership is the
// operation's name, the base as the only parent and the expected tree. A commit
// with the right tree and a copied trailer, or an extra parent, is a conflict,
// and never gets the final review skipped.
func TestRecover_a_commit_that_looks_like_its_own_but_is_not(t *testing.T) {
	for name, parents := range map[string]func(base, other string) []string{
		"the same tree and copied trailers": func(base, _ string) []string { return []string{"-p", base} },
		"an extra parent":                   func(base, other string) []string { return []string{"-p", base, "-p", other} },
	} {
		t.Run(name, func(t *testing.T) {
			r := interrupted(t, inSrc(&operator{approve: true}), orchestrator.RunOptions{ApproveInScope: true, Agreed: "dev@example.com"})
			base := strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1"))
			// the tree the approvals produce, built the long way round in a scratch clone
			scratch := t.TempDir()
			git(t, scratch, "clone", "-q", r.Repo, ".")
			git(t, scratch, "config", "user.email", "t@t")
			git(t, scratch, "config", "user.name", "T")
			require.NoError(t, os.MkdirAll(filepath.Join(scratch, "src"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(scratch, "src", "a.txt"), []byte("first\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(scratch, "src", "b.txt"), []byte("second\n"), 0o644))
			git(t, scratch, "add", "-A")
			git(t, scratch, "commit", "-q", "-m", "the approved files")
			tree := strings.TrimSpace(git(t, scratch, "rev-parse", "HEAD^{tree}"))
			git(t, r.Repo, "fetch", "-q", scratch, "HEAD") // brings the tree into the run's repository
			other := strings.TrimSpace(git(t, r.Repo, "commit-tree", base+"^{tree}", "-m", "other history"))
			args := append([]string{"-c", "user.email=t@t", "-c", "user.name=T", "commit-tree", tree}, parents(base, other)...)
			args = append(args, "-m", "staircase: run #1\n\nStaircase-Chain: sha256:abc\nStaircase-Recovery: 0000")
			fake := strings.TrimSpace(git(t, r.Repo, args...))
			git(t, r.Repo, "update-ref", "refs/heads/staircase/run-1", fake)

			asked := 0
			res, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID,
				orchestrator.RecoverOptions{Confirm: func(domain.YieldRequest) domain.YieldResponse { asked++; return domain.Decide(true, "ok") }})
			require.Error(t, err, "adopted a commit it did not make: %+v", res)
			assert.Contains(t, err.Error(), "not this recovery's")
			run, _ := r.Store.GetRun(r.Run.ID)
			assert.Empty(t, run.GitCommitHash)
			assert.Equal(t, fake, strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1")), "the branch is left as it was")
		})
	}
}

// TestRecover_review_that_cannot_be_recorded_commits_nothing: the final review is
// on the audit chain before any commit is made.
func TestRecover_review_that_cannot_be_recorded_commits_nothing(t *testing.T) {
	r := interrupted(t, inSrc(&operator{approve: true}), orchestrator.RunOptions{ApproveInScope: true, Agreed: "dev@example.com"})
	_, err := r.DB.Exec(`CREATE TRIGGER no_review BEFORE INSERT ON run_event_logs WHEN NEW.event_type = 'recovery_final_review' BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	require.NoError(t, err)
	_, err = orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Confirm: approveAll})
	assert.ErrorContains(t, err, "record the final review")
	branchUntouched(t, r)
	_, statErr := os.Stat(filepath.Join(r.WsDir, "journal", "run-1.recovery.json"))
	assert.True(t, os.IsNotExist(statErr), "no operation was begun")

	_, err = r.DB.Exec(`DROP TRIGGER no_review`)
	require.NoError(t, err)
	res, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Confirm: approveAll})
	require.NoError(t, err)
	assert.True(t, res.FinalReview)
}

// TestRecover_concurrent_retries_deliver_one_commit: two recoveries of one run
// race; one commit results, and the evidence is consistent.
func TestRecover_concurrent_retries_deliver_one_commit(t *testing.T) {
	r := interrupted(t, nil, orchestrator.RunOptions{})
	var wg sync.WaitGroup
	results := make([]orchestrator.RecoverResult, 4)
	errs := make([]error, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
		}()
	}
	wg.Wait()
	commit := strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1"))
	succeeded := 0
	for i := range results {
		if errs[i] == nil {
			succeeded++
			assert.Equal(t, commit, results[i].Commit)
		} else {
			assert.True(t, strings.Contains(errs[i].Error(), "in progress") || strings.Contains(errs[i].Error(), "already"), "%v", errs[i])
		}
	}
	assert.Equal(t, 1, succeeded, "exactly one recovery delivered")
	assert.Equal(t, 1, strings.Count(git(t, r.Repo, "log", "--format=%H", "main..staircase/run-1"), "\n"), "one commit on the branch")
	run, _ := r.Store.GetRun(r.Run.ID)
	assert.Equal(t, commit, run.GitCommitHash)
	require.NoError(t, r.Store.VerifyChain(r.Run.ID))
	types := 0
	events, _ := r.Store.ListEventLogs(r.Run.ID)
	for _, e := range events {
		if e.EventType == "run_recovered" {
			types++
		}
	}
	assert.Equal(t, 1, types)
}

// TestRecover_repairs_evidence_on_the_same_commit: each part of the evidence is
// made to fail on its own. The commit is delivered and the result says what is
// missing (an error with RequireEvidence); running recover again, with the fault
// gone, repairs the same commit, and a third run has nothing to do.
func TestRecover_repairs_evidence_on_the_same_commit(t *testing.T) {
	for name, f := range map[string]struct {
		inject func(t *testing.T, repo, ws string, exec func(string))
		heal   func(t *testing.T, repo, ws string, exec func(string))
		label  string
	}{
		"ledger": {label: "ledger",
			inject: func(t *testing.T, _, ws string, _ func(string)) {
				require.NoError(t, os.MkdirAll(filepath.Join(ws, "audit", "run-1.ledger.json"), 0o700))
			},
			heal: func(t *testing.T, _, ws string, _ func(string)) {
				require.NoError(t, os.RemoveAll(filepath.Join(ws, "audit", "run-1.ledger.json")))
			}},
		"certificate note": {label: "certificate",
			inject: func(t *testing.T, repo, _ string, _ func(string)) {
				require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git", "refs", "notes"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "refs", "notes", "staircase.lock"), nil, 0o644))
			},
			heal: func(t *testing.T, repo, _ string, _ func(string)) {
				require.NoError(t, os.Remove(filepath.Join(repo, ".git", "refs", "notes", "staircase.lock")))
			}},
		"certificate audit": {label: "certificate",
			inject: func(_ *testing.T, _, _ string, exec func(string)) {
				exec(`CREATE TRIGGER no_cert BEFORE INSERT ON run_event_logs WHEN NEW.event_type = 'certificate_issued' BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
			},
			heal: func(_ *testing.T, _, _ string, exec func(string)) { exec(`DROP TRIGGER no_cert`) }},
		"audit record": {label: "audit record",
			inject: func(_ *testing.T, _, _ string, exec func(string)) {
				exec(`CREATE TRIGGER no_rec BEFORE INSERT ON run_event_logs WHEN NEW.event_type = 'run_recovered' BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
			},
			heal: func(_ *testing.T, _, _ string, exec func(string)) { exec(`DROP TRIGGER no_rec`) }},
		"summary": {label: "summary",
			inject: func(t *testing.T, _, ws string, _ func(string)) {
				require.NoError(t, os.RemoveAll(filepath.Join(ws, "runs", "1", "summary.json")))
				require.NoError(t, os.MkdirAll(filepath.Join(ws, "runs", "1", "summary.json"), 0o700))
			},
			heal: func(t *testing.T, _, ws string, _ func(string)) {
				require.NoError(t, os.RemoveAll(filepath.Join(ws, "runs", "1", "summary.json")))
			}},
		"the run's record": {label: "the run's record",
			inject: func(_ *testing.T, _, _ string, exec func(string)) {
				exec(`CREATE TRIGGER no_upd BEFORE UPDATE ON runs WHEN NEW.git_commit_hash != '' BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
			},
			heal: func(_ *testing.T, _, _ string, exec func(string)) { exec(`DROP TRIGGER no_upd`) }},
	} {
		t.Run(name, func(t *testing.T) {
			r := interrupted(t, nil, orchestrator.RunOptions{})
			exec := func(q string) { _, err := r.DB.Exec(q); require.NoError(t, err) }
			runner := orchestrator.NewRunner(r.Store, r.WsDir)
			f.inject(t, r.Repo, r.WsDir, exec)

			first, err := runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
			require.NoError(t, err, "the commit is delivered; the evidence is reported")
			require.NotEmpty(t, first.Commit)
			require.NotEmpty(t, first.EvidenceErrors)
			assert.Contains(t, strings.Join(first.EvidenceErrors, ";"), f.label)
			assert.Equal(t, first.Commit, strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1")))

			_, err = runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{RequireEvidence: true})
			assert.ErrorIs(t, err, orchestrator.ErrEvidenceIncomplete, "strict mode fails while the evidence is incomplete")

			f.heal(t, r.Repo, r.WsDir, exec)
			second, err := runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{RequireEvidence: true})
			require.NoError(t, err)
			assert.True(t, second.Repaired)
			assert.Equal(t, first.Commit, second.Commit, "the same commit, not a second one")
			assert.Empty(t, second.EvidenceErrors)
			assert.Equal(t, 1, strings.Count(git(t, r.Repo, "log", "--format=%H", "main..staircase/run-1"), "\n"))
			run, _ := r.Store.GetRun(r.Run.ID)
			assert.Equal(t, first.Commit, run.GitCommitHash)
			require.NoError(t, r.Store.VerifyChain(r.Run.ID))

			_, err = runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
			assert.ErrorContains(t, err, "nothing to recover", "complete evidence is not recovered again")
		})
	}
}

// TestRecover_keeps_the_policy_the_run_loaded: the certificate of a recovered run
// names the policy the run decided under, not the policy file at recovery time.
func TestRecover_keeps_the_policy_the_run_loaded(t *testing.T) {
	original := []byte(`{"version":1,"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`)
	var policyFile string
	r := interrupted(t, func(_ *persistence.Store, ws string, _ int64) {
		policyFile = filepath.Join(ws, "policy.json")
		require.NoError(t, os.WriteFile(policyFile, original, 0o600))
	}, orchestrator.RunOptions{})
	require.NoError(t, os.WriteFile(policyFile, []byte(`{"version":1,"rules":[],"limits":{"max_auto_approved":1}}`), 0o600)) // replaced afterwards

	_, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(r.WsDir, "audit", "run-1.certificate.json"))
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal(b, &env))
	pub, _ := crypto.LoadSigningPublicKey(r.WsDir)
	s, err := certificate.Open(env, pub)
	require.NoError(t, err)
	sum := sha256.Sum256(original)
	assert.Equal(t, hex.EncodeToString(sum[:]), s.Predicate.Policy, "the policy the run loaded")
	assert.LessOrEqual(t, s.Predicate.CAL, 2, "an interrupted run's assurance is capped")
	assert.Contains(t, strings.Join(s.Predicate.Notes, " "), "interrupted")
}

// TestRecoveryOp_owns_only_its_own_commit: the name in the trailer, the base as the
// only parent and the expected tree must all agree.
func TestRecoveryOp_owns_only_its_own_commit(t *testing.T) {
	r := interrupted(t, nil, orchestrator.RunOptions{})
	base := strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1"))
	tree := strings.TrimSpace(git(t, r.Repo, "rev-parse", base+"^{tree}"))
	scratch := t.TempDir() // a tree that is not the expected one
	git(t, scratch, "clone", "-q", r.Repo, ".")
	require.NoError(t, os.WriteFile(filepath.Join(scratch, "x.txt"), []byte("x\n"), 0o644))
	git(t, scratch, "add", "-A")
	git(t, scratch, "-c", "user.email=t@t", "-c", "user.name=T", "commit", "-q", "-m", "x")
	git(t, r.Repo, "fetch", "-q", scratch, "HEAD")
	otherTree := strings.TrimSpace(git(t, scratch, "rev-parse", "HEAD^{tree}"))
	other := strings.TrimSpace(git(t, r.Repo, "commit-tree", tree, "-m", "elsewhere"))
	mk := func(tree, msg string, parents ...string) string {
		args := []string{"-c", "user.email=t@t", "-c", "user.name=T", "commit-tree", tree}
		for _, p := range parents {
			args = append(args, "-p", p)
		}
		return strings.TrimSpace(git(t, r.Repo, append(args, "-m", msg)...))
	}
	msg := "staircase: run #1\n\nStaircase-Chain: sha256:abc\nStaircase-Recovery: op-1"
	owns := func(tip string) bool { return orchestrator.ExportedOwnsCommit(r.Repo, "op-1", base, tree, tip) }

	assert.True(t, owns(mk(tree, msg, base)), "name, sole parent and tree agree")
	assert.False(t, owns(mk(tree, msg, base, other)), "an extra parent")
	assert.False(t, owns(mk(tree, msg, other)), "another parent")
	assert.False(t, owns(mk(otherTree, msg, base)), "another tree")
	assert.False(t, owns(mk(tree, strings.Replace(msg, "op-1", "op-2", 1), base)), "another operation's name")
	assert.False(t, owns(mk(tree, "staircase: run #1\n\nStaircase-Chain: sha256:abc", base)), "no name at all")
	assert.False(t, owns(mk(tree, msg+"\nStaircase-Recovery: op-9", base)), "two names")
}
