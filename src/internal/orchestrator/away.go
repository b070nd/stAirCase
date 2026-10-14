package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

// Away mode (ADR 0006): a run nobody watches ends, when its approved tree passes the checks, awaiting the owner's review.

// awaitReview is the acceptance of an away run when its agent stopped: the checks run once more, on the exact approved tree
// (the agent stopping is not acceptance), and their result is recorded as phase_checked. A tree that passes is offered for
// review (awaiting_review, naming that phase_checked entry); one that does not ends the run FAILED. Nothing is committed.
func (r *Runner) awaitReview(ctx context.Context, runID int64, appr *approvals, gate *evidenceGate, parked int) (string, error) {
	tree, err := appr.tree()
	if err != nil {
		return persistence.RunStatusFailed, fmt.Errorf("the approved tree: %w", err)
	}
	results, failed := gate.runChecks(ctx, nil)
	passed := failed == "" && len(results) > 0
	if err := r.audit(runID, "phase_checked", map[string]any{"tree": tree, "checks": checkEvidence(results), "passed": passed, "failed": failed}); err != nil {
		return persistence.RunStatusFailed, fmt.Errorf("audit phase_checked: %w", err)
	}
	if !passed {
		fmt.Fprintf(os.Stdout, "   ❌ The approved change does not pass its checks now: %s\n", failed)
		return persistence.RunStatusFailed, nil
	}
	checked, err := r.store.GetLastEventHash(runID)
	if err != nil {
		return persistence.RunStatusFailed, err
	}
	if err := r.audit(runID, "awaiting_review", map[string]any{"tree": tree, "files": digest(appr.files), "phase_checked": checked, "parked": parked}); err != nil {
		return persistence.RunStatusFailed, fmt.Errorf("audit awaiting_review: %w", err)
	}
	fmt.Fprintf(os.Stdout, "   ⏸  Awaiting your review: %d file(s) approved on evidence, %d proposal(s) parked. Decide with: staircase decide %d\n",
		len(appr.files), parked, runID)
	return persistence.RunStatusAwaitingReview, nil
}

// acceptedReview is what Decide hands to recovery: the tree the owner accepted and the checks that ran on it.
type acceptedReview struct {
	tree   string
	checks []certificate.Check
}

// DecideOptions are for [Runner.Decide].
type DecideOptions struct {
	Accept   bool   // accept (Y): deliver the change; otherwise reject it (N)
	Shown    string // the hash of the awaiting_review entry the owner was shown; empty: the run's current one
	Feedback string
	// RequireEvidence: as for recover, a commit whose evidence is incomplete ends with ErrEvidenceIncomplete.
	RequireEvidence bool
}

// ErrNotAwaiting: the run is not waiting for a review (it never was, or it was decided).
var ErrNotAwaiting = errors.New("the run is not awaiting review")

// ParkedProposal is a proposal an away run kept for the owner instead of asking: what it would have changed and why it needed a person.
type ParkedProposal struct {
	Action string
	Files  []string
	Why    string
}

// Review is what an away run offers for review, read from its verified audit chain.
type Review struct {
	RunID   int64
	Entry   string // the hash of its awaiting_review entry: a decision names it
	Base    string // the commit the run started from
	Tree    string
	Files   map[string]string // path -> SHA-256 of the approved content
	Parked  int
	Checks  []certificate.Check
	Decided string // "", "accepted" (delivery not finished) or "rejected"

	ParkedProposals []ParkedProposal
}

// ReviewOf reads the review an away run offers. It trusts the chain only after verifying it, and the checks only when they ran
// on the offered tree and passed.
func (r *Runner) ReviewOf(runID int64) (Review, error) {
	rv := Review{RunID: runID}
	run, err := r.store.GetRun(runID)
	if err != nil || run == nil {
		return rv, fmt.Errorf("run #%d not found", runID)
	}
	if run.Status != persistence.RunStatusAwaitingReview {
		return rv, fmt.Errorf("run #%d is %s: %w", runID, run.Status, ErrNotAwaiting)
	}
	events, err := r.store.ListVerifiedEventLogs(runID)
	if err != nil {
		return rv, fmt.Errorf("the audit chain of run #%d does not verify, so nothing in it can be trusted: %w", runID, err)
	}
	hist, err := auditedHistory(events)
	if err != nil {
		return rv, fmt.Errorf("the audit history of run #%d is not one a run can have written: %w", runID, err)
	}
	rv.Base = hist.Bound.Base
	at := -1
	for i, e := range events {
		switch e.EventType {
		case "awaiting_review":
			at = i
		case "yield_decided":
			var d struct {
				Source string   `json:"source"`
				Action string   `json:"action_type"`
				Paths  []string `json:"paths"`
				Why    string   `json:"parked_why"`
			}
			if json.Unmarshal([]byte(e.Payload), &d) == nil && d.Source == "parked" {
				rv.ParkedProposals = append(rv.ParkedProposals, ParkedProposal{Action: d.Action, Files: d.Paths, Why: d.Why})
			}
		}
	}
	if at < 0 {
		return rv, fmt.Errorf("run #%d has no awaiting_review entry on its chain", runID)
	}
	var aw struct {
		Tree         string            `json:"tree"`
		Files        map[string]string `json:"files"`
		PhaseChecked string            `json:"phase_checked"`
		Parked       int               `json:"parked"`
	}
	if err := json.Unmarshal([]byte(events[at].Payload), &aw); err != nil {
		return rv, fmt.Errorf("the awaiting_review entry of run #%d: %w", runID, err)
	}
	rv.Entry, rv.Tree, rv.Files, rv.Parked = events[at].EventHash, aw.Tree, aw.Files, aw.Parked
	checked := false
	for _, e := range events[:at] {
		if e.EventType != "phase_checked" || e.EventHash != aw.PhaseChecked {
			continue
		}
		var pc struct {
			Tree   string `json:"tree"`
			Passed bool   `json:"passed"`
			Checks []struct {
				Command      string `json:"command"`
				ExitCode     int    `json:"exit_code"`
				Sandboxed    bool   `json:"sandboxed"`
				OutputSHA256 string `json:"output_sha256"`
			} `json:"checks"`
		}
		if err := json.Unmarshal([]byte(e.Payload), &pc); err != nil || !pc.Passed || pc.Tree != aw.Tree {
			return rv, fmt.Errorf("the checks run #%d names did not pass on the tree it offers", runID)
		}
		for _, c := range pc.Checks {
			rv.Checks = append(rv.Checks, certificate.Check{Command: c.Command, ExitCode: c.ExitCode, Sandboxed: c.Sandboxed, OutputSHA256: c.OutputSHA256})
		}
		checked = true
	}
	if !checked {
		return rv, fmt.Errorf("run #%d offers a tree without the checks that ran on it", runID)
	}
	for _, e := range events[at+1:] {
		switch e.EventType {
		case "review_accepted":
			rv.Decided = "accepted"
		case "review_rejected":
			rv.Decided = "rejected"
		}
	}
	return rv, nil
}

// Decide is the owner's review of an away run (ADR 0006): accepting delivers exactly the change that was offered, through the
// recovery path (the files derived again from the chain, the tree compared with the one offered, one commit under the recovery
// lock and operation record, the evidence), without starting the agent; rejecting records it and ends the run FAILED. A
// decision made on another awaiting_review entry than the run's own is refused. An accepted run whose delivery was interrupted
// is finished by accepting it again.
func (r *Runner) Decide(ctx context.Context, runID int64, o DecideOptions) (RecoverResult, error) {
	rv, err := r.ReviewOf(runID)
	if err != nil {
		return RecoverResult{}, err
	}
	if o.Shown != "" && o.Shown != rv.Entry {
		return RecoverResult{}, fmt.Errorf("this decision was made on %.12s, which is not the review run #%d offers (%.12s): look at it again", o.Shown, runID, rv.Entry)
	}
	switch {
	case rv.Decided == "rejected":
		return RecoverResult{}, fmt.Errorf("run #%d was rejected: %w", runID, ErrNotAwaiting)
	case rv.Decided == "accepted" && !o.Accept:
		return RecoverResult{}, fmt.Errorf("run #%d was accepted and its delivery is not finished: accept it again to finish it", runID)
	}
	if !o.Accept {
		if err := r.audit(runID, "review_rejected", map[string]any{"awaiting_review": rv.Entry, "feedback": o.Feedback}); err != nil {
			return RecoverResult{}, fmt.Errorf("record the rejection: %w", err)
		}
		return RecoverResult{}, r.store.FinishRun(runID, persistence.RunStatusFailed, time.Now(), "")
	}
	if rv.Decided == "" {
		if err := r.audit(runID, "review_accepted", map[string]any{"awaiting_review": rv.Entry, "tree": rv.Tree, "feedback": o.Feedback}); err != nil {
			return RecoverResult{}, fmt.Errorf("record the acceptance: %w", err)
		}
	}
	return r.Recover(ctx, runID, RecoverOptions{RequireEvidence: o.RequireEvidence, accepted: &acceptedReview{tree: rv.Tree, checks: rv.Checks}})
}
