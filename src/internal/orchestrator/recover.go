package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
)

// A run keeps every approval it gives in a journal, written before the agent
// is told the answer, so that what was approved survives a crash or a kill:
// `staircase recover <run>` commits exactly it. The journal is JSON Lines under
// journal/ (readable by the user only); a line is trusted only if the audit
// chain also records that approval.

// journalEntry is one approved proposal: the exact request that was decided, as
// its SHA-256 appears in the proposal's yield_decided record.
type journalEntry struct {
	Seq     int             `json:"seq"`
	Source  string          `json:"source"`
	Request json.RawMessage `json:"request"`
}

func journalPath(wsDir string, runID int64) string {
	return filepath.Join(wsDir, "journal", fmt.Sprintf("run-%d.approved.jsonl", runID))
}

// appendJournal writes one approved proposal and flushes it to disk.
func appendJournal(wsDir string, runID int64, req domain.YieldRequest, seq int, source string) error {
	path := journalPath(wsDir, runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	reqJSON, _ := json.Marshal(req) // the bytes yieldDecided hashes
	line, err := json.Marshal(journalEntry{Seq: seq, Source: source, Request: reqJSON})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// RecoverOptions are for [Runner.Recover].
type RecoverOptions struct {
	// Force recovers a run whose record still says RUNNING: only when its
	// process is known to be gone.
	Force bool
	// Confirm asks a person to approve the whole change when some of it was
	// approved by the agreed task, evidence or reviewer models, as a run's final
	// review would. Nil refuses such a recovery.
	Confirm func(domain.YieldRequest) domain.YieldResponse
}

// RecoverResult is what a recovery committed.
type RecoverResult struct {
	Commit      string
	Proposals   int
	FinalReview bool
}

// ErrRecoveryRejected: the person refused the final review of a recovery.
var ErrRecoveryRejected = errors.New("the recovered change was rejected in its final review")

// Recover commits the proposals an interrupted run had approved. It trusts a
// journal entry only when the run's audit chain records the same approval, derives
// the files again from the base commit exactly as the run did, and moves the
// run's branch only if it is still where the run started. The certificate says the
// run did not finish. The run's worktree is left as it is.
func (r *Runner) Recover(_ context.Context, runID int64, opts RecoverOptions) (RecoverResult, error) {
	var res RecoverResult
	run, err := r.store.GetRun(runID)
	if err != nil || run == nil {
		return res, fmt.Errorf("run #%d not found", runID)
	}
	switch {
	case run.GitCommitHash != "":
		return res, fmt.Errorf("run #%d already has a commit (%.12s): there is nothing to recover", runID, run.GitCommitHash)
	case run.Status == persistence.RunStatusSuccess:
		return res, fmt.Errorf("run #%d finished: there is nothing to recover", runID)
	case run.Status == persistence.RunStatusRunning && !opts.Force:
		return res, fmt.Errorf("run #%d may still be running: if its process is gone, recover it with --force", runID)
	}
	// The audit chain must verify before any row of it is believed.
	if err := r.store.VerifyChain(runID); err != nil {
		return res, fmt.Errorf("the audit chain of run #%d does not verify, so nothing in it can be trusted: %w", runID, err)
	}
	events, err := r.store.ListEventLogs(runID)
	if err != nil {
		return res, err
	}
	hist, err := auditedHistory(events)
	if err != nil {
		return res, fmt.Errorf("the audit history of run #%d is not one a run can have written: %w", runID, err)
	}
	bound := hist.Bound
	if bound.Base == "" || bound.Branch == "" {
		return res, fmt.Errorf("run #%d never got as far as a run branch: there is nothing to recover", runID)
	}
	journal, err := readJournalFile(r.wsDir, runID)
	if errors.Is(err, os.ErrNotExist) {
		return res, fmt.Errorf("run #%d has no journal of approvals: there is nothing to recover", runID)
	} else if err != nil {
		return res, fmt.Errorf("the approval journal of run #%d cannot be trusted: %w", runID, err)
	}
	kept, err := reconcile(hist, journal)
	if err != nil {
		return res, fmt.Errorf("run #%d cannot be recovered: %w", runID, err)
	}
	if len(kept) == 0 {
		return res, fmt.Errorf("run #%d had nothing approved: there is nothing to recover", runID)
	}
	needReview := false
	for _, p := range kept {
		needReview = needReview || (p.Source != "operator" && p.Source != "policy")
	}

	c, err := r.store.GetCase(run.CaseID)
	if err != nil || c == nil {
		return res, fmt.Errorf("case of run #%d: %w", runID, err)
	}
	project, err := r.store.GetProject(c.ProjectID)
	if err != nil || project == nil {
		return res, fmt.Errorf("project of run #%d: %w", runID, err)
	}
	repo, err := OpenGitRepo(project.SourcePath)
	if err != nil {
		return res, err
	}
	// Derived against an empty directory, as a rebuild does: what is on disk
	// now has no say in what was approved.
	scratch, err := os.MkdirTemp("", "staircase-recover-")
	if err != nil {
		return res, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	a, err := newApprovals(&GitRepo{r: repo.r, w: repo.w, path: scratch}, bound.Base)
	if err != nil {
		return res, err
	}
	for i, p := range kept {
		next, err := a.derive(p.Edits)
		if err != nil {
			return res, fmt.Errorf("proposal %d no longer applies: %w", p.Seq, err)
		}
		if !matchesDigest(hist.Approvals[i].Files, next) { // what the chain says was approved is what is derived
			return res, fmt.Errorf("proposal %d derives other bytes than the audit chain recorded as approved", p.Seq)
		}
		a.record(next)
	}
	a.repo = repo // the commit is made in the run's repository

	// A recovery killed after its commit left that commit and none of the rest
	// (ledger, certificate, the run's record). The commit is the run's own when it
	// is on the base and has exactly the tree the approvals produce: then it is
	// finished, not made again, and not reviewed again.
	branch := strings.TrimPrefix(bound.Branch, "refs/heads/")
	var hash, chainHead string
	if tip := gitOut(repo.path, "rev-parse", "--verify", "-q", "refs/heads/"+branch); tip != "" && tip != bound.Base {
		want, err := a.tree()
		if err != nil {
			return res, err
		}
		if gitOut(repo.path, "rev-parse", tip+"^") == bound.Base && gitOut(repo.path, "rev-parse", tip+"^{tree}") == want {
			hash = tip
			chainHead = strings.TrimSpace(strings.TrimPrefix(gitOut(repo.path, "log", "-1", "--format=%(trailers:key=Staircase-Chain,valueonly)", tip), "sha256:"))
			if chainHead == "" {
				return res, fmt.Errorf("commit %.12s on %s names no audit chain head: it is not a recovered commit", tip, branch)
			}
			needReview = false
		} else {
			return res, fmt.Errorf("the branch %s has moved since the run started and is not this run's recovered commit", branch)
		}
	}

	if needReview {
		if opts.Confirm == nil {
			return res, errors.New("part of this change was approved as part of the agreed task, by evidence or by reviewer models: it needs the final review a finished run would have had, from a person at a terminal")
		}
		req := finalReviewRequest(a.files)
		req.ReasoningTrace = "Final review of a recovered run: the run was interrupted. Approve to commit exactly these files."
		resp := opts.Confirm(req)
		reqJSON, _ := json.Marshal(req)
		_ = r.audit(runID, "recovery_final_review", map[string]any{"approved": resp.Approved, "feedback": resp.Feedback, "request_sha256": sha256Hex(reqJSON)})
		if !resp.Approved {
			return res, ErrRecoveryRejected
		}
		res.FinalReview = true
	}

	pl := &plan.Plan{Harness: bound.Harness, Digest: bound.PlanDigest, BlueprintHash: bound.Blueprint}
	if hash == "" {
		if chainHead, err = r.store.GetLastEventHash(runID); err != nil {
			return res, err
		}
		if hash, err = a.commit(branch, commitMessage(runID, run.CaseID, pl, chainHead)); err != nil {
			return res, fmt.Errorf("commit the recovered change: %w", err)
		}
		if hash == "" {
			return res, errors.New("the approved changes leave the files as they were: there is nothing to commit")
		}
	}
	res.Commit, res.Proposals = hash, len(kept)

	var evidenceErrs []string
	ev := evidence{recovered: true}
	if ev.ledger, err = r.writeLedger(runID, Ledger{Base: bound.Base, Proposals: kept}); err != nil {
		evidenceErrs = append(evidenceErrs, "ledger: "+err.Error())
	} else if err := attachNote(repo, LedgerNotesRef, LedgerPath(r.wsDir, runID), hash); err != nil {
		evidenceErrs = append(evidenceErrs, "ledger note: "+err.Error())
	}
	if err := r.certify(runID, hash, bound.Base, chainHead, pl, repo, ev); err != nil {
		evidenceErrs = append(evidenceErrs, "certificate: "+err.Error())
	}
	now := time.Now()
	status, crashed := run.Status, run.Status == persistence.RunStatusRunning
	if crashed {
		status = persistence.RunStatusKilled
	}
	_ = r.audit(runID, "run_recovered", map[string]any{"commit": hash, "proposals": len(kept), "final_review": res.FinalReview,
		"forced": opts.Force, "evidence_errors": evidenceErrs})
	outcome := OutcomeRecovered
	if len(evidenceErrs) > 0 {
		outcome = OutcomeWithoutEvidence
	}
	_ = writeSummary(r.wsDir, RunSummary{RunID: runID, CaseID: run.CaseID, FinalStatus: status, CommitHash: hash, EndTime: now,
		Outcome: outcome, EvidenceErrors: evidenceErrs})
	// The run's own record comes last: it is what says "recovered", so a kill before
	// this point leaves a run that recover finishes again (it recognizes its commit).
	update := func() error { return r.store.UpdateRunStatus(runID, status, &now, hash) }
	if crashed { // nobody finished the run, so nobody finished its case either
		update = func() error { return r.store.FinishRun(runID, status, now, hash) }
	}
	if err := update(); err != nil {
		return res, fmt.Errorf("record the commit on run #%d: %w", runID, err)
	}
	return res, nil
}

// gitOut is git's output in repo, or "" when it fails.
func gitOut(repo string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", repo, "-c", "core.hooksPath=/dev/null"}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// JournaledApprovals is how many approved changes a run's journal holds, for
// `staircase doctor` to point at a run that can be recovered.
func JournaledApprovals(wsDir string, runID int64) int {
	raw, err := os.ReadFile(journalPath(wsDir, runID))
	if err != nil {
		return 0
	}
	n := 0 // counted leniently: a damaged journal is still a reason to run recover, which says what is wrong
	for _, line := range bytes.Split(raw, []byte("\n")) {
		var e journalEntry
		if json.Unmarshal(line, &e) == nil && e.Seq > 0 {
			n++
		}
	}
	return n
}
