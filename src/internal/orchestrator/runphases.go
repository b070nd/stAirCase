package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/approvalhttp"
	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/llm"
	"github.com/b070nd/stAirCase/src/internal/monitor"
	"github.com/b070nd/stAirCase/src/internal/obs"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/b070nd/stAirCase/src/internal/signal"
	"github.com/b070nd/stAirCase/src/internal/tui"
	"github.com/b070nd/stAirCase/src/internal/wslock"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// runState is one run, or one segment of it, while it goes: what its phases share. Each phase is a method; run calls them in the
// order of the run's state machine. What a phase sets up for the rest of the run is undone by cleanup, in the reverse order.
type runState struct {
	overlays []sandbox.Overlay    // the run's declared environment (Terms.Visible), as the sandbox shows it to a check
	dirty    *history.SourceDirty // what the source checkout held that the run does not have, when it held something
	r        *Runner
	ctx      context.Context
	caseID   int64
	opts     RunOptions
	cont     *continuation // nil for a new run
	seg      segment       // what this segment does that another would not

	harness, signers string

	// prepare: what the run starts from
	caseRec     *domain.Case
	project     *domain.Project
	gr          *GitRepo
	baseSHA     string
	topoVersion int
	gitBranch   string
	haltedRun   int64

	// open, bind: the run and its checkout
	run          *domain.Run
	releaseOwner func()
	runBranch    string
	worktree     string
	appr         *approvals // the trusted record of what the approvals mean, byte for byte
	ledger       Ledger
	initiator    *certificate.Initiator // the signed request of whoever started the run, if they signed

	// how it ends
	finalStatus  string
	commitHash   string
	evidenceErrs []string // what could not be written for the commit, if anything
	runErr       error    // how the agent's part ended, when not well

	// setup
	aesKey        []byte
	webhookSecret []byte
	snap          *policy.Snapshot
	limits        plan.Limits
	sup           *policy.Supervisor // drift supervision
	waitCtx       context.Context    // a person's answer is only good while this run is: its limit ends the wait
	runDeadline   <-chan time.Time
	approvalSrv   *approvalhttp.Server
	debugLog      io.Writer
	tracker       *monitor.Tracker
	display       *monitor.Display
	renderTicker  *time.Ticker

	// the agent
	proposals     chan proposal
	usage         chan Usage
	agentDone     <-chan error
	host          *agentHost
	stopped       chan struct{}
	cancelAgent   context.CancelFunc
	agentFinished bool
	launch        *history.LaunchClaimed // this segment's launch, once it is on the chain
	agentOutcome  string                 // how the agent ended, once that is known
	agentClass    string                 // the class its own error named (Rejected), "" when it named none
	launchEnded   bool                   // its outcome is recorded (or its recording was tried)
	val           *Validator
	evGate        *evidenceGate
	dec           *deciders

	cleanups []func()
}

// deferred adds f to what cleanup undoes when the run ends.
func (s *runState) deferred(f func()) { s.cleanups = append(s.cleanups, f) }

// cleanup undoes what the phases set up, last first.
func (s *runState) cleanup() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
}

// prepare loads what the run starts from and checks that it may start, making nothing. A dry run stops here (done).
func (s *runState) prepare() (done bool, err error) {
	r, opts, caseID := s.r, s.opts, s.caseID
	// ── Load case + project ───────────────────────────────────────────────────
	if s.caseRec, err = r.store.GetCase(caseID); err != nil {
		return false, fmt.Errorf("load case: %w", err)
	}
	if s.caseRec == nil {
		return false, fmt.Errorf("case %d not found", caseID)
	}
	if s.project, err = r.store.GetProject(s.caseRec.ProjectID); err != nil {
		return false, fmt.Errorf("load project: %w", err)
	}
	if s.project == nil {
		return false, fmt.Errorf("project %d not found", s.caseRec.ProjectID)
	}

	// ── Open git repo (used across the whole run lifecycle) ──────────────────
	if s.project.SourcePath != "" {
		if s.gr, err = OpenGitRepo(s.project.SourcePath); err != nil {
			return false, fmt.Errorf("open git repo: %w", err)
		}
	}

	// ── The declared environment: judged now, before anything is made ─────────
	if s.overlays, err = visibleOverlays(s.project.SourcePath, opts.Terms); err != nil {
		return false, err
	}

	// ── Optional reconcile ────────────────────────────────────────────────────
	if opts.Reconcile && s.project.SourcePath != "" && s.seg.first() {
		result, err := r.Reconcile(s.ctx, caseID, s.project.SourcePath, true)
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
	if s.baseSHA, err = s.seg.base(s); err != nil {
		return false, err
	}

	// ── Resolve topology ──────────────────────────────────────────────────────
	topology, err := r.store.GetLatestTopology(s.project.ID)
	if err != nil {
		return false, fmt.Errorf("load topology: %w", err)
	}
	if topology == nil && s.harness == "" { // a harness brings its own agent: version 0, no topology
		return false, fmt.Errorf("no swarm topology registered for project %q - run 'staircase topology register' first", s.project.Name)
	}
	if topology != nil {
		s.topoVersion = topology.Version
	}
	if opts.Plan != nil {
		s.topoVersion = opts.Plan.TopologyVersion // what runs is the plan, not the latest topology
	}

	// ── Resolve current git branch (capture SHA on detached HEAD) ─────────────
	s.gitBranch = "unknown"
	if s.gr != nil {
		if b, bErr := s.gr.CurrentBranch(); bErr == nil {
			s.gitBranch = b
		}
	}

	// ── Quality gate pre-flight ───────────────────────────────────────────────
	if !opts.SkipGates && s.seg.first() { // a continuation is checked by loadContinuation and the worktree checks, not by the case's gates
		if err := r.runGates(caseID); err != nil {
			return false, err
		}
	}
	if opts.DryRun {
		fmt.Fprintf(os.Stdout, "   [dry-run] case %d would run on a new worktree at %.12s (from %s); nothing was created\n",
			caseID, s.baseSHA, s.gitBranch)
		return true, nil
	}

	// A run halted for drift is reviewed before the case runs again.
	if s.haltedRun, err = r.lastDriftHalt(caseID); err != nil {
		return false, err
	}
	if !s.seg.first() { // the halt that matters is this run's own (below), not a previous run's
		s.haltedRun = 0
	}
	if s.haltedRun != 0 && !opts.AckDrift {
		return false, fmt.Errorf("run #%d of case #%d was halted for drift - review it ('staircase inspect log %d'), then run again with --ack-drift", s.haltedRun, caseID, s.haltedRun)
	}
	return false, nil
}

// open makes the run's record, or takes the continued run's, and owns it for the run's whole life (runowner.go).
func (s *runState) open() error { return s.seg.open(s) }

// finish records how the run ended, whatever happened: its status, the summary, its path, and what becomes of its worktree.
func (s *runState) finish(runErr error) error {
	r, opts, run := s.r, s.opts, s.run
	if runErr != nil && s.finalStatus == persistence.RunStatusSuccess {
		s.finalStatus = persistence.RunStatusFailed
	}
	var evidenceErr error // reported, but the delivered commit keeps its status
	if len(s.evidenceErrs) > 0 && opts.RequireEvidence {
		evidenceErr = fmt.Errorf("%w: %s", ErrEvidenceIncomplete, strings.Join(s.evidenceErrs, "; "))
	}
	endTime := time.Now()
	if err := r.store.FinishRun(run.ID, s.finalStatus, endTime, s.commitHash); err != nil {
		s.finalStatus = persistence.RunStatusFailed
		runErr = errors.Join(runErr, err)
		if err := r.store.FinishRun(run.ID, s.finalStatus, endTime, s.commitHash); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	barrier.Hit(barrier.DBCompleted)
	summary := RunSummary{RunID: run.ID, CaseID: s.caseID, FinalStatus: s.finalStatus, CommitHash: s.commitHash, EndTime: endTime,
		EvidenceErrors: s.evidenceErrs}
	if s.commitHash != "" {
		summary.Outcome = OutcomeCertified
		if len(s.evidenceErrs) > 0 {
			summary.Outcome = OutcomeWithoutEvidence
		}
	}
	if s.sup != nil {
		rep := s.sup.Report()
		summary.Drift = &rep
	}
	if err := writeSummary(r.wsDir, summary); err != nil {
		s.finalStatus = persistence.RunStatusFailed
		runErr = errors.Join(runErr, fmt.Errorf("write run summary: %w", err))
		if err := r.store.FinishRun(run.ID, s.finalStatus, endTime, s.commitHash); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	if err := r.enter(PhaseBranchRestore); err != nil {
		runErr = errors.Join(runErr, err)
	}
	phases := make([]string, len(r.path))
	for i, p := range r.path {
		phases[i] = string(p)
	}
	if err := r.log(run.ID).Append(history.Ended{Phases: phases, Status: s.finalStatus}); err != nil {
		runErr = errors.Join(runErr, err)
	}
	if s.worktree != "" {
		switch s.finalStatus {
		case persistence.RunStatusSuccess:
			// The deliverable is the run branch; the worktree was only scaffolding.
			if err := removeWorktree(s.project.SourcePath, s.worktree); err != nil {
				obs.Log.Warn("remove run worktree", "path", s.worktree, "err", err)
			}
		case persistence.RunStatusAwaitingReview:
			fmt.Fprintf(os.Stdout, "   🔎 Worktree kept until your review: %s\n", s.worktree)
		default:
			fmt.Fprintf(os.Stdout, "   🔎 Worktree kept for inspection: %s\n", s.worktree)
		}
	}
	if s.finalStatus != persistence.RunStatusSuccess && s.finalStatus != persistence.RunStatusAwaitingReview {
		runErr = errors.Join(runErr, fmt.Errorf("run #%d finished with status %s: %w", run.ID, s.finalStatus, ErrRunNotSuccessful))
	} else if evidenceErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("run #%d: %w", run.ID, evidenceErr))
	}
	if s.display != nil {
		s.display.Final(fmt.Sprintf("Run #%d %s", run.ID, s.finalStatus))
	}
	return runErr
}

// bind makes the run's branch and worktree, or takes the continued run's, and puts on its chain what this segment starts from
// and under which terms, before the agent runs.
func (s *runState) bind() error { return s.seg.checkout(s) }

// setup holds the workspace key, reads the policy the run decides under, sets the drift supervisor and the run's time limit,
// and starts what a person decides through and what the run shows.
func (s *runState) setup() error {
	r, opts, run := s.r, s.opts, s.run
	// ── RUN SETUP ─────────────────────────────────────────────────────────────
	// Acquire a shared advisory flock on the key file for the duration of this
	// run so that 'staircase secret rotate' (which holds an exclusive lock)
	// cannot replace the key while decryption is in progress (CHECK 4.3.3).
	keyLockF, err := os.Open(filepath.Join(r.wsDir, crypto.KeyFile))
	if err != nil {
		return fmt.Errorf("load workspace key: open for lock: %w", err)
	}
	s.deferred(func() { _ = wslock.Unlock(keyLockF.Fd()); _ = keyLockF.Close() })
	if err := wslock.LockShared(keyLockF.Fd()); err != nil {
		return fmt.Errorf("load workspace key: acquire lock: %w", err)
	}

	if s.aesKey, err = crypto.LoadKey(r.wsDir); err != nil {
		return fmt.Errorf("load workspace key: %w", err)
	}

	// Per-project HMAC secret for authenticating webhook approvals. When a
	// webhook is configured without a secret the channel is unauthenticated -
	// warn loudly so operators know to store one under __webhook_hmac_secret__.
	s.webhookSecret, err = r.loadWebhookSecret(s.caseRec.ProjectID, s.aesKey)
	if err != nil && s.project.WebhookURL != "" { // a stored secret we cannot read is not "no secret"
		return fmt.Errorf("webhook secret: %w", err)
	}
	if s.project.WebhookURL != "" && len(s.webhookSecret) == 0 {
		obs.Log.Warn(`webhook approvals are UNAUTHENTICATED - store an HMAC secret to prevent forged approvals: printf '%s' "$SECRET" | staircase secret set __webhook_hmac_secret__ --project <id>`,
			"project_id", s.caseRec.ProjectID)
	}

	tmpDir := filepath.Join(r.wsDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}
	if s.snap, err = s.seg.policy(s); err != nil {
		return err
	}
	s.limits = s.snap.Engine.Limits.Limits // the drift-supervision limits of the policy …
	if opts.Plan != nil {
		s.limits = s.limits.Tighter(opts.Plan.Limits) // … and of the plan (its blueprint's)
	}
	scope, maxFiles, err := r.driftScope(s.caseID, opts.Plan)
	if err != nil {
		return err
	}
	s.limits = s.limits.Tighter(plan.Limits{MaxFilesChanged: maxFiles})
	if opts.ApproveInScope && !opts.ApproveOnEvidence { // evidence replaces the spot checks
		s.limits = s.limits.Tighter(plan.Limits{CheckpointEvery: taskCheckpointEvery}) // spot checks inside the task
	}
	s.sup = policy.NewSupervisor(scope, s.limits)
	s.waitCtx = s.ctx
	if s.limits.MaxRunSecs > 0 {
		remaining := time.Duration(s.limits.MaxRunSecs)*time.Second - s.seg.spent()
		s.runDeadline = time.After(remaining)
		var cancelWait context.CancelFunc
		s.waitCtx, cancelWait = context.WithTimeout(s.ctx, remaining)
		s.deferred(cancelWait)
	}
	if s.snap.Digest != "" && !s.snap.Signed {
		obs.Log.Warn("policy.json is unsigned - run 'staircase policy sign' to enable tamper detection")
	}

	if s.approvalSrv, err = startApprovalServer(s.ctx, opts); err != nil {
		return err
	}
	if opts.Debug {
		if f := r.openDebugLog(run.ID); f != nil {
			s.debugLog = f
			s.deferred(func() { _ = f.Close() })
		}
	}

	if s.approvalSrv != nil { // the hub (staircase serve) finds this run through its file
		sess := approvalhttp.Session{Name: fmt.Sprintf("%s, run #%d", s.project.Name, run.ID),
			URL: "http://" + s.approvalSrv.ListenAddr(), Token: s.approvalSrv.Token()}
		if reg, err := approvalhttp.Register(r.wsDir, sess); err == nil {
			s.deferred(reg.Release)
		}
	}
	s.tracker = monitor.NewTracker(run.ID, s.caseID, s.project.Name, s.gitBranch)
	s.seg.carry(s.sup, s.tracker)
	_, budgetCap, _ := r.store.GetProjectConfig(s.caseRec.ProjectID)
	s.display = monitor.NewDisplay(s.tracker, budgetCap)
	s.display.Render()

	s.renderTicker = time.NewTicker(500 * time.Millisecond)
	s.deferred(s.renderTicker.Stop)
	return nil
}

// startAgent starts the agent in the run's worktree, with what decides its proposals.
func (s *runState) startAgent() error {
	r, opts, cont, run := s.r, s.opts, s.cont, s.run
	// ── AGENT_START ───────────────────────────────────────────────────────────
	if err := r.enter(PhaseAgentStart); err != nil {
		return err
	}
	if opts.Agent == nil {
		return errors.New("no agent to run")
	}
	// The decision loop reads proposals, usage and the agent's end from these.
	s.proposals = make(chan proposal)
	s.usage = make(chan Usage)
	done := make(chan error, 1)
	s.agentDone = done
	s.host = newAgentHost(r.store, run.ID, s.caseRec.ProjectID, s.aesKey, s.debugLog)
	agentCtx, cancelAgent := context.WithCancel(s.ctx)
	s.cancelAgent = cancelAgent
	s.deferred(cancelAgent)
	s.stopped = make(chan struct{})
	env := &AgentEnv{Session: agentSession(cont), Continuation: contBrief(cont), Brief: awayBrief(opts.Away), Worktree: s.worktree, AllowShell: opts.AllowShellExec, Sandbox: opts.Sandbox, Instructions: opts.Instructions, Unreadable: opts.Unreadable, Environment: s.overlays, Workspace: r.wsDir, Checks: opts.Checks, CheckTimeout: opts.CheckTimeout, proposals: s.proposals, usage: s.usage, host: s.host}
	if len(opts.ValidatorModels) > 0 {
		s.val = &Validator{Models: opts.ValidatorModels, Chat: opts.ValidatorChat}
		if s.val.Chat == nil {
			s.val.Chat = &llm.Router{Secret: s.host.secret}
		}
		if opts.Plan != nil {
			s.val.brief = opts.Plan.Brief()
		}
	}
	if opts.ApproveOnEvidence {
		if s.val != nil {
			s.val.noSampling = true // the evidence replaces the validator's own sampling
		}
		s.evGate = &evidenceGate{approvals: s.appr, checks: opts.Checks, sandbox: opts.Sandbox, wsDir: r.wsDir, timeout: opts.CheckTimeout, overlays: s.overlays}
	}
	signing := &decisionSigning{runID: run.ID, require: opts.RequireSignedApprovals, signKey: opts.SignKey, signAs: opts.SignAs, signers: s.signers}
	// askHuman puts a proposal to a person (personFor) while the run's display waits. The wait ends with the run's limit, a final
	// review's (after the agent) only with cancellation; an answer that arrives after that counts for nothing.
	who := opts.Person
	if who == nil {
		who = personFor(s.approvalSrv, s.project.WebhookURL, s.webhookSecret)
	}
	askHuman := func(req domain.YieldRequest, signText string) domain.YieldResponse {
		s.display.Pause()
		defer s.display.Resume()
		wait := s.waitCtx
		if req.ActionType == domain.ActionFinalReview {
			wait = s.ctx
		}
		resp := who.Ask(wait, req, signText)
		if wait.Err() != nil {
			return domain.Decide(false, "the run ended while this waited for a decision")
		}
		return resp
	}
	if err := s.claimLaunch(); err != nil {
		return err
	}
	s.deferred(s.endLaunch) // after the agent is stopped (below): how it ended, whatever ends the run
	go func() {
		defer close(s.stopped)
		done <- runAgent(agentCtx, opts.Agent, env)
	}()
	s.deferred(func() {
		if !s.agentFinished {
			s.stopAgent()
		}
	})

	// ── AGENT_LOOP ────────────────────────────────────────────────────────────
	if err := r.enter(PhaseAgentLoop); err != nil {
		return err
	}
	if err := r.store.UpdateCaseStatus(s.caseID, persistence.CaseStatusRunning); err != nil {
		return fmt.Errorf("mark case running: %w", err)
	}

	var sig *Signal
	if opts.SignalModel != "" {
		sig = &Signal{Model: opts.SignalModel, URL: opts.SignalURL, Eval: opts.SignalEval}
		if sig.Eval == nil {
			if sig.URL != "" { // a local Laya or TypeSafe: its own shape, a key only if it has one
				key, _ := s.host.secret("SIGNAL_API_KEY")
				sig.Eval = &signal.Client{Model: sig.Model, Key: key, BaseURL: sig.URL, API: signal.SystemOne}
			} else if key, err := s.host.secret("LLM_GATEWAY_API_KEY"); err == nil && key != "" {
				base, _ := s.host.secret("LLM_GATEWAY_URL") // as for the gateway's models (llm.New)
				sig.Eval = &signal.Client{Model: sig.Model, Key: key, BaseURL: strings.TrimSuffix(strings.TrimSuffix(base, "/"), "/v1")}
			}
		}
		if opts.Plan != nil {
			sig.brief = opts.Plan.Brief()
		}
	}
	delivered := s.host.deliveredSecrets
	s.dec = (&deciders{sign: signing, redact: func(str string) string { return redact(str, delivered()) }, signal: sig, evidence: s.evGate, approvals: s.appr, drift: s.sup, policy: s.snap.Engine, validator: s.val, askHuman: askHuman, waitCtx: s.waitCtx, ctx: s.ctx,
		display: s.display, tracker: s.tracker,
		audit: r.log(run.ID).Append}).under(opts.Terms)
	s.seg.resume(s.dec, s.val)
	return nil
}

// claimLaunch puts the agent's launch on the chain before its process starts (Cloudroom's claim before launch): its attempt
// number among the run's launches and a generation of its own, which its outcome names. A launch that cannot be claimed does not
// happen. A process lost after this leaves the claim open, and a restart then records the outcome as unknown and parks the run.
func (s *runState) claimLaunch() error {
	h, err := history.Read(s.r.store, s.run.ID)
	if err != nil {
		return err
	}
	gen, err := newOpID()
	if err != nil {
		return fmt.Errorf("a generation for the agent's launch: %w", err)
	}
	claim := history.LaunchClaimed{Attempt: len(h.Launches) + 1, Generation: gen, Segment: h.Segment}
	if err := s.r.log(s.run.ID).Append(claim); err != nil {
		return fmt.Errorf("audit launch_claimed: %w", err)
	}
	s.launch = &claim
	barrier.Hit(barrier.LaunchClaimed)
	return nil
}

// endLaunch records how the claimed launch ended, once. Its agent has stopped, or did not stop when asked (unresponsive). An
// outcome that cannot be written leaves the claim open, which a restart treats as a lost launch: the safe side.
func (s *runState) endLaunch() {
	if s.launch == nil || s.launchEnded {
		return
	}
	s.launchEnded = true
	s.endedAs(history.LaunchStopped) // stopped by the run, when nothing said otherwise
	barrier.Hit(barrier.AgentEnded)
	if err := s.r.log(s.run.ID).Append(history.LaunchEnded{Attempt: s.launch.Attempt, Generation: s.launch.Generation,
		Outcome: s.agentOutcome, Class: s.launchClass()}); err != nil {
		obs.Log.Warn("audit launch_ended: a restart will find the launch with no outcome", "run_id", s.run.ID, "err", err)
	}
}

// launchClass is why the launch did not end well, apart from its checks: the class the agent's own error named, else records of
// the launch that could not be written (the launch ended in a way the run established), else the class its outcome implies.
func (s *runState) launchClass() string {
	switch {
	case s.agentClass != "":
		return s.agentClass
	case s.host.storageFailed.Load() && (s.agentOutcome == history.LaunchExited || s.agentOutcome == history.LaunchFailed || s.agentOutcome == history.LaunchStopped):
		return history.ClassStorage
	}
	return history.ClassOf(s.agentOutcome)
}

// endedAs is how the agent ended, unless that is known already.
func (s *runState) endedAs(outcome string) {
	if s.agentOutcome == "" {
		s.agentOutcome = outcome
	}
}

// stopAgent ends the agent and returns once it has stopped (bounded).
func (s *runState) stopAgent() {
	s.cancelAgent()
	select {
	case <-s.stopped:
		s.endedAs(history.LaunchStopped)
	case <-time.After(lostGrace):
		// Nothing more can be done in-process; the run ends without it.
		s.endedAs(history.LaunchUnresponsive)
		obs.Log.Error("agent did not stop after cancellation", "grace", lostGrace, "run_id", s.run.ID)
		_ = s.r.log(s.run.ID).Append(history.AgentUnresponsive{GraceSeconds: lostGrace.Seconds()})
	}
}

func (s *runState) processExited(procErr error) {
	s.agentFinished = true
	if procErr != nil {
		s.endedAs(history.LaunchFailed)
		var le *LaunchError
		if errors.As(procErr, &le) {
			s.agentClass = le.Class
		}
	}
	s.endedAs(history.LaunchExited)
	if procErr != nil {
		s.keepStderr(procErr)
		s.runErr = fmt.Errorf("agent: %w", procErr)
		s.finalStatus = persistence.RunStatusFailed
	} else {
		s.finalStatus = persistence.RunStatusSuccess
	}
}

// keepStderr says where the stderr tail of a failed agent goes. The tail is a local diagnostic: it is not in the error the run ends
// with (a caller logs that, a runner may keep it: Cloudroom persisted such tails into its records), and not on the audit chain, in
// the certificate or in the ledger. It goes to the person at the terminal (for a hosted session, to the session's log) and to the
// debug log, with the delivered secrets scrubbed.
func (s *runState) keepStderr(err error) {
	var t interface{ StderrTail() string }
	if !errors.As(err, &t) || t.StderrTail() == "" {
		return
	}
	tail := redact(t.StderrTail(), s.host.deliveredSecrets())
	fmt.Fprintf(os.Stdout, "   agent stderr (kept out of the run's records):\n%s\n", tail)
	if s.debugLog != nil {
		_, _ = fmt.Fprintf(s.debugLog, "%s agent_stderr %q\n", time.Now().UTC().Format(time.RFC3339Nano), tail)
	}
}

func (s *runState) cancelled() {
	s.stopAgent()
	s.agentFinished = true
	s.finalStatus = persistence.RunStatusKilled
}

// overBudget kills the run once the model use so far (agents' and the validator's) exceeds the project's budget cap.
func (s *runState) overBudget() bool {
	if !s.display.BudgetExceeded() {
		return false
	}
	fmt.Fprintf(os.Stdout, "\n⚠️  Budget cap exceeded - killing run #%d\n", s.run.ID)
	s.cancelled()
	return true
}

func (s *runState) driftHalt(reason string) {
	s.sup.Halt(reason)
	_ = s.r.log(s.run.ID).Append(history.DriftHalt{Reason: reason})
	fmt.Fprintf(os.Stdout, "\n🧭 Drift: %s - halting run #%d\n", tui.Safe(reason), s.run.ID)
	s.cancelled()
}

// loop decides the agent's proposals, one at a time, until the agent stops or the run ends.
func (s *runState) loop() {
	for {
		select {
		case u := <-s.usage:
			s.tracker.Record(u.Agent, u.Model, u.InputTokens, u.OutputTokens)
			s.display.AddActivity(fmt.Sprintf("%-14s step %d", u.Agent, s.tracker.Totals().Steps))
			if s.overBudget() {
				return
			}

		case <-s.renderTicker.C:
			s.display.Render()

		case p := <-s.proposals:
			if !s.decide(p) {
				return
			}

		case procErr := <-s.agentDone:
			if s.ctx.Err() != nil { // it stopped because the run was cancelled, which is how the run ended
				s.cancelled()
				return
			}
			if s.waitCtx.Err() != nil { // or because the run's time was up while it waited for a person
				s.driftHalt(fmt.Sprintf("the run exceeded its %d s limit", s.limits.MaxRunSecs))
				return
			}
			s.processExited(procErr)
			return

		case <-s.runDeadline:
			s.driftHalt(fmt.Sprintf("the run exceeded its %d s limit", s.limits.MaxRunSecs))
			return

		case <-s.ctx.Done():
			s.cancelled()
			return
		}
	}
}

// decide decides one proposal and answers the agent; false ends the loop.
func (s *runState) decide(p proposal) bool {
	r, run, dec, appr := s.r, s.run, s.dec, s.appr
	if p.req.ReviewAfter { // the changes already happened: decide exactly them
		if !reviewAfter(&p, appr, s.host) {
			return true
		}
	}
	// CHECK 4.4.3 / 7.4.2: scrub delivered secret values from all
	// operator-visible fields before any HITL presentation path.
	req := scrubSecrets(p.req, s.host.deliveredSecrets())
	// Child span of the run: its duration is the yield→decision
	// latency, i.e. how long the human (or policy) took to decide.
	_, yieldSpan := obs.Tracer.Start(s.ctx, "staircase.yield",
		trace.WithAttributes(
			attribute.String("staircase.agent", req.AgentName),
			attribute.String("staircase.action_type", req.ActionType)))
	dec.entry = p.entry
	rl := dec.decide(s.waitCtx, &req) // checks, models, signals and signing end with the run's limit too
	barrier.Hit(barrier.DecisionMade)
	// Record the decision, with the exact approved content, before the runtime can act on it: an approval that is not on
	// the audit chain is never released (CHECK 7.3.1). consume says when it is consumed.
	decidedFor := func(journaled bool) history.Decided {
		decided := yieldDecided(dec.total, rl.source, req, rl.resp, s.baseSHA, rl.next, rl.drift)
		decided.RequestEntry = p.entry
		if st := s.val.state(); st != nil {
			decided.ValidatorState, _ = json.Marshal(st)
		}
		decided.Paths = rl.files                            // what a proposal touched: a continuation replays the drift supervisor from it
		decided.Unconsumed = journaled && !rl.resp.Approved // its journal line is an authorization that was never consumed
		decided.Signed = rl.signed
		if rl.evidence != nil {
			decided.Evidence, _ = json.Marshal(rl.evidence)
		}
		decided.ParkedWhy = rl.parkedWhy
		if rl.source == "operator" {
			decided.DecideMS, decided.Lines = &rl.decideMS, rl.lines
		}
		return decided
	}
	err := r.consume(s.waitCtx, run.ID, dec.total, req, &rl, decidedFor)
	yieldSpan.SetAttributes(
		attribute.Bool("staircase.approved", rl.resp.Approved),
		attribute.String("staircase.decision_source", rl.source))
	yieldSpan.End()
	if err != nil {
		why := "the orchestrator could not record this decision; the run is stopping"
		if ce, ok := err.(*consumeError); ok && ce.journal {
			why = "the orchestrator could not keep this approval; the run is stopping"
		}
		p.reply <- decision{resp: domain.Decide(false, why)}
		s.stopAgent()
		s.agentFinished = true
		s.finalStatus = persistence.RunStatusFailed
		s.runErr = err
		return false
	}
	d := decision{resp: rl.resp}
	if rl.resp.Approved && rl.next != nil {
		appr.record(rl.next)
		s.ledger.add(dec.total, rl.source, req.ProposedEdits)
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
		s.driftHalt(fmt.Sprintf("more than %d proposals reached outside the stories' scope", s.limits.MaxScopeViolations))
		return false
	}
	if !rl.refused {
		s.sup.Decided(rl.files, rl.resp.Approved, rl.source == "operator", rl.drift)
	}
	return !s.overBudget() // the validator's model use counts too
}

// parkedTotal is how many proposals the run has parked for the owner: this segment's, and those the earlier segments left that
// the owner has not adopted.
func (s *runState) parkedTotal() int {
	n := s.dec.parked
	if s.cont != nil {
		n += len(s.cont.history.Parked())
	}
	return n
}

// finalize checks the worktree, offers an away run for review or puts the final review to a person, and delivers the approved
// change with its evidence: the commit named on the chain before the branch moves to it.
func (s *runState) finalize() error {
	r, opts, run, appr, ctx := s.r, s.opts, s.run, s.appr, s.ctx
	// ── FINALIZE ──────────────────────────────────────────────────────────────
	if err := r.enter(PhaseFinalize); err != nil {
		return err
	}
	r.reportDrift(run.ID, s.sup)
	if s.finalStatus == persistence.RunStatusSuccess && appr != nil {
		ok, err := r.verifyWorktree(run.ID, appr, s.display)
		if err != nil {
			return err
		}
		if !ok {
			s.finalStatus = persistence.RunStatusFailed
		}
	}
	stillGoing := func() { // a run cancelled while finalizing delivers nothing
		if s.finalStatus == persistence.RunStatusSuccess && ctx.Err() != nil {
			s.finalStatus = persistence.RunStatusKilled
		}
	}
	stillGoing()
	// An away run that approved or parked something is offered for review: a run whose every proposal was parked offers an empty
	// change with its parked proposals, never a SUCCESS with nothing offered (V11-C01 acceptance 4). One that approved and parked
	// nothing (a hosted session excepted) has its checks run on the unchanged tree instead (checkUnchanged).
	parked := s.parkedTotal()
	authorized := "" // the tree a delivery by policy authorized
	if opts.Away && s.finalStatus == persistence.RunStatusSuccess && appr != nil && (len(appr.files) > 0 || parked > 0) {
		var byPolicy *deliveryAttempt
		if opts.Deliver == DeliverPolicy { // judged again from the chain and the saved terms, not from this value
			byPolicy = &deliveryAttempt{taskApproved: s.dec.taskApproved, unreviewed: s.val != nil && s.val.unreviewed,
				atBase: func() bool {
					return s.gr != nil && gitOut(s.gr.path, "rev-parse", "--verify", "-q", "refs/heads/"+s.runBranch) == s.baseSHA
				}}
		}
		var err error
		if s.finalStatus, authorized, err = r.awaitReview(ctx, run.ID, appr, s.evGate, parked, byPolicy); err != nil {
			return err
		}
	} else if opts.Away && !opts.Hosted && s.finalStatus == persistence.RunStatusSuccess && appr != nil && len(appr.files) == 0 && parked == 0 {
		// Nothing approved and nothing parked: nothing to offer. Whether the project is whole without a change is still worth saying.
		var err error
		if s.finalStatus, err = r.checkUnchanged(ctx, run.ID, appr, s.evGate, s.dec.total); err != nil {
			return err
		}
	}
	if s.finalStatus == persistence.RunStatusSuccess && (s.val != nil && s.val.unreviewed || s.dec.taskApproved) {
		approved, err := s.dec.finalReview(s.baseSHA, s.host.deliveredSecrets())
		if err != nil {
			return err
		}
		if !approved {
			fmt.Fprintf(os.Stdout, "   ✋ Final review rejected - nothing committed\n")
			s.finalStatus = persistence.RunStatusFailed
		}
	}
	stillGoing()
	if s.finalStatus != persistence.RunStatusSuccess || appr == nil || len(appr.files) == 0 {
		return nil
	}
	chainHead, err := r.store.GetLastEventHash(run.ID)
	if err != nil {
		return err
	}
	prep, err := appr.prepare(commitMessage(run.ID, s.caseID, opts.Plan, chainHead))
	if err != nil {
		return err
	}
	if authorized != "" && (prep == nil || prep.tree != authorized) { // exactly the tree whose checks passed, or nothing
		return fmt.Errorf("delivery by policy was authorized for tree %.12s, and the commit to make is not that tree: nothing is delivered (staircase recover %d refuses it too)", authorized, run.ID)
	}
	hash := ""
	if prep != nil {
		// The commit is named on the chain before any branch holds it: if that cannot be
		// recorded, nothing is delivered, and a recovery after a crash knows this commit as
		// the run's own by that name.
		if err := r.log(run.ID).Append(history.CommitPrepared{Commit: prep.commit, Base: prep.base, Tree: prep.tree, Branch: s.runBranch}); err != nil {
			return fmt.Errorf("audit commit_prepared: %w", err)
		}
		barrier.Hit(barrier.CommitPrepared)
		stillGoing() // cancelled after the commit was named: it is not delivered
		if s.finalStatus == persistence.RunStatusSuccess {
			if err := prep.deliver(s.runBranch); err != nil {
				return err
			}
			hash = prep.commit
		}
	}
	if hash == "" {
		return nil
	}
	barrier.Hit(barrier.GitCAS)
	s.commitHash = hash
	// The commit is made and stays delivered. Evidence that cannot be written
	// is reported, recorded and named in the run summary; with RequireEvidence
	// the run also ends with ErrEvidenceIncomplete.
	ev := evidence{checks: r.runChecks(ctx, run.ID, s.gr, hash, opts.Checks, opts.Sandbox, opts.CheckTimeout, s.overlays)}
	if ev.ledger, err = r.writeLedger(run.ID, s.ledger); err != nil {
		fmt.Fprintf(os.Stdout, "   ⚠️  Ledger not written: %v\n", err)
		s.evidenceErrs = append(s.evidenceErrs, "ledger: "+err.Error())
	} else if err := attachNote(s.gr, LedgerNotesRef, LedgerPath(r.wsDir, run.ID), hash); err != nil {
		fmt.Fprintf(os.Stdout, "   ⚠️  Ledger not attached to the commit: %v\n", err)
		s.evidenceErrs = append(s.evidenceErrs, "ledger note: "+err.Error())
	}
	ev.policy, ev.initiator, ev.byPolicy = s.snap.Digest, s.initiator, authorized != ""
	if err := r.certify(run.ID, hash, s.baseSHA, chainHead, opts.Plan, s.gr, ev); err != nil {
		fmt.Fprintf(os.Stdout, "   ⚠️  Change certificate not written: %v\n", err)
		_ = r.log(run.ID).Append(history.CertificateFailed{Commit: hash, Error: err.Error()})
		s.evidenceErrs = append(s.evidenceErrs, "certificate: "+err.Error())
	}
	barrier.Hit(barrier.EvidencePublished)
	if len(s.evidenceErrs) > 0 {
		_ = r.log(run.ID).Append(history.EvidenceFailed{Commit: hash, Errors: s.evidenceErrs})
	}
	return nil
}

// newRunState is a run, or one segment of it, about to start: nothing made yet.
func (r *Runner) newRunState(ctx context.Context, caseID int64, opts RunOptions, cont *continuation) *runState {
	s := &runState{r: r, ctx: ctx, caseID: caseID, opts: opts, cont: cont, seg: segmentOf(cont)}
	if opts.Plan != nil {
		s.harness = opts.Plan.Harness
	}
	s.seg.init(s)
	return s
}
