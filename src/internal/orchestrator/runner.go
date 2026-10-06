// Package orchestrator implements the stAirCase run lifecycle state machine.
//
// The orchestrator is the single authoritative coordinator of a run:
// it sequences pre-flight checks, the run's git worktree, the in-process agent,
// the decision (HITL) loop, finalize and teardown - each labelled
// by a [RunPhase] constant so that crash recovery and tests can reason about
// where execution stopped.
package orchestrator

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/approvalhttp"
	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/gate"
	"github.com/b070nd/stAirCase/src/internal/governance"
	"github.com/b070nd/stAirCase/src/internal/llm"
	"github.com/b070nd/stAirCase/src/internal/monitor"
	"github.com/b070nd/stAirCase/src/internal/obs"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/b070nd/stAirCase/src/internal/signal"
	"github.com/b070nd/stAirCase/src/internal/sshsig"
	"github.com/b070nd/stAirCase/src/internal/tui"
	"github.com/b070nd/stAirCase/src/internal/webhookauth"
	"github.com/b070nd/stAirCase/src/internal/wslock"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrRunNotSuccessful is returned by [Runner.Run] when a run reaches a
// non-success terminal state (FAILED or KILLED). The run is still fully
// recorded and finalized; this error exists so that `staircase run` exits
// non-zero and CI/automation can detect failure instead of treating a failed
// agent run as success.
var ErrRunNotSuccessful = errors.New("run did not complete successfully")

// ErrEvidenceIncomplete is returned with RequireEvidence when a commit was
// made but its ledger or change certificate could not be. The commit stays
// delivered on its run branch; the run is not relabelled as undelivered.
var ErrEvidenceIncomplete = errors.New("the commit was made but its evidence is incomplete")

// What a run that made a commit can say about its evidence (RunSummary.Outcome).
const (
	OutcomeCertified       = "certified"                  // a signed certificate and the ledger it names
	OutcomeWithoutEvidence = "delivered_without_evidence" // the commit exists; see RunSummary.EvidenceErrors
	OutcomeRecovered       = "recovered"                  // the run was interrupted; `recover` committed what it had approved
)

// lostGrace is how long a run waits for its agent to stop after cancellation
// before it ends without it (recording agent_unresponsive).
var lostGrace = 10 * time.Second

// RunPhase labels each boundary of the orchestration state machine.
// A crashed run leaves its [Runner.Phase] at the last phase it entered,
// which [Reconcile] uses to decide what cleanup is needed.
type RunPhase string

const (
	PhasePreFlight     RunPhase = "PRE_FLIGHT"
	PhaseResume        RunPhase = "RESUME" // a later segment of a run: the start of a continuation instead of PRE_FLIGHT
	PhaseBranchCreate  RunPhase = "BRANCH_CREATE"
	PhaseAgentStart    RunPhase = "AGENT_START"
	PhaseAgentLoop     RunPhase = "AGENT_LOOP"
	PhaseFinalize      RunPhase = "FINALIZE"
	PhaseBranchRestore RunPhase = "BRANCH_RESTORE"
)

// runMoves is the run's state machine: from each phase, the phases it may
// move to. A failure during setup (BRANCH_CREATE, AGENT_START) or finalize
// goes to BRANCH_RESTORE; the agent loop always ends in FINALIZE. A run that
// stops in PRE_FLIGHT never existed. Any other move is a bug, and the run
// fails rather than continue in an unknown state.
var runMoves = map[RunPhase][]RunPhase{
	PhasePreFlight:     {PhaseBranchCreate},
	PhaseResume:        {PhaseAgentStart, PhaseBranchRestore}, // checks passed, or the continuation was refused
	PhaseBranchCreate:  {PhaseAgentStart, PhaseBranchRestore},
	PhaseAgentStart:    {PhaseAgentLoop, PhaseBranchRestore},
	PhaseAgentLoop:     {PhaseFinalize},
	PhaseFinalize:      {PhaseBranchRestore},
	PhaseBranchRestore: nil,
}

// RunMoves is a copy of the run's state machine, for tests and documentation.
func RunMoves() map[RunPhase][]RunPhase { return maps.Clone(runMoves) }

// enter moves the run to phase p, if the state machine allows it from the
// current phase, and remembers the path for the run's evidence (run_path).
func (r *Runner) enter(p RunPhase) error {
	if !slices.Contains(runMoves[r.phase], p) {
		return fmt.Errorf("run state machine: %s cannot follow %s", p, r.phase)
	}
	r.phase, r.path = p, append(r.path, p)
	return nil
}

// RunOptions carries all user-supplied flags for a run.
type RunOptions struct {
	DryRun        bool
	SkipGates     bool
	Debug         bool
	Reconcile     bool // inspect orphan branches / stale runs before proceeding
	ApprovalPort  int
	ApprovalToken string
	// Agent runs in-process in the run's worktree: the compiled plan's agent
	// graph (staircase run), or a scripted agent in tests. Required.
	Agent Agent
	// Plan is the compiled plan the agent executes, when there is one: the run
	// records its topology version, digest and blueprint as provenance.
	Plan *plan.Plan
	// Validator, when set, decides in-scope file edits the policy leaves open
	// (see Validator); a human approves the run's final change once.
	Validator *Validator
	// Agreed says who agreed to the task before the run (a session's
	// confirmation); it is recorded after run_bound.
	Agreed string
	// AckDrift acknowledges that the case's previous run was halted for drift;
	// without it such a case does not run again.
	AckDrift bool
	// AllowShellExec, when true, includes run_shell in the agent tool list.
	// Defaults to false - operators must explicitly pass --allow-shell-exec.
	AllowShellExec bool
	// Sandbox is where approved commands run: "auto" (default: in the
	// sandbox when this machine has one), "required" or "off".
	Sandbox string
	// ApproveInScope approves changes inside the agreed task's scope as part
	// of the task (source "task"), with a checkpoint every few changes and a
	// person's final review of the whole change. It needs Agreed.
	ApproveInScope bool
	// ApproveOnEvidence approves in-scope changes on evidence instead of on the
	// agreed task alone: the Checks must pass on the state each change would
	// produce, and the Validator's models, if any, must agree; otherwise a
	// person decides. It replaces the sampled checkpoint of ApproveInScope, and
	// the whole change is still reviewed once before it is committed. It needs
	// Checks or a Validator.
	ApproveOnEvidence bool
	// Signal, when set, is a decision model asked about every change
	// approved without a person; it can only send it to a person.
	Signal *Signal
	// RequireSignedApprovals refuses a person's decision unless it carries an
	// SSH signature of a signer listed in the workspace's allowed_signers.
	RequireSignedApprovals bool
	// RequireEvidence makes a commit without its ledger and signed change
	// certificate a failure: the run ends with ErrEvidenceIncomplete (the commit
	// stays on its branch, reported as delivered).
	RequireEvidence bool
	// SignKey, when set, makes stAirCase sign each human decision with this
	// SSH key, as SignAs (a key that needs a touch makes it a presence check).
	SignKey, SignAs string
	// Checks are commands run on the commit the run made (--check), such as
	// its tests; their results are evidence in the change certificate.
	Checks []string
	// CheckTimeout is how long one check may run, wherever checks run (approving on
	// evidence, the definition of done, after the commit): 0 means 15 minutes.
	CheckTimeout time.Duration
}

// Runner orchestrates a single stAirCase run.
// Construct with [NewRunner]; call [Run] to execute.
type Runner struct {
	store *persistence.Store
	wsDir string
	phase RunPhase   // current phase; read via Phase() for observability and tests
	path  []RunPhase // the phases this run went through, in order
}

// NewRunner constructs a Runner bound to the given store and workspace directory.
func NewRunner(store *persistence.Store, wsDir string) *Runner {
	return &Runner{store: store, wsDir: wsDir, phase: PhasePreFlight}
}

// Phase returns the phase the runner most recently entered.
// Safe to call concurrently; the value is a point-in-time snapshot.
func (r *Runner) Phase() RunPhase { return r.phase }

// Run executes the full orchestration lifecycle for the given case.
//
// Phases executed in order: PRE_FLIGHT → BRANCH_CREATE → AGENT_START →
// AGENT_LOOP → FINALIZE → BRANCH_RESTORE.
func (r *Runner) Run(ctx context.Context, caseID int64, opts RunOptions) error {
	return r.run(ctx, caseID, opts, nil)
}

// run is Run, and with a continuation the continuation of an interrupted run: the same run, branch and worktree,
// under the terms it started with, from the state its chain says it stood in (ADR 0005, resume.go).
func (r *Runner) run(ctx context.Context, caseID int64, opts RunOptions, cont *continuation) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	obs.ActiveRuns.Inc()
	t0Run := time.Now()
	// Root span for the whole run; yield spans become children via ctx.
	ctx, runSpan := obs.Tracer.Start(ctx, "staircase.run",
		trace.WithAttributes(attribute.Int64("staircase.case_id", caseID)))
	defer runSpan.End()
	defer func() {
		obs.ActiveRuns.Dec()
		obs.RunDuration.Observe(time.Since(t0Run).Seconds())
	}()
	r.phase, r.path = PhasePreFlight, []RunPhase{PhasePreFlight}
	if cont != nil {
		r.phase, r.path = PhaseResume, []RunPhase{PhaseResume}
	}

	// ── Load case + project ───────────────────────────────────────────────────
	caseRec, err := r.store.GetCase(caseID)
	if err != nil {
		return fmt.Errorf("load case: %w", err)
	}
	if caseRec == nil {
		return fmt.Errorf("case %d not found", caseID)
	}

	project, err := r.store.GetProject(caseRec.ProjectID)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("project %d not found", caseRec.ProjectID)
	}

	// ── Open git repo (used across the whole run lifecycle) ──────────────────
	var gr *GitRepo
	if project.SourcePath != "" {
		gr, err = OpenGitRepo(project.SourcePath)
		if err != nil {
			return fmt.Errorf("open git repo: %w", err)
		}
	}

	// ── Optional reconcile ────────────────────────────────────────────────────
	if opts.Reconcile && project.SourcePath != "" && cont == nil {
		result, err := r.Reconcile(ctx, caseID, project.SourcePath, true)
		if err != nil {
			obs.Log.Warn("reconcile", "err", err)
		} else if len(result.StalledRuns) > 0 || len(result.OrphanBranches) > 0 {
			fmt.Fprintf(os.Stdout, "   🔄 Reconcile: %d stale run(s) killed, %d branch(es) retained for inspection\n",
				len(result.StalledRuns), len(result.OrphanBranches))
		}
	}

	// ── Base revision ─────────────────────────────────────────────────────────
	// The run works in its own git worktree at the current HEAD commit; the
	// developer's checkout (branch, index, files, stash) is never modified, so
	// uncommitted changes there are not visible to the agent.
	baseSHA := ""
	if cont != nil { // the run's own base, never today's HEAD
		baseSHA = cont.history.Bound.Base
	} else if gr != nil {
		if baseSHA, err = gr.HeadSHA(); err != nil {
			return fmt.Errorf("resolve base commit: %w", err)
		}
		if clean, err := gr.IsClean(); err == nil && !clean {
			fmt.Fprintf(os.Stdout, "   ℹ️  %s has uncommitted changes; the agent works on commit %.12s without them\n",
				project.SourcePath, baseSHA)
		}
	}

	// ── Resolve topology ──────────────────────────────────────────────────────
	topology, err := r.store.GetLatestTopology(project.ID)
	if err != nil {
		return fmt.Errorf("load topology: %w", err)
	}
	harness := ""
	if opts.Plan != nil {
		harness = opts.Plan.Harness
	}
	if topology == nil && harness == "" { // a harness brings its own agent: version 0, no topology
		return fmt.Errorf("no swarm topology registered for project %q - run 'staircase topology register' first", project.Name)
	}
	topoVersion := 0
	if topology != nil {
		topoVersion = topology.Version
	}
	if opts.Plan != nil {
		topoVersion = opts.Plan.TopologyVersion // what runs is the plan, not the latest topology
	}

	// ── Resolve current git branch (capture SHA on detached HEAD) ─────────────
	gitBranch := "unknown"
	if gr != nil {
		if b, bErr := gr.CurrentBranch(); bErr == nil {
			gitBranch = b
		}
	}

	// ── Quality gate pre-flight ───────────────────────────────────────────────
	if !opts.SkipGates && cont == nil { // a continuation is checked by loadContinuation and the worktree checks, not by the case's gates
		if err := r.runGates(caseID); err != nil {
			return err
		}
	}
	if opts.DryRun {
		fmt.Fprintf(os.Stdout, "   [dry-run] case %d would run on a new worktree at %.12s (from %s); nothing was created\n",
			caseID, baseSHA, gitBranch)
		return nil
	}

	// A run halted for drift is reviewed before the case runs again.
	haltedRun, err := r.lastDriftHalt(caseID)
	if err != nil {
		return err
	}
	if cont != nil { // the halt that matters is this run's own (below), not a previous run's
		haltedRun = 0
	}
	if haltedRun != 0 && !opts.AckDrift {
		return fmt.Errorf("run #%d of case #%d was halted for drift - review it ('staircase inspect log %d'), then run again with --ack-drift", haltedRun, caseID, haltedRun)
	}

	// ── Create run record ─────────────────────────────────────────────────────
	var run *domain.Run
	if cont != nil {
		run = cont.run
		fmt.Fprintf(os.Stdout, "↻ Run #%d  case=%d  continued (segment %d)\n", run.ID, caseID, cont.segment)
	} else {
		if n, err := r.store.KillStaleRuns(caseID, 2*time.Hour); err != nil {
			obs.Log.Warn("kill stale runs", "err", err)
		} else if n > 0 {
			fmt.Fprintf(os.Stdout, "   ⚠️  Killed %d stale run(s) for case %d\n", n, caseID)
		}
		if run, err = r.store.CreateRun(caseID, topoVersion, gitBranch); err != nil {
			return fmt.Errorf("create run: %w", err)
		}
		fmt.Fprintf(os.Stdout, "🚀 Run #%d  case=%d  branch=%s\n", run.ID, caseID, gitBranch)
	}
	// The run is owned by this process for its whole life (see runowner.go). It cannot already be owned: the
	// number is new. A failure to take the lock is a failure to run, not something to go on without.
	var releaseOwner func()
	if cont != nil { // Resume took the claim before it checked anything, and hands it over
		releaseOwner = cont.release
	} else {
		var err error
		if releaseOwner, err = claimRun(r.wsDir, run.ID); err != nil {
			_ = r.store.FinishRun(run.ID, persistence.RunStatusFailed, time.Now(), "")
			return fmt.Errorf("claim run #%d: %w", run.ID, err)
		}
	}
	defer releaseOwner() // after the cleanup below: the run is owned until it has finished

	// ── BRANCH_CREATE ─────────────────────────────────────────────────────────
	runBranch := fmt.Sprintf("staircase/run-%d", run.ID)
	if cont != nil {
		runBranch = cont.history.Bound.Branch
	} else if err := r.enter(PhaseBranchCreate); err != nil {
		return err
	}
	worktree := "" // the run's checkout: the agent's project root, never the developer's
	var wgr *GitRepo
	var appr *approvals // trusted record of what the approvals mean, byte for byte
	ledger := Ledger{Base: baseSHA}
	if cont != nil {
		ledger.Proposals = slices.Clone(cont.kept)
	}
	finalStatus := persistence.RunStatusFailed
	if cont != nil { // a continuation that fails to get going leaves the run interrupted, not failed: it can be continued or recovered
		finalStatus = persistence.RunStatusKilled
	}
	commitHash := ""
	var evidenceErrs []string            // what could not be written for the commit, if anything
	var initiator *certificate.Initiator // the signed request of whoever started the run, if they signed
	var display *monitor.Display
	var sup *policy.Supervisor // drift supervision, from RUN SETUP on

	defer func() {
		if runErr != nil && finalStatus == persistence.RunStatusSuccess {
			finalStatus = persistence.RunStatusFailed
		}
		var evidenceErr error // reported, but the delivered commit keeps its status
		if len(evidenceErrs) > 0 && opts.RequireEvidence {
			evidenceErr = fmt.Errorf("%w: %s", ErrEvidenceIncomplete, strings.Join(evidenceErrs, "; "))
		}
		endTime := time.Now()
		if err := r.store.FinishRun(run.ID, finalStatus, endTime, commitHash); err != nil {
			finalStatus = persistence.RunStatusFailed
			runErr = errors.Join(runErr, err)
			if err := r.store.FinishRun(run.ID, finalStatus, endTime, commitHash); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
		barrier.Hit(barrier.DBCompleted)
		summary := RunSummary{RunID: run.ID, CaseID: caseID, FinalStatus: finalStatus, CommitHash: commitHash, EndTime: endTime,
			EvidenceErrors: evidenceErrs}
		if commitHash != "" {
			summary.Outcome = OutcomeCertified
			if len(evidenceErrs) > 0 {
				summary.Outcome = OutcomeWithoutEvidence
			}
		}
		if sup != nil {
			rep := sup.Report()
			summary.Drift = &rep
		}
		if err := writeSummary(r.wsDir, summary); err != nil {
			finalStatus = persistence.RunStatusFailed
			runErr = errors.Join(runErr, fmt.Errorf("write run summary: %w", err))
			if err := r.store.FinishRun(run.ID, finalStatus, endTime, commitHash); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
		if err := r.enter(PhaseBranchRestore); err != nil {
			runErr = errors.Join(runErr, err)
		}
		if err := r.audit(run.ID, "run_path", map[string]any{"phases": r.path}); err != nil {
			runErr = errors.Join(runErr, err)
		}
		if worktree != "" {
			if finalStatus == persistence.RunStatusSuccess {
				// The deliverable is the run branch; the worktree was only scaffolding.
				if err := removeWorktree(project.SourcePath, worktree); err != nil {
					obs.Log.Warn("remove run worktree", "path", worktree, "err", err)
				}
			} else {
				fmt.Fprintf(os.Stdout, "   🔎 Worktree kept for inspection: %s\n", worktree)
			}
		}
		if finalStatus != persistence.RunStatusSuccess {
			runErr = errors.Join(runErr, fmt.Errorf("run #%d finished with status %s: %w", run.ID, finalStatus, ErrRunNotSuccessful))
		} else if evidenceErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("run #%d: %w", run.ID, evidenceErr))
		}
		if display != nil {
			display.Final(fmt.Sprintf("Run #%d %s", run.ID, finalStatus))
		}
	}()

	if cont != nil { // the run's own worktree, already checked to hold exactly the approved state
		worktree, appr = cont.worktree, cont.appr
		fmt.Fprintf(os.Stdout, "   🌿 Run branch %s in worktree %s (continued)\n", runBranch, worktree)
	} else if gr != nil {
		// A workspace-local run ID does not prove ownership of an existing ref.
		if gr.BranchExists(runBranch) {
			return fmt.Errorf("run branch %q already exists; inspect and preserve it before retrying", runBranch)
		}
		wt := filepath.Join(r.wsDir, "worktrees", fmt.Sprintf("run-%d", run.ID))
		if err := addWorktree(project.SourcePath, wt, runBranch, baseSHA); err != nil {
			return fmt.Errorf("create run worktree: %w", err)
		}
		worktree = wt
		if wgr, err = OpenGitRepo(worktree); err != nil {
			return fmt.Errorf("open run worktree: %w", err)
		}
		if appr, err = newApprovals(wgr, baseSHA); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "   🌿 Run branch %s in worktree %s\n", runBranch, worktree)
	}
	if cont != nil {
		// The run is reopened and the continuation put on its chain before anything else happens: from here on it is
		// a run that is going again, and the chain says from which state (the chain head) and under which terms.
		if err := r.store.ReopenRun(run.ID); err != nil {
			return err
		}
		initiator = cont.history.Initiator // the signed request of whoever started the run is kept, never signed again
		if err := r.audit(run.ID, "run_resumed", map[string]any{
			"segment": cont.segment, "generation": cont.chainHead, "approvals": approvalsOf(cont.history.Approvals),
			"next_seq": cont.lastSeq + 1, "policy": cont.history.PolicyDigest, "plan_digest": cont.history.Bound.PlanDigest,
			"undecided_requests": cont.undecided, "ack_drift": cont.ackDrift, "elapsed_secs": int(cont.elapsed.Seconds()), "context": cont.contextKind,
		}); err != nil {
			return fmt.Errorf("audit run_resumed: %w", err)
		}
		barrier.Hit(barrier.ResumeStarted)
	} else {
		// The terms this run starts under, saved for a continuation (ADR 0005): its options and its plan now, its policy
		// when it is read (below). A run that cannot save them still runs; it simply cannot be continued, only recovered.
		if b, err := json.Marshal(optionsToSave(opts)); err == nil {
			if err := saveRunFile(savedOptionsPath(r.wsDir, run.ID), b); err != nil {
				obs.Log.Warn("save run options: the run cannot be continued", "err", err)
			}
		}
		if opts.Plan != nil && len(opts.Plan.Raw) > 0 {
			if err := saveRunFile(savedPlanPath(r.wsDir, run.ID), opts.Plan.Raw); err != nil {
				obs.Log.Warn("save run plan: the run cannot be continued", "err", err)
			}
		}
		// Provenance: what this run started from, before the agent runs.
		bound := map[string]any{
			"type": "run_bound", "base_sha": baseSHA, "branch": runBranch,
			"worktree": worktree, "topology_version": topoVersion,
		}
		if opts.Plan != nil {
			bound["plan_digest"], bound["blueprint_hash"] = opts.Plan.Digest, opts.Plan.BlueprintHash
		}
		if harness != "" {
			bound["harness"] = harness
		}
		if haltedRun != 0 {
			bound["ack_drift"] = haltedRun
		}
		if err := r.audit(run.ID, "run_bound", bound); err != nil {
			return fmt.Errorf("audit run_bound: %w", err)
		}
		if opts.SignKey != "" { // the person who starts the run proves who they are, before any work is done
			var digest string
			if opts.Plan != nil {
				digest = opts.Plan.Digest
			}
			sig, err := sshsig.SignContext(ctx, opts.SignKey, certificate.InitiatorNamespace,
				certificate.InitiatorText(certificate.Predicate{Run: run.ID, BaseCommit: baseSHA, PlanDigest: digest}, opts.SignAs))
			if err != nil {
				return fmt.Errorf("sign as the run's initiator: %w", err)
			}
			initiator = &certificate.Initiator{Principal: opts.SignAs, Signature: base64.StdEncoding.EncodeToString(sig)}
			if err := r.audit(run.ID, "initiator_signed", map[string]any{"principal": opts.SignAs, "signature": initiator.Signature}); err != nil {
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

	}

	// ── RUN SETUP ─────────────────────────────────────────────────────────────
	// Acquire a shared advisory flock on the key file for the duration of this
	// run so that 'staircase secret rotate' (which holds an exclusive lock)
	// cannot replace the key while decryption is in progress (CHECK 4.3.3).
	keyLockF, err := os.Open(filepath.Join(r.wsDir, crypto.KeyFile))
	if err != nil {
		return fmt.Errorf("load workspace key: open for lock: %w", err)
	}
	defer func() { _ = wslock.Unlock(keyLockF.Fd()); _ = keyLockF.Close() }()
	if err := wslock.LockShared(keyLockF.Fd()); err != nil {
		return fmt.Errorf("load workspace key: acquire lock: %w", err)
	}

	aesKey, err := crypto.LoadKey(r.wsDir)
	if err != nil {
		return fmt.Errorf("load workspace key: %w", err)
	}

	// Per-project HMAC secret for authenticating webhook approvals. When a
	// webhook is configured without a secret the channel is unauthenticated -
	// warn loudly so operators know to store one under __webhook_hmac_secret__.
	webhookSecret, err := r.loadWebhookSecret(caseRec.ProjectID, aesKey)
	if err != nil && project.WebhookURL != "" { // a stored secret we cannot read is not "no secret"
		return fmt.Errorf("webhook secret: %w", err)
	}
	if project.WebhookURL != "" && len(webhookSecret) == 0 {
		obs.Log.Warn(`webhook approvals are UNAUTHENTICATED - store an HMAC secret to prevent forged approvals: printf '%s' "$SECRET" | staircase secret set __webhook_hmac_secret__ --project <id>`,
			"project_id", caseRec.ProjectID)
	}

	tmpDir := filepath.Join(r.wsDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}
	// One read of policy.json: the rules, the signature check and the digest the
	// certificate names all come from the same bytes. Fail closed: a broken or
	// tampered policy never runs as "no policy".
	var snap *policy.Snapshot
	if cont != nil { // the policy the run started under, never today's file
		snap = cont.policy
	} else {
		if snap, err = policy.LoadSnapshot(r.wsDir); err != nil {
			return fmt.Errorf("load policy: %w", err)
		}
		if len(snap.Raw) > 0 { // the exact bytes it decides under, for a continuation
			if err := saveRunFile(savedPolicyPath(r.wsDir, run.ID), snap.Raw); err != nil {
				obs.Log.Warn("save run policy: the run cannot be continued", "err", err)
			}
		}
		// The policy this run decides under, on the chain, so evidence made later (a
		// recovery) names it and not whatever policy.json holds by then.
		if err := r.audit(run.ID, "policy_snapshot", map[string]any{"digest": snap.Digest, "signed": snap.Signed}); err != nil {
			return fmt.Errorf("audit policy_snapshot: %w", err)
		}
	}
	policyEngine := snap.Engine
	limits := policyEngine.Limits.Limits // the drift-supervision limits of the policy …
	if opts.Plan != nil {
		limits = limits.Tighter(opts.Plan.Limits) // … and of the plan (its blueprint's)
	}
	scope, maxFiles, err := r.driftScope(caseID, opts.Plan)
	if err != nil {
		return err
	}
	limits = limits.Tighter(plan.Limits{MaxFilesChanged: maxFiles})
	if opts.ApproveInScope && !opts.ApproveOnEvidence { // evidence replaces the spot checks
		limits = limits.Tighter(plan.Limits{CheckpointEvery: taskCheckpointEvery}) // spot checks inside the task
	}
	sup = policy.NewSupervisor(scope, limits)
	if cont != nil { // the supervisor stands where it stood: every proposal of the earlier segments replayed
		for _, d := range cont.decisions {
			sup.Replay(d.Paths, d.Approved, d.Source == "operator")
		}
	}
	var runDeadline <-chan time.Time
	waitCtx := ctx // a person's answer is only good while this run is: its limit ends the wait
	if limits.MaxRunSecs > 0 {
		remaining := time.Duration(limits.MaxRunSecs) * time.Second
		if cont != nil { // the limit is for the whole run: what earlier segments used is spent (prepareContinuation refused a run with none left)
			remaining -= cont.elapsed
		}
		runDeadline = time.After(remaining)
		var cancelWait context.CancelFunc
		waitCtx, cancelWait = context.WithTimeout(ctx, remaining)
		defer cancelWait()
	}
	if snap.Digest != "" && !snap.Signed {
		obs.Log.Warn("policy.json is unsigned - run 'staircase policy sign' to enable tamper detection")
	}

	approvalSrv, err := startApprovalServer(ctx, opts)
	if err != nil {
		return err
	}
	var debugLog io.Writer
	if opts.Debug {
		if f := r.openDebugLog(run.ID); f != nil {
			debugLog = f
			defer func() { _ = f.Close() }()
		}
	}

	if approvalSrv != nil { // the hub (staircase serve) finds this run through its file
		sess := approvalhttp.Session{Name: fmt.Sprintf("%s, run #%d", project.Name, run.ID),
			URL: "http://" + approvalSrv.ListenAddr(), Token: approvalSrv.Token()}
		if reg, err := approvalhttp.Register(r.wsDir, sess); err == nil {
			defer reg.Release()
		}
	}
	tracker := monitor.NewTracker(run.ID, caseID, project.Name, gitBranch)
	if cont != nil { // tokens and cost used by the earlier segments count against the budget cap
		for _, u := range cont.usage {
			tracker.Record(u.Agent, u.Model, u.In, u.Out)
		}
	}
	_, budgetCap, _ := r.store.GetProjectConfig(caseRec.ProjectID)
	display = monitor.NewDisplay(tracker, budgetCap)
	display.Render()

	renderTicker := time.NewTicker(500 * time.Millisecond)
	defer renderTicker.Stop()

	// ── AGENT_START ───────────────────────────────────────────────────────────
	if err := r.enter(PhaseAgentStart); err != nil {
		return err
	}
	if opts.Agent == nil {
		return errors.New("no agent to run")
	}
	// The decision loop reads proposals, usage and the agent's end from these.
	proposals := make(chan proposal)
	usage := make(chan Usage)
	done := make(chan error, 1)
	var agentDone <-chan error = done
	host := &agentHost{store: r.store, runID: run.ID, projectID: caseRec.ProjectID, aesKey: aesKey, debug: debugLog}
	delivered := host.deliveredSecrets // secret values handed to the agent, for scrubbing
	agentCtx, cancelAgent := context.WithCancel(ctx)
	defer cancelAgent()
	stopped := make(chan struct{})
	env := &AgentEnv{Session: agentSession(cont), Continuation: contBrief(cont), Worktree: worktree, AllowShell: opts.AllowShellExec, Sandbox: opts.Sandbox, Workspace: r.wsDir, Checks: opts.Checks, CheckTimeout: opts.CheckTimeout, proposals: proposals, usage: usage, host: host}
	val := opts.Validator
	if val != nil {
		if val.Chat == nil {
			val.Chat = &llm.Router{Secret: host.secret}
		}
		if opts.Plan != nil {
			val.brief = opts.Plan.Brief()
		}
	}
	var evGate *evidenceGate
	if opts.ApproveOnEvidence {
		if len(opts.Checks) == 0 && val == nil {
			return errors.New("--approve-on-evidence needs evidence to decide on: name a --check and/or a --validator")
		}
		if val != nil {
			val.noSampling = true // the evidence replaces the validator's own sampling
		}
		evGate = &evidenceGate{approvals: appr, checks: opts.Checks, sandbox: opts.Sandbox, wsDir: r.wsDir, timeout: opts.CheckTimeout}
	}
	signing := &decisionSigning{runID: run.ID, require: opts.RequireSignedApprovals, signKey: opts.SignKey, signAs: opts.SignAs}
	if _, err := os.Stat(filepath.Join(r.wsDir, governance.AllowedSigners)); err == nil {
		signing.signers = filepath.Join(r.wsDir, governance.AllowedSigners)
	}
	if opts.RequireSignedApprovals && signing.signers == "" {
		return errors.New("--require-signed-approvals needs the trusted signers: an allowed_signers file in the workspace (staircase governance use, or copy git's allowed_signers there)")
	}
	// askHuman shows a proposal to the operator: approval API, webhook or TUI.
	askHuman := func(req domain.YieldRequest, signText string) domain.YieldResponse {
		display.Pause()
		defer display.Resume()
		wait := waitCtx
		if req.ActionType == domain.ActionFinalReview { // after the agent: only cancellation ends this wait
			wait = ctx
		}
		ended := func() domain.YieldResponse {
			return domain.Decide(false, "the run ended while this waited for a decision")
		}
		var resp domain.YieldResponse
		switch {
		case approvalSrv != nil:
			id, ch := approvalSrv.PendSigned(req, signText)
			select {
			case resp = <-ch:
			case <-wait.Done():
				approvalSrv.Withdraw(id)
				return ended()
			}
		case project.WebhookURL != "":
			resp = sendWebhookYield(wait, project.WebhookURL, webhookSecret, req)
		default:
			resp = tui.RunYieldTUI(wait, req) // ends with the run, or on Ctrl-C
		}
		if wait.Err() != nil { // an answer that arrives after the run ended counts for nothing
			return ended()
		}
		return resp
	}
	go func() {
		defer close(stopped)
		done <- runAgent(agentCtx, opts.Agent, env)
	}()
	// stopAgent ends the agent and returns once it has stopped (bounded).
	stopAgent := func() {
		cancelAgent()
		select {
		case <-stopped:
		case <-time.After(lostGrace):
			// Nothing more can be done in-process; the run ends without it.
			obs.Log.Error("agent did not stop after cancellation", "grace", lostGrace, "run_id", run.ID)
			_ = r.audit(run.ID, "agent_unresponsive", map[string]any{"grace_seconds": lostGrace.Seconds()})
		}
	}
	agentFinished := false
	defer func() {
		if !agentFinished {
			stopAgent()
		}
	}()

	// ── AGENT_LOOP ────────────────────────────────────────────────────────────
	if err := r.enter(PhaseAgentLoop); err != nil {
		return err
	}
	if err := r.store.UpdateCaseStatus(caseID, persistence.CaseStatusRunning); err != nil {
		return fmt.Errorf("mark case running: %w", err)
	}

	if opts.Signal != nil {
		if opts.Signal.Eval == nil {
			if opts.Signal.URL != "" { // a local Laya or TypeSafe: its own shape, a key only if it has one
				key, _ := host.secret("SIGNAL_API_KEY")
				opts.Signal.Eval = &signal.Client{Model: opts.Signal.Model, Key: key, BaseURL: opts.Signal.URL, API: signal.SystemOne}
			} else if key, err := host.secret("LLM_GATEWAY_API_KEY"); err == nil && key != "" {
				base, _ := host.secret("LLM_GATEWAY_URL") // as for the gateway's models (llm.New)
				opts.Signal.Eval = &signal.Client{Model: opts.Signal.Model, Key: key, BaseURL: strings.TrimSuffix(strings.TrimSuffix(base, "/"), "/v1")}
			}
		}
		if opts.Plan != nil {
			opts.Signal.brief = opts.Plan.Brief()
		}
	}
	if cont != nil { // the validator and the deciders continue from the counts the chain recorded
		val.restore(cont.validator)
	}
	dec := &deciders{sign: signing, redact: func(s string) string { return redact(s, delivered()) }, signal: opts.Signal, task: (opts.ApproveInScope || opts.ApproveOnEvidence) && opts.Agreed != "", evidence: evGate, approvals: appr, drift: sup, policy: policyEngine, validator: val, askHuman: askHuman, waitCtx: waitCtx, ctx: ctx,
		display: display, tracker: tracker,
		audit: func(event string, fields map[string]any) error { return r.audit(run.ID, event, fields) }}
	if cont != nil {
		dec.total, dec.autoApproved, dec.taskApproved = cont.lastSeq, cont.autoApproved, cont.taskApproved
	}

	processExited := func(procErr error) {
		agentFinished = true
		if procErr != nil {
			runErr = fmt.Errorf("agent: %w", procErr)
			finalStatus = persistence.RunStatusFailed
		} else {
			finalStatus = persistence.RunStatusSuccess
		}
	}
	cancelled := func() {
		stopAgent()
		agentFinished = true
		finalStatus = persistence.RunStatusKilled
	}
	// overBudget kills the run once the model use so far (agents' and the
	// validator's) exceeds the project's budget cap.
	overBudget := func() bool {
		if !display.BudgetExceeded() {
			return false
		}
		fmt.Fprintf(os.Stdout, "\n⚠️  Budget cap exceeded - killing run #%d\n", run.ID)
		cancelled()
		return true
	}
	driftHalt := func(reason string) {
		sup.Halt(reason)
		_ = r.audit(run.ID, "drift_halt", map[string]any{"reason": reason})
		fmt.Fprintf(os.Stdout, "\n🧭 Drift: %s - halting run #%d\n", tui.Safe(reason), run.ID)
		cancelled()
	}

runLoop:
	for {
		select {
		case u := <-usage:
			tracker.Record(u.Agent, u.Model, u.InputTokens, u.OutputTokens)
			display.AddActivity(fmt.Sprintf("%-14s step %d", u.Agent, tracker.Totals().Steps))
			if overBudget() {
				break runLoop
			}

		case <-renderTicker.C:
			display.Render()

		case p := <-proposals:
			if p.req.ReviewAfter { // the changes already happened: decide exactly them
				if !reviewAfter(&p, appr, host) {
					continue
				}
			}
			// CHECK 4.4.3 / 7.4.2: scrub delivered secret values from all
			// operator-visible fields before any HITL presentation path.
			req := scrubSecrets(p.req, delivered())
			// Child span of the run: its duration is the yield→decision
			// latency, i.e. how long the human (or policy) took to decide.
			_, yieldSpan := obs.Tracer.Start(ctx, "staircase.yield",
				trace.WithAttributes(
					attribute.String("staircase.agent", req.AgentName),
					attribute.String("staircase.action_type", req.ActionType)))
			rl := dec.decide(waitCtx, &req) // checks, models, signals and signing end with the run's limit too
			barrier.Hit(barrier.DecisionMade)
			// An approval is consumed when it is on the audit chain (journal first, then the chain,
			// then the answer to the agent): the last expiry check is immediately before that append,
			// and once the run is over nothing is consumed, however it was decided.
			expire := func() {
				rl.resp, rl.next, rl.evidence = domain.Decide(false, "the run ended before this was decided"), nil, nil
				rl.source, rl.signed = "orchestrator", nil
			}
			if rl.resp.Approved && waitCtx.Err() != nil {
				expire()
			}
			journaled := false
			if rl.resp.Approved && rl.next != nil {
				// The approval is kept before anything else: a run that dies after
				// this point can still be recovered with exactly what it approved.
				if err := appendJournal(r.wsDir, run.ID, req, dec.total, rl.source); err != nil {
					yieldSpan.End()
					p.reply <- decision{resp: domain.Decide(false, "the orchestrator could not keep this approval; the run is stopping")}
					stopAgent()
					agentFinished = true
					finalStatus = persistence.RunStatusFailed
					runErr = fmt.Errorf("journal approval: %w", err)
					break runLoop
				}
				journaled = true
				barrier.Hit(barrier.JournalSynced)
				if waitCtx.Err() != nil { // it ended between the journal and the chain: not consumed
					expire()
				}
			}
			yieldSpan.SetAttributes(
				attribute.Bool("staircase.approved", rl.resp.Approved),
				attribute.String("staircase.decision_source", rl.source))
			yieldSpan.End()
			// Record the decision, with the exact approved content, before the
			// runtime can act on it: an approval that is not on the audit chain
			// is never released (CHECK 7.3.1). An approval is CONSUMED when this entry is on the
			// chain: the run's limit and cancellation are checked under the chain's append lock,
			// immediately before the insert (there is no wait for another writer in between), and an
			// insert that has begun is written, which is the last moment the run's end can matter.
			decidedFor := func() map[string]any {
				decided := yieldDecided(dec.total, rl.source, req, rl.resp, baseSHA, rl.next, rl.drift)
				if st := val.state(); st != nil {
					decided["validator_state"] = st
				}
				if len(rl.files) > 0 {
					decided["paths"] = rl.files // what a proposal touched: a continuation replays the drift supervisor from it
				}
				if journaled && !rl.resp.Approved {
					decided["unconsumed"] = true // its journal line is an authorization that was never consumed
				}
				maps.Copy(decided, rl.signed)
				if rl.evidence != nil {
					decided["evidence"] = rl.evidence
				}
				if rl.source == "operator" {
					decided["decide_ms"], decided["lines"] = rl.decideMS, rl.lines
				}
				return decided
			}
			var auditErr error
			if rl.resp.Approved {
				expired := false
				auditErr = r.auditIf(run.ID, "yield_decided", decidedFor(), func() error {
					barrier.Hit(barrier.AuditAppend)
					if err := waitCtx.Err(); err != nil {
						expired = true
						return err
					}
					return nil
				})
				if expired { // the run ended while this waited for the chain: not consumed, recorded as a rejection
					expire()
					auditErr = r.audit(run.ID, "yield_decided", decidedFor())
				}
			} else {
				auditErr = r.audit(run.ID, "yield_decided", decidedFor())
			}
			if auditErr != nil {
				p.reply <- decision{resp: domain.Decide(false, "the orchestrator could not record this decision; the run is stopping")}
				stopAgent()
				agentFinished = true
				finalStatus = persistence.RunStatusFailed
				runErr = fmt.Errorf("audit yield_decided: %w", auditErr)
				break runLoop
			}
			barrier.Hit(barrier.AuditCommitted)
			d := decision{resp: rl.resp}
			if rl.resp.Approved && rl.next != nil {
				appr.record(rl.next)
				ledger.add(dec.total, rl.source, req.ProposedEdits)
				d.files = rl.next
			} else if req.ReviewAfter && appr != nil { // rejected or refused: undo what the command did
				paths := make([]string, len(req.ProposedEdits))
				for i, e := range req.ProposedEdits {
					paths[i] = e.File
				}
				if err := appr.restore(paths); err != nil {
					d.resp = domain.Decide(false, d.resp.Feedback+"; reverting the changes failed: "+err.Error())
				}
			}
			barrier.Hit(barrier.Consumed)
			p.reply <- d
			if rl.halt {
				driftHalt(fmt.Sprintf("more than %d proposals reached outside the stories' scope", limits.MaxScopeViolations))
				break runLoop
			}
			if !rl.refused {
				sup.Decided(rl.files, rl.resp.Approved, rl.source == "operator", rl.drift)
			}
			if overBudget() { // the validator's model use counts too
				break runLoop
			}

		case procErr := <-agentDone:
			if ctx.Err() != nil { // it stopped because the run was cancelled, which is how the run ended
				cancelled()
				break runLoop
			}
			if waitCtx.Err() != nil { // or because the run's time was up while it waited for a person
				driftHalt(fmt.Sprintf("the run exceeded its %d s limit", limits.MaxRunSecs))
				break runLoop
			}
			processExited(procErr)
			break runLoop

		case <-runDeadline:
			driftHalt(fmt.Sprintf("the run exceeded its %d s limit", limits.MaxRunSecs))
			break runLoop

		case <-ctx.Done():
			cancelled()
			break runLoop

		}
	}

	// ── FINALIZE ──────────────────────────────────────────────────────────────
	if err := r.enter(PhaseFinalize); err != nil {
		return err
	}
	r.reportDrift(run.ID, sup)
	if finalStatus == persistence.RunStatusSuccess && appr != nil {
		ok, err := r.verifyWorktree(run.ID, appr, display)
		if err != nil {
			return err
		}
		if !ok {
			finalStatus = persistence.RunStatusFailed
		}
	}
	stillGoing := func() { // a run cancelled while finalizing delivers nothing
		if finalStatus == persistence.RunStatusSuccess && ctx.Err() != nil {
			finalStatus = persistence.RunStatusKilled
		}
	}
	stillGoing()
	if finalStatus == persistence.RunStatusSuccess && (val != nil && val.unreviewed || dec.taskApproved) {
		approved, err := dec.finalReview(baseSHA, delivered())
		if err != nil {
			return err
		}
		if !approved {
			fmt.Fprintf(os.Stdout, "   ✋ Final review rejected - nothing committed\n")
			finalStatus = persistence.RunStatusFailed
		}
	}
	stillGoing()
	if finalStatus == persistence.RunStatusSuccess && appr != nil && len(appr.files) > 0 {
		chainHead, err := r.store.GetLastEventHash(run.ID)
		if err != nil {
			return err
		}
		prep, err := appr.prepare(commitMessage(run.ID, caseID, opts.Plan, chainHead))
		if err != nil {
			return err
		}
		hash := ""
		if prep != nil {
			// The commit is named on the chain before any branch holds it: if that cannot be
			// recorded, nothing is delivered, and a recovery after a crash knows this commit as
			// the run's own by that name.
			if err := r.audit(run.ID, "commit_prepared", map[string]any{"commit": prep.commit, "base": prep.base, "tree": prep.tree, "branch": runBranch}); err != nil {
				return fmt.Errorf("audit commit_prepared: %w", err)
			}
			barrier.Hit(barrier.CommitPrepared)
			stillGoing() // cancelled after the commit was named: it is not delivered
			if finalStatus == persistence.RunStatusSuccess {
				if err := prep.deliver(runBranch); err != nil {
					return err
				}
				hash = prep.commit
			}
		}
		if hash != "" {
			barrier.Hit(barrier.GitCAS)
			commitHash = hash
			// The commit is made and stays delivered. Evidence that cannot be written
			// is reported, recorded and named in the run summary; with RequireEvidence
			// the run also ends with ErrEvidenceIncomplete.
			ev := evidence{checks: r.runChecks(ctx, run.ID, gr, hash, opts.Checks, opts.Sandbox, opts.CheckTimeout)}
			if ev.ledger, err = r.writeLedger(run.ID, ledger); err != nil {
				fmt.Fprintf(os.Stdout, "   ⚠️  Ledger not written: %v\n", err)
				evidenceErrs = append(evidenceErrs, "ledger: "+err.Error())
			} else if err := attachNote(gr, LedgerNotesRef, LedgerPath(r.wsDir, run.ID), hash); err != nil {
				fmt.Fprintf(os.Stdout, "   ⚠️  Ledger not attached to the commit: %v\n", err)
				evidenceErrs = append(evidenceErrs, "ledger note: "+err.Error())
			}
			ev.policy, ev.initiator = snap.Digest, initiator
			if err := r.certify(run.ID, hash, baseSHA, chainHead, opts.Plan, gr, ev); err != nil {
				fmt.Fprintf(os.Stdout, "   ⚠️  Change certificate not written: %v\n", err)
				_ = r.audit(run.ID, "certificate_failed", map[string]any{"commit": hash, "error": err.Error()})
				evidenceErrs = append(evidenceErrs, "certificate: "+err.Error())
			}
			barrier.Hit(barrier.EvidencePublished)
			if len(evidenceErrs) > 0 {
				_ = r.audit(run.ID, "evidence_failed", map[string]any{"commit": hash, "errors": evidenceErrs})
			}
		}
	}

	return runErr
}

// RunSummary is the on-disk representation of a completed run (CHECK 10.4.1).
type RunSummary struct {
	RunID       int64     `json:"run_id"`
	CaseID      int64     `json:"case_id"`
	FinalStatus string    `json:"final_status"`
	CommitHash  string    `json:"commit_hash,omitempty"`
	EndTime     time.Time `json:"end_time"`
	// Outcome is what a run that made a commit can say about its evidence:
	// OutcomeCertified, or OutcomeWithoutEvidence with EvidenceErrors saying why.
	Outcome        string   `json:"outcome,omitempty"`
	EvidenceErrors []string `json:"evidence_errors,omitempty"`
	// Drift is the run's drift supervision record.
	Drift *policy.DriftReport `json:"drift,omitempty"`
}

// writeSummary writes a RunSummary as summary.json to
// $STAIRCASE_DIR/runs/<run_id>/ (CHECK 10.4.1).
func writeSummary(wsDir string, s RunSummary) error {
	dir := filepath.Join(wsDir, "runs", fmt.Sprintf("%d", s.RunID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir runs: %w", err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	f, err := os.CreateTemp(dir, ".summary-*")
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
	return os.Rename(f.Name(), filepath.Join(dir, "summary.json"))
}

// runGates executes all quality gates and returns an error if any BLOCK gate fails.
// The full gate report is written to stderr.
func (r *Runner) runGates(caseID int64) error {
	report := gate.RunAll(gate.Context{
		CaseID: caseID,
		WsDir:  r.wsDir,
		Store:  r.store,
	})
	if report.Blocking() {
		return fmt.Errorf("quality gate check failed - %d BLOCK failure(s); run 'staircase gate %d' for details",
			report.Summary.Fail, caseID)
	}
	return nil
}

// ─── Git helpers ──────────────────────────────────────────────────────────────

// webhookClient is shared across all webhook calls within a single run.
// It never follows a redirect: the approval must come from the address that was configured.
var webhookClient = &http.Client{Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// loadWebhookSecret returns the decrypted per-project webhook HMAC secret, or
// nil with no error when none is stored - that means the webhook channel runs
// unauthenticated (legacy behaviour, warned about once). A secret that is
// stored but cannot be looked up or decrypted is an error, never "none".
func (r *Runner) loadWebhookSecret(projectID int64, aesKey []byte) ([]byte, error) {
	sec, err := r.store.GetSecret(webhookauth.SecretKeyName, &projectID)
	if err != nil {
		return nil, fmt.Errorf("look up: %w", err)
	}
	if sec == nil {
		return nil, nil
	}
	plaintext, err := crypto.Decrypt(aesKey, sec.EncryptedValue)
	if err != nil {
		return nil, fmt.Errorf("decrypt (was it stored under another workspace key?): %w", err)
	}
	return []byte(plaintext), nil
}

// sendWebhookYield POSTs the yield to the project webhook and returns the
// operator decision. When secret is non-empty the request is HMAC-signed and
// the response signature is verified; an unsigned, mis-signed, stale, or
// otherwise unverifiable response is treated as a rejection so a network
// attacker cannot forge an approval.
func sendWebhookYield(ctx context.Context, webhookURL string, secret []byte, req domain.YieldRequest) domain.YieldResponse {
	reject := func(msg string) domain.YieldResponse {
		return domain.Decide(false, msg)
	}
	// A fresh yield_id makes every request unique, so an approval captured for
	// one request never matches another - not even an identical re-proposal.
	yieldID := rand.Text()
	body, _ := json.Marshal(struct {
		YieldID string `json:"yield_id"`
		domain.YieldRequest
	}{yieldID, req})
	reqHash := sha256Hex(body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		obs.Log.Warn("webhook request build failed - auto-rejecting", "err", err)
		return reject("webhook error: " + err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(webhookauth.HeaderRequestSHA256, reqHash)
	if len(secret) > 0 {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		httpReq.Header.Set(webhookauth.HeaderTimestamp, ts)
		httpReq.Header.Set(webhookauth.HeaderSignature, webhookauth.Sign(secret, ts, body))
	}

	resp, err := webhookClient.Do(httpReq)
	if err != nil {
		obs.Log.Warn("webhook POST failed - auto-rejecting", "err", err)
		return reject("webhook error: " + err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 { // an error page is not an answer, whatever its body says
		obs.Log.Warn("webhook answered with an error status - auto-rejecting", "status", resp.StatusCode)
		return reject(fmt.Sprintf("webhook error: HTTP %d", resp.StatusCode))
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		obs.Log.Warn("webhook response read failed - auto-rejecting", "err", err)
		return reject("webhook read error: " + err.Error())
	}

	// Authenticated channel: the response MUST carry a valid signature.
	if len(secret) > 0 {
		if verr := webhookauth.Verify(secret,
			resp.Header.Get(webhookauth.HeaderTimestamp),
			resp.Header.Get(webhookauth.HeaderSignature),
			respBody, time.Now(), webhookauth.DefaultMaxSkew); verr != nil {
			obs.Log.Error("webhook response signature INVALID - rejecting (possible forgery)", "err", verr)
			return reject("webhook response failed signature verification: " + verr.Error())
		}
	}

	var yieldResp struct {
		domain.YieldResponse
		RequestSHA256 string `json:"request_sha256"`
		YieldID       string `json:"yield_id"`
	}
	if err := json.Unmarshal(respBody, &yieldResp); err != nil {
		obs.Log.Warn("webhook response decode failed - auto-rejecting", "err", err)
		return reject("webhook decode error: " + err.Error())
	}
	// Authenticated channel: the signed body must name the request it answers,
	// or a captured approval could be replayed against another pending yield.
	if len(secret) > 0 && (yieldResp.RequestSHA256 != reqHash || yieldResp.YieldID != yieldID) {
		obs.Log.Error("webhook response answers a different request - rejecting (possible replay)")
		return reject("webhook response does not echo this request's yield_id and request_sha256 (possible replay)")
	}
	if yieldResp.Type == "" {
		yieldResp.Type = "yield_response"
	}
	return yieldResp.YieldResponse
}

func timePtr(t time.Time) *time.Time { return &t }

// scrubSecrets replaces every occurrence of each active secret value with
// "<REDACTED>" across all operator-visible fields before the yield request
// is presented via TUI, webhook, or HTTP approval server (CHECK 4.4.3 / 7.4.2).
// Scrubbing covers: ReasoningTrace, and every proposed edit's File, SearchBlock,
// and ReplaceBlock - agents can embed plaintext secrets in any of these.
func scrubSecrets(req domain.YieldRequest, activeValues []string) domain.YieldRequest {
	if len(activeValues) == 0 {
		return req
	}
	req.ReasoningTrace = redact(req.ReasoningTrace, activeValues)
	for i := range req.ProposedEdits {
		req.ProposedEdits[i].File = redact(req.ProposedEdits[i].File, activeValues)
		req.ProposedEdits[i].SearchBlock = redact(req.ProposedEdits[i].SearchBlock, activeValues)
		req.ProposedEdits[i].ReplaceBlock = redact(req.ProposedEdits[i].ReplaceBlock, activeValues)
	}
	return req
}

// redact removes the delivered secret values from s.
func redact(s string, values []string) string {
	return string(crypto.ScrubBytes([]byte(s), values))
}

// runAgent runs a; a panic becomes the run's error instead of the process's.
func runAgent(ctx context.Context, a Agent, env *AgentEnv) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("agent panicked: %v", p)
		}
	}()
	return a.Run(ctx, env)
}

// lastDriftHalt returns the case's latest run if drift supervision halted it.
func (r *Runner) lastDriftHalt(caseID int64) (int64, error) {
	runs, err := r.store.ListRunsByCase(caseID) // newest first
	if err != nil || len(runs) == 0 {
		return 0, err
	}
	events, err := r.store.ListEventLogs(runs[0].ID)
	if err != nil {
		return 0, err
	}
	for _, e := range events {
		if e.EventType == "drift_halt" {
			return runs[0].ID, nil
		}
	}
	return 0, nil
}

// driftScope is the union of the allowed paths of the case's open stories
// (not yet accepted), from the plan when the run has one, otherwise from the
// stories' own scope; empty means no scope check. maxFiles is the sum of their
// max_files when every open story sets one (0 = no cap).
func (r *Runner) driftScope(caseID int64, pl *plan.Plan) (scope []string, maxFiles int, err error) {
	stories, err := r.store.ListUserStoriesByCase(caseID)
	if err != nil {
		return nil, 0, fmt.Errorf("load stories: %w", err)
	}
	open := map[int64]bool{}
	var planned []plan.Story
	for _, st := range stories {
		open[st.ID] = st.Status != persistence.StoryStatusImplemented
		if pl == nil && st.CustomConfig != "" {
			ps := plan.Story{ID: st.ID}
			if err := json.Unmarshal([]byte(st.CustomConfig), &ps); err != nil {
				return nil, 0, fmt.Errorf("story #%d scope: %w", st.ID, err)
			}
			planned = append(planned, ps)
		}
	}
	if pl != nil {
		planned = pl.Stories
	}
	capped := true
	for _, st := range planned {
		if open[st.ID] {
			scope = append(scope, st.Allow...)
			maxFiles += st.MaxFiles
			capped = capped && st.MaxFiles > 0
		}
	}
	if !capped {
		maxFiles = 0
	}
	return scope, maxFiles, nil
}

// audit appends event to the run's chain; its payload is fields with the
// event's type.
func (r *Runner) audit(runID int64, event string, fields map[string]any) error {
	return r.auditIf(runID, event, fields, func() error { return nil })
}

// auditIf appends an event unless proceed (called under the chain's append lock, right before the
// insert) says not to: then nothing is written and proceed's error is returned.
func (r *Runner) auditIf(runID int64, event string, fields map[string]any, proceed func() error) error {
	payload := map[string]any{"type": event}
	maps.Copy(payload, fields)
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = r.store.AppendEventLogChainedIf(runID, event, string(b), "", proceed)
	return err
}

// reportDrift audits the run's drift report and summarizes it.
func (r *Runner) reportDrift(runID int64, sup *policy.Supervisor) {
	rep := sup.Report()
	_ = r.audit(runID, "drift_report", map[string]any{"report": rep})
	if len(rep.Scope) == 0 && rep.Violations == 0 && rep.Halted == "" {
		return
	}
	halted := ""
	if rep.Halted != "" {
		halted = ", halted: " + rep.Halted
	}
	fmt.Fprintf(os.Stdout, "   🧭 Drift: %d file(s) in scope, %d outside (human overrides: %d), %d scope violation(s)%s\n",
		len(rep.InScope), len(rep.OutOfScope), rep.Overrides, rep.Violations, halted)
}

// verifyWorktree checks that the worktree holds exactly the approved state on
// the base commit - approved paths with their approved bytes, no other change
// in the files or the index, no commits of the agent's own - and audits every
// violation. It reports whether the run may commit.
func (r *Runner) verifyWorktree(runID int64, appr *approvals, display *monitor.Display) (bool, error) {
	violations, err := appr.verify()
	if err != nil {
		return false, err
	}
	for _, v := range violations {
		event := map[string]any{"detail": v.detail}
		if v.file != "" {
			event["file"] = v.file
		}
		_ = r.audit(runID, v.event, event)
		obs.Log.Error("worktree does not match the approvals - refusing to commit",
			"event", v.event, "file", v.file, "detail", v.detail, "run_id", runID)
		display.AddActivity(fmt.Sprintf("%-14s ABORT  %s: %s", "finalize", v.file, v.detail))
	}
	return len(violations) == 0, nil
}

// startApprovalServer starts the local approval API when the run asks for one
// (--approval-port); nil otherwise.
func startApprovalServer(ctx context.Context, opts RunOptions) (*approvalhttp.Server, error) {
	if opts.ApprovalPort <= 0 {
		return nil, nil
	}
	token := opts.ApprovalToken
	if token == "" {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("generate approval token: %w", err)
		}
		token = hex.EncodeToString(raw)
	}
	srv := approvalhttp.NewServer(fmt.Sprintf("127.0.0.1:%d", opts.ApprovalPort), token)
	if err := srv.Start(ctx); err != nil {
		return nil, fmt.Errorf("approval http server: %w", err)
	}
	fmt.Fprintf(os.Stdout, "   🌐 Review in your browser: %s\n", srv.ReviewURL())
	fmt.Fprintf(os.Stdout, "   🌐 Approval API: http://%s/v1/yields\n", srv.ListenAddr())
	fmt.Fprintf(os.Stdout, "   🔑 Approval token: %s\n", token)
	return srv, nil
}

// openDebugLog opens the run's --debug log; nil when it cannot (the run goes on).
func (r *Runner) openDebugLog(runID int64) *os.File {
	logDir := filepath.Join(r.wsDir, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("staircase-debug-run%d.log", runID))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	fmt.Fprintf(os.Stdout, "   🔍 Debug log: %s\n", logPath)
	return f
}

// reviewAfter fills a review-after proposal with the worktree's unapproved
// changes and records it. With nothing to decide it answers the agent right
// away and reports false.
func reviewAfter(p *proposal, appr *approvals, host *agentHost) bool {
	if appr == nil {
		p.reply <- decision{resp: domain.Decide(false, "no worktree to review")}
		return false
	}
	edits, err := appr.worktreeChanges()
	if err != nil {
		p.reply <- decision{resp: domain.Decide(false, "cannot read the worktree: "+err.Error())}
		return false
	}
	if len(edits) == 0 {
		p.reply <- decision{resp: domain.Decide(true, "no file changes to review")}
		return false
	}
	p.req.ProposedEdits = edits
	if err := host.auditRequest(p.req); err != nil {
		why := "the orchestrator could not record this request, so it was not decided: " + err.Error()
		paths := make([]string, len(edits))
		for i, e := range edits {
			paths[i] = e.File
		}
		if rerr := appr.restore(paths); rerr != nil { // what the command changed is put back, as for a rejection
			why += "; reverting the changes failed: " + rerr.Error()
		}
		p.reply <- decision{resp: domain.Decide(false, why)}
		return false
	}
	return true
}
