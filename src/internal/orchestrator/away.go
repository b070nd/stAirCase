package orchestrator

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/barrier"
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
//
// A run that delivers by policy (deliver is not nil) instead judges the seven conditions of ADR 0012 once its checks passed: when
// they all hold it audits delivery_authorized and ends SUCCESS, so that the commit is made and delivered like a live run's, with
// no person asked; otherwise it is offered for review like any other, and the awaiting_review entry and the message say which
// condition sent it to the owner. The tree it authorized is returned, "" when it did not authorize.
func (r *Runner) awaitReview(ctx context.Context, runID int64, appr *approvals, gate *evidenceGate, parked int, deliver *deliveryAttempt) (status, authorized string, err error) {
	tree, passed, failed, err := r.checkTree(ctx, runID, appr, gate)
	if err != nil {
		return persistence.RunStatusFailed, "", err
	}
	// An empty change whose checks fail has nothing to deliver, but its parked proposals may be what fixes the checks (a project that
	// is broken at its base): they are offered, with the failure shown, instead of being lost to a FAILED run.
	emptyButParked := !passed && len(appr.files) == 0 && parked > 0
	if !passed && !emptyButParked {
		fmt.Fprintf(os.Stdout, "   ❌ The approved change does not pass its checks now: %s\n", failed)
		return persistence.RunStatusFailed, "", nil
	}
	refused := ""
	if deliver != nil { // delivery by policy: every condition, from the verified history, or the owner decides
		var auth history.DeliveryAuthorized
		if auth, refused = r.judgeDelivery(runID, appr, tree, parked, deliver); refused == "" {
			if err := r.audit(runID, "delivery_authorized", auth); err != nil {
				return persistence.RunStatusFailed, "", fmt.Errorf("audit delivery_authorized: %w", err)
			}
			barrier.Hit(barrier.DeliveryAuthorized)
			fmt.Fprintf(os.Stdout, "   ✅ Delivering by policy: every condition holds, so %d file(s) that rules of your signed policy approved are committed to the run branch without your review. Nothing is pushed.\n", len(appr.files))
			return persistence.RunStatusSuccess, tree, nil
		}
	}
	checked, err := r.store.GetLastEventHash(runID)
	if err != nil {
		return persistence.RunStatusFailed, "", err
	}
	if err := r.audit(runID, "awaiting_review", history.AwaitingReview{Tree: tree, Files: digest(appr.files), PhaseChecked: checked, Parked: parked, DeliveryRefused: refused}); err != nil {
		return persistence.RunStatusFailed, "", fmt.Errorf("audit awaiting_review: %w", err)
	}
	if refused != "" {
		fmt.Fprintf(os.Stdout, "   🙋 Delivery by policy is refused, and falling back to your review is a normal outcome: %s\n", refused)
	}
	if emptyButParked {
		fmt.Fprintf(os.Stdout, "   ⏸  Awaiting your review: nothing was approved and the checks fail on the unchanged tree (%s); %d proposal(s) parked for you. Decide with: staircase decide %d\n", failed, parked, runID)
		return persistence.RunStatusAwaitingReview, "", nil
	}
	if len(appr.files) == 0 {
		fmt.Fprintf(os.Stdout, "   ⏸  Awaiting your review: nothing was approved, %d proposal(s) parked for you (an empty change). Decide with: staircase decide %d\n", parked, runID)
		return persistence.RunStatusAwaitingReview, "", nil
	}
	fmt.Fprintf(os.Stdout, "   ⏸  Awaiting your review: %d file(s) approved on evidence, %d proposal(s) parked. Decide with: staircase decide %d\n",
		len(appr.files), parked, runID)
	return persistence.RunStatusAwaitingReview, "", nil
}

// checkTree runs the checks on the approved tree as it stands and records the result as phase_checked: the first half of an away
// run's acceptance, for a run that offers its change and for one that did nothing (checkUnchanged). failed says what failed, or "".
func (r *Runner) checkTree(ctx context.Context, runID int64, appr *approvals, gate *evidenceGate) (tree string, passed bool, failed string, err error) {
	if tree, err = appr.tree(); err != nil {
		return "", false, "", fmt.Errorf("the approved tree: %w", err)
	}
	results, failed := gate.runChecks(ctx, nil)
	passed = failed == "" && len(results) > 0
	if err := r.audit(runID, "phase_checked", map[string]any{"tree": tree, "checks": checkEvidence(results), "passed": passed, "failed": failed}); err != nil {
		return "", false, "", fmt.Errorf("audit phase_checked: %w", err)
	}
	return tree, passed, failed, nil
}

// checkUnchanged is the end of an away run that approved nothing and parked nothing (its agent made `made` proposals, every one
// refused): there is no change to offer, so the checks run once on the unchanged tree. A project whose checks fail without any
// change ends the run FAILED, its worktree kept, since nothing was done and the project is broken; one whose checks pass ends it
// SUCCESS, which its outcome calls "no change". Nothing is committed either way.
func (r *Runner) checkUnchanged(ctx context.Context, runID int64, appr *approvals, gate *evidenceGate, made int) (string, error) {
	_, passed, failed, err := r.checkTree(ctx, runID, appr, gate)
	if err != nil {
		return persistence.RunStatusFailed, err
	}
	barrier.Hit(barrier.NothingChecked)
	if !passed {
		fmt.Fprintf(os.Stdout, "   ❌ Nothing to review, and your checks fail: no proposal was approved or parked (the agent made %d), and your checks fail on the unchanged tree (%s). Nothing was committed. Read what the agent did with `staircase inspect log %d`.\n",
			made, cmp.Or(failed, "the checks ran nothing"), runID)
		return persistence.RunStatusFailed, nil
	}
	fmt.Fprintf(os.Stdout, "   ✅ Nothing to review: no proposal was approved or parked (the agent made %d), and your checks pass on the unchanged tree. If you expected a change, read what the agent said with `staircase inspect log %d`.\n",
		made, runID)
	return persistence.RunStatusSuccess, nil
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
	// ChecksFailed is, for an empty change only, why the checks failed on its unchanged tree: the run parked what it proposed, and
	// the project's own checks do not pass without it. Empty when the checks passed (always, for a change with files).
	ChecksFailed string
	// DeliveryRefused is, for a run that was to deliver by policy and fell back to the owner, the condition that sent it (ADR 0012).
	DeliveryRefused string

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
	rv.DeliveryRefused = aw.DeliveryRefused
	if aw.Checked == nil {
		return rv, fmt.Errorf("run #%d offers a tree without the checks that ran on it", runID)
	}
	if aw.Checked.Tree != aw.Tree {
		return rv, fmt.Errorf("the checks run #%d names did not run on the tree it offers", runID)
	}
	if !aw.Checked.Passed {
		if len(aw.Files) > 0 { // only an empty change, which delivers nothing, may be offered on failing checks
			return rv, fmt.Errorf("the checks run #%d names did not pass on the tree it offers", runID)
		}
		rv.ChecksFailed = cmp.Or(aw.Checked.Failed, "the checks ran nothing")
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
			if err := r.audit(runID, "review_accepted", history.ReviewDecided{AwaitingReview: rv.Entry, Tree: rv.Tree, Feedback: o.Feedback, Empty: true}); err != nil {
				return RecoverResult{}, fmt.Errorf("record the acceptance: %w", err)
			}
		}
		return RecoverResult{}, r.store.FinishRun(runID, persistence.RunStatusFailed, time.Now(), "")
	}
	if !o.Accept {
		if err := r.audit(runID, "review_rejected", history.ReviewDecided{AwaitingReview: rv.Entry, Feedback: o.Feedback}); err != nil {
			return RecoverResult{}, fmt.Errorf("record the rejection: %w", err)
		}
		return RecoverResult{}, r.store.FinishRun(runID, persistence.RunStatusFailed, time.Now(), "")
	}
	if rv.Decided == "" {
		if err := r.audit(runID, "review_accepted", history.ReviewDecided{AwaitingReview: rv.Entry, Tree: rv.Tree, Feedback: o.Feedback}); err != nil {
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
