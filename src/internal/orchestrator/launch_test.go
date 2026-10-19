package orchestrator_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launches reads the run's launch_claimed and launch_ended entries, in chain order.
func launches(t *testing.T, events []domain.RunEventLog) (claims []history.LaunchClaimed, ends []history.LaunchEnded) {
	t.Helper()
	for _, e := range events {
		switch e.EventType {
		case "launch_claimed":
			var c history.LaunchClaimed
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &c))
			claims = append(claims, c)
		case "launch_ended":
			var p history.LaunchEnded
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &p))
			ends = append(ends, p)
		}
	}
	return claims, ends
}

// TestRun_claims_the_launch_before_the_agent_starts_and_records_its_end: the agent's launch is on the chain, with its attempt
// number and generation, before the agent runs (the agent itself finds it there, with no outcome), and how it ended is on the
// chain once it stopped and before the run's commit is named.
func TestRun_claims_the_launch_before_the_agent_starts_and_records_its_end(t *testing.T) {
	var store *persistence.Store
	var atStart []string
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, ws string, _ int64) {
			store = s
			require.NoError(t, crypto.GenerateSigningKey(ws))
		},
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			runs, err := store.ListRuns()
			require.NoError(t, err)
			events, err := store.ListEventLogs(runs[0].ID)
			require.NoError(t, err)
			for _, e := range events {
				atStart = append(atStart, e.EventType)
			}
			write(c, env, "src/a.txt", "one\n")
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Contains(t, atStart, "launch_claimed", "claimed before the agent started")
	assert.NotContains(t, atStart, "launch_ended")

	claims, ends := launches(t, r.Events)
	require.Len(t, claims, 1)
	require.Len(t, ends, 1)
	assert.Equal(t, 1, claims[0].Attempt)
	assert.Equal(t, 1, claims[0].Segment)
	assert.Len(t, claims[0].Generation, 32, "a generation of its own")
	assert.Equal(t, history.LaunchEnded{Attempt: 1, Generation: claims[0].Generation, Outcome: history.LaunchExited}, ends[0])

	types := r.Types()
	at := func(event string) int { return slices.Index(types, event) }
	assert.Less(t, at("launch_claimed"), at("yield_request"))
	assert.Less(t, slices.Index(types, "yield_decided"), at("launch_ended"))
	assert.Less(t, at("launch_ended"), at("commit_prepared"))
}

// loseOutcomes makes the outcome of every launch fail to reach the chain, as when the process that launched the agent dies
// before it could write it; a restart's own record of the outcome (unknown_after_restart) still goes through. It returns what
// puts the store back.
func loseOutcomes(t *testing.T, db *sql.DB) (restore func()) {
	_, err := db.Exec(`CREATE TRIGGER lost_outcome BEFORE INSERT ON run_event_logs
		WHEN NEW.event_type = 'launch_ended' AND NEW.payload NOT LIKE '%unknown_after_restart%' BEGIN SELECT RAISE(ABORT, 'the process was lost'); END`)
	require.NoError(t, err)
	return func() { _, err := db.Exec(`DROP TRIGGER lost_outcome`); require.NoError(t, err) }
}

// lostLaunch is a run interrupted after it approved files, whose launch has no outcome on the chain.
func lostLaunch(t *testing.T, files ...[2]string) runtest.Result {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var restore func()
	r := runtest.Run(t, runtest.Options{Ctx: ctx,
		DB:    func(db *sql.DB) { restore = loseOutcomes(t, db) },
		Setup: func(_ *persistence.Store, ws string, _ int64) { require.NoError(t, crypto.GenerateSigningKey(ws)) },
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			for _, f := range files {
				write(c, env, f[0], f[1])
			}
			cancel()
			<-c.Done()
			return c.Err()
		})})
	restore()
	require.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	return r
}

// loseAgain continues the run, acknowledging its lost launch, and loses the continuation's launch too.
func loseAgain(t *testing.T, r runtest.Result) {
	restore := loseOutcomes(t, r.DB)
	defer restore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := orchestrator.NewRunner(r.Store, r.WsDir).Resume(ctx, r.Run.ID, orchestrator.ResumeOptions{AckUnknown: true,
		Agent: orchestrator.AgentFunc(func(c context.Context, _ *orchestrator.AgentEnv) error { cancel(); <-c.Done(); return c.Err() })})
	require.NotErrorIs(t, err, orchestrator.ErrNotContinuable)
	_, ends := launches(t, eventsOf(t, r, "launch_ended"))
	require.NotEmpty(t, ends)
}

// TestResume_a_launch_with_no_outcome_parks_the_run: a restart that finds the launch with no outcome records it as unknown and
// does not launch the agent again; the run stays parked until a person acknowledges it, and then goes on as its next attempt.
func TestResume_a_launch_with_no_outcome_parks_the_run(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	claims, ends := launches(t, eventsOf(t, r, "launch_claimed"))
	require.Len(t, claims, 1)
	require.Empty(t, ends)

	launched := 0
	agent := orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
		launched++
		write(c, env, "src/b.txt", "two\n")
		return nil
	})
	err := resume(t, r, orchestrator.ResumeOptions{Agent: agent})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
	assert.Contains(t, err.Error(), "launch 1")
	assert.Contains(t, err.Error(), "--ack-unknown")
	assert.Equal(t, 0, launched, "the agent is not launched again blindly")
	_, ends = launches(t, eventsOf(t, r, "launch_ended"))
	assert.Equal(t, []history.LaunchEnded{{Attempt: 1, Generation: claims[0].Generation, Outcome: history.LaunchUnknown, Class: history.ClassUncertainDispatch}}, ends)
	assert.Empty(t, eventsOf(t, r, "run_resumed"))

	err = resume(t, r, orchestrator.ResumeOptions{Agent: agent})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable, "still parked")
	assert.Len(t, eventsOf(t, r, "launch_ended"), 1, "the outcome is recorded once")
	assert.Equal(t, 0, launched)

	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{Agent: agent, AckUnknown: true}))
	assert.Equal(t, 1, launched)
	var resumed struct {
		AckUnknown int `json:"ack_unknown"`
	}
	require.Len(t, eventsOf(t, r, "run_resumed"), 1)
	require.NoError(t, json.Unmarshal([]byte(eventsOf(t, r, "run_resumed")[0].Payload), &resumed))
	assert.Equal(t, 1, resumed.AckUnknown, "the acknowledgement is on the chain")
	claims, _ = launches(t, eventsOf(t, r, "launch_claimed"))
	require.Len(t, claims, 2)
	assert.Equal(t, 2, claims[1].Attempt)
	assert.Equal(t, 2, claims[1].Segment)
	assert.Equal(t, "one", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
	assert.Equal(t, "two", trim(git(t, r.Repo, "show", "staircase/run-1:src/b.txt")))
	require.NoError(t, r.Store.VerifyChain(r.Run.ID))
}

// TestResume_a_run_whose_launches_were_lost_three_times_is_not_launched_again: the bound holds even when acknowledged, the
// refusal says why, and what the run approved can still be recovered.
func TestResume_a_run_whose_launches_were_lost_three_times_is_not_launched_again(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	loseAgain(t, r)
	loseAgain(t, r)
	launched := false
	err := resume(t, r, orchestrator.ResumeOptions{AckUnknown: true,
		Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error { launched = true; return nil })})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d times", orchestrator.MaxLostLaunches))
	assert.Contains(t, err.Error(), "staircase recover 1")
	assert.False(t, launched)
	assert.Equal(t, 3, orchestrator.MaxLostLaunches, "the bound the documentation states")
	_, ends := launches(t, eventsOf(t, r, "launch_ended"))
	require.Len(t, ends, 3)
	for _, e := range ends {
		assert.Equal(t, history.LaunchUnknown, e.Outcome)
	}

	_, err = recoverRun(t, r)
	require.NoError(t, err)
	assert.Equal(t, "one", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
}

// TestRun_records_how_the_launch_ended: an agent that fails, one the run stops, and one that does not stop when asked.
func TestRun_records_how_the_launch_ended(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		r := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error {
			return errors.New("the agent broke")
		})})
		_, ends := launches(t, r.Events)
		require.Len(t, ends, 1)
		assert.Equal(t, history.LaunchFailed, ends[0].Outcome)
	})
	t.Run("stopped", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		_, ends := launches(t, r.Events)
		require.Len(t, ends, 1)
		assert.Equal(t, history.LaunchStopped, ends[0].Outcome)
	})
	t.Run("unresponsive", func(t *testing.T) {
		t.Cleanup(orchestrator.SetLostGraceForTest(300 * time.Millisecond))
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		r := runtest.Run(t, runtest.Options{Ctx: ctx, Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error {
			cancel()
			<-release
			return nil
		})})
		_, ends := launches(t, r.Events)
		require.Len(t, ends, 1)
		assert.Equal(t, history.LaunchUnresponsive, ends[0].Outcome)
	})
}
