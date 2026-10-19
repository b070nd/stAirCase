package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boundedAwayRun is an away run under a one-hour time limit and a $5 budget cap that used 100k tokens each way, approved one
// change on evidence and parked one.
func boundedAwayRun(t *testing.T) runtest.Result {
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			require.NoError(t, crypto.GenerateSigningKey(wsDir))
			inSrc(&operator{approve: true})(s, wsDir, projectID)
			require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(`{"rules":[],"limits":{"max_run_secs":3600}}`), 0o600))
			require.NoError(t, s.SetProjectBudgetCap(projectID, 5))
		},
		Run: awayOpts(),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			env.Emit(ctx, orchestrator.Usage{Agent: "coder", Model: "claude-sonnet-4-6", InputTokens: 100000, OutputTokens: 100000})
			write(ctx, env, "src/one.txt", "fine\n")
			write(ctx, env, "src/bad.txt", "FORBIDDEN\n")
			return nil
		})})
	require.NoError(t, r.Err)
	return r
}

// TestStatusOf_an_away_run_its_outcome_attempts_and_budget (V11-C01 scope 6): the morning status of an away run is read from its
// verified history: its outcome and why, its attempts, what it parked, and the time, tokens and estimated cost it used against what
// its terms leave.
func TestStatusOf_an_away_run_its_outcome_attempts_and_budget(t *testing.T) {
	r := boundedAwayRun(t)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	st, err := runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeAwaitingReview, st.Outcome, st.Why)
	assert.True(t, st.Away)
	assert.Equal(t, 1, st.Attempts, "one segment: the agent was started once")
	assert.Equal(t, 1, st.Parked)
	assert.Equal(t, 100000, st.InputTokens)
	assert.Equal(t, 100000, st.OutputTokens)
	assert.InDelta(t, 1.8, st.CostUSD, 0.0001, "estimated at claude-sonnet-4-6's price: $3 + $15 per million")
	assert.Equal(t, 5.0, st.BudgetCap)
	assert.Equal(t, time.Hour, st.TimeLimit, "the saved policy's max_run_secs")
	assert.Less(t, st.Elapsed, time.Minute)

	_, err = runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true})
	require.NoError(t, err)
	st, err = runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeDelivered, st.Outcome, st.Why)
	assert.NotEmpty(t, st.Commit)
}

// TestStatusOf_a_failed_and_a_rejected_run: checks that fail on the final tree, and a review the owner rejected, are failures, with
// the chain's reason.
func TestStatusOf_a_failed_and_a_rejected_run(t *testing.T) {
	counter := t.TempDir() + "/n"
	opts := awayOpts()
	opts.Sandbox = "off"
	opts.Checks = []string{`n=$(cat ` + counter + ` 2>/dev/null || echo 0); n=$((n+1)); echo $n > ` + counter + `; test $n -le 1 || { echo "failed on run $n"; exit 1; }`}
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{approve: true}),
		Run:   opts,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/one.txt", "fine\n")
			return nil
		})})
	require.ErrorIs(t, r.Err, orchestrator.ErrRunNotSuccessful)
	st, err := orchestrator.NewRunner(r.Store, r.WsDir).StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeFailed, st.Outcome)
	assert.Contains(t, st.Why, "checks failed")
	assert.Zero(t, st.TimeLimit, "no time limit in the policy")

	agentRuns := 0
	r = awayRun(t, &agentRuns)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	_, err = runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: false})
	require.NoError(t, err)
	st, err = runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeFailed, st.Outcome)
	assert.Contains(t, st.Why, "rejected")
}

// TestRun_records_how_each_segment_ended: run_path carries the status the segment ended with, so the chain alone tells a failed
// run from a stopped one.
func TestRun_records_how_each_segment_ended(t *testing.T) {
	failing := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(
		func(context.Context, *orchestrator.AgentEnv) error { return os.ErrClosed })})
	require.Error(t, failing.Err)
	h, err := history.Read(failing.Store, failing.Run.ID)
	require.NoError(t, err)
	out, why := h.Outcome()
	assert.Equal(t, history.OutcomeFailed, out, why)
	assert.Equal(t, "the run failed: nothing was delivered", why)
}
