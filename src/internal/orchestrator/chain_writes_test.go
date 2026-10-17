package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What runs write for the one replay of the chain to read (ADR 0010, section 3).

// requestsNamed checks that every decision but a final review names the entry of a request of its kind audited before it, and
// that no request is named twice; it returns how many decisions it checked.
func requestsNamed(t *testing.T, r runtest.Result) int {
	t.Helper()
	events, err := r.Store.ListVerifiedEventLogs(r.Run.ID)
	require.NoError(t, err)
	requests, named, n := map[string]string{}, map[string]bool{}, 0
	for _, e := range events {
		var p struct {
			ActionType   string `json:"action_type"`
			RequestEntry string `json:"request_entry"`
		}
		require.NoError(t, json.Unmarshal([]byte(e.Payload), &p))
		switch {
		case e.EventType == "yield_request":
			requests[e.EventHash] = p.ActionType
		case e.EventType == "yield_decided" && p.ActionType != domain.ActionFinalReview:
			n++
			kind, ok := requests[p.RequestEntry]
			assert.True(t, ok, "decision at entry %d names no earlier request (%q)", e.ID, p.RequestEntry)
			assert.Equal(t, p.ActionType, kind)
			assert.False(t, named[p.RequestEntry], "a request is answered once")
			named[p.RequestEntry] = true
		}
	}
	return n
}

// TestDecisions_name_their_request (Q19): a decision names the chain entry of the request it answers, so a reader pairs them
// exactly, whatever order requests and decisions were audited in; an owner's adoption does too.
func TestDecisions_name_their_request(t *testing.T) {
	agentRuns := 0
	r := awayRun(t, &agentRuns)
	decided := requestsNamed(t, r)
	assert.Positive(t, decided)

	runner := orchestrator.NewRunner(r.Store, r.WsDir)
	rv, err := runner.ReviewOf(r.Run.ID)
	require.NoError(t, err)
	_, err = runner.Adopt(context.Background(), r.Run.ID, parkedNamed(t, rv, "elsewhere.txt").Seq, rv.Entry)
	require.NoError(t, err)
	assert.Equal(t, decided+1, requestsNamed(t, r), "the adoption is a decision of its own request")
}

// rewriteOptions changes the options a run saved when it started, keeping them readable.
func rewriteOptions(t *testing.T, r runtest.Result, change func(map[string]any)) {
	t.Helper()
	path := filepath.Join(r.WsDir, "journal", "run-1.options.json")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var o map[string]any
	require.NoError(t, json.Unmarshal(b, &o))
	change(o)
	b, err = json.Marshal(o)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

// TestTerms_saved_options_that_changed_are_refused (Q22): the run binds the digest of the options it saved; a continuation and a
// prototype refuse options that are not those, as they refuse a changed policy or plan.
func TestTerms_saved_options_that_changed_are_refused(t *testing.T) {
	t.Run("resume", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		rewriteOptions(t, r, func(o map[string]any) { o["allow_shell_exec"] = true })
		err := resume(t, r, orchestrator.ResumeOptions{})
		require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
		assert.ErrorContains(t, err, "not the ones the audit chain names")
	})
	t.Run("prototype", func(t *testing.T) {
		agentRuns := 0
		r := awayRun(t, &agentRuns)
		runner := orchestrator.NewRunner(r.Store, r.WsDir)
		rv, err := runner.ReviewOf(r.Run.ID)
		require.NoError(t, err)
		rewriteOptions(t, r, func(o map[string]any) { o["checks"] = []string{"true"} })
		_, err = runner.Prototype(context.Background(), r.Run.ID, parkedNamed(t, rv, "elsewhere.txt").Seq)
		assert.ErrorContains(t, err, "not the ones the audit chain names")
	})
}

// TestRecover_the_final_review_is_a_decision (Q21): recovery's final review is written as a live run's is, a final_review decision
// of the whole approved state, so the certificate counts it as it counts a finished run's.
func TestRecover_the_final_review_is_a_decision(t *testing.T) {
	r := interrupted(t, inSrc(&operator{approve: true}), orchestrator.RunOptions{Terms: orchestrator.Terms{ApproveInScope: true, Agreed: "dev@example.com"}})
	res, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Confirm: approveAll})
	require.NoError(t, err)
	require.True(t, res.FinalReview)
	var final map[string]any
	for _, e := range eventsOf(t, r, "yield_decided") {
		var d map[string]any
		require.NoError(t, json.Unmarshal([]byte(e.Payload), &d))
		if d["action_type"] == domain.ActionFinalReview {
			final = d
		}
	}
	require.NotNil(t, final, "the review is a decision on the chain")
	assert.Equal(t, true, final["approved"])
	assert.Equal(t, "operator", final["source"])
	assert.NotEmpty(t, final["files"], "the digest of what was put to the person")
	assert.Empty(t, eventsOf(t, r, "recovery_final_review"))
}
