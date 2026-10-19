package main

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusLines_say_the_outcome_attempts_and_what_is_left (V11-C01 scope 6): the outcome and why, the attempts, and the time,
// tokens and estimated cost used against what is left.
func TestStatusLines_say_the_outcome_attempts_and_what_is_left(t *testing.T) {
	st := orchestrator.Status{RunID: 7, Away: true, Outcome: history.OutcomeBlocked, Why: "1 proposal(s) parked for you", Attempts: 2, Parked: 1,
		Elapsed: 20 * time.Minute, TimeLimit: time.Hour, InputTokens: 120000, OutputTokens: 30000, CostUSD: 0.81, BudgetCap: 5}
	text := strings.Join(statusLines(st), "\n")
	for _, want := range []string{"blocked or partial", "1 proposal(s) parked for you", "attempts", "2", "20m0s of 1h0m0s", "40m0s left",
		"120000 in", "30000 out", "no token limit", "$0.81 of $5.00", "$4.19 left", "estimated"} {
		assert.Contains(t, text, want)
	}

	st = orchestrator.Status{Outcome: history.OutcomeRunning, Attempts: 1, Elapsed: time.Minute, TimeUnknown: "run #3 saved no policy when it started", CostUSD: 0.1}
	text = strings.Join(statusLines(st), "\n")
	assert.Contains(t, text, "no time limit could be read: run #3 saved no policy")
	assert.Contains(t, text, "no budget cap")

	st = orchestrator.Status{Outcome: history.OutcomeFailed, Attempts: 1, Elapsed: 2 * time.Hour, TimeLimit: time.Hour, CostUSD: 6, BudgetCap: 5}
	text = strings.Join(statusLines(st), "\n")
	assert.Contains(t, text, "2h0m0s of 1h0m0s, nothing left", "used up")
	assert.Contains(t, text, "$6.00 of $5.00 (the project's cap per run), nothing left")
}

// TestDecide_a_run_that_does_not_wait_shows_its_status: `staircase decide <run>` on a run that is not awaiting review says how it
// stands instead of refusing; the inbox shows the status of each run that waits.
func TestDecide_a_run_that_does_not_wait_shows_its_status(t *testing.T) {
	awaySession(t)
	out, err := captureStdout(t, func() error { return decideHandler(nil, nil) })
	assert.ErrorContains(t, err, "no terminal", "the inbox is shown, then it asks for a terminal to decide in")
	assert.Contains(t, out, "outcome    awaiting review")
	assert.Contains(t, out, "attempts")

	decideReject = true
	require.NoError(t, decideHandler(nil, []string{"1"}))
	decideReject = false
	out, err = captureStdout(t, func() error { return decideHandler(nil, []string{"1"}) })
	require.NoError(t, err)
	assert.Contains(t, out, "Run #1")
	assert.Contains(t, out, "failed")
	assert.Contains(t, out, "you rejected its review")
}

// TestInspect_shows_each_runs_outcome: inspect runs has an outcome column for every run, a run awaiting review included, and
// inspect log says the run's outcome above its events.
func TestInspect_shows_each_runs_outcome(t *testing.T) {
	awaySession(t)
	out, err := captureStdout(t, func() error { return inspectRunsCmd.RunE(inspectRunsCmd, nil) })
	require.NoError(t, err)
	assert.Contains(t, out, "Outcome")
	assert.Contains(t, out, "AWAITING_REVIEW")
	assert.Contains(t, out, "awaiting review")
	out, err = captureStdout(t, func() error { return inspectLogCmd.RunE(inspectLogCmd, []string{"1"}) })
	require.NoError(t, err)
	assert.Contains(t, out, "outcome")
	assert.Contains(t, out, "awaiting review")
}
