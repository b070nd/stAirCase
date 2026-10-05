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

	"github.com/b070nd/stAirCase/src/internal/barrier"
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
	// RequireEvidence makes a recovery whose commit is made but whose evidence is
	// incomplete end with ErrEvidenceIncomplete (the result still names the commit).
	RequireEvidence bool
}

// RecoverResult is what a recovery committed.
type RecoverResult struct {
	Commit      string
	Proposals   int
	FinalReview bool
	// EvidenceErrors says what of the evidence (ledger, certificate, notes, audit
	// record, summary, the run's record) could not be written. The commit exists
	// either way; running recover again repairs the evidence on the same commit.
	EvidenceErrors []string
	Repaired       bool // the commit was already there: this run of recover finished what was missing
}

// ErrRecoveryRejected: the person refused the final review of a recovery.
var ErrRecoveryRejected = errors.New("the recovered change was rejected in its final review")

// Recover commits the proposals an interrupted run had approved. It trusts the
// audit chain only after verifying it, a journal entry only when the chain also
// records that approval, derives the files again from the base commit exactly as
// the run did, and moves the run's branch only if it is still where the run
// started. It writes a record of the operation before touching git, names it in the
// commit, and so can finish its own work after a crash and tell it from a commit
// anyone else made. The certificate says the run did not finish. The run's worktree
// is left as it is.
func (r *Runner) Recover(ctx context.Context, runID int64, opts RecoverOptions) (RecoverResult, error) {
	var res RecoverResult
	if err := ctx.Err(); err != nil {
		return res, err
	}
	run, err := r.store.GetRun(runID)
	if err != nil || run == nil {
		return res, fmt.Errorf("run #%d not found", runID)
	}
	op, err := loadOp(r.wsDir, runID)
	if err != nil {
		return res, err
	}
	switch {
	case run.GitCommitHash != "" && (op == nil || op.Commit != run.GitCommitHash):
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
	want, err := a.tree()
	if err != nil {
		return res, err
	}
	branch := strings.TrimPrefix(bound.Branch, "refs/heads/")
	fingerprint := approvalsOf(hist.Approvals)

	if err := ctx.Err(); err != nil { // cancelled while the history was read: nothing was begun
		return res, err
	}
	// One recovery of a run at a time.
	unlock, err := lockRecovery(r.wsDir, runID)
	if err != nil {
		return res, err
	}
	defer unlock()
	if op, err = loadOp(r.wsDir, runID); err != nil { // read again: another recovery may have finished meanwhile
		return res, err
	}
	if run, err = r.store.GetRun(runID); err != nil || run == nil {
		return res, fmt.Errorf("run #%d not found", runID)
	}
	if run.GitCommitHash != "" && (op == nil || op.Commit != run.GitCommitHash) {
		return res, fmt.Errorf("run #%d already has a commit (%.12s): there is nothing to recover", runID, run.GitCommitHash)
	}
	if op != nil && !op.matches(bound.Base, bound.Branch, want, fingerprint) {
		return res, errors.New("a recovery of this run was started for other approvals than the ones it has now: not continuing it")
	}
	if run.GitCommitHash != "" && r.recoveryEvidenceComplete(run, repo) {
		return res, fmt.Errorf("run #%d is already recovered (%.12s): there is nothing to recover", runID, run.GitCommitHash)
	}

	pl := &plan.Plan{Harness: bound.Harness, Digest: bound.PlanDigest, BlueprintHash: bound.Blueprint}
	// The final review a finished run would have had, kept durably before any commit
	// is made: if it cannot be recorded, nothing is committed.
	review := func() error {
		if opts.Confirm == nil {
			return errors.New("part of this change was approved as part of the agreed task, by evidence or by reviewer models: it needs the final review a finished run would have had, from a person at a terminal")
		}
		req := finalReviewRequest(a.files)
		req.ReasoningTrace = "Final review of a recovered run: the run was interrupted. Approve to commit exactly these files."
		answered := make(chan domain.YieldResponse, 1)
		go func() { answered <- opts.Confirm(req) }()
		var resp domain.YieldResponse
		select {
		case resp = <-answered:
		case <-ctx.Done(): // an answer nobody is waiting for any more is not one
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		reqJSON, _ := json.Marshal(req)
		if err := r.audit(runID, "recovery_final_review", map[string]any{"approved": resp.Approved, "feedback": resp.Feedback, "request_sha256": sha256Hex(reqJSON)}); err != nil {
			return fmt.Errorf("record the final review: %w", err)
		}
		if !resp.Approved {
			return ErrRecoveryRejected
		}
		res.FinalReview = true
		return nil
	}
	// The commit may be the run's own, made before its process died: its final review, if one
	// was needed, is then already on the audit chain.
	ownTip := gitOut(repo.path, "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	ownHead, adopted := "", false
	if ownTip != "" && ownTip != bound.Base {
		ownHead, adopted = runMadeCommit(repo.path, runID, run.CaseID, bound.Base, ownTip, a, events, needReview)
		needReview = needReview && !adopted
	}
	if op == nil {
		if needReview {
			if err := review(); err != nil {
				return res, err
			}
		}
		chainHead, err := r.store.GetLastEventHash(runID)
		if err != nil {
			return res, err
		}
		id, err := newOpID()
		if err != nil {
			return res, err
		}
		op = &recoveryOp{Op: id, RunID: runID, Base: bound.Base, Branch: bound.Branch, Tree: want, Approvals: fingerprint,
			ChainHead: chainHead, NeedsReview: needReview, Reviewed: needReview}
		if err := op.save(r.wsDir); err != nil {
			return res, fmt.Errorf("keep the recovery record: %w", err)
		}
		barrier.Hit(barrier.RecoverOpRecorded)
	} else if op.NeedsReview && !op.Reviewed {
		if err := review(); err != nil {
			return res, err
		}
		op.Reviewed = true
		if err := op.save(r.wsDir); err != nil {
			return res, fmt.Errorf("keep the recovery record: %w", err)
		}
	}

	// Delivery: the branch moves from the base to this operation's commit, or it is
	// already there. A recovery cancelled before this point delivers nothing.
	if err := ctx.Err(); err != nil {
		return res, err
	}
	var hash string
	switch tip := gitOut(repo.path, "rev-parse", "--verify", "-q", "refs/heads/"+branch); {
	case tip == "":
		return res, fmt.Errorf("the branch %s no longer exists", branch)
	case tip == bound.Base:
		if hash, err = a.commit(branch, commitMessage(runID, run.CaseID, pl, op.ChainHead)+recoveryTrailer+": "+op.Op+"\n"); err != nil {
			return res, fmt.Errorf("commit the recovered change: %w", err)
		}
		if hash == "" {
			return res, errors.New("the approved changes leave the files as they were: there is nothing to commit")
		}
	case op.owns(repo.path, tip):
		hash, res.Repaired = tip, true
	case adopted && tip == ownTip:
		hash, res.Repaired, op.ChainHead = tip, true, ownHead
	default:
		return res, fmt.Errorf("the branch %s has moved since the run started and its commit is not this recovery's", branch)
	}
	barrier.Hit(barrier.RecoverCommitted)
	res.Commit, res.Proposals = hash, len(kept)
	var evidenceErrs []string
	if op.Commit != hash {
		op.Commit = hash
		if err := op.save(r.wsDir); err != nil {
			evidenceErrs = append(evidenceErrs, "recovery record: "+err.Error())
		}
	}

	// The evidence. Every step is safe to do again, so a recover after a crash or a
	// failure repairs what is missing on the same commit.
	ev := evidence{recovered: true, policy: hist.PolicyDigest, initiator: hist.Initiator}
	ledger := Ledger{Base: bound.Base, Proposals: kept}
	// The run's own ledger and certificate, if it got that far, stay: the certificate names the ledger's digest.
	ownLedger, _ := os.ReadFile(LedgerPath(r.wsDir, runID))
	if adopted && len(ownLedger) > 0 {
		ev.ledger = sha256Hex(ownLedger)
	} else if ev.ledger, err = r.writeLedger(runID, ledger); err != nil {
		evidenceErrs = append(evidenceErrs, "ledger: "+err.Error())
	}
	if ev.ledger != "" {
		if err := attachNote(repo, LedgerNotesRef, LedgerPath(r.wsDir, runID), hash); err != nil {
			evidenceErrs = append(evidenceErrs, "ledger note: "+err.Error())
		}
	}
	ownCertificate := adopted && gitOut(repo.path, "notes", "--ref="+NotesRef, "show", hash) != "" && hasEvent(r.store, runID, "certificate_issued", hash)
	if !ownCertificate {
		if err := r.certify(runID, hash, bound.Base, op.ChainHead, pl, repo, ev); err != nil {
			evidenceErrs = append(evidenceErrs, "certificate: "+err.Error())
		}
	}
	now := time.Now()
	status, crashed := run.Status, run.Status == persistence.RunStatusRunning
	if crashed {
		status = persistence.RunStatusKilled
	}
	if !hasEvent(r.store, runID, "run_recovered", hash) {
		if err := r.audit(runID, "run_recovered", map[string]any{"commit": hash, "proposals": len(kept), "final_review": op.Reviewed,
			"forced": opts.Force, "evidence_errors": evidenceErrs}); err != nil {
			evidenceErrs = append(evidenceErrs, "audit record: "+err.Error())
		}
	} else if res.Repaired {
		if err := r.audit(runID, "recovery_repaired", map[string]any{"commit": hash, "evidence_errors": evidenceErrs}); err != nil {
			evidenceErrs = append(evidenceErrs, "audit record: "+err.Error())
		}
	}
	outcome := OutcomeRecovered
	if len(evidenceErrs) > 0 {
		outcome = OutcomeWithoutEvidence
	}
	if err := writeSummary(r.wsDir, RunSummary{RunID: runID, CaseID: run.CaseID, FinalStatus: status, CommitHash: hash, EndTime: now,
		Outcome: outcome, EvidenceErrors: evidenceErrs}); err != nil {
		evidenceErrs = append(evidenceErrs, "summary: "+err.Error())
	}
	// The run's own record comes last: it is what says "recovered", so a kill before
	// this point leaves a run that recover finishes again (it recognizes its commit).
	if run.GitCommitHash != hash {
		update := func() error { return r.store.UpdateRunStatus(runID, status, &now, hash) }
		if crashed { // nobody finished the run, so nobody finished its case either
			update = func() error { return r.store.FinishRun(runID, status, now, hash) }
		}
		if err := update(); err != nil {
			evidenceErrs = append(evidenceErrs, "the run's record: "+err.Error())
		}
	}
	res.EvidenceErrors = evidenceErrs
	if len(evidenceErrs) > 0 && opts.RequireEvidence {
		return res, fmt.Errorf("%w: %s", ErrEvidenceIncomplete, strings.Join(evidenceErrs, "; "))
	}
	return res, nil
}

// hasEvent reports whether the run's chain holds an event of that type about commit.
func hasEvent(store *persistence.Store, runID int64, eventType, commit string) bool {
	events, err := store.ListEventLogs(runID)
	if err != nil {
		return false
	}
	for _, e := range events {
		if e.EventType == eventType && strings.Contains(e.Payload, `"commit":"`+commit+`"`) {
			return true
		}
	}
	return false
}

// recoveryEvidenceComplete reports whether everything a recovery writes is there for
// the run's commit: ledger and certificate files, both notes, the audit record, the
// summary and the run's record. Presence is what is checked, not a flag: files and
// notes are things that go missing.
func (r *Runner) recoveryEvidenceComplete(run *persistence.Run, repo *GitRepo) bool {
	commit := run.GitCommitHash
	for _, f := range []string{LedgerPath(r.wsDir, run.ID), filepath.Join(r.wsDir, "audit", fmt.Sprintf("run-%d.certificate.json", run.ID))} {
		if _, err := os.Stat(f); err != nil {
			return false
		}
	}
	for _, ref := range []string{NotesRef, LedgerNotesRef} {
		if gitOut(repo.path, "notes", "--ref="+ref, "show", commit) == "" {
			return false
		}
	}
	if !hasEvent(r.store, run.ID, "run_recovered", commit) || !hasEvent(r.store, run.ID, "certificate_issued", commit) {
		return false
	}
	b, err := os.ReadFile(filepath.Join(r.wsDir, "runs", fmt.Sprintf("%d", run.ID), "summary.json"))
	if err != nil {
		return false
	}
	var s RunSummary
	return json.Unmarshal(b, &s) == nil && s.Outcome == OutcomeRecovered && s.CommitHash == commit
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
