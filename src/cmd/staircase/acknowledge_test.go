package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lostHostedRun is a hosted session whose process died with its agent's launch unrecorded: its launch has no outcome on the chain
// and its run is KILLED, as a hard kill leaves it.
func lostHostedRun(t *testing.T, ws, session string) {
	t.Helper()
	db, err := persistence.InitDB(ws)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TRIGGER lost_outcome BEFORE INSERT ON run_event_logs
		WHEN NEW.event_type = 'launch_ended' AND NEW.payload NOT LIKE '%unknown_after_restart%' BEGIN SELECT RAISE(ABORT, 'the process was lost'); END`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	var out bytes.Buffer
	_ = hostClaude(cloudroomArgs(session), strings.NewReader(`{"type":"user"}`+"\n"), &out)
	db, err = persistence.InitDB(ws)
	require.NoError(t, err)
	_, err = db.Exec(`DROP TRIGGER lost_outcome`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE runs SET status = 'KILLED' WHERE id = 1`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	require.Empty(t, hostEvents(t, ws, 1, "launch_ended"), "the launch has no outcome")
	require.Len(t, hostEvents(t, ws, 1, "launch_claimed"), 1)
}

// TestAcknowledge_lets_a_hosted_resume_proceed: a hosted session's resume never acknowledges by itself, and its refusal names the
// command; after `staircase acknowledge <run>` the same resume goes on, and a second acknowledgement changes nothing.
func TestAcknowledge_lets_a_hosted_resume_proceed(t *testing.T) {
	_, ws, _ := hostRepo(t)
	const id = "77777777-2222-3333-4444-555555555555"
	lostHostedRun(t, ws, id)

	var out bytes.Buffer
	err := hostClaude(resumeArgs(id), strings.NewReader(`{"type":"user"}`+"\n"), &out)
	require.Error(t, err, "parked: the runner's resume does not acknowledge")
	assert.Contains(t, err.Error(), "staircase acknowledge 1", "the refusal names the command")
	assert.Empty(t, hostEvents(t, ws, 1, "launch_acknowledged"))

	var said bytes.Buffer
	require.NoError(t, acknowledgeRun(&said, 1))
	assert.Contains(t, said.String(), "Launch 1 of run #1 is acknowledged")
	assert.Contains(t, said.String(), "Nothing was launched")
	assert.Len(t, hostEvents(t, ws, 1, "launch_acknowledged"), 1)
	assert.Empty(t, hostEvents(t, ws, 1, "run_resumed"), "the command resumes nothing")

	said.Reset()
	require.NoError(t, acknowledgeRun(&said, 1))
	assert.Contains(t, said.String(), "was acknowledged already")
	assert.Len(t, hostEvents(t, ws, 1, "launch_acknowledged"), 1, "a second call writes nothing")

	assert.Contains(t, strings.Join(hostedRun(t, resumeArgs(id)), " "), "--resume "+id, "the runner's own resume proceeds")
	assert.Len(t, hostEvents(t, ws, 1, "run_resumed"), 1)
}

// TestAcknowledge_refuses_what_has_nothing_to_acknowledge: a run whose launch ended well, and a run that does not exist.
func TestAcknowledge_refuses_what_has_nothing_to_acknowledge(t *testing.T) {
	_, ws, _ := hostRepo(t)
	hostedRun(t, cloudroomArgs("88888888-2222-3333-4444-555555555555"))
	var said bytes.Buffer
	err := acknowledgeRun(&said, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no open or lost launch")
	assert.Empty(t, said.String())
	assert.Empty(t, hostEvents(t, ws, 1, "launch_acknowledged"))
	assert.ErrorContains(t, acknowledgeRun(&said, 99), "not found")
}
