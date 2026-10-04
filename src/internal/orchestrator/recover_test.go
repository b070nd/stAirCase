package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interrupted runs a session whose agent has two changes approved and then the
// run is cancelled before it can finish: on disk it is what a crashed or killed
// run leaves (no commit, a kept worktree, the audit chain, the journal).
func interrupted(t *testing.T, setup func(*persistence.Store, string, int64), opts orchestrator.RunOptions) runtest.Result {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if setup == nil {
		setup = func(*persistence.Store, string, int64) {}
	}
	r := runtest.Run(t, runtest.Options{Ctx: ctx, Run: opts,
		Setup: func(s *persistence.Store, ws string, p int64) {
			require.NoError(t, crypto.GenerateSigningKey(ws))
			setup(s, ws, p)
		},
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			write(c, env, "src/a.txt", "first\n")
			write(c, env, "src/b.txt", "second\n")
			cancel() // the run ends here, with the work approved but not committed
			<-c.Done()
			return c.Err()
		})})
	require.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	require.Empty(t, r.Run.GitCommitHash, "nothing was committed")
	return r
}

// TestRecover_commits_what_was_approved: the approvals of an interrupted run
// were kept as they were made, so `recover` commits exactly them, with a
// certificate that says the run did not finish.
func TestRecover_commits_what_was_approved(t *testing.T) {
	r := interrupted(t, nil, orchestrator.RunOptions{})
	runner := orchestrator.NewRunner(r.Store, r.WsDir)

	res, err := runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Proposals)
	assert.False(t, res.FinalReview, "a person's or policy's decisions need no final review")
	for f, want := range map[string]string{"src/a.txt": "first\n", "src/b.txt": "second\n"} {
		got, err := r.OnBranch(f)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	assert.Equal(t, res.Commit, strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1")))

	b, err := os.ReadFile(filepath.Join(r.WsDir, "audit", "run-1.certificate.json"))
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal(b, &env))
	pub, _ := crypto.LoadSigningPublicKey(r.WsDir)
	s, err := certificate.Open(env, pub)
	require.NoError(t, err)
	assert.Equal(t, res.Commit, s.Commit())
	assert.LessOrEqual(t, s.Predicate.CAL, 2, "the end of the run was not checked")
	assert.Contains(t, strings.Join(s.Predicate.Notes, " "), "interrupted")
	require.NotEmpty(t, s.Predicate.Ledger, "and the commit can still be rebuilt from its ledger")
	ledger, err := os.ReadFile(orchestrator.LedgerPath(r.WsDir, 1))
	require.NoError(t, err)
	tree, _, err := orchestrator.RebuildTree(r.Repo, ledger)
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", res.Commit+"^{tree}")), tree)
	assert.Contains(t, git(t, r.Repo, "log", "-1", "--format=%B", res.Commit), "Assisted-by:")

	run, err := r.Store.GetRun(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, res.Commit, run.GitCommitHash, "the run's record names the commit")
	assert.Equal(t, persistence.RunStatusKilled, run.Status, "and still says it did not finish")
	events, _ := r.Store.ListEventLogs(r.Run.ID)
	var types []string
	for _, e := range events {
		types = append(types, e.EventType)
	}
	assert.Contains(t, types, "run_recovered")
	require.NoError(t, r.Store.VerifyChain(r.Run.ID))

	_, err = runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
	assert.ErrorContains(t, err, "already")
}

// TestRecover_needs_a_final_review_for_approvals_nobody_made: changes approved
// as part of the agreed task were meant to be reviewed as a whole at the end; a
// recovery asks for that review, and a refusal commits nothing.
func TestRecover_needs_a_final_review_for_approvals_nobody_made(t *testing.T) {
	setup := inSrc(&operator{approve: true})
	r := interrupted(t, setup, orchestrator.RunOptions{ApproveInScope: true, Agreed: "dev@example.com"})
	runner := orchestrator.NewRunner(r.Store, r.WsDir)

	var asked []domain.YieldRequest
	reject := func(req domain.YieldRequest) domain.YieldResponse {
		asked = append(asked, req)
		return domain.Decide(false, "not like this")
	}
	_, err := runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Confirm: reject})
	require.ErrorIs(t, err, orchestrator.ErrRecoveryRejected)
	require.Len(t, asked, 1)
	assert.Equal(t, domain.ActionFinalReview, asked[0].ActionType)
	assert.Len(t, asked[0].ProposedEdits, 2, "the person is shown the whole change")
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", "main")), strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1")), "nothing committed")

	_, err = runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
	assert.ErrorContains(t, err, "final review", "with nobody to ask, it refuses rather than commit unreviewed")

	// Editing "who decided" in the journal does not get the review skipped: the audit chain's word counts.
	path := filepath.Join(r.WsDir, "journal", "run-1.approved.jsonl")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(b), `"source":"task"`)
	require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(string(b), `"source":"task"`, `"source":"operator"`)), 0o600))
	_, err = runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
	assert.ErrorContains(t, err, "final review")

	res, err := runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Confirm: func(domain.YieldRequest) domain.YieldResponse { return domain.Decide(true, "ok") }})
	require.NoError(t, err)
	assert.True(t, res.FinalReview)
	got, err := r.OnBranch("src/b.txt")
	require.NoError(t, err)
	assert.Equal(t, "second\n", got)
}

// TestRecover_refuses_what_it_cannot_do_safely: a run that may still be going,
// one that finished, one with nothing approved, and a branch that has moved.
func TestRecover_refuses_what_it_cannot_do_safely(t *testing.T) {
	r := interrupted(t, nil, orchestrator.RunOptions{})
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	ctx := context.Background()

	now := time.Now()
	require.NoError(t, r.Store.UpdateRunStatus(r.Run.ID, persistence.RunStatusRunning, nil, ""))
	_, err := runner.Recover(ctx, r.Run.ID, orchestrator.RecoverOptions{})
	assert.ErrorContains(t, err, "still be running", "a RUNNING record may belong to a live process")
	require.NoError(t, r.Store.UpdateRunStatus(r.Run.ID, persistence.RunStatusKilled, &now, ""))

	git(t, r.Repo, "commit", "--allow-empty", "-q", "-m", "elsewhere")
	git(t, r.Repo, "update-ref", "refs/heads/staircase/run-1", "HEAD") // the branch moved: not at the run's base
	_, err = runner.Recover(ctx, r.Run.ID, orchestrator.RecoverOptions{})
	assert.Error(t, err, "the branch is no longer where the run started")

	_, err = runner.Recover(ctx, 999, orchestrator.RecoverOptions{})
	assert.ErrorContains(t, err, "not found")

	// A run that never had anything approved has nothing to recover.
	empty := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error { return nil })})
	_, err = orchestrator.NewRunner(empty.Store, empty.WsDir).Recover(ctx, empty.Run.ID, orchestrator.RecoverOptions{})
	assert.Error(t, err)
}

// TestRun_an_approval_that_cannot_be_kept_is_not_given: the approved change is
// written to the journal before the agent hears the answer; when that cannot be
// done the proposal is refused and the run stops, like an unwritable audit chain.
func TestRun_an_approval_that_cannot_be_kept_is_not_given(t *testing.T) {
	r := runtest.Run(t, runtest.Options{
		Setup: func(_ *persistence.Store, ws string, _ int64) {
			require.NoError(t, os.WriteFile(filepath.Join(ws, "journal"), []byte("a file where a directory should be"), 0o600))
		},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			ap := write(ctx, env, "src/a.txt", "x\n")
			if ap.Approved {
				t.Errorf("the approval was given although it could not be kept")
			}
			return nil
		})})
	require.Error(t, r.Err)
	assert.Empty(t, r.Run.GitCommitHash)
}

// TestRecover_trusts_only_what_the_audit_chain_records: a journal line the
// audit chain has no approval for, or whose request differs from the one the
// chain recorded, is never committed, so nobody can add bytes by editing the file.
func TestRecover_trusts_only_what_the_audit_chain_records(t *testing.T) {
	r := interrupted(t, nil, orchestrator.RunOptions{})
	path := filepath.Join(r.WsDir, "journal", "run-1.approved.jsonl")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	require.Len(t, lines, 2)
	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	evil := func(seq any) string {
		first["seq"] = seq
		first["request"] = map[string]any{"type": "yield_request", "action_type": "file_edit", "proposed_edits": []map[string]string{
			{"file": "src/evil.txt", "search_block": "(new file)", "replace_block": "evil\n"}}}
		out, _ := json.Marshal(first)
		return string(out)
	}
	forged := append(lines, evil(99), evil(1)) // one the chain never approved, one that reuses a real seq with other bytes
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(forged, "\n")+"\n"), 0o600))

	res, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Proposals)
	_, err = r.OnBranch("src/evil.txt")
	assert.Error(t, err, "the forged lines were ignored")
	got, err := r.OnBranch("src/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "first\n", got)
}

// TestRecover_finishes_a_recovery_that_was_killed_after_its_commit: recover makes
// the commit and then writes the ledger, the certificate and the run's record.
// A kill in between left a commit nobody could complete (the branch had already
// moved). A second recover now recognizes its own commit, by parent and tree, and
// finishes the rest, with the commit's own chain head in the certificate.
func TestRecover_finishes_a_recovery_that_was_killed_after_its_commit(t *testing.T) {
	r := interrupted(t, nil, orchestrator.RunOptions{})
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	first, err := runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
	require.NoError(t, err)

	// What a kill right after the commit leaves: the commit, and none of the rest.
	require.NoError(t, r.Store.UpdateRunStatus(r.Run.ID, persistence.RunStatusRunning, nil, ""))
	for _, f := range []string{"run-1.certificate.json", "run-1.ledger.json"} {
		require.NoError(t, os.Remove(filepath.Join(r.WsDir, "audit", f)))
	}
	for _, ref := range []string{"staircase", "staircase-ledger"} {
		git(t, r.Repo, "notes", "--ref="+ref, "remove", first.Commit)
	}

	second, err := runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Force: true})
	require.NoError(t, err, "the commit exists; the rest is finished")
	assert.Equal(t, first.Commit, second.Commit, "no second commit")
	assert.Equal(t, first.Commit, strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1")))

	run, err := r.Store.GetRun(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, first.Commit, run.GitCommitHash)
	b, err := os.ReadFile(filepath.Join(r.WsDir, "audit", "run-1.certificate.json"))
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal(b, &env))
	pub, _ := crypto.LoadSigningPublicKey(r.WsDir)
	s, err := certificate.Open(env, pub)
	require.NoError(t, err)
	assert.Contains(t, git(t, r.Repo, "log", "-1", "--format=%B", first.Commit), "Staircase-Chain: sha256:"+s.Predicate.ChainHead,
		"the certificate carries the chain head the commit names, not whatever the chain has grown to")
	ledger, err := os.ReadFile(orchestrator.LedgerPath(r.WsDir, 1))
	require.NoError(t, err)
	tree, _, err := orchestrator.RebuildTree(r.Repo, ledger)
	require.NoError(t, err)
	assert.Equal(t, strings.TrimSpace(git(t, r.Repo, "rev-parse", first.Commit+"^{tree}")), tree)
	require.NoError(t, r.Store.VerifyChain(r.Run.ID))
}

// TestRecover_does_not_adopt_someone_elses_commit: a branch tip that is not what
// the approvals produce, even on the same base, is not the run's commit.
func TestRecover_does_not_adopt_someone_elses_commit(t *testing.T) {
	r := interrupted(t, nil, orchestrator.RunOptions{})
	base := strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1"))
	tree := strings.TrimSpace(git(t, r.Repo, "rev-parse", base+"^{tree}"))
	other := strings.TrimSpace(git(t, r.Repo, "commit-tree", tree, "-p", base, "-m", "someone else's commit, same base, other bytes"))
	git(t, r.Repo, "update-ref", "refs/heads/staircase/run-1", other)
	_, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
	assert.Error(t, err)
	run, _ := r.Store.GetRun(r.Run.ID)
	assert.Empty(t, run.GitCommitHash, "nothing was recorded for a commit that is not the run's")
}

// TestRecover_a_crashed_case_is_not_left_running: a process that died holds no
// chance to finish its run or its case, so both still say RUNNING; recovering
// it ends them the way a killed run ends.
func TestRecover_a_crashed_case_is_not_left_running(t *testing.T) {
	r := interrupted(t, nil, orchestrator.RunOptions{})
	require.NoError(t, r.Store.UpdateRunStatus(r.Run.ID, persistence.RunStatusRunning, nil, ""))
	require.NoError(t, r.Store.UpdateCaseStatus(r.Run.CaseID, persistence.CaseStatusRunning))

	_, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Force: true})
	require.NoError(t, err)
	run, err := r.Store.GetRun(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusKilled, run.Status)
	c, err := r.Store.GetCase(r.Run.CaseID)
	require.NoError(t, err)
	assert.Equal(t, persistence.CaseStatusFailed, c.Status)
}
