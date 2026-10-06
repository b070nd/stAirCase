package orchestrator_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecover_an_approved_proposal_too_large_to_audit_in_full is still recoverable: the audit entry of its
// request is capped (a stub with the request's digest), and the stub says what kind of request it was, so
// the decision that follows still finds its request. The journal holds the whole request.
func TestRecover_an_approved_proposal_too_large_to_audit_in_full(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	big := strings.Repeat("é", 40000) // 80 kB: over the audit cap
	r := runtest.Run(t, runtest.Options{Ctx: ctx,
		Setup: func(_ *persistence.Store, ws string, _ int64) { require.NoError(t, crypto.GenerateSigningKey(ws)) },
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			write(c, env, "src/big.txt", big)
			cancel()
			<-c.Done()
			return c.Err()
		})})
	require.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	stub := false
	for _, e := range r.Events {
		if e.EventType == "yield_request" && strings.Contains(e.Payload, `"truncated":true`) {
			stub = true
			assert.Contains(t, e.Payload, `"action_type":"file_edit"`, "the capped entry still says what kind of request it was")
		}
	}
	require.True(t, stub, "the request was capped")
	res, err := recoverRun(t, r)
	require.NoError(t, err, "a large approved change must not make a run unrecoverable")
	assert.Equal(t, 1, res.Proposals)
	assert.Equal(t, big, git(t, r.Repo, "show", "staircase/run-1:src/big.txt"))
}

// TestPropose_a_request_that_cannot_be_audited_is_refused: the request is on the chain before any decision
// about it, and recovery needs it there. When it cannot be written the proposal is refused (the agent is
// told no, nothing is journaled, no decision is recorded), so a consumed approval can never be left
// without its request; the next proposal, once the chain accepts writes again, goes through, and the run is
// recoverable.
func TestPropose_a_request_that_cannot_be_audited_is_refused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var db *sql.DB
	var answers []bool
	r := runtest.Run(t, runtest.Options{Ctx: ctx, DB: func(d *sql.DB) { db = d },
		Setup: func(_ *persistence.Store, ws string, _ int64) { require.NoError(t, crypto.GenerateSigningKey(ws)) },
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			_, err := db.Exec(`CREATE TRIGGER no_request BEFORE INSERT ON run_event_logs WHEN NEW.event_type = 'yield_request' BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
			require.NoError(t, err)
			answers = append(answers, write(c, env, "src/a.txt", "lost\n").Approved)
			_, err = db.Exec(`DROP TRIGGER no_request`)
			require.NoError(t, err)
			answers = append(answers, write(c, env, "src/b.txt", "kept\n").Approved)
			cancel()
			<-c.Done()
			return c.Err()
		})})
	require.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	assert.Equal(t, []bool{false, true}, answers, "the proposal whose request could not be recorded was refused; the next went through")
	assert.Equal(t, 1, journalCount(t, r), "only the second proposal was journaled")
	decided := 0
	for _, e := range r.Events {
		if e.EventType == "yield_decided" {
			decided++
		}
	}
	assert.Equal(t, 1, decided, "no decision was recorded for the refused one")
	res, err := recoverRun(t, r)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Proposals)
	assert.Equal(t, "src/b.txt", trim(git(t, r.Repo, "diff", "--name-only", "HEAD", "staircase/run-1")), "exactly what was approved")
}
