package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// diedAfterTheCommit is a run that made its commit (and, as far as the test says,
// its evidence) and whose process was killed before the run's own record was
// completed: the record still says RUNNING and names no commit.
func diedAfterTheCommit(t *testing.T, opts orchestrator.RunOptions, dropEvidence bool) (runtest.Result, string) {
	r := runtest.Run(t, runtest.Options{Run: opts,
		Setup: func(s *persistence.Store, ws string, p int64) {
			require.NoError(t, crypto.GenerateSigningKey(ws))
			inSrc(&operator{approve: true})(s, ws, p)
		},
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			write(c, env, "src/a.txt", "first\n")
			write(c, env, "src/b.txt", "second\n")
			return nil
		})})
	require.NoError(t, r.Err)
	commit := r.Run.GitCommitHash
	require.NotEmpty(t, commit)
	_, err := r.DB.Exec(`UPDATE runs SET status = 'RUNNING', git_commit_hash = '', end_time = NULL WHERE id = ?`, r.Run.ID)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(r.WsDir, "runs", "1", "summary.json")))
	if dropEvidence { // killed between the branch moving and the evidence
		require.NoError(t, os.Remove(filepath.Join(r.WsDir, "audit", "run-1.certificate.json")))
		require.NoError(t, os.Remove(filepath.Join(r.WsDir, "audit", "run-1.ledger.json")))
		git(t, r.Repo, "update-ref", "-d", "refs/notes/staircase")
		git(t, r.Repo, "update-ref", "-d", "refs/notes/staircase-ledger")
	}
	return r, commit
}

// TestRecover_adopts_the_commit_the_run_itself_made: a run killed after its own
// commit is not a dead end. Recovery recognizes the commit (the base as the sole
// parent, the approved tree, the chain head it names is on this run's chain, and a
// final review on the chain when one was needed), keeps it, finishes what is
// missing and completes the run's record. It makes no second commit.
func TestRecover_adopts_the_commit_the_run_itself_made(t *testing.T) {
	for name, f := range map[string]struct {
		opts         orchestrator.RunOptions
		dropEvidence bool
	}{
		"evidence published, record not completed": {orchestrator.RunOptions{}, false},
		"commit made, no evidence yet":             {orchestrator.RunOptions{}, true},
		"task approvals and a final review":        {orchestrator.RunOptions{ApproveInScope: true, Agreed: "dev@example.com"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			r, commit := diedAfterTheCommit(t, f.opts, f.dropEvidence)
			res, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Force: true})
			require.NoError(t, err)
			assert.Equal(t, commit, res.Commit, "the run's own commit is kept")
			assert.True(t, res.Adopted, "reported as the run's own commit")
			assert.Equal(t, !f.dropEvidence, res.CertificateKept, "the run's certificate is kept when it had issued one")
			assert.Equal(t, commit, strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1")), "the branch did not move")
			run, err := r.Store.GetRun(r.Run.ID)
			require.NoError(t, err)
			assert.Equal(t, commit, run.GitCommitHash)
			assert.Equal(t, persistence.RunStatusKilled, run.Status)
			assert.NotEmpty(t, git(t, r.Repo, "notes", "--ref=staircase", "show", commit), "the certificate is on the commit")
			assert.NotEmpty(t, git(t, r.Repo, "notes", "--ref=staircase-ledger", "show", commit), "so is the ledger")
			_, err = os.Stat(filepath.Join(r.WsDir, "audit", "run-1.certificate.json"))
			assert.NoError(t, err)
		})
	}
}

// TestRecover_keeps_the_certificate_the_run_issued: the run's own certificate and
// ledger are not replaced by a recovery's.
func TestRecover_keeps_the_certificate_the_run_issued(t *testing.T) {
	r, commit := diedAfterTheCommit(t, orchestrator.RunOptions{}, false)
	cert := filepath.Join(r.WsDir, "audit", "run-1.certificate.json")
	before, err := os.ReadFile(cert)
	require.NoError(t, err)
	_, err = orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Force: true})
	require.NoError(t, err)
	after, err := os.ReadFile(cert)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the certificate the run signed is the one that stays")
	events, err := r.Store.ListEventLogs(r.Run.ID)
	require.NoError(t, err)
	issued := 0
	for _, e := range events {
		if e.EventType == "certificate_issued" && strings.Contains(e.Payload, commit) {
			issued++
		}
	}
	assert.Equal(t, 1, issued, "and it is on the chain once")
}

// TestRecover_does_not_adopt_a_copy_of_the_runs_commit: a commit someone else made with the
// run's base as its only parent, the approved tree and the run's own message (its case,
// its chain head), when no final review was needed, is the same bytes under another
// name. The run named its commit on the audit chain before the branch moved
// (commit_prepared), and that name is the identity: a copy is a conflict and the branch
// is left alone.
func TestRecover_does_not_adopt_a_copy_of_the_runs_commit(t *testing.T) {
	r, commit := diedAfterTheCommit(t, orchestrator.RunOptions{}, true)
	base := strings.TrimSpace(git(t, r.Repo, "rev-parse", commit+"^"))
	tree := strings.TrimSpace(git(t, r.Repo, "rev-parse", commit+"^{tree}"))
	msg := git(t, r.Repo, "log", "-1", "--format=%B", commit)
	copied := strings.TrimSpace(git(t, r.Repo, "-c", "user.email=other@example.com", "-c", "user.name=Other", "commit-tree", tree, "-p", base, "-m", strings.TrimSpace(msg)))
	require.NotEqual(t, commit, copied, "another author gives another commit")
	git(t, r.Repo, "update-ref", "refs/heads/staircase/run-1", copied)

	res, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Force: true})
	require.Error(t, err, "adopted a commit the run did not name: %+v", res)
	assert.Contains(t, err.Error(), "not this recovery's")
	assert.Equal(t, copied, strings.TrimSpace(git(t, r.Repo, "rev-parse", "staircase/run-1")), "the branch is left as it was")
	run, _ := r.Store.GetRun(r.Run.ID)
	assert.Empty(t, run.GitCommitHash)
}

// TestRun_names_its_commit_on_the_chain_before_the_branch_moves: the commit_prepared event
// holds the commit's name, its base and its tree, and comes after the decisions and before
// the certificate that is about the commit.
func TestRun_names_its_commit_on_the_chain_before_the_branch_moves(t *testing.T) {
	r, commit := diedAfterTheCommit(t, orchestrator.RunOptions{}, false)
	events, err := r.Store.ListEventLogs(r.Run.ID)
	require.NoError(t, err)
	prepared, certified := -1, -1
	for i, e := range events {
		switch e.EventType {
		case "commit_prepared":
			prepared = i
			assert.Contains(t, e.Payload, `"commit":"`+commit+`"`)
			assert.Contains(t, e.Payload, `"tree":"`+strings.TrimSpace(git(t, r.Repo, "rev-parse", commit+"^{tree}"))+`"`)
			assert.Contains(t, e.Payload, `"base":"`+strings.TrimSpace(git(t, r.Repo, "rev-parse", commit+"^"))+`"`)
		case "certificate_issued":
			certified = i
		}
	}
	require.GreaterOrEqual(t, prepared, 0, "no commit_prepared on the chain: %v", r.Types())
	assert.Less(t, prepared, certified)
}
