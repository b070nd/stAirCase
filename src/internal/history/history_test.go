package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chain builds bare events (no hashes, nothing in a store): what the fold reads and nothing about verification, which Read
// leaves to the store (TestRead_*).
type chain struct {
	events []domain.RunEventLog
	sec    int
}

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func (c *chain) add(typ string, p any) *chain {
	b, _ := json.Marshal(p)
	c.sec++
	c.events = append(c.events, domain.RunEventLog{EventType: typ, Payload: string(b), EventHash: fmt.Sprintf("e%d", len(c.events)+1),
		Timestamp: t0.Add(time.Duration(c.sec) * time.Second)})
	return c
}

func bound() *chain {
	return (&chain{}).add("run_bound", map[string]any{"base_sha": "b", "branch": "refs/heads/staircase/run-1", "worktree": "w"})
}

func (c *chain) req(action string) *chain {
	return c.add("yield_request", map[string]any{"action_type": action, "agent_name": "coder"})
}

func (c *chain) edit(files ...string) *chain {
	var edits []map[string]string
	for _, f := range files {
		edits = append(edits, map[string]string{"file": f, "replace_block": "x"})
	}
	return c.add("yield_request", map[string]any{"action_type": domain.ActionFileEdit, "proposed_edits": edits})
}

func (c *chain) decided(seq int, source string, approved bool, extra ...any) *chain {
	p := map[string]any{"seq": seq, "source": source, "approved": approved, "action_type": domain.ActionFileEdit, "request_sha256": fmt.Sprintf("h%d", seq)}
	for i := 0; i+1 < len(extra); i += 2 {
		p[extra[i].(string)] = extra[i+1]
	}
	return c.add("yield_decided", p)
}

func (c *chain) final(seq int, approved bool, files map[string]string) *chain {
	return c.add("yield_decided", map[string]any{"seq": seq, "source": "operator", "approved": approved, "action_type": domain.ActionFinalReview, "files": files})
}

func (c *chain) replay(t *testing.T) History {
	t.Helper()
	h, err := replay(c.events)
	require.NoError(t, err)
	return h
}

// TestReplay_refuses_what_no_run_can_have_written: every grammar rule of ADR 0010, section 2, with the entry it names.
func TestReplay_refuses_what_no_run_can_have_written(t *testing.T) {
	for name, tc := range map[string]struct {
		c     *chain
		entry int
		why   string
	}{
		"a decision before the run began": {(&chain{}).req("file_edit").add("run_bound", nil), 1, "begins with"},
		"two bindings":                    {bound().add("run_bound", nil), 2, "more than once"},
		"proposal numbers that repeat":    {bound().req("file_edit").req("file_edit").decided(2, "policy", true).decided(2, "policy", true), 5, "do not increase"},
		"numbers going backwards":         {bound().req("file_edit").req("file_edit").decided(3, "policy", true).decided(1, "policy", true), 5, "do not increase"},
		"a decision with no request":      {bound().decided(1, "policy", true), 2, "no request"},
		"more decisions than requests":    {bound().req("file_edit").decided(1, "policy", true).decided(2, "policy", true), 4, "no request"},
		"a request of another kind":       {bound().req("shell_exec").decided(1, "policy", true), 3, "no request"},
		"a request of an earlier segment (Q16)": {bound().req("file_edit").add("run_resumed", map[string]any{"segment": 2}).decided(1, "policy", true),
			4, "no request"},
		"a named request that is not pending (Q19)": {bound().req("file_edit").decided(1, "policy", true, "request_entry", "e9"), 3, "no request"},
		"a named request of another kind":           {bound().req("shell_exec").decided(1, "policy", true, "request_entry", "e2"), 3, "no request"},
		"a decision after commit_prepared (Q17)":    {bound().req("file_edit").add("commit_prepared", nil).decided(1, "policy", true), 4, "follows commit_prepared"},
		"a request after commit_prepared":           {bound().add("commit_prepared", nil).req("file_edit"), 3, "follows commit_prepared"},
		"a resume after commit_prepared":            {bound().add("commit_prepared", nil).add("run_resumed", nil), 3, "resumed after commit_prepared"},
		"a decision after the certificate":          {bound().req("file_edit").add("certificate_issued", nil).decided(1, "policy", true), 4, "follows certificate_issued"},
		"a decision after a recovery":               {bound().req("file_edit").add("run_recovered", nil).decided(1, "policy", true), 4, "follows run_recovered"},
		"a decision after an away delivery":         {bound().req("file_edit").add("review_delivered", nil).decided(1, "policy", true), 4, "follows review_delivered"},
		"a final review after the certificate":      {bound().add("certificate_issued", nil).final(1, true, nil), 3, "follows certificate_issued"},
		"a resume after a recovery":                 {bound().add("run_recovered", nil).add("run_resumed", nil), 3, "resumed after run_recovered"},
		"an outcome with no launch":                 {bound().launchEnded(1, "g1", LaunchExited), 2, "names no open launch"},
		"an outcome of another launch":              {bound().claim(1, "g1").launchEnded(1, "g0", LaunchExited), 3, "names no open launch"},
		"two outcomes of one launch":                {bound().claim(1, "g1").launchEnded(1, "g1", LaunchExited).launchEnded(1, "g1", LaunchExited), 4, "names no open launch"},
		"an outcome nobody defined":                 {bound().claim(1, "g1").launchEnded(1, "g1", "fine"), 3, "unknown outcome"},
		"a launch while one is open":                {bound().claim(1, "g1").claim(2, "g2"), 3, "launch 1 has no outcome"},
		"a launch that skips an attempt":            {bound().claim(2, "g2"), 2, "attempt 2"},
		"a resume while a launch is open":           {bound().claim(1, "g1").add("run_resumed", map[string]any{"segment": 2}), 3, "launch 1 has no outcome"},
		"a launch after commit_prepared":            {bound().add("commit_prepared", nil).claim(1, "g1"), 3, "follows commit_prepared"},
		"an outcome after the certificate":          {bound().claim(1, "g1").add("certificate_issued", nil).launchEnded(1, "g1", LaunchExited), 4, "follows certificate_issued"},
		"an unreadable decision":                    {&chain{events: append(bound().events, domain.RunEventLog{EventType: "yield_decided", Payload: "not json"})}, 2, "unreadable"},
		"an unreadable binding":                     {&chain{events: []domain.RunEventLog{{EventType: "run_bound", Payload: "{"}}}, 1, "unreadable"},
		"an unreadable offered review":              {&chain{events: append(bound().events, domain.RunEventLog{EventType: "awaiting_review", Payload: "["})}, 2, "unreadable"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := replay(tc.c.events)
			var g *GrammarError
			require.True(t, errors.As(err, &g), "%v", err)
			assert.Equal(t, tc.entry, g.Index, g.Error())
			assert.Contains(t, g.Error(), tc.why)
			assert.Contains(t, g.Error(), fmt.Sprintf("entry %d (%s)", g.Index, g.Event), "one message shape for every caller")
		})
	}
}

// TestReplay_accepts_what_runs_write: requests audited ahead of their decisions, a final review with no request, a final review
// after commit_prepared (recovery asks it of a run that died before its branch moved), events and fields it does not know.
func TestReplay_accepts_what_runs_write(t *testing.T) {
	for name, c := range map[string]*chain{
		"requests ahead of their decisions": bound().req("file_edit").req("file_edit").decided(1, "policy", true).decided(2, "operator", false),
		"a final review needs no request":   bound().req("file_edit").decided(1, "policy", true).final(2, true, nil),
		"recovery's final review after commit_prepared": bound().req("file_edit").decided(1, "task", true).add("commit_prepared", nil).
			final(2, true, nil).add("run_recovered", nil),
		"unknown events and fields":     bound().add("something_new", map[string]any{"x": 1}).add("yield_request", map[string]any{"action_type": "file_edit", "new_key": true}).decided(1, "policy", true),
		"an unreadable unknown event":   &chain{events: append(bound().events, domain.RunEventLog{EventType: "from_the_future", Payload: "?"})},
		"a truncated request":           bound().add("yield_request", map[string]any{"truncated": true, "action_type": "file_edit", "bytes": 9, "sha256": "s"}).decided(1, "policy", true),
		"an adoption after the review":  bound().edit("a").decided(1, "parked", false).add("awaiting_review", nil).edit("a").decided(2, "operator", true, "adopts", 1),
		"the old recovery final review": bound().req("file_edit").decided(1, "task", true).add("recovery_final_review", map[string]any{"approved": true}).add("run_recovered", nil),
		"a lost launch's outcome after commit_prepared, before the recovery": bound().claim(1, "g1").add("commit_prepared", nil).
			launchEnded(1, "g1", LaunchUnknown).add("run_recovered", nil),
		"a launch per segment": bound().claim(1, "g1").launchEnded(1, "g1", LaunchUnknown).add("run_resumed", map[string]any{"segment": 2, "ack_unknown": 1}).
			claim(2, "g2").launchEnded(2, "g2", LaunchExited),
	} {
		t.Run(name, func(t *testing.T) { c.replay(t) })
	}
}

// TestReplay_pairs_each_decision_with_its_request (Q7, Q19): a decision that names its request's entry uses up exactly that one,
// whatever the order; an older chain's decision uses up the oldest request of its kind.
func TestReplay_pairs_each_decision_with_its_request(t *testing.T) {
	// e2 is A's request, e3 is B's; B is decided first
	h := bound().edit("a").edit("b").decided(1, "parked", false, "request_entry", "e3").decided(2, "parked", false, "request_entry", "e2").replay(t)
	require.Len(t, h.Decisions, 2)
	assert.Equal(t, "b", h.Decisions[0].Request.ProposedEdits[0].File)
	assert.Equal(t, "a", h.Decisions[1].Request.ProposedEdits[0].File)
	assert.Equal(t, 0, h.Undecided)

	h = bound().edit("a").req("shell_exec").edit("b").decided(1, "policy", true).replay(t)
	assert.Equal(t, "a", h.Decisions[0].Request.ProposedEdits[0].File, "without request_entry: the oldest request of its kind")
	assert.Equal(t, 2, h.Undecided)

	h = bound().req("file_edit").decided(1, "policy", true).final(2, true, nil).replay(t)
	assert.Nil(t, h.Decisions[1].Request, "a final review answers no request")
}

// TestReplay_reads_the_run: the binding, policy, initiator, session, usage, shells, entries and the chain head.
func TestReplay_reads_the_run(t *testing.T) {
	c := (&chain{}).add("run_bound", map[string]any{"base_sha": "b", "branch": "x", "worktree": "w", "plan_digest": "p", "blueprint_hash": "bp",
		"harness": "claude-code", "topology_version": 3, "terms_sha256": "t", "away_commands": map[string]any{"commands": []string{"make"}, "sha256": "d"}}).
		add("initiator_signed", map[string]any{"principal": "dev@example.com", "signature": "c2ln"}).
		add("policy_snapshot", map[string]any{"digest": "abc", "signed": true}).
		add("agent_session", map[string]any{"agent": "claude-code", "id": "s1", "mode": "started"}).
		add("agent_session", map[string]any{"agent": "claude-code"}). // names no session: the last one named stands
		add("state_emit", map[string]any{"active_agent": "coder", "state": map[string]any{"model": "m", "input_tokens": 10, "output_tokens": 4}}).
		add("shell_ran", map[string]any{"sandboxed": true}).add("shell_ran", map[string]any{"sandboxed": false})
	h := c.replay(t)
	assert.Equal(t, Bound{BaseSHA: "b", Branch: "x", Worktree: "w", PlanDigest: "p", BlueprintHash: "bp", Harness: "claude-code", TopologyVersion: 3,
		TermsSHA256: "t", AwayCommands: &DeclaredCommands{Commands: []string{"make"}, SHA256: "d"}}, h.Bound)
	assert.Equal(t, &InitiatorSigned{"dev@example.com", "c2ln"}, h.Initiator)
	assert.Equal(t, &PolicySnapshot{"abc", true}, h.Policy)
	assert.Equal(t, "s1", h.Session.ID)
	require.Len(t, h.Usage, 1)
	assert.Equal(t, 10, h.Usage[0].State.In)
	assert.Equal(t, ShellCount{Ran: 2, Sandboxed: 1}, h.Shells)
	assert.Equal(t, "e8", h.Head)
	assert.True(t, h.Holds("e3"))
	assert.False(t, h.Holds("e9"))
	assert.False(t, h.Holds(""))

	h = bound().add("initiator_signed", map[string]any{"principal": "old@example.com"}).replay(t)
	assert.Nil(t, h.Initiator, "a record from before the signature was kept names nobody")
	assert.Nil(t, h.Policy, "no policy_snapshot")
}

// TestReplay_approvals_and_what_recovery_reads: the approved file changes in order, the last decided number, the expired
// authorizations, and which final review counts (Q18).
func TestReplay_approvals_and_what_recovery_reads(t *testing.T) {
	files := map[string]string{"a": "1"}
	h := bound().req("file_edit").req("shell_exec").req("file_edit").req("file_edit").
		decided(1, "policy", true, "files", files).
		decided(2, "operator", true, "action_type", "shell_exec").
		decided(3, "operator", false, "unconsumed", true).
		decided(4, "task", true).replay(t)
	var seqs []int
	for _, a := range h.Approvals() {
		seqs = append(seqs, a.Seq)
	}
	assert.Equal(t, []int{1, 4}, seqs, "approved file changes only")
	assert.Equal(t, files, h.Approvals()[0].Files)
	assert.Equal(t, 4, h.LastDecided)
	assert.Equal(t, map[int]string{3: "h3"}, h.Unconsumed())

	other := map[string]string{"a": "2"}
	for name, tc := range map[string]struct {
		c        *chain
		reviewed bool
		err      bool
	}{
		"none":                        {bound().req("file_edit").decided(1, "task", true), false, false},
		"of exactly this state":       {bound().req("file_edit").decided(1, "task", true).final(2, true, files), true, false},
		"rejected":                    {bound().req("file_edit").decided(1, "task", true).final(2, false, files), false, false},
		"of other bytes":              {bound().req("file_edit").decided(1, "task", true).final(2, true, other), false, true},
		"before a later approval":     {bound().req("file_edit").decided(1, "task", true).final(2, true, other).req("file_edit").decided(3, "task", true), false, false},
		"again after a later one too": {bound().req("file_edit").decided(1, "task", true).final(2, true, other).req("file_edit").decided(3, "task", true).final(4, true, files), true, false},
	} {
		t.Run(name, func(t *testing.T) {
			ok, err := tc.c.replay(t).FinalReviewed(files)
			assert.Equal(t, tc.reviewed, ok)
			assert.Equal(t, tc.err, err != nil, "%v", err)
		})
	}
}

// TestReplay_what_a_continuation_reads: counters, the supervisor's decisions (Q20), the validator's state, segments, elapsed
// time, undecided requests, and the drift halt with its acknowledgement.
func TestReplay_what_a_continuation_reads(t *testing.T) {
	c := bound().
		req("file_edit").decided(1, "policy", true, "paths", []string{"a"}).
		req("file_edit").decided(2, "orchestrator", false). // refused before the supervisor saw it
		req("file_edit").decided(3, "task", true, "paths", []string{"b"}, "validator_state", map[string]any{"approvals": 2}).
		req("file_edit").decided(4, "orchestrator", false, "paths", []string{"c"}). // an approval that expired: it was checked
		req("shell_exec").decided(5, "declared", true, "action_type", "shell_exec").
		req("file_edit") // nobody decided it: the segment ended
	c.sec += 100 // the second segment began long after
	c.add("run_resumed", map[string]any{"segment": 2}).
		req("file_edit").decided(6, "operator", false, "paths", []string{"d"}).
		final(7, true, nil).
		add("drift_halt", nil)
	h := c.replay(t)
	assert.Equal(t, 1, h.AutoApproved())
	assert.True(t, h.TaskApproved())
	assert.JSONEq(t, `{"approvals":2}`, string(h.ValidatorState()))
	var seqs []int
	for _, d := range h.Supervised() {
		seqs = append(seqs, d.Seq)
	}
	assert.Equal(t, []int{1, 3, 4, 6}, seqs, "not the refusal, the declared command or the final review")
	assert.Equal(t, 2, h.Segment)
	// segment 1 from entry 1 to the request at second 12; segment 2 from second 113 to the halt at second 117
	assert.Equal(t, 11*time.Second+4*time.Second, h.Elapsed)
	assert.Equal(t, 0, h.Undecided, "the request left by segment 1 is not this segment's")
	assert.True(t, h.Halted)

	h = c.add("run_resumed", map[string]any{"segment": 3, "ack_drift": true}).replay(t)
	assert.False(t, h.Halted, "an acknowledged resume clears the halt (Q20)")
	h = c.add("drift_halt", nil).replay(t)
	assert.True(t, h.Halted)

	h = bound().req("file_edit").req("file_edit").replay(t)
	assert.Equal(t, 2, h.Undecided)
	assert.Nil(t, h.ValidatorState())
	assert.False(t, h.TaskApproved())
}

// TestReplay_what_review_and_adoption_read: the offered review with its checks and the owner's decision, the parked proposals,
// and adoption read from the approved decision that names it (Q19).
func TestReplay_what_review_and_adoption_read(t *testing.T) {
	c := bound().
		edit("a").decided(1, "parked", false, "paths", []string{"a"}, "parked_why", "outside the scope").
		edit("b").decided(2, "parked", false).
		edit("c").decided(3, "evidence", true).
		add("phase_checked", map[string]any{"tree": "t1", "passed": true, "checks": []map[string]any{{"command": "go test", "exit_code": 0, "sandboxed": true}}}).
		add("awaiting_review", map[string]any{"tree": "t1", "files": map[string]string{"c": "1"}, "phase_checked": "e8", "parked": 2})
	h := c.replay(t)
	require.NotNil(t, h.Review)
	assert.Equal(t, "e9", h.Review.Entry)
	assert.Equal(t, "t1", h.Review.Tree)
	require.NotNil(t, h.Review.Checked)
	assert.True(t, h.Review.Checked.Passed)
	assert.Equal(t, "go test", h.Review.Checked.Checks[0].Command)
	assert.Equal(t, "", h.Review.Decided)
	require.Len(t, h.Parked(), 2)
	assert.Equal(t, "outside the scope", h.Parked()[0].ParkedWhy)
	d, ok := h.Decision(1)
	require.True(t, ok)
	assert.Equal(t, "a", d.Request.ProposedEdits[0].File)
	_, ok = h.Decision(9)
	assert.False(t, ok)

	// the owner adopts proposal 1: a request, an approved decision naming it, and a new review
	c.edit("a").decided(4, "operator", true, "adopts", 1)
	h = c.replay(t)
	assert.Equal(t, map[int]int{1: 11}, h.Adopted())
	assert.Len(t, h.Parked(), 1, "an adopted proposal is part of the change now")
	assert.False(t, h.ReviewedSinceAdoption(1), "no review was offered since")
	c.add("phase_checked", map[string]any{"tree": "t2", "passed": true}).add("awaiting_review", map[string]any{"tree": "t2", "phase_checked": "e12"})
	h = c.replay(t)
	assert.True(t, h.ReviewedSinceAdoption(1))
	assert.Equal(t, "t2", h.Review.Checked.Tree, "the checks the latest review names")

	h = c.add("review_accepted", map[string]any{"awaiting_review": "e13"}).replay(t)
	assert.Equal(t, "accepted", h.Review.Decided)

	// a rejected decision that names a parked proposal adopts nothing
	h = bound().edit("a").decided(1, "parked", false).edit("a").decided(2, "operator", false, "adopts", 1).replay(t)
	assert.Empty(t, h.Adopted())
	assert.Len(t, h.Parked(), 1)

	h = bound().add("awaiting_review", map[string]any{"tree": "t", "phase_checked": "nowhere"}).replay(t)
	assert.Nil(t, h.Review.Checked, "a review that names no checks on the chain has none")
}

// TestReplay_delivery: the commit-naming events, and the first that sealed the chain.
func TestReplay_delivery(t *testing.T) {
	h := bound().add("commit_prepared", map[string]any{"commit": "c1", "base": "b", "tree": "t"}).
		add("certificate_issued", map[string]any{"commit": "c1", "cal": 3, "envelope_sha256": "env"}).replay(t)
	assert.Equal(t, "commit_prepared", h.Sealed)
	assert.True(t, h.Delivers("certificate_issued", "c1"))
	assert.False(t, h.Delivers("run_recovered", "c1"))
	assert.Equal(t, "env", h.Delivered[1].EnvelopeSHA256)
	assert.Equal(t, "", bound().replay(t).Sealed)
}

func (c *chain) claim(attempt int, generation string) *chain {
	return c.add("launch_claimed", map[string]any{"attempt": attempt, "generation": generation, "segment": 1})
}

func (c *chain) launchEnded(attempt int, generation, outcome string) *chain {
	return c.add("launch_ended", map[string]any{"attempt": attempt, "generation": generation, "outcome": outcome})
}

func (c *chain) launchClassed(attempt int, generation, outcome, class string) *chain {
	return c.add("launch_ended", map[string]any{"attempt": attempt, "generation": generation, "outcome": outcome, "class": class})
}

// TestReplay_what_a_restart_reads_of_the_launches: every launch with its outcome, the one still open, the ones whose end was never
// established, and whether the last of those parks the run until a resume acknowledges it. A lost launch's outcome, written by the
// restart, adds nothing to the dead segment's active time.
func TestReplay_what_a_restart_reads_of_the_launches(t *testing.T) {
	h := bound().claim(1, "g1").launchEnded(1, "g1", LaunchExited).replay(t)
	require.Len(t, h.Launches, 1)
	assert.Equal(t, LaunchClaimed{Attempt: 1, Generation: "g1", Segment: 1}, h.Launches[0].LaunchClaimed)
	assert.Equal(t, LaunchExited, h.Launches[0].Outcome)
	assert.Nil(t, h.OpenLaunch())
	assert.Equal(t, 0, h.Lost())
	assert.Equal(t, 0, h.LaunchParked())

	c := bound().claim(1, "g1")
	h = c.replay(t)
	require.NotNil(t, h.OpenLaunch(), "a claim with no outcome: the process that launched it was lost")
	assert.Equal(t, "g1", h.OpenLaunch().Generation)
	assert.Equal(t, 1, h.Lost(), "an open claim counts as lost")
	assert.Equal(t, 1, h.LaunchParked())
	before := h.Elapsed

	c.sec += 3600 // the restart came an hour later
	h = c.launchEnded(1, "g1", LaunchUnknown).replay(t)
	assert.Nil(t, h.OpenLaunch())
	assert.Equal(t, 1, h.Lost())
	assert.Equal(t, 1, h.LaunchParked(), "still parked: nobody acknowledged it")
	assert.Equal(t, before, h.Elapsed, "the time the run was down is not time it used")

	c.add("run_resumed", map[string]any{"segment": 2, "ack_unknown": 1})
	h = c.replay(t)
	assert.Equal(t, 0, h.LaunchParked(), "a resume acknowledged it")
	h = c.claim(2, "g2").launchEnded(2, "g2", LaunchUnresponsive).replay(t)
	assert.Equal(t, 2, h.Lost(), "an agent that did not stop is not an established end either")
	assert.Equal(t, 2, h.LaunchParked())

	h = bound().claim(1, "g1").launchEnded(1, "g1", LaunchStopped).add("run_resumed", map[string]any{"segment": 2}).claim(2, "g2").launchEnded(2, "g2", LaunchFailed).replay(t)
	assert.Equal(t, 0, h.Lost(), "a known failure is not a lost launch")
	assert.Equal(t, 0, h.LaunchParked())
}

// TestLaunchClass_names_a_lifecycle_failure_apart_from_the_checks (V11-C01 scope 8): a launch that did not end well has a
// class, on its launch_ended or derived from its outcome for a chain written before classes; a launch that ended by itself or
// was stopped by the run has none. The class never touches the run's checks or its acceptance.
func TestLaunchClass_names_a_lifecycle_failure_apart_from_the_checks(t *testing.T) {
	for name, tc := range map[string]struct {
		c    *chain
		want string
	}{
		"exited":                      {bound().claim(1, "g").launchEnded(1, "g", LaunchExited), ""},
		"stopped":                     {bound().claim(1, "g").launchEnded(1, "g", LaunchStopped), ""},
		"failed, an older chain":      {bound().claim(1, "g").launchEnded(1, "g", LaunchFailed), ClassHostExit},
		"failed, rejected":            {bound().claim(1, "g").launchClassed(1, "g", LaunchFailed, ClassRejected), ClassRejected},
		"failed, storage trouble":     {bound().claim(1, "g").launchClassed(1, "g", LaunchFailed, ClassStorage), ClassStorage},
		"exited, storage trouble":     {bound().claim(1, "g").launchClassed(1, "g", LaunchExited, ClassStorage), ClassStorage},
		"unresponsive":                {bound().claim(1, "g").launchEnded(1, "g", LaunchUnresponsive), ClassCleanup},
		"lost, an older chain":        {bound().claim(1, "g").launchEnded(1, "g", LaunchUnknown), ClassUncertainDispatch},
		"lost, classed":               {bound().claim(1, "g").launchClassed(1, "g", LaunchUnknown, ClassUncertainDispatch), ClassUncertainDispatch},
		"a claim with no outcome yet": {bound().claim(1, "g"), ""},
	} {
		t.Run(name, func(t *testing.T) {
			h := tc.c.replay(t)
			require.Len(t, h.Launches, 1)
			assert.Equal(t, tc.want, h.Launches[0].Class)
		})
	}
	h := bound().claim(1, "g1").launchEnded(1, "g1", LaunchExited).add("run_resumed", map[string]any{"segment": 2}).
		claim(2, "g2").launchClassed(2, "g2", LaunchFailed, ClassHostExit).replay(t)
	require.Len(t, h.Failures(), 1)
	assert.Equal(t, 2, h.Failures()[0].Attempt)
}

// TestLaunchClass_refuses_a_class_the_outcome_cannot_have: a chain that names a class its outcome does not fit, or one nobody
// defined, is not a chain this version reads.
func TestLaunchClass_refuses_a_class_the_outcome_cannot_have(t *testing.T) {
	for name, tc := range map[string]*chain{
		"a class nobody defined":            bound().claim(1, "g").launchClassed(1, "g", LaunchFailed, "oops"),
		"a rejection that exited":           bound().claim(1, "g").launchClassed(1, "g", LaunchExited, ClassRejected),
		"a host exit that was stopped":      bound().claim(1, "g").launchClassed(1, "g", LaunchStopped, ClassHostExit),
		"an uncertain dispatch that failed": bound().claim(1, "g").launchClassed(1, "g", LaunchFailed, ClassUncertainDispatch),
		"a cleanup failure that exited":     bound().claim(1, "g").launchClassed(1, "g", LaunchExited, ClassCleanup),
		"storage trouble that was lost":     bound().claim(1, "g").launchClassed(1, "g", LaunchUnknown, ClassStorage),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := replay(tc.events)
			var g *GrammarError
			require.True(t, errors.As(err, &g), "%v", err)
			assert.Equal(t, 3, g.Index)
			assert.Contains(t, g.Error(), "class")
		})
	}
}
