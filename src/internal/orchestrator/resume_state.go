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
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/policy"
)

// This file is how a run is continued (ADR 0005): the state of an interrupted run is derived by replaying its verified
// audit chain and its approval journal, the same history recovery reads, never restored from a snapshot, and the run
// continues under the exact policy, plan and options it started with (saved when it started).

// savedOptions are the options a run started with that decide how it decides: what a continuation must not change.
// Where it listens, how it is shown and the agent that carries it out are not terms of the run.
type savedOptions struct {
	Version                int      `json:"version"`
	Agreed                 string   `json:"agreed,omitempty"`
	ApproveInScope         bool     `json:"approve_in_scope,omitempty"`
	ApproveOnEvidence      bool     `json:"approve_on_evidence,omitempty"`
	AllowShellExec         bool     `json:"allow_shell_exec,omitempty"`
	Sandbox                string   `json:"sandbox,omitempty"`
	RequireSignedApprovals bool     `json:"require_signed_approvals,omitempty"`
	SignKey                string   `json:"sign_key,omitempty"` // a path, never a key
	SignAs                 string   `json:"sign_as,omitempty"`
	RequireEvidence        bool     `json:"require_evidence,omitempty"`
	Checks                 []string `json:"checks,omitempty"`
	CheckTimeoutNS         int64    `json:"check_timeout_ns,omitempty"`
	SignalModel            string   `json:"signal_model,omitempty"`
	SignalURL              string   `json:"signal_url,omitempty"`
	ValidatorModels        []string `json:"validator_models,omitempty"`
}

const savedOptionsVersion = 1

func optionsToSave(o RunOptions) savedOptions {
	s := savedOptions{Version: savedOptionsVersion, Agreed: o.Agreed, ApproveInScope: o.ApproveInScope, ApproveOnEvidence: o.ApproveOnEvidence,
		AllowShellExec: o.AllowShellExec, Sandbox: o.Sandbox, RequireSignedApprovals: o.RequireSignedApprovals, SignKey: o.SignKey,
		SignAs: o.SignAs, RequireEvidence: o.RequireEvidence, Checks: o.Checks, CheckTimeoutNS: int64(o.CheckTimeout)}
	if o.Signal != nil {
		s.SignalModel, s.SignalURL = o.Signal.Model, o.Signal.URL
	}
	if o.Validator != nil {
		s.ValidatorModels = o.Validator.Models
	}
	return s
}

// apply returns the run options a continuation runs with: the saved terms, and nothing else of the first segment.
func (s savedOptions) apply(base RunOptions) RunOptions {
	base.Agreed, base.ApproveInScope, base.ApproveOnEvidence, base.AllowShellExec, base.Sandbox = s.Agreed, s.ApproveInScope, s.ApproveOnEvidence, s.AllowShellExec, s.Sandbox
	base.RequireSignedApprovals, base.SignKey, base.SignAs, base.RequireEvidence = s.RequireSignedApprovals, s.SignKey, s.SignAs, s.RequireEvidence
	base.Checks, base.CheckTimeout = s.Checks, time.Duration(s.CheckTimeoutNS)
	if s.SignalModel != "" {
		base.Signal = &Signal{Model: s.SignalModel, URL: s.SignalURL}
	}
	if len(s.ValidatorModels) > 0 {
		base.Validator = &Validator{Models: s.ValidatorModels}
	}
	return base
}

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
	history   history
	kept      []LedgerProposal // the approvals, in chain order, with their requests
	lastSeq   int
	segment   int // this segment's number: 2 for the first continuation
	chainHead string
	undecided int // requests of earlier segments that were never decided

	autoApproved int
	taskApproved bool
	decisions    []replayedDecision
	usage        []usageRecord
	validator    *validatorState
	halted       bool          // the run was halted for drift
	elapsed      time.Duration // active time used by the earlier segments

	policy *policy.Snapshot
	plan   plan.Plan
	opts   savedOptions

	// What Resume prepared before any change was made (see resume.go).
	release     func() // the run's owner lock, held since before the first check
	worktree    string
	wgr         *GitRepo
	appr        *approvals // the approved state, rebuilt from the chain and checked against the worktree
	ackDrift    bool
	contextKind string // how the agent's context is carried: "fresh_grounded" (the only kind so far)
}

// replayChain reads the parts of a run's history that a continuation needs beyond what recovery needs.
func replayChain(events []domain.RunEventLog, c *continuation) error {
	c.segment = 1
	var segStart, last time.Time
	undecided := map[string]int{}
	for _, e := range events {
		var p struct {
			Source         string          `json:"source"`
			Approved       bool            `json:"approved"`
			ActionType     string          `json:"action_type"`
			Seq            int             `json:"seq"`
			Paths          []string        `json:"paths"`
			ValidatorState *validatorState `json:"validator_state"`
			State          struct {
				Model string `json:"model"`
				In    int    `json:"input_tokens"`
				Out   int    `json:"output_tokens"`
			} `json:"state"`
			ActiveAgent string `json:"active_agent"`
		}
		switch e.EventType {
		case "run_bound":
			segStart = e.Timestamp
		case "run_resumed":
			c.elapsed += last.Sub(segStart)
			segStart = e.Timestamp
			c.segment++
			undecided = map[string]int{} // what an earlier segment left pending is counted at its resume
		case "yield_request":
			if json.Unmarshal([]byte(e.Payload), &p) == nil {
				undecided[p.ActionType]++
			}
		case "drift_halt":
			c.halted = true
		case "state_emit":
			if json.Unmarshal([]byte(e.Payload), &p) == nil {
				c.usage = append(c.usage, usageRecord{p.ActiveAgent, p.State.Model, p.State.In, p.State.Out})
			}
		case "yield_decided":
			if json.Unmarshal([]byte(e.Payload), &p) != nil {
				return fmt.Errorf("the decision at entry %d is unreadable", e.ID)
			}
			if p.ActionType == domain.ActionFinalReview {
				break
			}
			if undecided[p.ActionType] > 0 {
				undecided[p.ActionType]--
			}
			if p.Approved && p.Source == "policy" {
				c.autoApproved++
			}
			if p.Approved && (p.Source == "task" || p.Source == "evidence") {
				c.taskApproved = true
			}
			if p.ValidatorState != nil {
				c.validator = p.ValidatorState
			}
			if p.Source != "orchestrator" { // refused before the supervisor saw it
				c.decisions = append(c.decisions, replayedDecision{p.Seq, p.Source, p.Approved, p.Paths})
			}
		}
		last = e.Timestamp
	}
	c.elapsed += last.Sub(segStart)
	for _, n := range undecided {
		c.undecided += n
	}
	if len(events) > 0 {
		c.chainHead = events[len(events)-1].EventHash
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
func (r *Runner) loadContinuation(runID int64) (*continuation, error) {
	run, err := r.store.GetRun(runID)
	if err != nil || run == nil {
		return nil, fmt.Errorf("run #%d not found", runID)
	}
	if run.GitCommitHash != "" {
		return nil, notContinuable("run #%d already has a commit (%.12s): there is nothing to continue", runID, run.GitCommitHash)
	}
	if run.Status != persistence.RunStatusRunning && run.Status != persistence.RunStatusKilled {
		return nil, notContinuable("run #%d is %s: only a run that was interrupted (or stopped) before it committed is continued", runID, run.Status)
	}
	if op, err := loadOp(r.wsDir, runID); err != nil {
		return nil, err
	} else if op != nil {
		return nil, notContinuable("a recovery of run #%d has begun: it is recovered, not continued", runID)
	}
	events, err := r.store.ListVerifiedEventLogs(runID)
	if err != nil {
		return nil, fmt.Errorf("the audit chain of run #%d does not verify, so nothing in it can be trusted: %w", runID, err)
	}
	for _, e := range events {
		switch e.EventType {
		case "commit_prepared", "certificate_issued", "run_recovered":
			return nil, notContinuable("run #%d already made its commit (%s is on its chain): recover it with `staircase recover %d`", runID, e.EventType, runID)
		}
	}
	hist, err := auditedHistory(events)
	if err != nil {
		return nil, fmt.Errorf("the audit history of run #%d is not one a run can have written: %w", runID, err)
	}
	if hist.Bound.Base == "" || hist.Bound.Branch == "" || hist.Bound.Worktree == "" {
		return nil, notContinuable("run #%d never got as far as a run branch and worktree", runID)
	}
	journal, err := readJournalFile(r.wsDir, runID)
	switch {
	case errors.Is(err, os.ErrNotExist):
		journal = journalRead{} // nothing was approved yet: a run that only asked
	case err != nil:
		return nil, fmt.Errorf("the approval journal of run #%d cannot be trusted: %w", runID, err)
	}
	kept, err := reconcile(hist, journal)
	if err != nil {
		return nil, fmt.Errorf("run #%d cannot be continued: %w", runID, err)
	}
	c := &continuation{run: run, history: hist, kept: kept, lastSeq: hist.LastDecided}
	if err := replayChain(events, c); err != nil {
		return nil, err
	}
	c.segment++ // this is the next segment

	// The terms the run started under, saved when it started.
	if b, err := os.ReadFile(savedOptionsPath(r.wsDir, runID)); err != nil {
		return nil, notContinuable("run #%d saved no options when it started (it began before runs could be continued): recover it instead", runID)
	} else if json.Unmarshal(b, &c.opts) != nil || c.opts.Version != savedOptionsVersion {
		return nil, notContinuable("the options saved for run #%d are unreadable", runID)
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

// savedPolicy is the policy the run decided under: the bytes it saved, which must be the ones the chain names.
func (r *Runner) savedPolicy(runID int64, hist history) (*policy.Snapshot, error) {
	if hist.PolicyDigest == "" && !hist.PolicySnapshotted {
		return nil, notContinuable("the audit chain of run #%d names no policy it decided under", runID)
	}
	if hist.PolicyDigest == "" { // there was no policy.json: nothing approves
		return &policy.Snapshot{Engine: &policy.Engine{}}, nil
	}
	b, err := os.ReadFile(savedPolicyPath(r.wsDir, runID))
	if err != nil {
		return nil, notContinuable("run #%d saved no policy when it started: recover it instead", runID)
	}
	snap, err := policy.SnapshotFromBytes(b, hist.PolicySigned)
	if err != nil {
		return nil, notContinuable("the policy saved for run #%d does not load: %v", runID, err)
	}
	if snap.Digest != hist.PolicyDigest {
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

// contBrief is the continuation's note to the agent, or "" for a run's first segment.
func contBrief(c *continuation) string {
	if c == nil {
		return ""
	}
	return c.continuationBrief()
}
