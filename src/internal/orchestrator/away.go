package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
)

// Away mode (ADR 0006): a run nobody watches ends, when its approved tree passes the checks, awaiting the owner's review.

// AwayBrief is what an away run's agent is told before its task (ADR 0008): it works unattended, and a refusal is a parking, not a
// question. It is guidance, never permission: what is approved is still decided by the run. Its version and digest are on the run's
// run_bound entry; a change of its text is a new version.
const AwayBrief = `You are working unattended: nobody will answer questions or approve anything while you work.
- Every file change you make is decided by stAirCase. Changes inside the agreed scope are approved only when the project's checks pass on them.
- A change that is refused has been parked for the owner, who will see it and why. Do not retry it or work around it; go on with what you can do without it.
- Make each change complete on its own, so the checks can pass after it.
- When you are done or cannot go on, stop, and say in your last message what you did, what you left for the owner and why.`

// AwayBriefVersion is the version of AwayBrief.
const AwayBriefVersion = 1

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
	if len(appr.files) == 0 {
		fmt.Fprintf(os.Stdout, "   ⏸  Awaiting your review: nothing was approved, %d proposal(s) parked for you (an empty change). Decide with: staircase decide %d\n", parked, runID)
		return persistence.RunStatusAwaitingReview, nil
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
	Seq    int // the number of its decision
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
	hist, err := history.Read(r.store, runID)
	if err != nil {
		return rv, err
	}
	rv.Base = hist.Bound.BaseSHA
	for _, d := range hist.Parked() { // a parked proposal the owner adopted (ADR 0007) is part of the change now, not parked
		rv.ParkedProposals = append(rv.ParkedProposals, ParkedProposal{Seq: d.Seq, Action: d.ActionType, Files: d.Paths, Why: d.ParkedWhy})
	}
	aw := hist.Review
	if aw == nil {
		return rv, fmt.Errorf("run #%d has no awaiting_review entry on its chain", runID)
	}
	rv.Entry, rv.Tree, rv.Files, rv.Parked, rv.Decided = aw.Entry, aw.Tree, aw.Files, aw.Parked, aw.Decided
	if aw.Checked == nil {
		return rv, fmt.Errorf("run #%d offers a tree without the checks that ran on it", runID)
	}
	if !aw.Checked.Passed || aw.Checked.Tree != aw.Tree {
		return rv, fmt.Errorf("the checks run #%d names did not pass on the tree it offers", runID)
	}
	for _, c := range aw.Checked.Checks {
		rv.Checks = append(rv.Checks, certificate.Check{Command: c.Command, ExitCode: c.ExitCode, Sandboxed: c.Sandboxed, OutputSHA256: c.OutputSHA256})
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
	if o.Accept && len(rv.Files) == 0 { // an empty change: accepting it delivers nothing, and says so
		if rv.Decided == "" {
			if err := r.audit(runID, "review_accepted", map[string]any{"awaiting_review": rv.Entry, "tree": rv.Tree, "feedback": o.Feedback, "empty": true}); err != nil {
				return RecoverResult{}, fmt.Errorf("record the acceptance: %w", err)
			}
		}
		return RecoverResult{}, r.store.FinishRun(runID, persistence.RunStatusFailed, time.Now(), "")
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

// awayBrief is the brief an agent is given: AwayBrief when the run is away, nothing otherwise.
func awayBrief(away bool) string {
	if away {
		return AwayBrief
	}
	return ""
}

// declaredSet is the declared commands, for an exact match.
func declaredSet(commands []string) map[string]bool {
	set := map[string]bool{}
	for _, c := range commands {
		set[c] = true
	}
	return set
}

// declaredDigest names a declaration: the SHA-256 of its commands, sorted, one per line.
func declaredDigest(commands []string) string {
	return sha256Hex([]byte(strings.Join(slices.Sorted(slices.Values(commands)), "\n")))
}

// noNetworkMode is sandbox.NoNetworkMode; a test replaces it, as the decisions do not depend on the machine's sandboxes.
var noNetworkMode = sandbox.NoNetworkMode
