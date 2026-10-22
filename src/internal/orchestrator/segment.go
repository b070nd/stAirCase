package orchestrator

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/monitor"
	"github.com/b070nd/stAirCase/src/internal/obs"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/b070nd/stAirCase/src/internal/sshsig"
)

// segment is what a run's first segment and a continuation of it (ADR 0005) do differently; everything else is the same run.
// The first starts from today's HEAD, makes the run's record, branch and worktree, and binds them on the chain under the terms it
// saves. A continuation starts from the run's own base, record, branch and worktree, says on the chain that it goes on, and
// carries over what the earlier segments left: the policy they decided under, the drift supervisor's decisions, the time and
// model use spent, and the deciders' counts.
type segment interface {
	init(s *runState)                 // where the state machine starts, and how a run that fails to get going ends
	first() bool                      // the run's first segment: reconciled, gated, and checked for an earlier run's halt
	base(s *runState) (string, error) // the commit the run works from
	open(s *runState) error           // the run's record, owned by this process
	checkout(s *runState) error       // the run branch and worktree, and the chain entry that says what this segment starts from
	policy(s *runState) (*policy.Snapshot, error)
	carry(sup *policy.Supervisor, tracker *monitor.Tracker) // the supervisor's decisions and the model use of earlier segments
	resume(dec *deciders, val *Validator)                   // the deciders' and the validator's counts
	spent() time.Duration                                   // the run time earlier segments used
}

type (
	firstSegment     struct{}
	continuedSegment struct{ c *continuation }
)

func segmentOf(cont *continuation) segment {
	if cont != nil {
		return continuedSegment{cont}
	}
	return firstSegment{}
}

func (firstSegment) init(s *runState) {
	s.r.phase, s.r.path = PhasePreFlight, []RunPhase{PhasePreFlight}
	s.finalStatus = persistence.RunStatusFailed
}

func (g continuedSegment) init(s *runState) {
	s.r.phase, s.r.path = PhaseResume, []RunPhase{PhaseResume}
	// a continuation that fails to get going leaves the run interrupted, not failed: it can be continued or recovered
	s.finalStatus = persistence.RunStatusKilled
	s.ledger.Proposals = slices.Clone(g.c.kept)
}

func (firstSegment) first() bool     { return true }
func (continuedSegment) first() bool { return false }

func (firstSegment) base(s *runState) (string, error) {
	if s.gr == nil {
		return "", nil
	}
	base, err := s.gr.HeadSHA()
	if err != nil {
		return "", fmt.Errorf("resolve base commit: %w", err)
	}
	if clean, err := s.gr.IsClean(); err == nil && !clean {
		fmt.Fprintf(os.Stdout, "   ℹ️  %s has uncommitted changes; the agent works on commit %.12s without them\n", s.project.SourcePath, base)
	}
	return base, nil
}

func (g continuedSegment) base(*runState) (string, error) { return g.c.history.Bound.BaseSHA, nil } // never today's HEAD

func (firstSegment) open(s *runState) error {
	r, caseID := s.r, s.caseID
	if n, err := r.store.KillStaleRuns(caseID, 2*time.Hour); err != nil {
		obs.Log.Warn("kill stale runs", "err", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stdout, "   ⚠️  Killed %d stale run(s) for case %d\n", n, caseID)
	}
	var err error
	if s.run, err = r.store.CreateRun(caseID, s.topoVersion, s.gitBranch); err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	fmt.Fprintf(os.Stdout, "🚀 Run #%d  case=%d  branch=%s\n", s.run.ID, caseID, s.gitBranch)
	// It cannot already be owned: the number is new. A failure to take the lock is a failure to run, not something to go on without.
	if s.releaseOwner, err = claimRun(r.wsDir, s.run.ID); err != nil {
		_ = r.store.FinishRun(s.run.ID, persistence.RunStatusFailed, time.Now(), "")
		return fmt.Errorf("claim run #%d: %w", s.run.ID, err)
	}
	return nil
}

func (g continuedSegment) open(s *runState) error {
	s.run = g.c.run
	fmt.Fprintf(os.Stdout, "↻ Run #%d  case=%d  continued (segment %d)\n", s.run.ID, s.caseID, g.c.segment)
	s.releaseOwner = g.c.release // Resume took the claim before it checked anything, and hands it over
	return nil
}

func (firstSegment) checkout(s *runState) error {
	r, opts, run := s.r, s.opts, s.run
	s.runBranch = fmt.Sprintf("staircase/run-%d", run.ID)
	if err := r.enter(PhaseBranchCreate); err != nil {
		return err
	}
	if s.gr != nil {
		// A workspace-local run ID does not prove ownership of an existing ref.
		if s.gr.BranchExists(s.runBranch) {
			return fmt.Errorf("run branch %q already exists; inspect and preserve it before retrying", s.runBranch)
		}
		wt := filepath.Join(r.wsDir, "worktrees", fmt.Sprintf("run-%d", run.ID))
		if err := addWorktree(s.project.SourcePath, wt, s.runBranch, s.baseSHA); err != nil {
			return fmt.Errorf("create run worktree: %w", err)
		}
		s.worktree = wt
		wgr, err := OpenGitRepo(s.worktree)
		if err != nil {
			return fmt.Errorf("open run worktree: %w", err)
		}
		if s.appr, err = newApprovals(wgr, s.baseSHA); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "   🌿 Run branch %s in worktree %s\n", s.runBranch, s.worktree)
	}
	// The terms this run starts under, saved for a continuation (ADR 0005): its options and its plan now, its policy
	// when it is read (policy). A run that cannot save them still runs; it simply cannot be continued, only recovered.
	terms, _ := json.Marshal(savedTerms{termsVersion, opts.Terms})
	if err := saveRunFile(savedOptionsPath(r.wsDir, run.ID), terms); err != nil {
		obs.Log.Warn("save run options: the run cannot be continued", "err", err)
	}
	if opts.Plan != nil && len(opts.Plan.Raw) > 0 {
		if err := saveRunFile(savedPlanPath(r.wsDir, run.ID), opts.Plan.Raw); err != nil {
			obs.Log.Warn("save run plan: the run cannot be continued", "err", err)
		}
	}
	// Provenance: what this run started from, before the agent runs.
	bound := history.Bound{BaseSHA: s.baseSHA, Branch: s.runBranch, Worktree: s.worktree, TopologyVersion: s.topoVersion,
		Harness: s.harness, AckDrift: s.haltedRun, TermsSHA256: sha256Hex(terms), // the saved options are the terms a continuation keeps
		ResumesSession: opts.ResumesSession, ResumesRun: opts.ResumesRun}
	if opts.Plan != nil {
		bound.PlanDigest, bound.BlueprintHash = opts.Plan.Digest, opts.Plan.BlueprintHash
	}
	if opts.Away { // the brief the agent is given is bound before it starts
		bound.AwayBrief = &history.AwayBrief{Version: AwayBriefVersion, SHA256: sha256Hex([]byte(AwayBrief))}
	}
	if len(opts.AwayCommands) > 0 { // so are the commands it may run without asking
		bound.AwayCommands = &history.DeclaredCommands{Commands: opts.AwayCommands, SHA256: declaredDigest(opts.AwayCommands)}
	}
	if len(opts.AwayTools) > 0 { // and the tools
		bound.AwayTools = &history.DeclaredTools{Tools: opts.AwayTools, SHA256: declaredDigest(opts.AwayTools)}
	}
	if err := r.audit(run.ID, "run_bound", bound); err != nil {
		return fmt.Errorf("audit run_bound: %w", err)
	}
	if opts.SignKey != "" { // the person who starts the run proves who they are, before any work is done
		var digest string
		if opts.Plan != nil {
			digest = opts.Plan.Digest
		}
		sig, err := sshsig.SignContext(s.ctx, opts.SignKey, certificate.InitiatorNamespace,
			certificate.InitiatorText(certificate.Predicate{Run: run.ID, BaseCommit: s.baseSHA, PlanDigest: digest}, opts.SignAs))
		if err != nil {
			return fmt.Errorf("sign as the run's initiator: %w", err)
		}
		s.initiator = &certificate.Initiator{Principal: opts.SignAs, Signature: base64.StdEncoding.EncodeToString(sig)}
		if err := r.audit(run.ID, "initiator_signed", map[string]any{"principal": opts.SignAs, "signature": s.initiator.Signature}); err != nil {
			return fmt.Errorf("audit initiator_signed: %w", err)
		}
	}
	if opts.Agreed != "" {
		agreed := map[string]any{"by": opts.Agreed}
		if opts.Plan != nil {
			agreed["plan_digest"] = opts.Plan.Digest
		}
		if err := r.audit(run.ID, "task_agreed", agreed); err != nil {
			return fmt.Errorf("audit task_agreed: %w", err)
		}
	}
	return nil
}

func (g continuedSegment) checkout(s *runState) error {
	r, run, cont := s.r, s.run, g.c
	// the run's own branch and worktree, already checked to hold exactly the approved state
	s.runBranch, s.worktree, s.appr = cont.history.Bound.Branch, cont.worktree, cont.appr
	fmt.Fprintf(os.Stdout, "   🌿 Run branch %s in worktree %s (continued)\n", s.runBranch, s.worktree)
	// The run is reopened and the continuation put on its chain before anything else happens: from here on it is
	// a run that is going again, and the chain says from which state (the chain head) and under which terms.
	if err := r.store.ReopenRun(run.ID); err != nil {
		return err
	}
	s.initiator = initiatorOf(cont.history) // the signed request of whoever started the run is kept, never signed again
	approvals, err := json.Marshal(approvalsOf(cont.history.Approvals()))
	if err != nil {
		return err
	}
	resumed := history.Resumed{
		Segment: cont.segment, Generation: cont.chainHead, Approvals: approvals, NextSeq: cont.lastSeq + 1,
		Policy: policyDigest(cont.history), PlanDigest: cont.history.Bound.PlanDigest, Undecided: cont.undecided,
		AckDrift: cont.ackDrift, Elapsed: int(cont.elapsed.Seconds()), Context: cont.contextKind,
		JournalDropped: cont.journalDropped, WithdrewReview: cont.withdrawn, AckUnknown: cont.ackUnknown,
	}
	if err := r.audit(run.ID, "run_resumed", resumed); err != nil {
		return fmt.Errorf("audit run_resumed: %w", err)
	}
	// Only now, with the chain saying so, are the never-consumed lines dropped; a crash in between drops them next time.
	if err := trimJournal(r.wsDir, run.ID, cont.lastSeq); err != nil {
		return fmt.Errorf("drop the journal lines that were never consumed: %w", err)
	}
	barrier.Hit(barrier.ResumeStarted)
	return nil
}

// policy is read once: the rules, the signature check and the digest the certificate names all come from the same bytes. Fail
// closed: a broken or tampered policy never runs as "no policy".
func (firstSegment) policy(s *runState) (*policy.Snapshot, error) {
	r, run := s.r, s.run
	snap, err := policy.LoadSnapshot(r.wsDir)
	if err != nil {
		return nil, fmt.Errorf("load policy: %w", err)
	}
	if s.opts.Deliver == DeliverPolicy && !snap.Signed { // an edited policy file must not arm delivery without a person (ADR 0012)
		return nil, errors.New("--deliver policy needs a signed policy: only rules you signed may approve what is delivered without review; sign yours with 'staircase policy sign'")
	}
	if len(snap.Raw) > 0 { // the exact bytes it decides under, for a continuation
		if err := saveRunFile(savedPolicyPath(r.wsDir, run.ID), snap.Raw); err != nil {
			obs.Log.Warn("save run policy: the run cannot be continued", "err", err)
		}
	}
	// The policy this run decides under, on the chain, so evidence made later (a
	// recovery) names it and not whatever policy.json holds by then.
	if err := r.audit(run.ID, "policy_snapshot", map[string]any{"digest": snap.Digest, "signed": snap.Signed}); err != nil {
		return nil, fmt.Errorf("audit policy_snapshot: %w", err)
	}
	return snap, nil
}

func (g continuedSegment) policy(*runState) (*policy.Snapshot, error) { return g.c.policy, nil } // never today's file

func (firstSegment) carry(*policy.Supervisor, *monitor.Tracker) {}

func (g continuedSegment) carry(sup *policy.Supervisor, tracker *monitor.Tracker) {
	for _, d := range g.c.decisions { // the supervisor stands where it stood: every proposal of the earlier segments replayed
		sup.Replay(d.Paths, d.Approved, d.Source == "operator")
	}
	for _, u := range g.c.usage { // tokens and cost used by the earlier segments count against the budget cap
		tracker.Record(u.Agent, u.Model, u.In, u.Out)
	}
}

func (firstSegment) resume(*deciders, *Validator) {}

func (g continuedSegment) resume(dec *deciders, val *Validator) { // they continue from the counts the chain recorded
	val.restore(g.c.validator)
	dec.total, dec.autoApproved, dec.taskApproved = g.c.lastSeq, g.c.autoApproved, g.c.taskApproved
}

func (firstSegment) spent() time.Duration { return 0 }

// spent: the limit is for the whole run, so what earlier segments used is gone (prepareContinuation refused a run with none left).
func (g continuedSegment) spent() time.Duration { return g.c.elapsed }
