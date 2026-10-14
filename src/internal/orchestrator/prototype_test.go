package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parkedNamed is the parked proposal of the review that touches file.
func parkedNamed(t *testing.T, rv orchestrator.Review, file string) orchestrator.ParkedProposal {
	for _, p := range rv.ParkedProposals {
		if len(p.Files) == 1 && p.Files[0] == file {
			return p
		}
	}
	t.Fatalf("no parked proposal for %s in %+v", file, rv.ParkedProposals)
	return orchestrator.ParkedProposal{}
}

func chainLength(t *testing.T, r runtest.Result) int {
	events, err := r.Store.ListVerifiedEventLogs(r.Run.ID)
	require.NoError(t, err)
	return len(events)
}

// TestPrototype_shows_what_a_parked_proposal_would_do_and_writes_nothing (ADR 0007, 1): the parked change on top of the change on
// offer, checked with the run's own checks; a passing one and a failing one; the run and its chain are untouched.
func TestPrototype_shows_what_a_parked_proposal_would_do_and_writes_nothing(t *testing.T) {
	agentRuns := 0
	r := awayRun(t, &agentRuns)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	rv, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err)
	before := chainLength(t, r)

	ok, err := runner.Prototype(context.Background(), r.Run.ID, parkedNamed(t, rv, "elsewhere.txt").Seq)
	require.NoError(t, err)
	assert.True(t, ok.Passed, "the check only looks at src: %s", ok.Failed)
	assert.Equal(t, []string{"elsewhere.txt"}, ok.Files)
	assert.Contains(t, ok.Diff, "+out of scope")
	require.Len(t, ok.Checks, 1)

	bad, err := runner.Prototype(context.Background(), r.Run.ID, parkedNamed(t, rv, "src/bad.txt").Seq)
	require.NoError(t, err)
	assert.False(t, bad.Passed)
	assert.Contains(t, bad.Failed, "FORBIDDEN found")

	assert.Equal(t, before, chainLength(t, r), "a prototype decides nothing")
	again, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, rv.Entry, again.Entry)
}

// TestAdopt_makes_a_parked_proposal_part_of_the_change (ADR 0007, 2): adopting a parked proposal whose checks pass records the owner's
// approval and a new review; the review shown before is stale; accepting the new one delivers both changes, and the commit verifies.
func TestAdopt_makes_a_parked_proposal_part_of_the_change(t *testing.T) {
	agentRuns := 0
	r := awayRun(t, &agentRuns)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	old, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err)
	parked := parkedNamed(t, old, "elsewhere.txt")

	_, err = runner.Adopt(context.Background(), r.Run.ID, parked.Seq, "0000")
	require.Error(t, err, "an adoption is made on the review that was shown")

	rv, err := runner.Adopt(context.Background(), r.Run.ID, parked.Seq, old.Entry)
	require.NoError(t, err)
	assert.NotEqual(t, old.Entry, rv.Entry, "a new review")
	assert.NotEqual(t, old.Tree, rv.Tree)
	assert.Contains(t, rv.Files, "elsewhere.txt")
	assert.Len(t, rv.ParkedProposals, 1, "only the failing one is still parked")
	assert.Equal(t, 1, agentRuns)

	_, err = runner.Adopt(context.Background(), r.Run.ID, parked.Seq, rv.Entry)
	require.Error(t, err, "a proposal is adopted once")

	_, err = runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true, Shown: old.Entry})
	require.Error(t, err, "the review shown before the adoption is stale")
	res, err := runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true, Shown: rv.Entry})
	require.NoError(t, err)
	assert.NotEmpty(t, res.Commit)
	for _, f := range []string{"src/one.txt", "src/two.txt", "elsewhere.txt"} {
		_, err := r.OnBranch(f)
		assert.NoError(t, err, f)
	}
	_, err = r.OnBranch("src/bad.txt")
	assert.Error(t, err)

	b, err := os.ReadFile(orchestrator.LedgerPath(r.WsDir, r.Run.ID))
	require.NoError(t, err)
	var ledger orchestrator.Ledger
	require.NoError(t, json.Unmarshal(b, &ledger))
	assert.Len(t, ledger.Proposals, 3, "the adopted proposal is an ordinary approval: rebuild needs no new rule")
	assert.Equal(t, "operator", ledger.Proposals[2].Source)
	_, err = os.Stat(filepath.Join(r.WsDir, "audit", "run-1.certificate.json"))
	assert.NoError(t, err)
	st, _ := status(t, r)
	assert.Equal(t, persistence.RunStatusSuccess, st)
}

// TestAdopt_refuses_a_proposal_whose_checks_fail: the checks decide on top of the change; a failing adoption records nothing.
func TestAdopt_refuses_a_proposal_whose_checks_fail(t *testing.T) {
	agentRuns := 0
	r := awayRun(t, &agentRuns)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	rv, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err)
	before := chainLength(t, r)
	_, err = runner.Adopt(context.Background(), r.Run.ID, parkedNamed(t, rv, "src/bad.txt").Seq, rv.Entry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FORBIDDEN found")
	assert.Equal(t, before, chainLength(t, r), "nothing is recorded")
}

// TestPrototype_a_parked_command_cannot_be_prototyped: it would have to run; the owner is told so, and nothing is written.
func TestPrototype_a_parked_command_cannot_be_prototyped(t *testing.T) {
	opts := awayOpts()
	opts.AllowShellExec = true
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{approve: true}),
		Run:   opts,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/one.txt", "fine\n")
			env.Propose(ctx, domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionShellExec,
				ProposedEdits: []domain.ProposedEdit{{ReplaceBlock: "make deploy"}}})
			return nil
		})})
	require.NoError(t, r.Err)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	rv, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err)
	require.Len(t, rv.ParkedProposals, 1)
	assert.Equal(t, domain.ActionShellExec, rv.ParkedProposals[0].Action)
	_, err = runner.Prototype(context.Background(), r.Run.ID, rv.ParkedProposals[0].Seq)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only a file change")
}
