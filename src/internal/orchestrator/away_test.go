package orchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awayOpts: away, approving in-scope changes on a check that refuses the word FORBIDDEN anywhere in src.
func awayOpts() orchestrator.RunOptions {
	return orchestrator.RunOptions{Terms: orchestrator.Terms{Away: true, ApproveOnEvidence: true, Agreed: "dev@example.com", Checks: []string{`! grep -rq FORBIDDEN src || { echo "FORBIDDEN found"; exit 1; }`}}}
}

// payload is the decoded payload of the first event of that type, or nil.
func payload(t *testing.T, r runtest.Result, event string) map[string]any {
	for _, e := range r.Events {
		if e.EventType == event {
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &m))
			return m
		}
	}
	return nil
}

// TestAway_parks_what_needs_a_person_and_ends_awaiting_review (ADR 0006, 2 to 5): while away nobody is asked. What evidence
// approves is approved; what would need a person is parked and the agent is told at once to go on; when the agent stops, the
// checks run on the exact approved tree, and the run ends awaiting review with nothing committed and its branch at its base.
func TestAway_parks_what_needs_a_person_and_ends_awaiting_review(t *testing.T) {
	op := &operator{approve: true} // would approve anything: it must never be asked
	var parked orchestrator.Approval
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(op),
		Run:   awayOpts(),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/one.txt", "fine\n")
			parked = write(ctx, env, "src/bad.txt", "FORBIDDEN\n") // no evidence: a person would decide
			write(ctx, env, "src/two.txt", "fine\n")
			write(ctx, env, "elsewhere.txt", "out of scope\n") // drift: a person would decide
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Empty(t, op.seen, "nobody is asked while away")
	assert.Equal(t, []string{"file_edit:evidence", "file_edit:parked", "file_edit:evidence", "file_edit:parked"}, sources(t, r))
	assert.False(t, parked.Approved)
	assert.Contains(t, parked.Feedback, "parked", "the agent is told the proposal is kept for the owner")

	assert.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status)
	assert.Empty(t, r.Run.GitCommitHash, "nothing is committed before the owner's review")
	_, err := r.OnBranch("src/one.txt")
	assert.Error(t, err, "the run branch stays at its base")

	checked := payload(t, r, "phase_checked")
	require.NotNil(t, checked, "the checks ran on the approved tree when the agent stopped")
	assert.NotEmpty(t, checked["tree"])
	assert.Equal(t, true, checked["passed"])
	awaiting := payload(t, r, "awaiting_review")
	require.NotNil(t, awaiting)
	assert.Equal(t, checked["tree"], awaiting["tree"], "the review rests on the checks of that very tree")
	assert.EqualValues(t, 2, awaiting["parked"])
	assert.Nil(t, payload(t, r, "commit_prepared"))
}

// TestAway_failing_checks_on_the_final_tree_end_the_run_failed: acceptance is a run of the checks on the exact approved tree when
// the agent stops, not the evidence each change had. A check that passed for the change and fails now (flaky, or depending on
// something outside the tree) ends the run FAILED, with nothing committed and nothing awaiting review.
func TestAway_failing_checks_on_the_final_tree_end_the_run_failed(t *testing.T) {
	counter := t.TempDir() + "/n"
	opts := awayOpts()
	opts.Sandbox = "off" // the check keeps a counter outside the tree, which a sandbox would not let it write
	opts.Checks = []string{`n=$(cat ` + counter + ` 2>/dev/null || echo 0); n=$((n+1)); echo $n > ` + counter + `; test $n -le 1 || { echo "failed on run $n"; exit 1; }`}
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{approve: true}),
		Run:   opts,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/one.txt", "fine\n") // the check's first run: passes, approved on evidence
			return nil
		})})
	require.ErrorIs(t, r.Err, orchestrator.ErrRunNotSuccessful, "a failed phase is a failed run")
	assert.Equal(t, []string{"file_edit:evidence"}, sources(t, r))
	checked := payload(t, r, "phase_checked")
	require.NotNil(t, checked)
	assert.Equal(t, false, checked["passed"])
	assert.Nil(t, payload(t, r, "awaiting_review"), "a failing phase is not offered for review")
	assert.Equal(t, persistence.RunStatusFailed, r.Run.Status)
	assert.Empty(t, r.Run.GitCommitHash)
}

// TestAway_needs_a_check: away approves only on evidence, so a run without a check is refused before it starts.
func TestAway_needs_a_check(t *testing.T) {
	opts := awayOpts()
	opts.Checks = nil
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{}),
		Run:   opts,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
	require.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "--check")
}

// awayRun is an away run that approved src/one.txt and src/two.txt on evidence and parked two proposals; agentRuns counts how
// often its agent was started.
func awayRun(t *testing.T, agentRuns *int) runtest.Result {
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			require.NoError(t, crypto.GenerateSigningKey(wsDir)) // the delivered commit gets its certificate
			inSrc(&operator{approve: true})(s, wsDir, projectID)
		},
		Run: awayOpts(),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			*agentRuns++
			write(ctx, env, "src/one.txt", "fine\n")
			write(ctx, env, "src/bad.txt", "FORBIDDEN\n")
			write(ctx, env, "src/two.txt", "fine\n")
			write(ctx, env, "elsewhere.txt", "out of scope\n")
			return nil
		})})
	require.NoError(t, r.Err)
	require.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status)
	return r
}

// status is the run's status as the store has it now.
func status(t *testing.T, r runtest.Result) (string, string) {
	run, err := r.Store.GetRun(r.Run.ID)
	require.NoError(t, err)
	return run.Status, run.GitCommitHash
}

// TestAway_accept_finalizes_once_without_the_agent (ADR 0006, 6 and 7): accepting commits exactly the approved files, with a
// certificate that cites the checks of that tree, without starting the agent again; a second decision finds nothing to decide.
func TestAway_accept_finalizes_once_without_the_agent(t *testing.T) {
	agentRuns := 0
	r := awayRun(t, &agentRuns)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	res, err := runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true, Feedback: "looks right"})
	require.NoError(t, err)
	assert.NotEmpty(t, res.Commit)
	assert.Equal(t, 1, agentRuns, "the agent is not started again")

	st, commit := status(t, r)
	assert.Equal(t, persistence.RunStatusSuccess, st)
	assert.Equal(t, res.Commit, commit)
	for _, f := range []string{"src/one.txt", "src/two.txt"} {
		got, err := r.OnBranch(f)
		require.NoError(t, err, f)
		assert.Equal(t, "fine\n", got)
	}
	for _, f := range []string{"src/bad.txt", "elsewhere.txt"} {
		_, err := r.OnBranch(f)
		assert.Error(t, err, "a parked proposal is not delivered: %s", f)
	}
	events, err := r.Store.ListVerifiedEventLogs(r.Run.ID)
	require.NoError(t, err)
	var types []string
	for _, e := range events {
		types = append(types, e.EventType)
	}
	assert.Contains(t, types, "review_accepted")
	assert.Contains(t, types, "certificate_issued")
	b, err := os.ReadFile(filepath.Join(r.WsDir, "audit", fmt.Sprintf("run-%d.certificate.json", r.Run.ID)))
	require.NoError(t, err)
	var env certificate.Envelope
	require.NoError(t, json.Unmarshal(b, &env))
	pub, err := crypto.LoadSigningPublicKey(r.WsDir)
	require.NoError(t, err)
	s, err := certificate.Open(env, pub)
	require.NoError(t, err)
	assert.Equal(t, commit, s.Commit())
	assert.Equal(t, 3, s.Predicate.CAL, "the run reached its end: not capped like a recovered run: %v", s.Predicate.Notes)
	require.Len(t, s.Predicate.Checks, 1, "the checks that ran on this very tree")
	assert.Equal(t, 0, s.Predicate.Checks[0].ExitCode)
	assert.Contains(t, strings.Join(s.Predicate.Notes, " "), "away")

	_, err = runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true})
	require.Error(t, err, "a delivered run has nothing to decide")
	_, again := status(t, r)
	assert.Equal(t, commit, again, "one commit")
}

// TestAway_reject_commits_nothing: the owner's rejection is recorded, the run ends FAILED and its branch stays at its base.
func TestAway_reject_commits_nothing(t *testing.T) {
	agentRuns := 0
	r := awayRun(t, &agentRuns)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	_, err := runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: false, Feedback: "not like this"})
	require.NoError(t, err)
	st, commit := status(t, r)
	assert.Equal(t, persistence.RunStatusFailed, st)
	assert.Empty(t, commit)
	_, err = r.OnBranch("src/one.txt")
	assert.Error(t, err)
	_, err = runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true})
	assert.Error(t, err, "a rejected run cannot be accepted afterwards")
}

// TestAway_a_decision_on_another_review_entry_is_refused: a decision names the awaiting_review entry it was shown; another one
// (a stale screen, a second inbox) is refused and changes nothing.
func TestAway_a_decision_on_another_review_entry_is_refused(t *testing.T) {
	agentRuns := 0
	r := awayRun(t, &agentRuns)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	_, err := runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true, Shown: "0000000000000000000000000000000000000000000000000000000000000000"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not the review")
	st, commit := status(t, r)
	assert.Equal(t, persistence.RunStatusAwaitingReview, st)
	assert.Empty(t, commit)
}

// TestAway_resume_and_recover_refuse_a_run_awaiting_review: it has ended; decide is how it goes on.
func TestAway_resume_and_recover_refuse_a_run_awaiting_review(t *testing.T) {
	agentRuns := 0
	r := awayRun(t, &agentRuns)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	_, err := runner.Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Force: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "staircase decide")
	err = runner.Resume(context.Background(), r.Run.ID, orchestrator.ResumeOptions{Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "staircase decide")
	assert.Equal(t, 1, agentRuns)
}

// TestAway_a_run_that_requires_signed_decisions_cannot_be_away: the owner's review in staircase decide is not signed in v1.1, so
// such a run is refused rather than reviewed with a weaker decision.
func TestAway_a_run_that_requires_signed_decisions_cannot_be_away(t *testing.T) {
	opts := awayOpts()
	opts.RequireSignedApprovals = true
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{}),
		Run:   opts,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
	require.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "signed")
}

// TestAway_the_review_names_what_was_parked_and_why: the owner sees each parked proposal's files and the reason it needed them,
// read from the verified chain, next to the change on offer and the checks of its tree.
func TestAway_the_review_names_what_was_parked_and_why(t *testing.T) {
	agentRuns := 0
	r := awayRun(t, &agentRuns)
	rv, err := orchestrator.NewRunner(r.Store, r.WsDir).ReviewOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"src/one.txt", "src/two.txt"}, slices.Sorted(maps.Keys(rv.Files)))
	assert.NotEmpty(t, rv.Base)
	require.Len(t, rv.ParkedProposals, 2)
	assert.Equal(t, []string{"src/bad.txt"}, rv.ParkedProposals[0].Files)
	assert.Contains(t, rv.ParkedProposals[0].Why, "FORBIDDEN found", "the failing check is why")
	assert.Equal(t, []string{"elsewhere.txt"}, rv.ParkedProposals[1].Files)
	assert.NotEmpty(t, rv.ParkedProposals[1].Why)
	assert.Equal(t, "", rv.Decided)
}

// TestAway_the_agent_is_told_it_works_unattended (ADR 0008, 3): an away run gives its agent the pinned Away brief (park, do not retry,
// hand off), and binds its version and digest on run_bound before the agent starts; a run that is not away gives none.
func TestAway_the_agent_is_told_it_works_unattended(t *testing.T) {
	var brief string
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{approve: true}),
		Run:   awayOpts(),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			brief = env.Brief
			write(ctx, env, "src/one.txt", "fine\n")
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Equal(t, orchestrator.AwayBrief, brief)
	assert.Contains(t, brief, "parked")
	bound := payload(t, r, "run_bound")
	require.NotNil(t, bound)
	ab, ok := bound["away_brief"].(map[string]any)
	require.True(t, ok, "run_bound names the brief: %v", bound)
	assert.EqualValues(t, orchestrator.AwayBriefVersion, ab["version"])
	assert.Equal(t, briefDigest(orchestrator.AwayBrief), ab["sha256"])

	var none string
	r2 := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{approve: true}),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { none = env.Brief; return nil })})
	require.NoError(t, r2.Err)
	assert.Empty(t, none)
	assert.Nil(t, payload(t, r2, "run_bound")["away_brief"])
}

func briefDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
