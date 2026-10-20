package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/wslock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acknowledgements reads the run's launch_acknowledged entries.
func acknowledgements(t *testing.T, r runtest.Result) []history.LaunchAcknowledged {
	t.Helper()
	var out []history.LaunchAcknowledged
	for _, e := range eventsOf(t, r, "launch_acknowledged") {
		var p history.LaunchAcknowledged
		require.NoError(t, json.Unmarshal([]byte(e.Payload), &p))
		out = append(out, p)
	}
	return out
}

func acknowledge(t *testing.T, r runtest.Result) (orchestrator.AckResult, error) {
	t.Helper()
	return orchestrator.NewRunner(r.Store, r.WsDir).Acknowledge(r.Run.ID)
}

// loseAfterAcknowledging continues the acknowledged run as a hosted runner does (no flag: it never acknowledges by itself) and
// loses the continuation's launch too.
func loseAfterAcknowledging(t *testing.T, r runtest.Result) {
	t.Helper()
	restore := loseOutcomes(t, r.DB)
	defer restore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := orchestrator.NewRunner(r.Store, r.WsDir).Resume(ctx, r.Run.ID, orchestrator.ResumeOptions{Hosted: true,
		Agent: orchestrator.AgentFunc(func(c context.Context, _ *orchestrator.AgentEnv) error { cancel(); <-c.Done(); return c.Err() })})
	require.NotErrorIs(t, err, orchestrator.ErrNotContinuable)
}

// TestAcknowledge_refuses_a_run_with_no_open_or_lost_launch: nothing to acknowledge, and nothing is written.
func TestAcknowledge_refuses_a_run_with_no_open_or_lost_launch(t *testing.T) {
	t.Run("a launch that ended by itself", func(t *testing.T) {
		clean := runtest.Run(t, runtest.Options{Agent: writesThenEnds([2]string{"src/a.txt", "one\n"})})
		require.NoError(t, clean.Err)
		before := len(clean.Events)
		_, err := acknowledge(t, clean)
		require.ErrorIs(t, err, orchestrator.ErrNoLostLaunch)
		assert.Contains(t, err.Error(), "run #1")
		assert.Len(t, eventsOf(t, clean, "launch_acknowledged"), 0)
		assert.Len(t, eventsOf(t, clean, "launch_ended"), 1)
		all, e := clean.Store.ListEventLogs(clean.Run.ID)
		require.NoError(t, e)
		assert.Len(t, all, before, "nothing was written")
	})
	t.Run("a launch the run stopped itself", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		_, err := acknowledge(t, r)
		require.ErrorIs(t, err, orchestrator.ErrNoLostLaunch, "a stopped launch has an established end")
		assert.Empty(t, eventsOf(t, r, "launch_acknowledged"))
	})
	t.Run("a run that does not exist", func(t *testing.T) {
		clean := runtest.Run(t, runtest.Options{Agent: writesThenEnds()})
		_, err := orchestrator.NewRunner(clean.Store, clean.WsDir).Acknowledge(99)
		require.Error(t, err)
		assert.NotErrorIs(t, err, orchestrator.ErrNoLostLaunch)
	})
}

// TestAcknowledge_writes_the_launch_end_and_the_acknowledgement_once: an open launch gets its end (unknown) and then the
// acknowledgement, naming the exact launch; a second call changes nothing; the run is neither launched nor resumed.
func TestAcknowledge_writes_the_launch_end_and_the_acknowledgement_once(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	claims, ends := launches(t, eventsOf(t, r, "launch_claimed"))
	require.Len(t, claims, 1)
	require.Empty(t, ends)

	res, err := acknowledge(t, r)
	require.NoError(t, err)
	assert.Equal(t, orchestrator.AckResult{Attempt: 1, EndRecorded: true, Acknowledged: true}, res)

	types := make([]string, 0, 8)
	all, err := r.Store.ListEventLogs(r.Run.ID)
	require.NoError(t, err)
	for _, e := range all {
		types = append(types, e.EventType)
	}
	endAt, ackAt := slices.Index(types, "launch_ended"), slices.Index(types, "launch_acknowledged")
	require.GreaterOrEqual(t, endAt, 0)
	assert.Equal(t, endAt+1, ackAt, "the end first, then the acknowledgement")
	_, ends = launches(t, eventsOf(t, r, "launch_ended"))
	assert.Equal(t, []history.LaunchEnded{{Attempt: 1, Generation: claims[0].Generation, Outcome: history.LaunchUnknown, Class: history.ClassUncertainDispatch}}, ends)
	assert.Equal(t, []history.LaunchAcknowledged{{Attempt: 1, Generation: claims[0].Generation}}, acknowledgements(t, r))
	assert.Empty(t, eventsOf(t, r, "run_resumed"), "nothing was resumed")
	assert.Len(t, eventsOf(t, r, "launch_claimed"), 1, "nothing was launched")
	require.NoError(t, r.Store.VerifyChain(r.Run.ID))

	n := len(all)
	res, err = acknowledge(t, r)
	require.NoError(t, err)
	assert.Equal(t, orchestrator.AckResult{Attempt: 1}, res, "a second call does nothing")
	all, err = r.Store.ListEventLogs(r.Run.ID)
	require.NoError(t, err)
	assert.Len(t, all, n)

	h, err := history.Read(r.Store, r.Run.ID)
	require.NoError(t, err)
	assert.Zero(t, h.LaunchParked())
}

// TestAcknowledge_after_a_restart_recorded_the_outcome: a launch whose unknown end an earlier refusal wrote only needs the
// acknowledgement.
func TestAcknowledge_after_a_restart_recorded_the_outcome(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	require.ErrorIs(t, resume(t, r, orchestrator.ResumeOptions{}), orchestrator.ErrNotContinuable)
	require.Len(t, eventsOf(t, r, "launch_ended"), 1)

	res, err := acknowledge(t, r)
	require.NoError(t, err)
	assert.Equal(t, orchestrator.AckResult{Attempt: 1, Acknowledged: true}, res, "the end was there already")
	assert.Len(t, eventsOf(t, r, "launch_ended"), 1)
	assert.Len(t, acknowledgements(t, r), 1)
}

// TestAcknowledge_refuses_a_run_a_process_still_holds: an open claim is a lost launch only when nobody runs the run.
func TestAcknowledge_refuses_a_run_a_process_still_holds(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	held, err := os.OpenFile(filepath.Join(r.WsDir, "journal", "run-1.owner.lock"), os.O_RDWR, 0)
	require.NoError(t, err)
	require.NoError(t, wslock.LockExclusive(held.Fd()))
	defer func() { _ = wslock.Unlock(held.Fd()); _ = held.Close() }()
	_, err = acknowledge(t, r)
	assert.ErrorContains(t, err, "still running")
	assert.Empty(t, eventsOf(t, r, "launch_ended"))
}

// TestResume_after_an_acknowledgement_goes_on_without_the_flag: a hosted runner's resume never acknowledges by itself; once a
// person did, it proceeds; before, it is parked and the refusal names the command.
func TestResume_after_an_acknowledgement_goes_on_without_the_flag(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	launched := 0
	agent := orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
		launched++
		write(c, env, "src/b.txt", "two\n")
		return nil
	})

	err := resume(t, r, orchestrator.ResumeOptions{Agent: agent, Hosted: true})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable, "parked before the acknowledgement")
	assert.Contains(t, err.Error(), "staircase acknowledge 1", "the refusal names the command that lifts it")
	assert.Equal(t, 0, launched)
	assert.Empty(t, acknowledgements(t, r), "a hosted resume never acknowledges by itself")

	_, err = acknowledge(t, r)
	require.NoError(t, err)
	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{Agent: agent, Hosted: true}))
	assert.Equal(t, 1, launched)
	assert.Equal(t, "two", trim(git(t, r.Repo, "show", "staircase/run-1:src/b.txt")))
	require.Len(t, eventsOf(t, r, "run_resumed"), 1)
	require.NoError(t, r.Store.VerifyChain(r.Run.ID))
}

// TestResume_with_the_flag_acknowledges_through_the_same_operation: --ack-unknown writes the launch's end and acknowledgement
// like the command does, and then the resume.
func TestResume_with_the_flag_acknowledges_through_the_same_operation(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{AckUnknown: true}))
	all, err := r.Store.ListEventLogs(r.Run.ID)
	require.NoError(t, err)
	var types []string
	for _, e := range all {
		types = append(types, e.EventType)
	}
	end, ack, resumed := slices.Index(types, "launch_ended"), slices.Index(types, "launch_acknowledged"), slices.Index(types, "run_resumed")
	require.GreaterOrEqual(t, end, 0)
	assert.Equal(t, end+1, ack)
	assert.Less(t, ack, resumed)
	var p struct {
		AckUnknown int `json:"ack_unknown"`
	}
	require.NoError(t, json.Unmarshal([]byte(eventsOf(t, r, "run_resumed")[0].Payload), &p))
	assert.Equal(t, 1, p.AckUnknown, "run_resumed still names the acknowledged attempt")
	require.NoError(t, r.Store.VerifyChain(r.Run.ID))
}

// TestAcknowledge_of_attempt_one_does_not_cover_a_later_loss: the acknowledgement names its launch; the next lost launch is
// parked again.
func TestAcknowledge_of_attempt_one_does_not_cover_a_later_loss(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	_, err := acknowledge(t, r)
	require.NoError(t, err)
	loseAfterAcknowledging(t, r) // attempt 2 is launched and lost

	launched := false
	err = resume(t, r, orchestrator.ResumeOptions{Hosted: true,
		Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error { launched = true; return nil })})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
	assert.Contains(t, err.Error(), "launch 2")
	assert.Contains(t, err.Error(), "staircase acknowledge 1")
	assert.False(t, launched)

	res, err := acknowledge(t, r)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempt)
	acks := acknowledgements(t, r)
	require.Len(t, acks, 2)
	assert.Equal(t, []int{1, 2}, []int{acks[0].Attempt, acks[1].Attempt})
}

// TestAcknowledge_does_not_lift_the_limit_of_lost_launches: three lost launches stay refused, acknowledged or not, and recover
// still works.
func TestAcknowledge_does_not_lift_the_limit_of_lost_launches(t *testing.T) {
	r := lostLaunch(t, [2]string{"src/a.txt", "one\n"})
	for range 2 {
		_, err := acknowledge(t, r)
		require.NoError(t, err)
		loseAfterAcknowledging(t, r)
	}
	// The third launch is lost and open: the acknowledgement records its end, and refuses to go further.
	_, err := acknowledge(t, r)
	require.ErrorIs(t, err, orchestrator.ErrLaunchLost)
	assert.Contains(t, err.Error(), "staircase recover 1")
	assert.Len(t, acknowledgements(t, r), 2, "the third launch was not acknowledged")

	launched := false
	err = resume(t, r, orchestrator.ResumeOptions{Hosted: true, AckUnknown: true,
		Agent: orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error { launched = true; return nil })})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
	assert.Contains(t, err.Error(), "3 times")
	assert.False(t, launched)

	_, err = recoverRun(t, r)
	require.NoError(t, err)
	assert.Equal(t, "one", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
}
