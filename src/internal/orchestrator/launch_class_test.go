package orchestrator_test

import (
	"context"
	"database/sql"
	"errors"
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

// endOf runs a stand-in agent and returns the one launch_ended it left.
func endOf(t *testing.T, opts runtest.Options) (history.LaunchEnded, runtest.Result) {
	t.Helper()
	r := runtest.Run(t, opts)
	_, ends := launches(t, r.Events)
	require.Len(t, ends, 1)
	return ends[0], r
}

// TestRun_records_the_class_of_a_lifecycle_failure (V11-C01 scope 8): a launch that did not end well is on the chain with its
// class, beside its outcome and apart from every check result: the agent refused, the agent's process ending with an error, the
// agent not stopping when asked, and records that could not be written while it ran.
func TestRun_records_the_class_of_a_lifecycle_failure(t *testing.T) {
	t.Run("an agent that exits cleanly has no class", func(t *testing.T) {
		end, _ := endOf(t, runtest.Options{Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error { return nil })})
		assert.Equal(t, history.LaunchExited, end.Outcome)
		assert.Empty(t, end.Class)
	})
	t.Run("a host exit", func(t *testing.T) {
		end, _ := endOf(t, runtest.Options{Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error {
			return errors.New("exit status 1")
		})})
		assert.Equal(t, history.LaunchFailed, end.Outcome)
		assert.Equal(t, history.ClassHostExit, end.Class)
	})
	t.Run("a rejected launch", func(t *testing.T) {
		end, r := endOf(t, runtest.Options{Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error {
			return orchestrator.Rejected(errors.New("the session was not governed"))
		})})
		assert.Equal(t, history.LaunchFailed, end.Outcome)
		assert.Equal(t, history.ClassRejected, end.Class)
		assert.ErrorContains(t, r.Err, "not governed", "the agent's own words still reach the caller")
		assert.NotContains(t, r.Types(), "phase_checked", "a launch failure is not a check result")
	})
	t.Run("a cleanup failure", func(t *testing.T) {
		t.Cleanup(orchestrator.SetLostGraceForTest(300 * time.Millisecond))
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		end, _ := endOf(t, runtest.Options{Ctx: ctx, Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error {
			cancel()
			<-release
			return nil
		})})
		assert.Equal(t, history.LaunchUnresponsive, end.Outcome)
		assert.Equal(t, history.ClassCleanup, end.Class)
	})
	t.Run("storage trouble", func(t *testing.T) {
		for name, agentErr := range map[string]error{"an agent that finished": nil, "an agent that failed": errors.New("exit status 1")} {
			t.Run(name, func(t *testing.T) {
				end, _ := endOf(t, runtest.Options{
					DB: func(db *sql.DB) {
						_, err := db.Exec(`CREATE TRIGGER no_session BEFORE INSERT ON run_event_logs
							WHEN NEW.event_type = 'agent_session' BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
						require.NoError(t, err)
					},
					Agent: orchestrator.AgentFunc(func(_ context.Context, env *orchestrator.AgentEnv) error {
						env.RecordSession("stand-in", "s1", "started") // cannot be written
						return agentErr
					})})
				assert.Equal(t, history.ClassStorage, end.Class)
			})
		}
	})
}

// TestResume_records_a_lost_launch_as_an_uncertain_dispatch: the restart's record of a launch it found with no outcome carries its
// class, and the run's morning status shows it beside the outcome, in the class's words.
func TestResume_records_a_lost_launch_as_an_uncertain_dispatch(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	err := resume(t, r, orchestrator.ResumeOptions{})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
	_, ends := launches(t, eventsOf(t, r, "launch_ended"))
	require.Len(t, ends, 1)
	assert.Equal(t, history.LaunchUnknown, ends[0].Outcome)
	assert.Equal(t, history.ClassUncertainDispatch, ends[0].Class)

	st, err := orchestrator.NewRunner(r.Store, r.WsDir).StatusOf(r.Run.ID)
	require.NoError(t, err)
	require.Len(t, st.Failures, 1)
	assert.Equal(t, 1, st.Failures[0].Attempt)
	assert.Equal(t, history.ClassUncertainDispatch, st.Failures[0].Class)
	assert.NotEqual(t, history.OutcomeDelivered, st.Outcome, "an uncertain dispatch is never acceptance")
}

// TestStatusOf_a_host_exit_is_not_a_failed_check: a run whose agent exited with an error fails with that class, and its status
// says no check ran and nothing was accepted.
func TestStatusOf_a_host_exit_is_not_a_failed_check(t *testing.T) {
	r := runtest.Run(t, runtest.Options{
		Setup: func(_ *persistence.Store, ws string, _ int64) { require.NoError(t, crypto.GenerateSigningKey(ws)) },
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			write(c, env, "src/a.txt", "one\n")
			return errors.New("exit status 137")
		})})
	st, err := orchestrator.NewRunner(r.Store, r.WsDir).StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeFailed, st.Outcome, st.Why)
	assert.Contains(t, st.Why, "host exit")
	assert.NotContains(t, st.Why, "checks failed")
	require.Len(t, st.Failures, 1)
	assert.Equal(t, history.ClassHostExit, st.Failures[0].Class)
}
