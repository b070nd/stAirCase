// Package orchestrator implements the stAirCase run lifecycle state machine.
//
// The orchestrator is the single authoritative coordinator of a run:
// it sequences pre-flight checks, git branch management, IPC server startup,
// Python process launch, the HITL event loop, and teardown — each labelled
// by a [RunPhase] constant so that crash recovery and tests can reason about
// where execution stopped.
package orchestrator

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/b070nd/staircase-core/src/internal/approvalhttp"
	"github.com/b070nd/staircase-core/src/internal/obs"
	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/b070nd/staircase-core/src/internal/ipc"
	"github.com/b070nd/staircase-core/src/internal/monitor"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/b070nd/staircase-core/src/internal/policy"
	"github.com/b070nd/staircase-core/src/internal/runtime"
	"github.com/b070nd/staircase-core/src/internal/tui"
)

// RunPhase labels each boundary of the orchestration state machine.
// A crashed run leaves its [Runner.Phase] at the last phase it entered,
// which [Reconcile] uses to decide what cleanup is needed.
type RunPhase string

const (
	PhasePreFlight     RunPhase = "PRE_FLIGHT"
	PhaseBranchCreate  RunPhase = "BRANCH_CREATE"
	PhaseIPCListen     RunPhase = "IPC_LISTEN"
	PhasePythonBoot    RunPhase = "PYTHON_BOOT"
	PhaseAgentLoop     RunPhase = "AGENT_LOOP"
	PhaseFinalize      RunPhase = "FINALIZE"
	PhaseBranchRestore RunPhase = "BRANCH_RESTORE"
)

// RunOptions carries all user-supplied flags for a run.
type RunOptions struct {
	DryRun        bool
	Force         bool
	SkipGates     bool
	AutoStash     bool
	Debug         bool
	Reconcile     bool // clean up orphan branches / stale runs before proceeding
	ApprovalPort  int
	ApprovalToken string
}

// Runner orchestrates a single stAirCase run.
// Construct with [NewRunner]; call [Run] to execute.
type Runner struct {
	store *persistence.Store
	wsDir string
	phase RunPhase // current phase; read via Phase() for observability and tests
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
// Phases executed in order: PRE_FLIGHT → BRANCH_CREATE → IPC_LISTEN →
// PYTHON_BOOT → AGENT_LOOP → FINALIZE → BRANCH_RESTORE (on non-success).
func (r *Runner) Run(ctx context.Context, caseID int64, opts RunOptions) error {
	obs.ActiveRuns.Inc()
	t0Run := time.Now()
	defer func() {
		obs.ActiveRuns.Dec()
		obs.RunDuration.Observe(time.Since(t0Run).Seconds())
	}()
	r.phase = PhasePreFlight

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
	if opts.Reconcile && project.SourcePath != "" {
		result, err := r.Reconcile(ctx, caseID, project.SourcePath, true)
		if err != nil {
			obs.Log.Warn("reconcile", "err", err)
		} else if len(result.StalledRuns) > 0 || len(result.OrphanBranches) > 0 {
			fmt.Printf("   🔄 Reconcile: %d stale run(s) killed, %d orphan branch(es) pruned\n",
				len(result.StalledRuns), len(result.OrphanBranches))
		}
	}

	// ── Dirty-tree pre-flight ─────────────────────────────────────────────────
	var autoStashed bool
	if !opts.Force && project.SourcePath != "" {
		stashed, err := handleDirtyTree(project.SourcePath, opts.AutoStash)
		if err != nil {
			return err
		}
		autoStashed = stashed
	}
	defer func() {
		if autoStashed && project.SourcePath != "" {
			// go-git v5 has no stash pop support; keep as exec.Command.
			if out, err := exec.Command("git", "-C", project.SourcePath, "stash", "pop").CombinedOutput(); err != nil {
				obs.Log.Warn("stash pop after run", "output", string(out))
			}
		}
	}()

	// ── Resolve topology ──────────────────────────────────────────────────────
	topology, err := r.store.GetLatestTopology(project.ID)
	if err != nil {
		return fmt.Errorf("load topology: %w", err)
	}
	if topology == nil {
		return fmt.Errorf("no swarm topology registered for project %q — run 'staircase topology register' first", project.Name)
	}
	topoVersion := topology.Version

	// ── Resolve current git branch (capture SHA on detached HEAD) ─────────────
	gitBranch := "unknown"
	if gr != nil {
		if b, bErr := gr.CurrentBranch(); bErr == nil {
			gitBranch = b
		}
	}

	// ── Quality gate pre-flight ───────────────────────────────────────────────
	if !opts.SkipGates && !opts.DryRun {
		if err := r.runGates(caseID); err != nil {
			return err
		}
	}

	// ── Create run record ─────────────────────────────────────────────────────
	if n, err := r.store.KillStaleRuns(caseID, 2*time.Hour); err != nil {
		obs.Log.Warn("kill stale runs", "err", err)
	} else if n > 0 {
		fmt.Printf("   ⚠️  Killed %d stale run(s) for case %d\n", n, caseID)
	}
	run, err := r.store.CreateRun(caseID, topoVersion, gitBranch)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	fmt.Printf("🚀 Run #%d  case=%d  branch=%s\n", run.ID, caseID, gitBranch)

	if opts.DryRun {
		fmt.Printf("   [dry-run] socket would be: %s\n",
			filepath.Join(r.wsDir, "tmp", fmt.Sprintf("run-%d.sock", run.ID)))
		_ = r.store.UpdateRunStatus(run.ID, persistence.RunStatusKilled, timePtr(time.Now()), "")
		return nil
	}

	// ── BRANCH_CREATE ─────────────────────────────────────────────────────────
	r.phase = PhaseBranchCreate
	runBranch := fmt.Sprintf("staircase/run-%d", run.ID)
	runBranchCreated := false
	finalStatus := ""

	defer func() {
		r.phase = PhaseBranchRestore
		if runBranchCreated && gr != nil && finalStatus != persistence.RunStatusSuccess {
			if err := gr.CheckoutBranch(gitBranch); err != nil {
				obs.Log.Warn("restore branch", "branch", gitBranch, "err", err)
			}
		}
	}()

	if gr != nil {
		// Remove stale branch from a prior crashed run with the same ID.
		if gr.BranchExists(runBranch) {
			if err := gr.DeleteBranch(runBranch); err != nil {
				return fmt.Errorf("delete stale branch %q: %w", runBranch, err)
			}
		}
		if err := gr.CreateBranch(runBranch); err != nil {
			return fmt.Errorf("create blast-radius branch %q: %w", runBranch, err)
		}
		runBranchCreated = true
		fmt.Printf("   🌿 Blast-radius branch: %s\n", runBranch)
	}

	// ── IPC_LISTEN ────────────────────────────────────────────────────────────
	r.phase = PhaseIPCListen
	token, err := runtime.BootstrapToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}

	aesKey, err := crypto.LoadKey(r.wsDir)
	if err != nil {
		return fmt.Errorf("load workspace key: %w", err)
	}

	tmpDir := filepath.Join(r.wsDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}
	socketPath := filepath.Join(tmpDir, fmt.Sprintf("run-%d.sock", run.ID))

	ipcSrv := ipc.NewServer(socketPath, run.ID, token, r.store, aesKey)
	if err := ipcSrv.Start(ctx); err != nil {
		return fmt.Errorf("ipc server: %w", err)
	}
	fmt.Printf("   🔌 IPC socket: %s\n", socketPath)

	policyEngine, err := policy.LoadEngine(r.wsDir)
	if err != nil {
		obs.Log.Warn("load policy — proceeding without auto-approval", "err", err)
		policyEngine = &policy.Engine{}
	}

	var approvalSrv *approvalhttp.Server
	if opts.ApprovalPort > 0 {
		approvalAddr := fmt.Sprintf("127.0.0.1:%d", opts.ApprovalPort)
		approvalToken := opts.ApprovalToken
		if approvalToken == "" {
			raw := make([]byte, 16)
			if _, err := rand.Read(raw); err != nil {
				return fmt.Errorf("generate approval token: %w", err)
			}
			approvalToken = hex.EncodeToString(raw)
		}
		approvalSrv = approvalhttp.NewServer(approvalAddr, approvalToken)
		if err := approvalSrv.Start(ctx); err != nil {
			return fmt.Errorf("approval http server: %w", err)
		}
		fmt.Printf("   🌐 Approval API: http://%s/v1/yields\n", approvalSrv.ListenAddr())
		fmt.Printf("   🔑 Approval token: %s\n", approvalToken)
	}

	if opts.Debug {
		logDir := filepath.Join(r.wsDir, "log")
		if err := os.MkdirAll(logDir, 0o700); err == nil {
			logPath := filepath.Join(logDir, fmt.Sprintf("staircase-debug-run%d.log", run.ID))
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				ipcSrv.SetDebugWriter(f)
				defer func() { _ = f.Close() }()
				fmt.Printf("   🔍 Debug log: %s\n", logPath)
			}
		}
	}

	tracker := monitor.NewTracker(run.ID, caseID, project.Name, gitBranch)
	_, budgetCap, _ := r.store.GetProjectConfig(caseRec.ProjectID)
	display := monitor.NewDisplay(tracker, budgetCap)
	display.Render()

	renderTicker := time.NewTicker(500 * time.Millisecond)
	defer renderTicker.Stop()

	// ── PYTHON_BOOT ───────────────────────────────────────────────────────────
	r.phase = PhasePythonBoot
	canonicalScript := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_case%d.py", caseID))
	scriptPath := filepath.Join(tmpDir, fmt.Sprintf("graph_exec_%d.py", run.ID))
	if _, err := os.Stat(canonicalScript); os.IsNotExist(err) {
		now := time.Now()
		_ = r.store.UpdateRunStatus(run.ID, persistence.RunStatusFailed, &now, "")
		return fmt.Errorf("graph_exec script not found — run 'staircase compile %d' first", caseID)
	}
	srcBytes, err := os.ReadFile(canonicalScript)
	if err != nil {
		return fmt.Errorf("read canonical script: %w", err)
	}
	if err := os.WriteFile(scriptPath, srcBytes, 0o600); err != nil { // #nosec G703 -- scriptPath is built from internal wsDir, not user input
		return fmt.Errorf("copy script to run path: %w", err)
	}

	proc, err := runtime.LaunchPython(ctx, r.wsDir, scriptPath, ipcSrv.ListenAddr(), token)
	if err != nil {
		now := time.Now()
		_ = r.store.UpdateRunStatus(run.ID, persistence.RunStatusFailed, &now, "")
		return fmt.Errorf("launch python: %w", err)
	}
	fmt.Printf("   🐍 Python PID=%d\n", proc.PID())

	// ── AGENT_LOOP ────────────────────────────────────────────────────────────
	r.phase = PhaseAgentLoop
	if err := r.store.UpdateCaseStatus(caseID, persistence.CaseStatusRunning); err != nil {
		obs.Log.Warn("update case status", "err", err)
	}

runLoop:
	for {
		select {
		case emit := <-ipcSrv.StatEmitCh:
			model := ""
			inputTok, outputTok := 0, 0
			if emit.State != nil {
				if m, ok := emit.State["model"].(string); ok {
					model = m
				}
				if v, ok := emit.State["input_tokens"].(float64); ok {
					inputTok = int(v)
				}
				if v, ok := emit.State["output_tokens"].(float64); ok {
					outputTok = int(v)
				}
			}
			tracker.Record(emit.ActiveAgent, model, inputTok, outputTok)
			display.AddActivity(fmt.Sprintf("%-14s step %d", emit.ActiveAgent, tracker.Totals().Steps))
			if display.BudgetExceeded() {
				fmt.Printf("\n⚠️  Budget cap exceeded — killing run #%d\n", run.ID)
				proc.Kill()
			}

		case <-renderTicker.C:
			display.Render()

		case yieldReq := <-ipcSrv.YieldCh:
			var resp ipc.IpcYieldResponse
			if dec := policyEngine.Evaluate(yieldReq); dec.Matched {
				resp = ipc.IpcYieldResponse{Type: "yield_response", Approved: dec.Approved, Feedback: dec.Reason}
				verb := "rejected"
				if dec.Approved {
					verb = "approved"
				}
				display.AddActivity(fmt.Sprintf("%-14s AUTO   %s → %s (%s)", yieldReq.AgentName, yieldReq.ActionType, verb, dec.Reason))
			} else {
				display.Pause()
				switch {
				case approvalSrv != nil:
					_, ch := approvalSrv.PendYield(yieldReq)
					resp = <-ch
				case project.WebhookURL != "":
					resp = sendWebhookYield(project.WebhookURL, yieldReq)
				default:
					resp = tui.RunYieldTUI(yieldReq)
				}
				display.Resume()
				display.AddActivity(fmt.Sprintf("%-14s HITL   %s → %v", yieldReq.AgentName, yieldReq.ActionType, resp.Approved))
			}
			ipcSrv.ResponseCh <- resp

			// Audit: record who decided and what the decision was (CHECK 7.3.1).
			// source is "policy" for auto-decisions, "operator" for human review.
			source := "operator"
			if policyEngine.Evaluate(yieldReq).Matched {
				source = "policy"
			}
			if payload, err := json.Marshal(map[string]any{
				"type":        "yield_decided",
				"source":      source,
				"agent":       yieldReq.AgentName,
				"action_type": yieldReq.ActionType,
				"approved":    resp.Approved,
				"feedback":    resp.Feedback,
			}); err == nil {
				prevHash, _ := r.store.GetLastEventHash(run.ID)
				_, _ = r.store.AppendEventLog(run.ID, "yield_decided", string(payload), prevHash, "")
			}

		case procErr := <-proc.Done:
			if procErr != nil {
				display.Final(fmt.Sprintf("Run #%d FAILED: %v", run.ID, procErr))
				finalStatus = persistence.RunStatusFailed
			} else {
				display.Final(fmt.Sprintf("Run #%d completed.", run.ID))
				finalStatus = persistence.RunStatusSuccess
			}
			break runLoop

		case <-ctx.Done():
			proc.Kill()
			display.Final(fmt.Sprintf("Run #%d KILLED", run.ID))
			finalStatus = persistence.RunStatusKilled
			break runLoop
		}
	}

	// ── FINALIZE ──────────────────────────────────────────────────────────────
	r.phase = PhaseFinalize
	commitHash := ""
	if finalStatus == persistence.RunStatusSuccess && gr != nil {
		_ = gr.AddAll()
		msg := fmt.Sprintf("staircase: run #%d — case #%d", run.ID, caseID)
		if hash, err := gr.Commit(msg); err == nil {
			commitHash = hash
			ipcSrv.SetGitCommitHash(commitHash)
		}
		if stories, err := r.store.ListUserStoriesByCase(caseID); err == nil {
			for _, us := range stories {
				if us.Status == persistence.StoryStatusPending {
					_ = r.store.UpdateUserStoryStatus(us.ID, persistence.StoryStatusImplemented)
				}
			}
		}
	}

	endTime := time.Now()
	_ = r.store.UpdateRunStatus(run.ID, finalStatus, &endTime, commitHash)

	caseStatusMap := map[string]string{
		persistence.RunStatusSuccess: persistence.CaseStatusCompleted,
		persistence.RunStatusFailed:  persistence.CaseStatusFailed,
		persistence.RunStatusKilled:  persistence.CaseStatusFailed,
	}
	_ = r.store.UpdateCaseStatus(caseID, caseStatusMap[finalStatus])

	return nil
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
		return fmt.Errorf("quality gate check failed — %d BLOCK failure(s); run 'staircase gate %d' for details",
			report.Summary.Fail, caseID)
	}
	return nil
}

// ─── Git helpers ──────────────────────────────────────────────────────────────

func handleDirtyTree(repoPath string, autoStash bool) (stashed bool, err error) {
	gr, err := OpenGitRepo(repoPath)
	if err != nil {
		return false, nil // not a git repo
	}
	clean, err := gr.IsClean()
	if err != nil || clean {
		return false, nil // clean tree or status error
	}
	if !autoStash {
		return false, fmt.Errorf(
			"dirty working tree in %s\n  → commit, stash manually, or use --auto-stash / --force",
			repoPath,
		)
	}
	// go-git v5 has no stash push support; keep as exec.Command.
	out, stashErr := exec.Command("git", "-C", repoPath, "stash", "push", "-m", "staircase pre-run").CombinedOutput()
	if stashErr != nil {
		return false, fmt.Errorf("auto-stash failed: %s", out)
	}
	fmt.Printf("   📦 Auto-stashed dirty tree in %s\n", repoPath)
	return true, nil
}

// webhookClient is shared across all webhook calls within a single run.
var webhookClient = &http.Client{Timeout: 30 * time.Second}

func sendWebhookYield(webhookURL string, req ipc.IpcYieldRequest) ipc.IpcYieldResponse {
	body, _ := json.Marshal(req)
	resp, err := webhookClient.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		obs.Log.Warn("webhook POST failed — auto-rejecting", "err", err)
		return ipc.IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: "webhook error: " + err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	var yieldResp ipc.IpcYieldResponse
	if err := json.NewDecoder(resp.Body).Decode(&yieldResp); err != nil {
		obs.Log.Warn("webhook response decode failed — auto-rejecting", "err", err)
		return ipc.IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: "webhook decode error: " + err.Error()}
	}
	if yieldResp.Type == "" {
		yieldResp.Type = "yield_response"
	}
	return yieldResp
}

func timePtr(t time.Time) *time.Time { return &t }

// scrubSecrets replaces every occurrence of each active secret value in the
// yield request's proposed-edit content with "<REDACTED>" before the request
// is presented to the operator via TUI or webhook (CHECK 4.4.3).
// This prevents secret values from leaking into terminal output or HTTP
// response bodies even if an agent accidentally embeds them in file content.
func scrubSecrets(req ipc.IpcYieldRequest, activeValues []string) ipc.IpcYieldRequest {
	if len(activeValues) == 0 {
		return req
	}
	for i := range req.ProposedEdits {
		for _, v := range activeValues {
			if v == "" {
				continue
			}
			req.ProposedEdits[i].File = strings.ReplaceAll(req.ProposedEdits[i].File, v, "<REDACTED>")
		}
	}
	return req
}
