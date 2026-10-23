package orchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
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

// allParkedRun is an away run whose every proposal was parked: nothing was approved.
func allParkedRun(t *testing.T) runtest.Result {
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			require.NoError(t, crypto.GenerateSigningKey(wsDir))
			inSrc(&operator{approve: true})(s, wsDir, projectID)
		},
		Run: awayOpts(),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/bad.txt", "FORBIDDEN\n")      // no evidence: a person would decide
			write(ctx, env, "elsewhere.txt", "out of scope\n") // drift: a person would decide
			return nil
		})})
	return r
}

// TestAway_a_run_whose_every_proposal_was_parked_is_offered_for_review (V11-C01 acceptance 4, ticket 37): it does not end SUCCESS with
// nothing offered. It awaits the owner's review of an empty change, on the checks of the unchanged tree, with its parked proposals
// listed so the owner can prototype or adopt them.
func TestAway_a_run_whose_every_proposal_was_parked_is_offered_for_review(t *testing.T) {
	r := allParkedRun(t)
	require.NoError(t, r.Err)
	assert.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status, "not SUCCESS with nothing offered")
	assert.Empty(t, r.Run.GitCommitHash)
	checked := payload(t, r, "phase_checked")
	require.NotNil(t, checked, "the checks ran on the tree of the empty change")
	assert.Equal(t, true, checked["passed"])
	awaiting := payload(t, r, "awaiting_review")
	require.NotNil(t, awaiting)
	assert.EqualValues(t, 2, awaiting["parked"])
	assert.Equal(t, checked["tree"], awaiting["tree"])

	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	rv, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err)
	assert.Empty(t, rv.Files, "an empty change")
	require.Len(t, rv.ParkedProposals, 2)
	st, err := runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeAwaitingReview, st.Outcome, st.Why)
	assert.Contains(t, st.Why, "empty")
}

// TestAway_an_empty_review_can_prototype_and_adopt_a_parked_proposal (ADR 0007): adopting works on a review that offers nothing yet;
// accepting the new review delivers exactly the adopted proposal.
func TestAway_an_empty_review_can_prototype_and_adopt_a_parked_proposal(t *testing.T) {
	r := allParkedRun(t)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	rv, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err)
	parked := parkedNamed(t, rv, "elsewhere.txt")

	proto, err := runner.Prototype(context.Background(), r.Run.ID, parked.Seq)
	require.NoError(t, err)
	assert.True(t, proto.Passed, proto.Failed)
	assert.Contains(t, proto.Diff, "+out of scope")

	next, err := runner.Adopt(context.Background(), r.Run.ID, parked.Seq, rv.Entry)
	require.NoError(t, err)
	assert.Equal(t, []string{"elsewhere.txt"}, slices.Sorted(maps.Keys(next.Files)))
	res, err := runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true, Shown: next.Entry})
	require.NoError(t, err)
	assert.NotEmpty(t, res.Commit)
	got, err := r.OnBranch("elsewhere.txt")
	require.NoError(t, err)
	assert.Equal(t, "out of scope\n", got)
	_, err = r.OnBranch("src/bad.txt")
	assert.Error(t, err, "what was not adopted stays out")
}

// TestAway_accepting_an_empty_review_delivers_nothing_and_says_so: the owner took the empty change; no commit, no certificate, the run
// does not end SUCCESS, and its outcome says nothing was delivered.
func TestAway_accepting_an_empty_review_delivers_nothing_and_says_so(t *testing.T) {
	r := allParkedRun(t)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	res, err := runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true})
	require.NoError(t, err)
	assert.Empty(t, res.Commit)
	st, commit := status(t, r)
	assert.NotEqual(t, persistence.RunStatusSuccess, st, "nothing was delivered")
	assert.Empty(t, commit)
	_, err = r.OnBranch("elsewhere.txt")
	assert.Error(t, err)
	now, err := runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeFailed, now.Outcome, now.Why)
	assert.Contains(t, now.Why, "empty change")
	assert.Contains(t, now.Why, "nothing was delivered")
	_, err = runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true})
	assert.Error(t, err, "decided once")
}

// failingBaseRun is an away run whose checks fail on the unchanged tree (they want elsewhere.txt, which the project lacks) and whose
// only proposal, that very file outside the agreed scope, was parked.
func failingBaseRun(t *testing.T) runtest.Result {
	opts := awayOpts()
	opts.Checks = []string{`test -f elsewhere.txt || { echo "elsewhere.txt is missing"; exit 1; }`}
	return runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{approve: true}),
		Run:   opts,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "elsewhere.txt", "out of scope\n") // drift: a person would decide
			return nil
		})})
}

// TestAway_parked_proposals_are_offered_even_when_the_checks_fail_on_the_unchanged_tree: a run that approved nothing and parked what
// it proposed does not end FAILED with the proposals lost because the project's checks fail without them. The owner is offered an
// empty review that says its checks failed, with the parked proposals, and can prototype and adopt one that fixes the checks.
func TestAway_parked_proposals_are_offered_even_when_the_checks_fail_on_the_unchanged_tree(t *testing.T) {
	r := failingBaseRun(t)
	require.NoError(t, r.Err)
	assert.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status, "not FAILED with the parked proposal lost")
	checked := payload(t, r, "phase_checked")
	require.NotNil(t, checked)
	assert.Equal(t, false, checked["passed"], "the checks failed on the unchanged tree")
	awaiting := payload(t, r, "awaiting_review")
	require.NotNil(t, awaiting)
	assert.EqualValues(t, 1, awaiting["parked"])

	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	rv, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err)
	assert.Empty(t, rv.Files, "an empty change")
	assert.Contains(t, rv.ChecksFailed, "elsewhere.txt is missing")
	require.Len(t, rv.ParkedProposals, 1)
	st, err := runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeAwaitingReview, st.Outcome, st.Why)
	assert.Contains(t, st.Why, "checks failed", "an empty review whose checks failed is shown as such")

	proto, err := runner.Prototype(context.Background(), r.Run.ID, rv.ParkedProposals[0].Seq)
	require.NoError(t, err)
	assert.True(t, proto.Passed, proto.Failed, "the parked proposal is what fixes the checks")
	next, err := runner.Adopt(context.Background(), r.Run.ID, rv.ParkedProposals[0].Seq, rv.Entry)
	require.NoError(t, err)
	assert.Empty(t, next.ChecksFailed, "with the proposal adopted the checks pass")
	res, err := runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true, Shown: next.Entry})
	require.NoError(t, err)
	assert.NotEmpty(t, res.Commit)
}

// Accepting the empty review whose checks failed delivers nothing, as for any empty change.
func TestAway_accepting_an_empty_review_whose_checks_failed_delivers_nothing(t *testing.T) {
	r := failingBaseRun(t)
	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	res, err := runner.Decide(context.Background(), r.Run.ID, orchestrator.DecideOptions{Accept: true})
	require.NoError(t, err)
	assert.Empty(t, res.Commit)
	now, err := runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeFailed, now.Outcome, now.Why)
}

// stdoutOf is what f printed to the terminal.
func stdoutOf(t *testing.T, f func()) string {
	t.Helper()
	rd, wr, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdout
	os.Stdout = wr
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(rd)
		done <- string(b)
	}()
	defer func() { os.Stdout = old }()
	f()
	os.Stdout = old
	require.NoError(t, wr.Close())
	return <-done
}

// nothingRun is an away run whose agent proposes what proposals does (nothing, when nil) and whose checks are the given ones; it
// returns what the run printed too.
func nothingRun(t *testing.T, check string, hosted bool, proposals func(context.Context, *orchestrator.AgentEnv)) (runtest.Result, string) {
	opts := awayOpts()
	opts.Checks = []string{check}
	opts.Hosted = hosted
	var r runtest.Result
	out := stdoutOf(t, func() {
		r = runtest.Run(t, runtest.Options{
			Setup: inSrc(&operator{approve: true}),
			Run:   opts,
			Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
				if proposals != nil {
					proposals(ctx, env)
				}
				return nil
			})})
	})
	return r, out
}

const (
	checksPass = `true`
	checksFail = `test -f elsewhere.txt || { echo "elsewhere.txt is missing"; exit 1; }`
)

// TestAway_a_run_that_did_nothing_and_whose_checks_fail_ends_failed (ticket 02, PRD 7): nothing approved and nothing parked is not
// a success when the project's own checks fail on the unchanged tree. The run ends FAILED with its worktree kept, the check recorded
// on the chain, and a message that says what happened in plain words.
func TestAway_a_run_that_did_nothing_and_whose_checks_fail_ends_failed(t *testing.T) {
	r, out := nothingRun(t, checksFail, false, nil)
	require.ErrorIs(t, r.Err, orchestrator.ErrRunNotSuccessful)
	assert.Equal(t, persistence.RunStatusFailed, r.Run.Status)
	assert.Empty(t, r.Run.GitCommitHash)
	checked := payload(t, r, "phase_checked")
	require.NotNil(t, checked, "the checks ran on the unchanged tree")
	assert.Equal(t, false, checked["passed"])
	assert.Contains(t, checked["failed"], "elsewhere.txt is missing")
	assert.Nil(t, payload(t, r, "awaiting_review"), "an empty change with nothing parked has nothing for a person to decide")
	_, err := os.Stat(payload(t, r, "run_bound")["worktree"].(string))
	assert.NoError(t, err, "the worktree is kept for inspection")

	assert.Contains(t, out, "Nothing to review, and your checks fail: no proposal was approved or parked (the agent made 0), and your checks fail on the unchanged tree (")
	assert.Contains(t, out, "elsewhere.txt is missing")
	assert.Contains(t, out, "Nothing was committed. Read what the agent did with `staircase inspect log 1`.")

	st, err := orchestrator.NewRunner(r.Store, r.WsDir).StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeFailed, st.Outcome, st.Why)
	assert.Contains(t, st.Why, "no proposal was approved or parked, and its checks fail on the unchanged tree (")
	assert.Contains(t, st.Why, "elsewhere.txt is missing")
	assert.Contains(t, st.Why, "nothing was delivered")
}

// TestAway_a_run_that_did_nothing_and_whose_checks_pass_is_a_success_with_no_change: the agent may have had nothing to do. The
// run stays SUCCESS, says so, and its outcome is "no change".
func TestAway_a_run_that_did_nothing_and_whose_checks_pass_is_a_success_with_no_change(t *testing.T) {
	r, out := nothingRun(t, checksPass, false, nil)
	require.NoError(t, r.Err)
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	checked := payload(t, r, "phase_checked")
	require.NotNil(t, checked)
	assert.Equal(t, true, checked["passed"])
	assert.Nil(t, payload(t, r, "awaiting_review"))
	assert.Contains(t, out, "Nothing to review: no proposal was approved or parked (the agent made 0), and your checks pass on the unchanged tree. If you expected a change, read what the agent said with `staircase inspect log 1`.")

	st, err := orchestrator.NewRunner(r.Store, r.WsDir).StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeNoChange, st.Outcome, st.Why)
	assert.Contains(t, st.Why, "no proposal was approved or parked, and its checks pass on the unchanged tree: there is nothing to deliver")
}

// TestAway_refused_proposals_are_counted_not_reported_as_none: a proposal the orchestrator refused (a path outside the repository) is
// neither approved nor parked, and the message counts it, so it never says the agent proposed nothing.
func TestAway_refused_proposals_are_counted_not_reported_as_none(t *testing.T) {
	r, out := nothingRun(t, checksPass, false, func(ctx context.Context, env *orchestrator.AgentEnv) {
		write(ctx, env, "../escape.txt", "x\n")
		write(ctx, env, "../escape2.txt", "x\n")
	})
	require.NoError(t, r.Err)
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	assert.Contains(t, out, "(the agent made 2)")
	assert.NotContains(t, out, "proposed nothing")
}

// TestAway_a_hosted_session_that_did_nothing_is_not_checked: a chat that touched no file is a conversation, not a failed task.
func TestAway_a_hosted_session_that_did_nothing_is_not_checked(t *testing.T) {
	r, out := nothingRun(t, checksFail, true, nil)
	require.NoError(t, r.Err)
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	assert.Nil(t, payload(t, r, "phase_checked"), "the checks did not run")
	assert.NotContains(t, out, "Nothing to review")
	st, err := orchestrator.NewRunner(r.Store, r.WsDir).StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeNoChange, st.Outcome, st.Why)
	assert.NotContains(t, st.Why, "checks pass", "it never claims a check that did not run")
}

// TestAway_a_resumed_run_whose_earlier_segment_approved_files_is_not_nothing: the second segment proposed nothing, but the run's
// change is what the first approved, so it is offered for review as always.
func TestAway_a_resumed_run_whose_earlier_segment_approved_files_is_not_nothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	opts := awayOpts()
	r := runtest.Run(t, runtest.Options{Ctx: ctx,
		Setup: func(s *persistence.Store, ws string, p int64) {
			require.NoError(t, crypto.GenerateSigningKey(ws))
			inSrc(&operator{approve: true})(s, ws, p)
		},
		Run: opts,
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			write(c, env, "src/one.txt", "fine\n")
			cancel()
			<-c.Done()
			return c.Err()
		})})
	require.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{}))
	run, err := r.Store.GetRun(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusAwaitingReview, run.Status)
	rv, err := orchestrator.NewRunner(r.Store, r.WsDir).ReviewOf(r.Run.ID)
	require.NoError(t, err)
	assert.Contains(t, rv.Files, "src/one.txt")
}

// TestAway_a_review_of_a_large_change_is_offered_whole (ADR 0014): the entries that hold the files of a change grow with the change.
// One that was cut at 64 KiB made history.Read refuse the run ("its payload is unreadable"), and a run nobody can read cannot be decided.
func TestAway_a_review_of_a_large_change_is_offered_whole(t *testing.T) {
	const files = 800 // about 100 bytes of path and digest each: the offered review and the decision on them are over 64 KiB
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{approve: true}),
		Run:   awayOpts(),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			var edits []domain.ProposedEdit
			for i := range files {
				edits = append(edits, domain.ProposedEdit{File: fmt.Sprintf("src/generated/pkg%04d/file.go", i),
					SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "package p\n"})
			}
			ap := env.Propose(ctx, domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit, ProposedEdits: edits})
			require.True(t, ap.Approved, ap.Feedback)
			return ap.Apply(env.Worktree)
		})})
	require.NoError(t, r.Err)
	require.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status)

	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	rv, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err, "the chain reads")
	assert.Len(t, rv.Files, files)
	st, err := runner.StatusOf(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, history.OutcomeAwaitingReview, st.Outcome, st.Why)
}

// TestRun_says_what_a_dirty_checkout_leaves_out (spec: real use): a worktree is made from a commit, so a checkout with uncommitted
// work starts a run that does not have it. The warning names the files, tracked changes apart from untracked files, and says ignored
// paths are not there either; the fact (the counts and a digest of the names) is on the chain, right after run_bound.
func TestRun_says_what_a_dirty_checkout_leaves_out(t *testing.T) {
	var r runtest.Result
	out := stdoutOf(t, func() {
		r = runtest.Run(t, runtest.Options{
			Base: map[string]runtest.File{".gitignore": {Content: "vendor/\n", Mode: 0o644}, "src/Module.php": {Content: "<?php // committed\n", Mode: 0o644}},
			Setup: func(s *persistence.Store, _ string, projectID int64) {
				p, err := s.GetProject(projectID)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(p.SourcePath, "src/Module.php"), []byte("<?php // edited\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(p.SourcePath, "src/New.php"), []byte("<?php\n"), 0o644))
				require.NoError(t, os.MkdirAll(filepath.Join(p.SourcePath, "vendor"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(p.SourcePath, "vendor/autoload.php"), []byte("<?php\n"), 0o644))
			},
			Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
	})
	require.NoError(t, r.Err)
	assert.Contains(t, out, "has uncommitted changes; the agent works on commit")
	assert.Contains(t, out, "tracked changes: src/Module.php")
	assert.Contains(t, out, "untracked files: src/New.php")
	assert.NotContains(t, out, "vendor/autoload.php", "an ignored path is not a change")
	assert.Contains(t, out, "ignored paths", "it says ignored paths are not in the worktree")

	types := r.Types()
	require.Greater(t, len(types), 1)
	assert.Equal(t, []string{"run_bound", "source_dirty"}, types[:2], "the fact follows the binding")
	fact := payload(t, r, "source_dirty")
	assert.EqualValues(t, 1, fact["tracked"])
	assert.EqualValues(t, 1, fact["untracked"])
	names := "M src/Module.php\n? src/New.php"
	assert.Equal(t, sha256Of(names), fact["names_sha256"])
	h, err := history.Read(r.Store, r.Run.ID)
	require.NoError(t, err, "the history reads with the new entry")
	require.NotNil(t, h.SourceDirty)
	assert.Equal(t, 1, h.SourceDirty.Tracked)
}

// TestRun_a_clean_checkout_records_and_prints_nothing_new: nothing to say, nothing on the chain.
func TestRun_a_clean_checkout_records_and_prints_nothing_new(t *testing.T) {
	var r runtest.Result
	out := stdoutOf(t, func() {
		r = runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
	})
	require.NoError(t, r.Err)
	assert.NotContains(t, out, "uncommitted")
	assert.NotContains(t, r.Types(), "source_dirty")
}

func sha256Of(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestRun_a_check_sees_the_declared_environment (spec: real use): a check that needs a directory the repository ignores (composer's
// vendor/) cannot pass in a checkout of the commit. With the directory declared as the run's environment the check passes, the record
// of the check and the certificate say it ran with it, and the user's checkout is not touched; without it, the same check fails.
func TestRun_a_check_sees_the_declared_environment(t *testing.T) {
	run := func(visible []string) (runtest.Result, string) {
		var r runtest.Result
		var source string
		out := stdoutOf(t, func() {
			r = runtest.Run(t, runtest.Options{
				Base: map[string]runtest.File{".gitignore": {Content: "vendor/\n", Mode: 0o644}, "src/a.php": {Content: "<?php\n", Mode: 0o644}},
				Setup: func(s *persistence.Store, wsDir string, projectID int64) {
					require.NoError(t, crypto.GenerateSigningKey(wsDir))
					p, err := s.GetProject(projectID)
					require.NoError(t, err)
					source = p.SourcePath
					require.NoError(t, os.MkdirAll(filepath.Join(source, "vendor", "bin"), 0o755))
					require.NoError(t, os.WriteFile(filepath.Join(source, "vendor", "autoload.php"), []byte("<?php\n"), 0o644))
				},
				Run: orchestrator.RunOptions{Terms: orchestrator.Terms{Checks: []string{"test -f vendor/autoload.php"}, Visible: visible, Sandbox: "off"}},
				Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
					ap := env.ProposeEdit(ctx, "coder", "add", domain.ProposedEdit{File: "src/b.php", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "<?php\n"})
					if ap.Approved {
						return ap.Apply(env.Worktree)
					}
					return nil
				})})
		})
		_ = out
		return r, source
	}
	checks := func(r runtest.Result) []map[string]any {
		var out []map[string]any
		for _, e := range r.Events {
			if e.EventType == "check_ran" {
				var m map[string]any
				require.NoError(t, json.Unmarshal([]byte(e.Payload), &m))
				out = append(out, m)
			}
		}
		return out
	}

	r, _ := run(nil)
	require.Len(t, checks(r), 1)
	assert.EqualValues(t, 1, checks(r)[0]["exit_code"], "without the environment the check cannot see vendor/")
	assert.Nil(t, checks(r)[0]["environment"])

	r, source := run([]string{"vendor"})
	require.NoError(t, r.Err)
	require.Len(t, checks(r), 1)
	assert.EqualValues(t, 0, checks(r)[0]["exit_code"], "with it the check passes")
	assert.Equal(t, []any{"vendor"}, checks(r)[0]["environment"], "the record says the check ran with it")
	assert.FileExists(t, filepath.Join(source, "vendor", "autoload.php"), "the user's checkout is untouched")
	assert.NoFileExists(t, filepath.Join(source, "src", "b.php"))

	raw, err := os.ReadFile(filepath.Join(r.WsDir, "audit", fmt.Sprintf("run-%d.certificate.json", r.Run.ID)))
	require.NoError(t, err)
	var env struct {
		Payload string `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(raw, &env))
	statement, err := base64.StdEncoding.DecodeString(env.Payload)
	require.NoError(t, err)
	assert.Contains(t, string(statement), `"environment":["vendor"]`, "the certificate says the checks ran with a declared environment")
}

// TestRun_a_declared_environment_is_judged_before_anything_is_made: a path that does not exist in the checkout, one the repository
// tracks, and one outside the repository refuse the run before it makes a record or a worktree.
func TestRun_a_declared_environment_is_judged_before_anything_is_made(t *testing.T) {
	for name, tc := range map[string]struct {
		visible []string
		why     string
	}{
		"it does not exist": {[]string{"vendor"}, "does not exist"},
		"it is tracked":     {[]string{"src"}, "is tracked"},
		"it is outside":     {[]string{"../elsewhere"}, "inside the repository"},
		"it is a pattern":   {[]string{"vendor/*"}, "not a pattern"},
		"it is git's own":   {[]string{".git"}, "git directory"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runtest.Run(t, runtest.Options{
				Base:  map[string]runtest.File{"src/a.php": {Content: "<?php\n", Mode: 0o644}},
				Run:   orchestrator.RunOptions{Terms: orchestrator.Terms{Checks: []string{"true"}, Visible: tc.visible, Sandbox: "off"}},
				Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
			require.Error(t, r.Err)
			assert.Contains(t, r.Err.Error(), tc.why)
			assert.Empty(t, r.Events, "nothing was written")
		})
	}
}

// TestRun_the_environment_reaches_a_docker_sandbox_read_only (simulated Docker): with a docker sandbox the declared directory is a
// read-only mount of the container the check runs in; the image is not pulled. A stand-in `docker` that logs its arguments stands for
// the real one, which is tested where an image exists.
func TestRun_the_environment_reaches_a_docker_sandbox_read_only(t *testing.T) {
	bin := t.TempDir()
	logFile := filepath.Join(bin, "docker.log")
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\necho \"$*\" >> "+logFile+"\n[ \"$1 $2\" = \"image inspect\" ] && echo sha256:fake\nexit 0\n"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var source string
	var r runtest.Result
	_ = stdoutOf(t, func() {
		r = runtest.Run(t, runtest.Options{
			Base: map[string]runtest.File{".gitignore": {Content: "vendor/\n", Mode: 0o644}, "src/a.php": {Content: "<?php\n", Mode: 0o644}},
			Setup: func(s *persistence.Store, wsDir string, projectID int64) {
				require.NoError(t, crypto.GenerateSigningKey(wsDir))
				p, err := s.GetProject(projectID)
				require.NoError(t, err)
				source = p.SourcePath
				require.NoError(t, os.MkdirAll(filepath.Join(source, "vendor"), 0o755))
			},
			Run: orchestrator.RunOptions{Terms: orchestrator.Terms{Checks: []string{"vendor/bin/phpunit"}, Visible: []string{"vendor"}, Sandbox: "docker:image=php:8.2"}},
			Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
				ap := env.ProposeEdit(ctx, "coder", "add", domain.ProposedEdit{File: "src/b.php", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "<?php\n"})
				if ap.Approved {
					return ap.Apply(env.Worktree)
				}
				return nil
			})})
	})
	require.NoError(t, r.Err)
	b, err := os.ReadFile(logFile)
	require.NoError(t, err)
	realSource, err := filepath.EvalSymlinks(source)
	require.NoError(t, err)
	assert.Regexp(t, regexp.MustCompile(regexp.QuoteMeta("-v "+filepath.Join(realSource, "vendor")+":")+`\S+/vendor:ro`), string(b), "the environment is a read-only mount")
	assert.Contains(t, string(b), "--pull never")
	var ran bool
	for _, e := range r.Events {
		if e.EventType == "check_ran" {
			ran = true
			assert.Contains(t, e.Payload, `"sandboxed":true`)
			assert.Contains(t, e.Payload, `"environment":["vendor"]`)
		}
	}
	assert.True(t, ran)
}
