package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/policy"
)

// This file is how a run is continued (ADR 0005): the state of an interrupted run is derived by replaying its verified
// audit chain and its approval journal, the same history recovery reads, never restored from a snapshot, and the run
// continues under the exact policy, plan and options it started with (saved when it started).

// Files a run saves when it starts, next to its journal.
func savedPolicyPath(wsDir string, runID int64) string  { return runFile(wsDir, runID, "policy.json") }
func savedPlanPath(wsDir string, runID int64) string    { return runFile(wsDir, runID, "plan.json") }
func savedOptionsPath(wsDir string, runID int64) string { return runFile(wsDir, runID, "options.json") }

// replayedDecision is one decided proposal as the supervisor must see it again.
type replayedDecision struct {
	Seq      int
	Source   string
	Approved bool
	Paths    []string
}

// validatorState is what the validator carries between proposals, as the chain recorded it after the last decision.
type validatorState struct {
	Approvals  int  `json:"approvals"`
	RejectRun  int  `json:"reject_run"`
	Unreviewed bool `json:"unreviewed"`
}

// usageRecord is one model step, for the token and cost totals.
type usageRecord struct {
	Agent, Model string
	In, Out      int
}

// continuation is everything a run needs to continue from where its chain says it stood.
type continuation struct {
	run       *domain.Run
	opened    *openedRun
	history   history.History
	kept      []LedgerProposal // the approvals, in chain order, with their requests
	lastSeq   int
	segment   int // this segment's number: 2 for the first continuation
	chainHead string
	undecided int // requests of earlier segments that were never decided
	// journalDropped is how many journal lines the continuation drops: written after the chain's last decision (or torn) by a
	// segment that died before their decision was on the chain, so never consumed. Kept, they would collide with the
	// continuation's own approvals, which take the same numbers.
	journalDropped int
	withdrawn      string // the awaiting_review entry this continuation withdraws (a resumed hosted session, ADR 0008)

	autoApproved int
	taskApproved bool
	decisions    []replayedDecision
	usage        []usageRecord
	validator    *validatorState
	session      struct{ Agent, ID string } // the agent's vendor session as the chain last named it
	halted       bool                       // the run was halted for drift
	elapsed      time.Duration              // active time used by the earlier segments

	policy *policy.Snapshot
	plan   plan.Plan
	terms  Terms

	// What Resume prepared before any change was made (see resume.go).
	release     func() // the run's owner lock, held since before the first check
	worktree    string
	wgr         *GitRepo
	appr        *approvals // the approved state, rebuilt from the chain and checked against the worktree
	ackDrift    bool
	ackUnknown  int    // the lost launch a person acknowledged for this continuation
	lostEnded   bool   // this continuation wrote the outcome of a lost launch to the chain
	sessionID   string // the vendor session to resume, when contextKind is native_resume
	contextKind string // how the agent's context is carried: "fresh_grounded" (the only kind so far)
}

// continueFrom is what a continuation takes from the run's history beyond what recovery needs: the counters the deciders stand
// at, the drift supervisor's decisions, the validator's state, usage, time used, segments and requests nobody decided.
func continueFrom(h history.History, c *continuation) error {
	c.segment, c.elapsed, c.undecided, c.chainHead, c.halted = h.Segment, h.Elapsed, h.Undecided, h.Head, h.Halted
	c.session.Agent, c.session.ID = h.Session.Agent, h.Session.ID
	c.autoApproved, c.taskApproved = h.AutoApproved(), h.TaskApproved()
	if st := h.ValidatorState(); st != nil {
		c.validator = &validatorState{}
		if err := json.Unmarshal(st, c.validator); err != nil {
			return fmt.Errorf("the reviewer model's state on the chain is unreadable: %w", err)
		}
	}
	for _, d := range h.Supervised() {
		c.decisions = append(c.decisions, replayedDecision{d.Seq, d.Source, d.Approved, d.Paths})
	}
	for _, u := range h.Usage {
		c.usage = append(c.usage, usageRecord{u.ActiveAgent, u.State.Model, u.State.In, u.State.Out})
	}
	return nil
}

// ErrNotContinuable is returned for a run that cannot be continued; its message says why. Nothing was changed.
var ErrNotContinuable = errors.New("the run cannot be continued")

func notContinuable(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrNotContinuable}, a...)...)
}

// loadContinuation checks everything about a run that can be checked without touching its repository, and derives
// the state it continues from. It reads, and changes nothing.
func (r *Runner) loadContinuation(runID int64, withdrawReview bool) (*continuation, error) {
	run, err := r.store.GetRun(runID)
	if err != nil || run == nil {
		return nil, fmt.Errorf("run #%d not found", runID)
	}
	if run.GitCommitHash != "" {
		return nil, notContinuable("run #%d already has a commit (%.12s): there is nothing to continue", runID, run.GitCommitHash)
	}
	if run.Status == persistence.RunStatusAwaitingReview && !withdrawReview {
		return nil, notContinuable("run #%d has ended and awaits your review: decide it with staircase decide %d", runID, runID)
	}
	if run.Status != persistence.RunStatusRunning && run.Status != persistence.RunStatusKilled && run.Status != persistence.RunStatusAwaitingReview {
		return nil, notContinuable("run #%d is %s: only a run that was interrupted (or stopped) before it committed is continued", runID, run.Status)
	}
	if op, err := loadOp(r.wsDir, runID); err != nil {
		return nil, err
	} else if op != nil {
		return nil, notContinuable("a recovery of run #%d has begun: it is recovered, not continued", runID)
	}
	opened, err := r.openRun(run)
	if err != nil {
		return nil, err
	}
	hist, withdrawn := opened.hist, ""
	if run.Status == persistence.RunStatusAwaitingReview { // continued by a resumed hosted session: its review is withdrawn
		if hist.Review == nil || hist.Review.Decided != "" {
			return nil, notContinuable("run #%d was decided (%s) and its delivery is not finished: finish it with staircase decide %d --accept", runID, reviewState(hist.Review), runID)
		}
		withdrawn = hist.Review.Entry
	}
	if hist.Sealed != "" {
		return nil, notContinuable("run #%d already made its commit (%s is on its chain): recover it with `staircase recover %d`", runID, hist.Sealed, runID)
	}
	if hist.Bound.BaseSHA == "" || hist.Bound.Branch == "" || hist.Bound.Worktree == "" {
		return nil, notContinuable("run #%d never got as far as a run branch and worktree", runID)
	}
	c := &continuation{run: run, opened: opened, history: hist, kept: opened.kept, lastSeq: hist.LastDecided, withdrawn: withdrawn} // no journal: nothing was approved yet
	c.journalDropped = staleJournal(opened.journal, hist.LastDecided)
	if err := continueFrom(hist, c); err != nil {
		return nil, err
	}
	c.segment++ // this is the next segment

	// The terms the run started under, saved when it started.
	if c.terms, err = opened.terms(r.wsDir); errors.Is(err, os.ErrNotExist) {
		return nil, notContinuable("run #%d saved no options when it started (it began before runs could be continued): recover it instead", runID)
	} else if err != nil {
		return nil, notContinuable("run #%d: %v", runID, err)
	}
	rawPlan, err := os.ReadFile(savedPlanPath(r.wsDir, runID))
	if hist.Bound.PlanDigest != "" {
		if err != nil {
			return nil, notContinuable("run #%d saved no plan when it started: recover it instead", runID)
		}
		if c.plan, err = plan.Parse(rawPlan, hist.Bound.PlanDigest); err != nil {
			return nil, notContinuable("the plan saved for run #%d is not the one it ran under: %v", runID, err)
		}
	}
	if c.policy, err = r.savedPolicy(runID, hist); err != nil {
		return nil, err
	}
	return c, nil
}

// termsBound reports whether saved are the options the run bound (ADR 0010, Q22). A run bound before the digest was recorded is
// read as before.
func termsBound(h history.History, saved []byte) bool {
	return h.Bound.TermsSHA256 == "" || h.Bound.TermsSHA256 == sha256Hex(saved)
}

// savedPolicy is the policy the run decided under: the bytes it saved, which must be the ones the chain names.
func (r *Runner) savedPolicy(runID int64, hist history.History) (*policy.Snapshot, error) {
	if hist.Policy == nil {
		return nil, notContinuable("the audit chain of run #%d names no policy it decided under", runID)
	}
	if hist.Policy.Digest == "" { // there was no policy.json: nothing approves
		return &policy.Snapshot{Engine: &policy.Engine{}}, nil
	}
	b, err := os.ReadFile(savedPolicyPath(r.wsDir, runID))
	if err != nil {
		return nil, notContinuable("run #%d saved no policy when it started: recover it instead", runID)
	}
	snap, err := policy.SnapshotFromBytes(b, hist.Policy.Signed)
	if err != nil {
		return nil, notContinuable("the policy saved for run #%d does not load: %v", runID, err)
	}
	if snap.Digest != hist.Policy.Digest {
		return nil, notContinuable("the policy saved for run #%d is not the one the audit chain names", runID)
	}
	return snap, nil
}

// continuationBrief is what a fresh agent session is told when a run is continued, built by Go from the chain:
// the agent's own memory of the earlier segments is not carried (and never trusted as state).
func (c *continuation) continuationBrief() string {
	var b strings.Builder
	fmt.Fprintf(&b, "This task was started earlier and interrupted; you are continuing it (segment %d). ", c.segment)
	if len(c.kept) == 0 {
		b.WriteString("Nothing has been approved yet: start from the task above.")
		return b.String()
	}
	b.WriteString("These files were already approved and are in the worktree now; do not propose them again unless the task needs a different change to them:")
	seen := map[string]bool{}
	for _, p := range c.kept {
		for _, e := range p.Edits {
			if !seen[e.File] {
				seen[e.File] = true
				fmt.Fprintf(&b, "\n- %s", e.File)
			}
		}
	}
	b.WriteString("\nContinue with what the task still needs.")
	return b.String()
}

// Segment is the number of the continuation's segment, for the caller's messages.
func (c *continuation) Segment() int { return c.segment }

func runFile(wsDir string, runID int64, name string) string {
	return filepath.Join(wsDir, "journal", fmt.Sprintf("run-%d.%s", runID, name))
}

// saveRunFile writes one of the files a run keeps for its continuation, atomically and flushed: a run that dies later
// either has the file whole or has none (and is then recovered, not continued).
func saveRunFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".save-*")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// agentSession is what the agent is told about its vendor session in a continuation: nil unless it is to resume one.
func agentSession(c *continuation) *AgentSession {
	if c == nil || c.contextKind != "native_resume" {
		return nil
	}
	return &AgentSession{ID: c.sessionID, Resume: true}
}

// contBrief is the continuation's note to the agent, or "" for a run's first segment.
func contBrief(c *continuation) string {
	if c == nil {
		return ""
	}
	return c.continuationBrief()
}

// reviewState is how an offered review stands, for a refusal.
func reviewState(rv *history.Review) string {
	if rv == nil {
		return "no review on its chain"
	}
	return rv.Decided
}
