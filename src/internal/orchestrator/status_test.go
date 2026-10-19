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
	assert.Equal(t, "the run failed, and its agent's launch 1 ended in "+history.ClassPhrase(history.ClassHostExit)+": nothing was delivered", why)
}

// TestStatusOf_attempts_are_the_claimed_launches: a run's attempts are the launches it claimed on its chain, and for a run without a
// lost launch they are the segments it had.
func TestStatusOf_attempts_are_the_claimed_launches(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{AckUnknown: true}))
	st, err := runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	h, err := history.Read(r.Store, r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, st.Attempts, "the lost launch counts, and so does the one after it")
	assert.Equal(t, h.Segment, st.Attempts)
	assert.Equal(t, 1, st.LostLaunches)
	assert.NotContains(t, st.Why, "--ack-unknown", "acknowledged and finished: not parked any more")

	clean := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
	require.NoError(t, clean.Err)
	st, err = orchestrator.NewRunner(clean.Store, clean.WsDir).StatusOf(clean.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, st.Attempts)
	assert.Zero(t, st.LostLaunches)
}

// TestStatusOf_a_parked_run_is_blocked_and_names_the_command: a run whose last launch has no established end waits for its owner, who
// is told the exact command to continue it after looking, and the one to recover what it approved; before and after a restart
// recorded the outcome as unknown.
func TestStatusOf_a_parked_run_is_blocked_and_names_the_command(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	st, err := runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeBlocked, st.Outcome, st.Why, "an open claim: the process was lost")
	assert.Equal(t, 1, st.Attempts, "a claimed launch counts")
	assert.Contains(t, st.Why, "staircase resume 1 --ack-unknown")
	assert.Contains(t, st.Why, "staircase recover 1")

	err = resume(t, r, orchestrator.ResumeOptions{})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable) // the restart records the outcome as unknown
	st, err = runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeBlocked, st.Outcome, st.Why)
	assert.Contains(t, st.Why, "staircase resume 1 --ack-unknown")
}

// TestStatusOf_a_run_with_three_lost_launches_is_refused: no acknowledgement continues it; the status says so and points to recover.
func TestStatusOf_a_run_with_three_lost_launches_is_refused(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	loseAgain(t, r)
	loseAgain(t, r)
	st, err := orchestrator.NewRunner(r.Store, r.WsDir).StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeBlocked, st.Outcome, st.Why)
	assert.Equal(t, 3, st.Attempts)
	assert.Equal(t, 3, st.LostLaunches)
	assert.Contains(t, st.Why, "refused")
	assert.Contains(t, st.Why, "staircase recover 1")
	assert.NotContains(t, st.Why, "--ack-unknown", "an acknowledgement does not lift the bound")
}

// TestStatusOf_a_run_whose_agent_is_running_is_running: an open claim is a lost launch only when no process holds the run; the run's own
// lock says it is alive.
func TestStatusOf_a_run_whose_agent_is_running_is_running(t *testing.T) {
	var store *persistence.Store
	var ws string
	var live orchestrator.Status
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, wsDir string, _ int64) { store, ws = s, wsDir },
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			var err error
			live, err = orchestrator.NewRunner(store, ws).StatusOf(1)
			require.NoError(t, err)
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Equal(t, history.OutcomeRunning, live.Outcome, live.Why)
	assert.Equal(t, 1, live.Attempts)
}
