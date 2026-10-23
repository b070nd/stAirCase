// Package orchestrator implements the stAirCase run lifecycle. It is the single coordinator of a run: pre-flight checks, the git
// worktree, the in-process agent, the decision (HITL) loop, finalize and teardown, each labelled by a [RunPhase] so crash recovery and
// tests know where execution stopped.
package orchestrator

import (
	"bytes"
	"context"
	"crypto/rand"
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
	"time"

	"github.com/b070nd/stAirCase/src/internal/approvalhttp"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/diag"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/gate"
	"github.com/b070nd/stAirCase/src/internal/governance"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/llm"
	"github.com/b070nd/stAirCase/src/internal/monitor"
	"github.com/b070nd/stAirCase/src/internal/obs"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/b070nd/stAirCase/src/internal/policy"
	"github.com/b070nd/stAirCase/src/internal/signal"
	"github.com/b070nd/stAirCase/src/internal/webhookauth"
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

// RunOptions are a run's Terms and how this run, or this segment of it, is carried out: where it listens, how it is shown, the
// agent, the plan, and what a test puts in place of a model.
type RunOptions struct {
	Terms
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
	// AckDrift acknowledges that the case's previous run was halted for drift;
	// without it such a case does not run again.
	AckDrift bool
	// ValidatorChat and SignalEval, when set, stand in for the reviewer models and the decision model the terms name
	// (nil: called with the project's secrets), and Person for the approval API, the project's webhook or the terminal.
	ValidatorChat llm.Model
	SignalEval    signal.Evaluator
	Person        Person
	// ResumesSession and ResumesRun, for a hosted session resumed after its earlier run was decided, are bound on run_bound.
	ResumesSession string
	ResumesRun     int64
	// Hosted marks a hosted session (staircase host): a conversation the runner ends, not a task, so an away run of it that approved
	// and parked nothing is not checked at its end. It is in memory only, never a saved term.
	Hosted bool
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
	s := r.newRunState(ctx, caseID, opts, cont)
	// The terms are judged before anything exists: a run no one can decide under leaves no record, branch or worktree.
	terms, err := NewTerms(opts.Terms, s.harness)
	if err != nil {
		return err
	}
	s.opts.Terms = terms
	s.signers = filepath.Join(r.wsDir, governance.AllowedSigners)
	if _, err := os.Stat(s.signers); err != nil {
		s.signers = ""
	}
	if s.opts.RequireSignedApprovals && s.signers == "" {
		return errors.New("--require-signed-approvals needs the trusted signers: an allowed_signers file in the workspace (staircase governance use, or copy git's allowed_signers there)")
	}
	if done, err := s.prepare(); err != nil || done {
		return err
	}
	s.ledger.Base = s.baseSHA
	if err := s.open(); err != nil {
		return err
	}
	defer s.releaseOwner()                       // after everything below: the run is owned until it has finished
	defer func() { runErr = s.finish(runErr) }() // whatever happens from here, the run's end is recorded
	defer s.cleanup()                            // what the phases set up is undone first
	for _, phase := range []func() error{s.bind, s.setup, s.startAgent} {
		if err := phase(); err != nil {
			return err
		}
	}
	s.loop()
	s.endLaunch() // the agent has stopped: how it ended is on the chain before the run is finalized
	if err := s.finalize(); err != nil {
		return err
	}
	return s.runErr
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
	h, err := history.Read(r.store, runs[0].ID) // a changed chain must not lift a halt
	if err != nil {
		return 0, fmt.Errorf("whether the case's last run was halted for drift cannot be known: %w", err)
	}
	if h.Halted {
		return runs[0].ID, nil
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

// log is the writer of a run's chain: the orchestrator's events go on it through history.Writer, typed (ADR 0014).
func (r *Runner) log(runID int64) *history.Writer {
	return history.NewWriter(r.store, runID, history.Settings{})
}

// reportDrift audits the run's drift report and summarizes it.
func (r *Runner) reportDrift(runID int64, sup *policy.Supervisor) {
	rep := sup.Report()
	if raw, err := json.Marshal(rep); err == nil {
		_ = r.log(runID).Append(history.DriftReport{Report: raw})
	}
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
		_ = r.log(runID).Append(v.recorded())
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
// The log is bounded and scrubbed of the secrets the process has handed to agents (internal/diag).
func (r *Runner) openDebugLog(runID int64) *diag.Log {
	logDir := filepath.Join(r.wsDir, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("staircase-debug-run%d.log", runID))
	f, err := diag.Open(logPath, diag.MaxLogBytes, diag.Delivered)
	if err != nil {
		return nil
	}
	fmt.Fprintf(os.Stdout, "   🔍 Debug log: %s (at most %d bytes)\n", logPath, diag.MaxLogBytes)
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
	if p.entry, err = host.auditRequest(p.req); err != nil {
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
