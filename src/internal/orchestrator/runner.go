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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/b070nd/staircase-core/src/internal/approvalhttp"
	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/b070nd/staircase-core/src/internal/ipc"
	"github.com/b070nd/staircase-core/src/internal/monitor"
	"github.com/b070nd/staircase-core/src/internal/obs"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/b070nd/staircase-core/src/internal/policy"
	"github.com/b070nd/staircase-core/src/internal/runtime"
	"github.com/b070nd/staircase-core/src/internal/tui"
	"github.com/b070nd/staircase-core/src/internal/webhookauth"
	"github.com/b070nd/staircase-core/src/internal/wslock"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrRunNotSuccessful is returned by [Runner.Run] when a run reaches a
// non-success terminal state (FAILED or KILLED). The run is still fully
// recorded and finalized; this error exists so that `staircase run` exits
// non-zero and CI/automation can detect failure instead of treating a failed
// agent run as success.
var ErrRunNotSuccessful = errors.New("run did not complete successfully")

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
	// RecordLLM, when non-empty, is a file path where the Python harness records
	// every LLM exchange for later deterministic replay (--record-llm flag).
	RecordLLM string
	// ReplayLLM, when non-empty, is a file path from which the Python harness
	// replays LLM exchanges instead of calling the real API (--replay-llm flag).
	ReplayLLM string
	// AllowShellExec, when true, includes run_shell in the agent tool list.
	// Defaults to false — operators must explicitly pass --allow-shell-exec.
	AllowShellExec bool
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
	// Root span for the whole run; yield spans become children via ctx.
	ctx, runSpan := obs.Tracer.Start(ctx, "staircase.run",
		trace.WithAttributes(attribute.Int64("staircase.case_id", caseID)))
	defer runSpan.End()
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
			fmt.Fprintf(os.Stdout, "   🔄 Reconcile: %d stale run(s) killed, %d orphan branch(es) pruned\n",
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
		fmt.Fprintf(os.Stdout, "   ⚠️  Killed %d stale run(s) for case %d\n", n, caseID)
	}
	run, err := r.store.CreateRun(caseID, topoVersion, gitBranch)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	fmt.Fprintf(os.Stdout, "🚀 Run #%d  case=%d  branch=%s\n", run.ID, caseID, gitBranch)

	if opts.DryRun {
		fmt.Fprintf(os.Stdout, "   [dry-run] socket would be: %s\n",
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
		fmt.Fprintf(os.Stdout, "   🌿 Blast-radius branch: %s\n", runBranch)
	}

	// ── IPC_LISTEN ────────────────────────────────────────────────────────────
	r.phase = PhaseIPCListen
	token, err := runtime.BootstrapToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}

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
	// webhook is configured without a secret the channel is unauthenticated —
	// warn loudly so operators know to set one with `staircase project set-webhook --secret`.
	webhookSecret := r.loadWebhookSecret(caseRec.ProjectID, aesKey)
	if project.WebhookURL != "" && len(webhookSecret) == 0 {
		obs.Log.Warn("webhook approvals are UNAUTHENTICATED — set a secret with 'staircase project set-webhook --secret' to prevent forged approvals",
			"project_id", caseRec.ProjectID)
	}

	tmpDir := filepath.Join(r.wsDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}
	socketPath := filepath.Join(tmpDir, fmt.Sprintf("run-%d.sock", run.ID))

	ipcSrv := ipc.NewServer(socketPath, run.ID, caseRec.ProjectID, token, r.store, aesKey, opts.AllowShellExec)
	if err := ipcSrv.Start(ctx); err != nil {
		return fmt.Errorf("ipc server: %w", err)
	}
	fmt.Fprintf(os.Stdout, "   🔌 IPC socket: %s\n", socketPath)

	policyEngine, err := policy.LoadEngine(r.wsDir)
	if err != nil {
		obs.Log.Warn("load policy — proceeding without auto-approval", "err", err)
		policyEngine = &policy.Engine{}
	}
	// P2: verify Ed25519 signature on policy.json when sidecar is present.
	// Absent signature warns (backward compat); invalid signature is fatal.
	if sigPresent, sigErr := policy.VerifyPolicySignature(r.wsDir); sigErr != nil {
		return fmt.Errorf("policy integrity check failed — re-sign with 'staircase policy sign': %w", sigErr)
	} else if !sigPresent {
		obs.Log.Warn("policy.json is unsigned — run 'staircase policy sign' to enable tamper detection")
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
		fmt.Fprintf(os.Stdout, "   🌐 Approval API: http://%s/v1/yields\n", approvalSrv.ListenAddr())
		fmt.Fprintf(os.Stdout, "   🔑 Approval token: %s\n", approvalToken)
	}

	if opts.Debug {
		logDir := filepath.Join(r.wsDir, "log")
		if err := os.MkdirAll(logDir, 0o700); err == nil {
			logPath := filepath.Join(logDir, fmt.Sprintf("staircase-debug-run%d.log", run.ID))
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				ipcSrv.SetDebugWriter(f)
				defer func() { _ = f.Close() }()
				fmt.Fprintf(os.Stdout, "   🔍 Debug log: %s\n", logPath)
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
	// P1: verify the SHA-256 sidecar written at compile time to detect
	// tampering between 'staircase compile' and 'staircase run'.
	// If the sidecar is absent (pre-existing script compiled before this
	// feature), log a warning and continue for backward compatibility.
	// If the sidecar is present but does not match, fail hard.
	hashSidecar := canonicalScript + ".sha256"
	if storedHex, readErr := os.ReadFile(hashSidecar); readErr == nil {
		actual := sha256.Sum256(srcBytes)
		if hex.EncodeToString(actual[:]) != strings.TrimSpace(string(storedHex)) {
			now := time.Now()
			_ = r.store.UpdateRunStatus(run.ID, persistence.RunStatusFailed, &now, "")
			return fmt.Errorf("graph_exec script integrity check failed — recompile with 'staircase compile %d'", caseID)
		}
	} else {
		obs.Log.Warn("graph_exec script has no .sha256 sidecar — skipping integrity check (recompile to enable)")
	}
	if err := os.WriteFile(scriptPath, srcBytes, 0o600); err != nil { // #nosec G703 -- scriptPath is built from internal wsDir, not user input
		return fmt.Errorf("copy script to run path: %w", err)
	}
	// Open the run-specific script as a file descriptor so the runtime can
	// deliver it to Python via ExtraFiles (fd 3) rather than a file-path arg
	// (CHECK 5.4.2).
	scriptFD, err := os.Open(scriptPath)
	if err != nil {
		return fmt.Errorf("open script fd: %w", err)
	}
	defer func() { _ = scriptFD.Close() }()

	// W3C traceparent of the run span so the Python runtime can join the trace.
	traceparent := ""
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		traceparent = fmt.Sprintf("00-%s-%s-%s", sc.TraceID(), sc.SpanID(), sc.TraceFlags())
	}

	proc, err := runtime.LaunchPython(ctx, r.wsDir, scriptFD, ipcSrv.ListenAddr(), token,
		runtime.LaunchPythonOptions{
			RecordLLM:      opts.RecordLLM,
			ReplayLLM:      opts.ReplayLLM,
			AllowShellExec: opts.AllowShellExec,
			Traceparent:    traceparent,
			// Redact any delivered secret from Python stderr before it is logged.
			ScrubStderr: func(line string) string {
				return string(crypto.ScrubBytes([]byte(line), ipcSrv.DeliveredSecrets()))
			},
		})
	if err != nil {
		now := time.Now()
		_ = r.store.UpdateRunStatus(run.ID, persistence.RunStatusFailed, &now, "")
		return fmt.Errorf("launch python: %w", err)
	}
	fmt.Fprintf(os.Stdout, "   🐍 Python PID=%d\n", proc.PID())

	// ── AGENT_LOOP ────────────────────────────────────────────────────────────
	r.phase = PhaseAgentLoop
	if err := r.store.UpdateCaseStatus(caseID, persistence.CaseStatusRunning); err != nil {
		obs.Log.Warn("update case status", "err", err)
	}

	// Yield counters for policy limit enforcement (CHECK 7.2.1, 7.2.2).
	var autoApproved, totalYields int
	// approvedFiles accumulates the file paths from each approved file_edit
	// yield. Used at finalize to stage only the operator-approved edits rather
	// than all worktree changes (prevents committing unrelated modifications).
	var approvedFiles []string
	// approvedHashes maps file path → SHA-256 of the operator-approved content
	// (last approval wins). Verified against on-disk content before commit so
	// an agent cannot apply different content than what was approved.
	approvedHashes := map[string]string{}

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
				fmt.Fprintf(os.Stdout, "\n⚠️  Budget cap exceeded — killing run #%d\n", run.ID)
				proc.Kill()
			}

		case <-renderTicker.C:
			display.Render()

		case yieldReq := <-ipcSrv.YieldCh:
			totalYields++
			// CHECK 4.4.3 / 7.4.2: scrub delivered secret values from all
			// operator-visible fields before any HITL presentation path.
			yieldReq = scrubSecrets(yieldReq, ipcSrv.DeliveredSecrets())
			// Child span of the run: its duration is the yield→decision
			// latency, i.e. how long the human (or policy) took to decide.
			_, yieldSpan := obs.Tracer.Start(ctx, "staircase.yield",
				trace.WithAttributes(
					attribute.String("staircase.agent", yieldReq.AgentName),
					attribute.String("staircase.action_type", yieldReq.ActionType)))
			var resp ipc.IpcYieldResponse
			// CHECK 7.2.1/7.2.2: once a session limit is hit, every subsequent
			// yield goes to the human operator regardless of policy rules.
			autoDecision := false
			if limitHit, limitReason := policyEngine.CheckLimits(autoApproved, totalYields); limitHit {
				display.AddActivity(fmt.Sprintf("%-14s LIMIT  %s → HITL (%s)", yieldReq.AgentName, yieldReq.ActionType, limitReason))
				display.Pause()
				switch {
				case approvalSrv != nil:
					_, ch := approvalSrv.PendYield(yieldReq)
					resp = <-ch
				case project.WebhookURL != "":
					resp = sendWebhookYield(project.WebhookURL, webhookSecret, yieldReq)
				default:
					resp = tui.RunYieldTUI(yieldReq)
				}
				display.Resume()
				display.AddActivity(fmt.Sprintf("%-14s HITL   %s → %v", yieldReq.AgentName, yieldReq.ActionType, resp.Approved))
			} else if dec := policyEngine.Evaluate(yieldReq); dec.Matched {
				resp = ipc.IpcYieldResponse{Type: "yield_response", Approved: dec.Approved, Feedback: dec.Reason}
				if dec.Approved {
					autoApproved++
				}
				verb := "rejected"
				if dec.Approved {
					verb = "approved"
				}
				display.AddActivity(fmt.Sprintf("%-14s AUTO   %s → %s (%s)", yieldReq.AgentName, yieldReq.ActionType, verb, dec.Reason))
				autoDecision = true
			} else {
				display.Pause()
				switch {
				case approvalSrv != nil:
					_, ch := approvalSrv.PendYield(yieldReq)
					resp = <-ch
				case project.WebhookURL != "":
					resp = sendWebhookYield(project.WebhookURL, webhookSecret, yieldReq)
				default:
					resp = tui.RunYieldTUI(yieldReq)
				}
				display.Resume()
				display.AddActivity(fmt.Sprintf("%-14s HITL   %s → %v", yieldReq.AgentName, yieldReq.ActionType, resp.Approved))
			}
			ipcSrv.ResponseCh <- resp

			// Collect approved file paths so finalize stages only operator-
			// approved edits (P1: commit only approved files, not git add all).
			if resp.Approved && yieldReq.ActionType == "file_edit" {
				for _, pe := range yieldReq.ProposedEdits {
					if pe.File != "" {
						approvedFiles = append(approvedFiles, pe.File)
						if pe.ContentHash != "" {
							approvedHashes[pe.File] = pe.ContentHash
						}
					}
				}
			}

			// Audit: record who decided and what the decision was (CHECK 7.3.1).
			// source is "policy" for auto-decisions, "operator" for human review.
			source := "operator"
			if autoDecision {
				source = "policy"
			}
			yieldSpan.SetAttributes(
				attribute.Bool("staircase.approved", resp.Approved),
				attribute.String("staircase.decision_source", source))
			yieldSpan.End()
			if payload, err := json.Marshal(map[string]any{
				"type":        "yield_decided",
				"source":      source,
				"agent":       yieldReq.AgentName,
				"action_type": yieldReq.ActionType,
				"approved":    resp.Approved,
				"feedback":    resp.Feedback,
			}); err == nil {
				_, _ = r.store.AppendEventLogChained(run.ID, "yield_decided", string(payload), "")
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
		// Trust-boundary path sandbox: a compromised runtime can bypass the
		// Python-side path guard and get an out-of-repo edit approved. Reject
		// any approved path that escapes the project root BEFORE reading it —
		// otherwise the content-hash check below becomes an arbitrary-file-read
		// primitive (audit: approval_path_escape).
		for _, file := range approvedFiles {
			if !pathWithinRoot(project.SourcePath, file) {
				if payload, err := json.Marshal(map[string]any{
					"type": "approval_path_escape", "file": file,
				}); err == nil {
					_, _ = r.store.AppendEventLogChained(run.ID, "approval_path_escape", string(payload), "")
				}
				obs.Log.Error("approved file path escapes project root — refusing to commit",
					"file", file, "root", project.SourcePath, "run_id", run.ID)
				display.AddActivity(fmt.Sprintf("%-14s ABORT  %s escapes the project root", "finalize", file))
				finalStatus = persistence.RunStatusFailed
			}
		}
	}
	if finalStatus == persistence.RunStatusSuccess && gr != nil {
		// Approval-content binding: re-hash every file whose edit carried a
		// content_hash and refuse to commit when the on-disk bytes differ from
		// what the operator approved (audit: approval_content_mismatch).
		for file, want := range approvedHashes {
			data, readErr := os.ReadFile(filepath.Join(project.SourcePath, file))
			got := ""
			if readErr == nil {
				sum := sha256.Sum256(data)
				got = hex.EncodeToString(sum[:])
			}
			if got != want {
				if payload, err := json.Marshal(map[string]any{
					"type": "approval_content_mismatch", "file": file,
					"approved_hash": want, "actual_hash": got,
				}); err == nil {
					_, _ = r.store.AppendEventLogChained(run.ID, "approval_content_mismatch", string(payload), "")
				}
				obs.Log.Error("approved content hash mismatch — refusing to commit",
					"file", file, "approved", want, "actual", got, "run_id", run.ID)
				display.AddActivity(fmt.Sprintf("%-14s ABORT  %s content differs from approved version", "finalize", file))
				finalStatus = persistence.RunStatusFailed
			}
		}
	}
	if finalStatus == persistence.RunStatusSuccess && gr != nil {
		// Stage only paths that were explicitly approved via file_edit yields.
		// AddAll() is intentionally NOT used: it would commit any unrelated
		// worktree changes (stale edits, leftover tmp files, etc.).
		_ = gr.AddFiles(approvedFiles)
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

	// Write per-run summary (CHECK 10.4.1).
	_ = writeSummary(r.wsDir, RunSummary{
		RunID:       run.ID,
		CaseID:      caseID,
		FinalStatus: finalStatus,
		CommitHash:  commitHash,
		EndTime:     endTime,
	})

	// Surface a non-success terminal state as an error so the CLI exits
	// non-zero (CI/automation must not treat a FAILED/KILLED run as success).
	if finalStatus != persistence.RunStatusSuccess {
		return fmt.Errorf("run #%d finished with status %s: %w", run.ID, finalStatus, ErrRunNotSuccessful)
	}
	return nil
}

// RunSummary is the on-disk representation of a completed run (CHECK 10.4.1).
type RunSummary struct {
	RunID       int64     `json:"run_id"`
	CaseID      int64     `json:"case_id"`
	FinalStatus string    `json:"final_status"`
	CommitHash  string    `json:"commit_hash,omitempty"`
	EndTime     time.Time `json:"end_time"`
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
	return os.WriteFile(filepath.Join(dir, "summary.json"), data, 0o600)
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
	fmt.Fprintf(os.Stdout, "   📦 Auto-stashed dirty tree in %s\n", repoPath)
	return true, nil
}

// webhookClient is shared across all webhook calls within a single run.
var webhookClient = &http.Client{Timeout: 30 * time.Second}

// loadWebhookSecret returns the decrypted per-project webhook HMAC secret, or
// nil when none is configured. A missing secret is not an error — it means the
// webhook channel runs unauthenticated (legacy behaviour, warned about once).
func (r *Runner) loadWebhookSecret(projectID int64, aesKey []byte) []byte {
	sec, err := r.store.GetSecret(webhookauth.SecretKeyName, &projectID)
	if err != nil || sec == nil {
		return nil
	}
	plaintext, err := crypto.Decrypt(aesKey, sec.EncryptedValue)
	if err != nil {
		obs.Log.Warn("webhook secret decrypt failed — treating channel as unauthenticated", "err", err)
		return nil
	}
	return []byte(plaintext)
}

// sendWebhookYield POSTs the yield to the project webhook and returns the
// operator decision. When secret is non-empty the request is HMAC-signed and
// the response signature is verified; an unsigned, mis-signed, stale, or
// otherwise unverifiable response is treated as a rejection so a network
// attacker cannot forge an approval.
func sendWebhookYield(webhookURL string, secret []byte, req ipc.IpcYieldRequest) ipc.IpcYieldResponse {
	reject := func(msg string) ipc.IpcYieldResponse {
		return ipc.IpcYieldResponse{Type: "yield_response", Approved: false, Feedback: msg}
	}
	body, _ := json.Marshal(req)

	httpReq, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		obs.Log.Warn("webhook request build failed — auto-rejecting", "err", err)
		return reject("webhook error: " + err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if len(secret) > 0 {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		httpReq.Header.Set(webhookauth.HeaderTimestamp, ts)
		httpReq.Header.Set(webhookauth.HeaderSignature, webhookauth.Sign(secret, ts, body))
	}

	resp, err := webhookClient.Do(httpReq)
	if err != nil {
		obs.Log.Warn("webhook POST failed — auto-rejecting", "err", err)
		return reject("webhook error: " + err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		obs.Log.Warn("webhook response read failed — auto-rejecting", "err", err)
		return reject("webhook read error: " + err.Error())
	}

	// Authenticated channel: the response MUST carry a valid signature.
	if len(secret) > 0 {
		if verr := webhookauth.Verify(secret,
			resp.Header.Get(webhookauth.HeaderTimestamp),
			resp.Header.Get(webhookauth.HeaderSignature),
			respBody, time.Now(), webhookauth.DefaultMaxSkew); verr != nil {
			obs.Log.Error("webhook response signature INVALID — rejecting (possible forgery)", "err", verr)
			return reject("webhook response failed signature verification: " + verr.Error())
		}
	}

	var yieldResp ipc.IpcYieldResponse
	if err := json.Unmarshal(respBody, &yieldResp); err != nil {
		obs.Log.Warn("webhook response decode failed — auto-rejecting", "err", err)
		return reject("webhook decode error: " + err.Error())
	}
	if yieldResp.Type == "" {
		yieldResp.Type = "yield_response"
	}
	return yieldResp
}

func timePtr(t time.Time) *time.Time { return &t }

// pathWithinRoot reports whether rel, joined onto root, stays inside root.
// Absolute paths and any path that climbs out via ".." are rejected. Used to
// sandbox agent-proposed file paths to the project repository at the commit
// boundary, independent of the Python runtime's own path checks.
func pathWithinRoot(root, rel string) bool {
	if rel == "" || filepath.IsAbs(rel) {
		return false
	}
	cleanRoot := filepath.Clean(root)
	joined := filepath.Clean(filepath.Join(cleanRoot, rel))
	return joined == cleanRoot || strings.HasPrefix(joined, cleanRoot+string(os.PathSeparator))
}

// scrubSecrets replaces every occurrence of each active secret value with
// "<REDACTED>" across all operator-visible fields before the yield request
// is presented via TUI, webhook, or HTTP approval server (CHECK 4.4.3 / 7.4.2).
// Scrubbing covers: ReasoningTrace, and every proposed edit's File, SearchBlock,
// and ReplaceBlock — agents can embed plaintext secrets in any of these.
func scrubSecrets(req ipc.IpcYieldRequest, activeValues []string) ipc.IpcYieldRequest {
	if len(activeValues) == 0 {
		return req
	}
	redact := func(s string) string {
		for _, v := range activeValues {
			if v != "" {
				s = strings.ReplaceAll(s, v, "<REDACTED>")
			}
		}
		return s
	}
	req.ReasoningTrace = redact(req.ReasoningTrace)
	for i := range req.ProposedEdits {
		req.ProposedEdits[i].File = redact(req.ProposedEdits[i].File)
		req.ProposedEdits[i].SearchBlock = redact(req.ProposedEdits[i].SearchBlock)
		req.ProposedEdits[i].ReplaceBlock = redact(req.ProposedEdits[i].ReplaceBlock)
	}
	return req
}
