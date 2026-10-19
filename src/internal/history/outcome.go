package history

import (
	"fmt"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// Outcome is how a run stands, as its history says (V11-C01 scope 6): one of six, for the owner who comes back to it. It is
// derived from the verified chain alone, never from the run's status column or its summary file, and it is shown, not stored.
type Outcome string

const (
	// OutcomeRunning: the chain does not say the run's last segment ended. Its process is working, or it died without being
	// able to say so (a crash records nothing); continuing or recovering it is how it goes on.
	OutcomeRunning Outcome = "running"
	// OutcomeAwaitingReview: an away run offered its change, checked on the exact tree, and the owner has not finished deciding.
	OutcomeAwaitingReview Outcome = "awaiting review"
	// OutcomeBlocked: the run stopped short and waits for the owner: what it proposed was parked and nothing was approved (a run of an older stAirCase; now such a run is offered as an empty change), drift
	// supervision halted it, it was stopped (cancelled, out of time or budget), or its commit was named but never delivered.
	OutcomeBlocked Outcome = "blocked or partial"
	// OutcomeFailed: the run ended without delivering, and nothing about it waits: its checks failed, its review was rejected,
	// or it failed.
	OutcomeFailed Outcome = "failed"
	// OutcomeDelivered: the run's commit is on its branch and its evidence is complete.
	OutcomeDelivered Outcome = "delivered"
	// OutcomeDeliveredIncomplete: the run's commit is on its branch, and some of its evidence could not be written.
	OutcomeDeliveredIncomplete Outcome = "delivered with incomplete evidence"
)

// Outcome is how the run stands and why, in the chain's own terms. A later event decides over an earlier one: a delivery over
// everything, then the review the last segment offered, then how the last segment ended.
func (h History) Outcome() (Outcome, string) {
	e := h.end
	if e.commit != "" {
		if len(e.evidence) > 0 {
			return OutcomeDeliveredIncomplete, fmt.Sprintf("commit %.12s is on the run branch, but its evidence is incomplete: %s", e.commit, strings.Join(e.evidence, "; "))
		}
		if e.recovered {
			return OutcomeDelivered, fmt.Sprintf("commit %.12s was recovered from the interrupted run, with its evidence", e.commit)
		}
		return OutcomeDelivered, fmt.Sprintf("commit %.12s is on the run branch, with its evidence", e.commit)
	}
	if rv := h.Review; rv != nil && rv.Index > e.segStart {
		switch rv.Decided {
		case "rejected":
			return OutcomeFailed, "you rejected its review: nothing was committed"
		case "accepted":
			if len(rv.Files) == 0 { // an empty change: accepting it delivers nothing, and there is nothing to finish
				return OutcomeFailed, fmt.Sprintf("you accepted a review of an empty change: nothing was delivered, and %d proposal(s) stay parked, not applied", len(h.Parked()))
			}
			return OutcomeAwaitingReview, "you accepted it and its delivery did not finish: accept it again (staircase decide --accept)"
		}
		if len(rv.Files) == 0 {
			return OutcomeAwaitingReview, fmt.Sprintf("it approved nothing, so its change is empty (tree %.12s, unchanged, passes its checks), with %d proposal(s) parked: decide it with staircase decide, or prototype and adopt a parked one", rv.Tree, len(h.Parked()))
		}
		return OutcomeAwaitingReview, fmt.Sprintf("its change passed its checks on tree %.12s, with %d proposal(s) parked: decide it with staircase decide", rv.Tree, len(h.Parked()))
	}
	if e.ended == nil {
		return OutcomeRunning, "the chain does not say it ended: it is running, or its process died"
	}
	if h.Sealed == "commit_prepared" {
		return OutcomeBlocked, fmt.Sprintf("its commit %.12s was named but not delivered: staircase recover finishes it", h.Delivered[0].Commit)
	}
	parked := len(h.Parked())
	checkFailed := e.checked > e.segStart && !e.check.Passed
	rejected := slices.ContainsFunc(h.Decisions, func(d Decision) bool {
		return d.ActionType == domain.ActionFinalReview && !d.Approved && d.Index > e.segStart
	})
	halted := "halted for drift: " + e.haltReason
	switch e.ended.Status {
	case domain.RunStatusFailed:
		switch {
		case checkFailed:
			return OutcomeFailed, "its checks failed on the approved tree: " + e.check.Failed
		case rejected:
			return OutcomeFailed, "its final review was rejected: nothing was committed"
		}
		return OutcomeFailed, "the run failed: nothing was delivered"
	case domain.RunStatusKilled:
		if h.Halted {
			return OutcomeBlocked, halted
		}
		return OutcomeBlocked, "it was stopped before it finished (cancelled, or over its time or budget): continue it with staircase resume, or recover what it approved with staircase recover"
	case domain.RunStatusSuccess:
		if parked > 0 {
			return OutcomeBlocked, fmt.Sprintf("%d proposal(s) parked for you, and nothing approved to review", parked)
		}
		return OutcomeFailed, "it ended without a change"
	case "": // a chain from before run_path said how the segment ended
		switch {
		case h.Halted:
			return OutcomeBlocked, halted
		case checkFailed:
			return OutcomeFailed, "its checks failed on the approved tree: " + e.check.Failed
		case rejected:
			return OutcomeFailed, "its final review was rejected: nothing was committed"
		case parked > 0:
			return OutcomeBlocked, fmt.Sprintf("%d proposal(s) parked for you", parked)
		}
		return OutcomeFailed, "it ended without a delivery, and its chain (from an older stAirCase) does not say how"
	}
	return OutcomeFailed, fmt.Sprintf("it ended %s without a delivery or a review on its chain", e.ended.Status)
}
