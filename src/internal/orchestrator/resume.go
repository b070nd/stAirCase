package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/plan"
)

// ResumeOptions are for [Runner.Resume]. Everything that decides how the run decides (policy, plan, the agreed task,
// scope, checks, signing, reviewers) is the run's own and cannot be set here: a continuation is under the same terms.
type ResumeOptions struct {
	// Agent carries the task out, in the run's worktree: a new session of the agent, grounded by AgentEnv.Continuation.
	Agent Agent
	// ApprovalPort and ApprovalToken are where this segment listens; each segment has its own key.
	ApprovalPort  int
	ApprovalToken string
	Debug         bool
	// AckDrift acknowledges that the run was halted for drift, as a new run needs for a case halted before.
	AckDrift bool
	// DiscardUnapproved puts the worktree back to the approved state when it holds anything else (what the agent did
	// after its last approval). Without it such a worktree refuses the continuation, naming the files.
	DiscardUnapproved bool
	// Context says how the agent's context is carried: "" for the agent's own way (the built-in agent: a fresh session
	// grounded by Go; an agent harness: resuming its own session, refused when that is not possible), "native_resume" to
	// require a resume, "fresh_grounded" when a person chose a new session grounded by the audit chain over a resume.
	Context string
	// WithdrawReview continues an away run that awaits review, withdrawing the review it offered: a resumed hosted session goes
	// on with the same run (ADR 0008). A review already decided is not withdrawn.
	WithdrawReview bool
	// AckUnknown continues a run parked because its last launch has no established end (its process was lost, or its agent did
	// not stop when asked), after a person looked at what that agent may have done. Recorded on run_resumed. It never lifts the
	// bound of MaxLostLaunches.
	AckUnknown bool
	// Hosted: the run is a hosted session (as RunOptions.Hosted): an away segment that approved and parked nothing is not checked.
	Hosted bool
}

// ErrLaunchRecorded is part of a refusal that came after the continuation recorded something: the outcome of a lost launch,
// as unknown_after_restart. A refusal without it changed nothing.
var ErrLaunchRecorded = errors.New("the lost launch's outcome was recorded")

// launchRecorded is a refusal that matches ErrLaunchRecorded and keeps its own message.
type launchRecorded struct{ error }

func (e launchRecorded) Unwrap() error      { return e.error }
func (launchRecorded) Is(target error) bool { return target == ErrLaunchRecorded }

// ErrLaunchLost is part of the refusal of a run parked for a launch with no established end. Unlike other refusals, the
// continuation recorded something first: the lost launch's outcome, as unknown_after_restart.
var ErrLaunchLost = errors.New("its agent's last launch has no established end")

// settleLostLaunch records the outcome of a launch the chain has no outcome for: its process was lost (killed, crashed, its
// machine gone) before it could write one, so the outcome is unknown. Then it decides whether the agent may be launched again:
// not past MaxLostLaunches, and not while a lost launch is unacknowledged. The caller holds the run's owner lock.
func (r *Runner) settleLostLaunch(c *continuation, ack bool) error {
	runID := c.run.ID
	h, ended, err := r.recordLostLaunchEnd(runID, c.history)
	if ended {
		c.lostEnded = true
	}
	c.history, c.chainHead = h, h.Head
	if err != nil {
		return err
	}
	if lost := h.Lost(); lost >= history.MaxLostLaunches {
		return fmt.Errorf("%w: %w: %s", ErrNotContinuable, ErrLaunchLost, lostLimitText(runID, lost))
	}
	parked := h.LaunchParked()
	if parked == 0 {
		return nil
	}
	if !ack {
		l := h.Launches[len(h.Launches)-1]
		return fmt.Errorf("%w: %w: launch %d of run #%d ended %s, so nobody knows what its agent did, outside the worktree included; the run is parked rather than launched again. Check what the agent could have done, then say you looked with `staircase acknowledge %d` and resume it, or continue with --ack-unknown, or recover what it approved with `staircase recover %d`",
			ErrNotContinuable, ErrLaunchLost, parked, runID, l.Outcome, runID, runID)
	}
	// --ack-unknown is the acknowledgement command, then the resume: the same operation, under the same claim.
	if h, err = r.writeAcknowledgement(runID, h); err != nil {
		return err
	}
	c.history, c.chainHead = h, h.Head
	c.ackUnknown = parked
	return nil
}

// lostLimitText is the refusal text of a run whose launches were lost MaxLostLaunches times.
func lostLimitText(runID int64, lost int) string {
	return fmt.Sprintf("the end of run #%d's agent could not be established %d times (its process was lost, or it did not stop when asked), the most a run allows (%d), so it is not launched again, acknowledged or not. Recover what it approved with `staircase recover %d`",
		runID, lost, history.MaxLostLaunches, runID)
}

// recordLostLaunchEnd writes the end of an open launch as unknown (its process was lost before it could record one) and returns
// the chain read again; ended says whether it wrote. h is returned as it was when nothing was written.
func (r *Runner) recordLostLaunchEnd(runID int64, h history.History) (out history.History, ended bool, err error) {
	o := h.OpenLaunch()
	if o == nil {
		return h, false, nil
	}
	if err := r.log(runID).Append(history.LaunchEnded{Attempt: o.Attempt, Generation: o.Generation, Outcome: history.LaunchUnknown, Class: history.ClassUncertainDispatch}); err != nil {
		return h, false, fmt.Errorf("record the outcome of launch %d as unknown: %w", o.Attempt, err)
	}
	h2, err := history.Read(r.store, runID)
	return h2, true, err
}

// writeAcknowledgement writes launch_acknowledged for the run's last launch, which h says is lost and not yet acknowledged, and
// returns the chain read again. The caller holds the run's owner lock.
func (r *Runner) writeAcknowledgement(runID int64, h history.History) (history.History, error) {
	barrier.Hit(barrier.LaunchOutcomeSet)
	l := h.Launches[len(h.Launches)-1]
	if err := r.log(runID).Append(history.LaunchAcknowledged{Attempt: l.Attempt, Generation: l.Generation}); err != nil {
		return h, fmt.Errorf("record the acknowledgement of launch %d: %w", l.Attempt, err)
	}
	return history.Read(r.store, runID)
}

// Resume continues an interrupted run: the same run, branch and worktree, under the policy, plan and options it
// started with, from the state its audit chain says it stood in (ADR 0005). Every check is made, and any failure
// refuses with nothing changed, before the run is reopened. It returns what Run returns for the continued segment.
func (r *Runner) Resume(ctx context.Context, runID int64, opts ResumeOptions) error {
	if opts.Agent == nil {
		return fmt.Errorf("no agent to continue the run")
	}
	cont, err := r.prepareContinuation(runID, opts)
	if err != nil {
		return err
	}
	ro := RunOptions{Terms: cont.terms, Agent: opts.Agent, ApprovalPort: opts.ApprovalPort, ApprovalToken: opts.ApprovalToken,
		Debug: opts.Debug, AckDrift: opts.AckDrift, SkipGates: true, Hosted: opts.Hosted} // a continuation decides under the terms the run saved
	if cont.history.Bound.PlanDigest != "" {
		ro.Plan = &cont.plan
	}
	return r.run(ctx, cont.run.CaseID, ro, cont)
}

// PlanOf is the plan a run executed, from the copy it saved when it started (nil when it ran without one), for a caller
// that must build the agent before continuing. It reads and changes nothing, and refuses what Resume would refuse about
// the run's record.
func (r *Runner) PlanOf(runID int64, withdrawReview bool) (*plan.Plan, error) {
	c, err := r.loadContinuation(runID, withdrawReview)
	if err != nil {
		return nil, err
	}
	if c.history.Bound.PlanDigest == "" {
		return nil, nil
	}
	return &c.plan, nil
}

// chooseContext decides how the agent's context is carried (ADR 0005, section 5) and refuses what cannot be done:
//   - the built-in agent has no vendor session: a fresh session grounded by the audit chain is its only way;
//   - an agent harness is continued by resuming its own session when it can (it implements SessionResumer) and the chain names
//     the session; when it cannot, the continuation REFUSES, saying why, unless a person asked for a fresh grounded context
//     (--fresh-context), because a silent fallback would let them believe the agent's conversation continued.
func chooseContext(c *continuation, opts ResumeOptions) (kind, sessionID string, err error) {
	const fresh, native = "fresh_grounded", "native_resume"
	harness := c.history.Bound.Harness
	switch opts.Context {
	case "", fresh, native:
	default:
		return "", "", notContinuable("unknown context %q: use native_resume or fresh_grounded", opts.Context)
	}
	if opts.Context == fresh || harness == "" {
		if opts.Context == native {
			return "", "", notContinuable("the built-in agent has no session of its own to resume")
		}
		return fresh, "", nil
	}
	r, canResume := opts.Agent.(SessionResumer)
	switch {
	case !canResume || !r.NativeResume():
		err = notContinuable("run #%d was made with %s, whose own session cannot be resumed by staircase", c.run.ID, harness)
	case c.session.ID == "" || c.session.Agent != harness:
		err = notContinuable("the audit chain of run #%d names no %s session (the run ended before the agent recorded one), so there is none to resume", c.run.ID, harness)
	default:
		return native, c.session.ID, nil
	}
	return "", "", fmt.Errorf("%w. Continue it with a new %s session grounded by the audit chain instead with --fresh-context, or recover what it approved with `staircase recover %d`", err, harness, c.run.ID)
}

// prepareContinuation checks everything and builds what the continuation starts from, changing nothing. On success
// the run's owner lock is held, and handed to the continuation; on any failure it is released.
func (r *Runner) prepareContinuation(runID int64, opts ResumeOptions) (c *continuation, err error) {
	release, err := claimRun(r.wsDir, runID)
	if err != nil {
		return nil, fmt.Errorf("run #%d is still running: its process holds the run's lock, so it cannot be continued: %w", runID, err)
	}
	recorded := false // the lost launch's outcome was written: a refusal from here on did not change nothing
	defer func() {
		if err != nil {
			release()
			if recorded {
				err = launchRecorded{err}
			}
		}
	}()
	if c, err = r.loadContinuation(runID, opts.WithdrawReview); err != nil {
		return nil, err
	}
	c.release = release
	c.ackDrift = opts.AckDrift
	err = r.settleLostLaunch(c, opts.AckUnknown)
	recorded = c.lostEnded
	if err != nil {
		return nil, err
	}
	if c.contextKind, c.sessionID, err = chooseContext(c, opts); err != nil {
		return nil, err
	}

	// The time limit is for the whole run: a run that has used it up is not given a fresh one.
	limits := c.policy.Engine.Limits.Limits
	if c.history.Bound.PlanDigest != "" {
		limits = limits.Tighter(c.plan.Limits)
	}
	if limits.MaxRunSecs > 0 && c.elapsed >= time.Duration(limits.MaxRunSecs)*time.Second {
		return nil, notContinuable("run #%d has used up its time limit (%ds): it cannot be continued, only recovered", runID, limits.MaxRunSecs)
	}

	if c.halted && !opts.AckDrift {
		return nil, notContinuable("run #%d was halted for drift: review it (`staircase inspect log %d`), then continue with --ack-drift", runID, runID)
	}

	repo, err := c.opened.repo()
	if err != nil {
		return nil, err
	}
	b := c.history.Bound
	branch := strings.TrimPrefix(b.Branch, "refs/heads/")
	if err := exec.Command("git", "-C", repo.path, "cat-file", "-e", b.BaseSHA+"^{commit}").Run(); err != nil {
		return nil, notContinuable("the run's base commit %.12s no longer exists", b.BaseSHA)
	}
	if tip := gitOut(repo.path, "rev-parse", "--verify", "-q", "refs/heads/"+branch); tip != b.BaseSHA {
		return nil, notContinuable("the run branch %s is not at the run's base (%.12s): it moved, or it was deleted", branch, b.BaseSHA)
	}
	if st, err := os.Stat(b.Worktree); err != nil || !st.IsDir() {
		return nil, notContinuable("the run's worktree %s is gone", b.Worktree)
	}
	wgr, err := OpenGitRepo(b.Worktree)
	if err != nil {
		return nil, notContinuable("the run's worktree %s is not a repository: %v", b.Worktree, err)
	}
	if on := gitOut(b.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); on != branch {
		return nil, notContinuable("the run's worktree is on %q, not on the run branch %s", on, branch)
	}

	// The approved state, derived from the chain's approvals and their journaled requests against an empty directory
	// (what is on disk has no say in what was approved), each checked against the digest the chain recorded.
	a, cleanup, err := c.opened.derive(wgr)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	a.repo = wgr
	c.worktree, c.wgr, c.appr = b.Worktree, wgr, a

	// The worktree must hold exactly the approved state. Anything else is work nobody approved: not adopted, not
	// staged; the continuation refuses, or, when asked, puts it back.
	viol, err := a.verify()
	if err != nil {
		return nil, err
	}
	// An approval whose change the agent had not applied yet (it died between the approval and the write) leaves the
	// file as it was at the base: applying exactly the approved bytes is what the approval authorizes, so it is done.
	// Everything else that differs is work nobody approved.
	var notApplied []string
	for _, v := range viol {
		if v.event == "approval_content_mismatch" && a.notApplied(v.file) {
			notApplied = append(notApplied, v.file)
		}
	}
	if len(notApplied) > 0 {
		if err := a.restore(notApplied); err != nil {
			return nil, fmt.Errorf("apply the approved change to %s: %w", strings.Join(notApplied, ", "), err)
		}
		if viol, err = a.verify(); err != nil {
			return nil, err
		}
	}
	if len(viol) > 0 {
		var paths, notes []string
		for _, v := range viol {
			if v.event == "run_branch_moved" {
				return nil, notContinuable("the run's worktree is not on the run's base: %s", v.detail)
			}
			paths = append(paths, v.file)
			notes = append(notes, fmt.Sprintf("%s (%s)", v.file, v.event))
		}
		if !opts.DiscardUnapproved {
			return nil, notContinuable("the worktree of run #%d holds changes nobody approved: %s. Continue with --discard-unapproved to put it back to the approved state", runID, strings.Join(notes, ", "))
		}
		if err := a.restore(paths); err != nil {
			return nil, fmt.Errorf("put the worktree back to the approved state: %w", err)
		}
		if viol, err = a.verify(); err != nil || len(viol) > 0 {
			return nil, notContinuable("the worktree of run #%d still differs from the approved state after it was put back", runID)
		}
	}
	barrier.Hit(barrier.ResumeChecked)
	return c, nil
}
