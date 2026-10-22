package main

import (
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/spf13/viper"
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

// TestStatusLines_show_a_lifecycle_failure_apart_from_the_outcome (V11-C01 scope 8): a launch that did not end well is listed with
// its class, and the line says it is about the agent and not about the checks or an acceptance. A run with none has no such line.
func TestStatusLines_show_a_lifecycle_failure_apart_from_the_outcome(t *testing.T) {
	st := orchestrator.Status{Outcome: history.OutcomeFailed, Why: "the run failed", Attempts: 2, Failures: []history.Launch{
		{LaunchClaimed: history.LaunchClaimed{Attempt: 1}, Outcome: history.LaunchUnknown, Class: history.ClassUncertainDispatch},
		{LaunchClaimed: history.LaunchClaimed{Attempt: 2}, Outcome: history.LaunchFailed, Class: history.ClassHostExit}}}
	text := strings.Join(statusLines(st), "\n")
	assert.Contains(t, text, "launches   attempt 1: an uncertain dispatch")
	assert.Contains(t, text, "attempt 2: a host exit")
	assert.Contains(t, text, "about the agent, not its checks or any acceptance")

	st.Failures = nil
	assert.NotContains(t, strings.Join(statusLines(st), "\n"), "launches ")
}

// TestStatusLines_say_how_many_launches_were_lost: the attempts line counts the launches claimed and says how many had no established end,
// and the outcome line carries the command to continue a parked run.
func TestStatusLines_say_how_many_launches_were_lost(t *testing.T) {
	st := orchestrator.Status{RunID: 7, Outcome: history.OutcomeBlocked, Attempts: 3, LostLaunches: 3,
		Why: "refused: it is not launched again: recover what it approved with staircase recover 7"}
	text := strings.Join(statusLines(st), "\n")
	assert.Contains(t, text, "attempts   3")
	assert.Contains(t, text, "3 launch(es) ended without an established end")
	assert.Contains(t, text, "staircase recover 7")
	assert.NotContains(t, strings.Join(statusLines(orchestrator.Status{Attempts: 1}), "\n"), "established end")
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

// TestDecide_a_run_whose_chain_cannot_be_read_is_listed_not_dropped: the other-away-runs list names a run whose status cannot be read
// (its chain does not verify) instead of leaving it out, as if it had nothing to say.
func TestDecide_a_run_whose_chain_cannot_be_read_is_listed_not_dropped(t *testing.T) {
	awaySession(t)
	decideReject = true
	require.NoError(t, decideHandler(nil, []string{"1"})) // the run ends FAILED
	decideReject = false

	store, db, err := openStore()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`UPDATE run_event_logs SET payload = payload || ' ' WHERE run_id = 1 AND id = (SELECT MIN(id) FROM run_event_logs WHERE run_id = 1)`)
	require.NoError(t, err)

	also := alsoAway(store, orchestrator.NewRunner(store, viper.GetString("STAIRCASE_DIR")))
	require.Len(t, also, 1, "the run is listed, not dropped")
	assert.Contains(t, also[0], "could not read run #1: ")
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

// TestStatusLines_say_what_the_reviewer_models_did: the votes on the chain are in the status the morning commands print.
func TestStatusLines_say_what_the_reviewer_models_did(t *testing.T) {
	lines := strings.Join(statusLines(orchestrator.Status{Outcome: history.OutcomeAwaitingReview, Votes: 6, VotedProposals: 3, UnsettledProposals: 2}), "\n")
	assert.Contains(t, lines, "6 vote(s) of the reviewer models on 3 proposal(s)")
	assert.Contains(t, lines, "did not settle 2")
	assert.NotContains(t, strings.Join(statusLines(orchestrator.Status{Outcome: history.OutcomeAwaitingReview}), "\n"), "reviewers")
}

// TestStatusLines_show_each_model_s_verdict (ADR 0014, section 3): the screens a person decides from list what every reviewer model
// said about a proposal and who decided it. The reasons are the models' own words: control characters are made visible, they stay on
// one line and they are cut short; and a long list is cut, saying how many more there are.
func TestStatusLines_show_each_model_s_verdict(t *testing.T) {
	panel := []history.Panelled{{Seq: 2, Paths: []string{"b.txt", "c.txt"}, DecidedBy: "operator", Approved: true, Votes: []history.Vote{
		{ValidatorVoted: history.ValidatorVoted{Model: "a/one", Verdict: history.VoteReject, Reason: "leaks a token\n\x1b[2Jall clear " + strings.Repeat("x", 200)}},
		{ValidatorVoted: history.ValidatorVoted{Model: "b/two", Verdict: history.VoteApprove, Reason: "looks fine"}}}}}
	lines := strings.Join(statusLines(orchestrator.Status{Outcome: history.OutcomeAwaitingReview, Votes: 2, VotedProposals: 1, UnsettledProposals: 1, Panel: panel}), "\n")
	assert.Contains(t, lines, "proposal 2 (b.txt and 1 more): a/one reject (leaks a token")
	assert.Contains(t, lines, "b/two approve (looks fine) -> approved by operator")
	assert.NotContains(t, lines, "\x1b", "an escape sequence in a model's reason is shown, not obeyed")
	assert.Contains(t, lines, "...", "a long reason is cut")

	var many []history.Panelled
	for i := range 20 {
		many = append(many, history.Panelled{Seq: i + 1, DecidedBy: "validator:a", Votes: []history.Vote{{ValidatorVoted: history.ValidatorVoted{Model: "a", Verdict: history.VoteApprove, Reason: "ok"}}}})
	}
	text := strings.Join(panelLines(many), "\n")
	assert.Contains(t, text, "proposal 12:")
	assert.NotContains(t, text, "proposal 13:")
	assert.Contains(t, text, "and 8 more proposal(s) with votes")
}
