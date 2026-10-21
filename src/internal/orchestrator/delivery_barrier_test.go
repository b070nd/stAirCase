//go:build barriers

package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These end a delivery by policy at a boundary (the hook fires in the run's own goroutine, where the kill drill's marker holds a
// process) and have a fresh recovery finish it. The later boundaries, with a process really killed, are in tests/barrier_drill.bats.

// policyRunEndedAt is a policy delivery of two documents whose run is cancelled at the first visit of point.
func policyRunEndedAt(t *testing.T, point string) runtest.Result {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelAt(t, point, 1, cancel)
	p := &policyRun{ctx: ctx, agent: writes([2]string{"docs/a.md", "first\n"}, [2]string{"docs/b.md", "second\n"})}
	return p.run(t)
}

func commitsOnBranch(t *testing.T, r runtest.Result) string {
	return trim(git(t, r.Repo, "rev-list", "--count", "main..staircase/run-1"))
}

// TestConsumption_policy_delivery_ended_after_the_authorization: the chain holds the authorization and no commit is named. The run
// is blocked and is not continued; recover commits exactly the approved bytes, once, without asking anyone, with a certificate that
// says it was delivered by policy; a second recover finds nothing to do.
func TestConsumption_policy_delivery_ended_after_the_authorization(t *testing.T) {
	r := policyRunEndedAt(t, barrier.DeliveryAuthorized)
	assert.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	assert.Equal(t, 1, strings.Count(strings.Join(r.Types(), " "), "delivery_authorized"))
	assert.Nil(t, payload(t, r, "commit_prepared"))
	branchUntouched(t, r)

	h, err := history.Read(r.Store, r.Run.ID)
	require.NoError(t, err)
	out, why := h.Outcome()
	assert.Equal(t, history.OutcomeBlocked, out)
	assert.Contains(t, why, "staircase recover")

	err = orchestrator.NewRunner(r.Store, r.WsDir).Resume(context.Background(), r.Run.ID, orchestrator.ResumeOptions{Agent: writes()})
	require.Error(t, err, "an authorized delivery is finished, not continued")
	assert.Contains(t, err.Error(), "recover")

	res, err := recoverRun(t, r)
	require.NoError(t, err, "nothing was approved by a person, so nobody is asked")
	assert.False(t, res.FinalReview)
	assert.Equal(t, "1", commitsOnBranch(t, r))
	assert.Equal(t, "first", trim(git(t, r.Repo, "show", "staircase/run-1:docs/a.md")))
	assert.Equal(t, "second", trim(git(t, r.Repo, "show", "staircase/run-1:docs/b.md")))
	s := certificateOf(t, r)
	assert.Equal(t, "policy", s.Predicate.Delivery, "a recovered delivery says it was delivered by policy too")
	assert.Contains(t, strings.Join(s.Predicate.Notes, " "), "no person reviewed the whole change")
	assert.Equal(t, res.Commit, s.Commit())
	h, err = history.Read(r.Store, r.Run.ID)
	require.NoError(t, err)
	require.NotNil(t, h.Authorized, "the chain still reads after the recovery")
	assert.NotEqual(t, "delivery_authorized", h.Sealed, "the recovery named the commit")

	_, err = recoverRun(t, r)
	require.Error(t, err, "recovered: nothing left to recover")
	assert.Equal(t, "1", commitsOnBranch(t, r))
}

// TestConsumption_policy_delivery_ended_after_the_commit_is_named: the commit exists on the chain, the branch has not moved; recover
// makes its commit: one commit, one certificate.
func TestConsumption_policy_delivery_ended_after_the_commit_is_named(t *testing.T) {
	r := policyRunEndedAt(t, barrier.CommitPrepared)
	branchUntouched(t, r)
	require.NotNil(t, payload(t, r, "commit_prepared"))
	_, err := recoverRun(t, r)
	require.NoError(t, err)
	assert.Equal(t, "1", commitsOnBranch(t, r))
	assert.Equal(t, "policy", certificateOf(t, r).Predicate.Delivery)
	h, err := history.Read(r.Store, r.Run.ID)
	require.NoError(t, err)
	require.NotNil(t, h.Authorized)
	assert.Equal(t, "commit_prepared", h.Sealed)
}
