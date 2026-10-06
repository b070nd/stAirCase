package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/barrier"
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
	// Context says how the agent's context is carried: "" for the agent's own way (a fresh session grounded by Go for
	// the built-in agent), "fresh_grounded" when a person chose it over a native resume.
	Context string
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
	ro := cont.opts.apply(RunOptions{Agent: opts.Agent, ApprovalPort: opts.ApprovalPort, ApprovalToken: opts.ApprovalToken,
		Debug: opts.Debug, AckDrift: opts.AckDrift, SkipGates: true})
	if cont.history.Bound.PlanDigest != "" {
		ro.Plan = &cont.plan
	}
	return r.run(ctx, cont.run.CaseID, ro, cont)
}

// PlanOf is the plan a run executed, from the copy it saved when it started (nil when it ran without one), for a caller
// that must build the agent before continuing. It reads and changes nothing, and refuses what Resume would refuse about
// the run's record.
func (r *Runner) PlanOf(runID int64) (*plan.Plan, error) {
	c, err := r.loadContinuation(runID)
	if err != nil {
		return nil, err
	}
	if c.history.Bound.PlanDigest == "" {
		return nil, nil
	}
	return &c.plan, nil
}

// prepareContinuation checks everything and builds what the continuation starts from, changing nothing. On success
// the run's owner lock is held, and handed to the continuation; on any failure it is released.
func (r *Runner) prepareContinuation(runID int64, opts ResumeOptions) (c *continuation, err error) {
	release, err := claimRun(r.wsDir, runID)
	if err != nil {
		return nil, fmt.Errorf("run #%d is still running: its process holds the run's lock, so it cannot be continued: %w", runID, err)
	}
	defer func() {
		if err != nil {
			release()
		}
	}()
	if c, err = r.loadContinuation(runID); err != nil {
		return nil, err
	}
	c.release = release
	c.contextKind, c.ackDrift = "fresh_grounded", opts.AckDrift
	if opts.Context != "" && opts.Context != c.contextKind {
		return nil, notContinuable("the agent's context cannot be carried as %q here; only a fresh session grounded by the audit chain is available", opts.Context)
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

	cs, err := r.store.GetCase(c.run.CaseID)
	if err != nil || cs == nil {
		return nil, fmt.Errorf("case of run #%d: %w", runID, err)
	}
	project, err := r.store.GetProject(cs.ProjectID)
	if err != nil || project == nil {
		return nil, fmt.Errorf("project of run #%d: %w", runID, err)
	}
	repo, err := OpenGitRepo(project.SourcePath)
	if err != nil {
		return nil, err
	}
	b := c.history.Bound
	branch := strings.TrimPrefix(b.Branch, "refs/heads/")
	if err := exec.Command("git", "-C", repo.path, "cat-file", "-e", b.Base+"^{commit}").Run(); err != nil {
		return nil, notContinuable("the run's base commit %.12s no longer exists", b.Base)
	}
	if tip := gitOut(repo.path, "rev-parse", "--verify", "-q", "refs/heads/"+branch); tip != b.Base {
		return nil, notContinuable("the run branch %s is not at the run's base (%.12s): it moved, or it was deleted", branch, b.Base)
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
	scratch, err := os.MkdirTemp("", "staircase-resume-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	a, err := newApprovals(&GitRepo{r: wgr.r, w: wgr.w, path: scratch}, b.Base)
	if err != nil {
		return nil, err
	}
	for i, p := range c.kept {
		next, derr := a.derive(p.Edits)
		if derr != nil {
			return nil, fmt.Errorf("proposal %d no longer applies: %w", p.Seq, derr)
		}
		if !matchesDigest(c.history.Approvals[i].Files, next) {
			return nil, fmt.Errorf("proposal %d derives other bytes than the audit chain recorded as approved", p.Seq)
		}
		a.record(next)
	}
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
