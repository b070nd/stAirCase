package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/domain"
)

// Bridging a parked proposal with a prototype (ADR 0007): the parked change on top of the change an away run offers, checked and
// thrown away; and, if the owner wants it, adopted as an ordinary approval of theirs.

// PrototypeResult is what a parked proposal would do on top of the change on offer.
type PrototypeResult struct {
	Seq     int
	Files   []string
	Diff    string // against the change on offer
	Checks  []certificate.Check
	Passed  bool
	Failed  string // why not, when it did not pass or no longer applies
	Applies bool   // its edits still apply on top of the change on offer
}

// approvedNow is an away run's approved state, derived again from its verified chain, with what checking it needs.
type approvedNow struct {
	a       *approvals
	repo    *GitRepo
	hist    history
	events  []domain.RunEventLog
	opts    savedOptions
	cleanup func()
}

func (r *Runner) approvedNow(runID int64) (*approvedNow, error) {
	run, err := r.store.GetRun(runID)
	if err != nil || run == nil {
		return nil, fmt.Errorf("run #%d not found", runID)
	}
	c, err := r.store.GetCase(run.CaseID)
	if err != nil || c == nil {
		return nil, fmt.Errorf("case of run #%d: %w", runID, err)
	}
	project, err := r.store.GetProject(c.ProjectID)
	if err != nil || project == nil {
		return nil, fmt.Errorf("project of run #%d: %w", runID, err)
	}
	repo, err := OpenGitRepo(project.SourcePath)
	if err != nil {
		return nil, err
	}
	events, err := r.store.ListVerifiedEventLogs(runID)
	if err != nil {
		return nil, fmt.Errorf("the audit chain of run #%d does not verify: %w", runID, err)
	}
	hist, err := auditedHistory(events)
	if err != nil {
		return nil, fmt.Errorf("the audit history of run #%d is not one a run can have written: %w", runID, err)
	}
	journal, err := readJournalFile(r.wsDir, runID)
	if err != nil {
		return nil, fmt.Errorf("the approval journal of run #%d: %w", runID, err)
	}
	kept, err := reconcile(hist, journal)
	if err != nil {
		return nil, err
	}
	var opts savedOptions
	if b, err := os.ReadFile(savedOptionsPath(r.wsDir, runID)); err != nil || json.Unmarshal(b, &opts) != nil || len(opts.Checks) == 0 {
		return nil, fmt.Errorf("the checks run #%d was started with cannot be read", runID)
	}
	a, cleanup, err := deriveApproved(repo, hist.Bound.Base, kept, hist.Approvals)
	if err != nil {
		return nil, err
	}
	a.repo = repo // checkouts for the checks are made from the run's repository
	return &approvedNow{a: a, repo: repo, hist: hist, events: events, opts: opts, cleanup: cleanup}, nil
}

func (s *approvedNow) gate(wsDir string) *evidenceGate {
	return &evidenceGate{approvals: s.a, checks: s.opts.Checks, sandbox: s.opts.Sandbox, wsDir: wsDir, timeout: time.Duration(s.opts.CheckTimeoutNS)}
}

// parkedRequest is the request of the parked proposal number seq, as the chain holds it (what the owner was shown).
func parkedRequest(events []domain.RunEventLog, seq int) (domain.YieldRequest, error) {
	var last string
	for _, e := range events {
		switch e.EventType {
		case "yield_request":
			last = e.Payload
		case "yield_decided":
			var d struct {
				Seq    int    `json:"seq"`
				Source string `json:"source"`
			}
			if json.Unmarshal([]byte(e.Payload), &d) != nil || d.Seq != seq {
				continue
			}
			if d.Source != "parked" {
				return domain.YieldRequest{}, fmt.Errorf("proposal %d was not parked", seq)
			}
			var req domain.YieldRequest
			if json.Unmarshal([]byte(last), &req) != nil || len(req.ProposedEdits) == 0 {
				return domain.YieldRequest{}, fmt.Errorf("the request of parked proposal %d is not on the chain whole (too large to keep?)", seq)
			}
			if req.ActionType != domain.ActionFileEdit {
				return domain.YieldRequest{}, fmt.Errorf("parked proposal %d is a %s: only a file change can be prototyped (a command would have to run)", seq, req.ActionType)
			}
			return req, nil
		}
	}
	return domain.YieldRequest{}, fmt.Errorf("no parked proposal %d on the chain", seq)
}

// adoptedProposals are the parked proposals an owner adopted: their numbers, from approved requests that name them.
func adoptedProposals(events []domain.RunEventLog) map[int]bool {
	adopted, pending := map[int]bool{}, 0
	for _, e := range events {
		switch e.EventType {
		case "yield_request":
			var q struct {
				Adopts int `json:"adopts"`
			}
			_ = json.Unmarshal([]byte(e.Payload), &q)
			pending = q.Adopts
		case "yield_decided":
			var d struct {
				Approved bool `json:"approved"`
			}
			if json.Unmarshal([]byte(e.Payload), &d) == nil && d.Approved && pending > 0 {
				adopted[pending] = true
			}
			pending = 0
		}
	}
	return adopted
}

// Prototype shows what parked proposal seq would do on top of the change an away run offers: its files, its diff and the run's own
// checks on the result, in a checkout that is thrown away. It writes nothing.
func (r *Runner) Prototype(ctx context.Context, runID int64, seq int) (PrototypeResult, error) {
	res := PrototypeResult{Seq: seq}
	if _, err := r.ReviewOf(runID); err != nil {
		return res, err
	}
	st, err := r.approvedNow(runID)
	if err != nil {
		return res, err
	}
	defer st.cleanup()
	req, err := parkedRequest(st.events, seq)
	if err != nil {
		return res, err
	}
	next, err := st.a.derive(req.ProposedEdits)
	if err != nil {
		res.Failed = "it no longer applies on top of the change on offer: " + err.Error()
		return res, nil
	}
	res.Applies, res.Files = true, slices.Sorted(maps.Keys(next))
	res.Diff = st.diff(next)
	res.Checks, res.Failed = st.gate(r.wsDir).runChecks(ctx, next)
	res.Passed = res.Failed == "" && len(res.Checks) > 0
	return res, nil
}

// diff is next against the approved state, as a unified diff.
func (s *approvedNow) diff(next map[string]*approvedFile) string {
	dir, err := os.MkdirTemp("", "staircase-prototype-diff-")
	if err != nil {
		return ""
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var out bytes.Buffer
	for _, p := range slices.Sorted(maps.Keys(next)) {
		before, after := filepath.Join(dir, "a"), filepath.Join(dir, "b")
		var was, is []byte
		if cur, _ := s.a.current(p); cur != nil && !cur.deleted {
			was = cur.content
		}
		if f := next[p]; f != nil && !f.deleted {
			is = f.content
		}
		_ = os.WriteFile(before, was, 0o600)
		_ = os.WriteFile(after, is, 0o600)
		d, _ := exec.Command("diff", "-u", "--label", "a/"+p, "--label", "b/"+p, before, after).Output() // exit 1: they differ
		out.Write(d)
	}
	return out.String()
}

// Adopt makes parked proposal seq of an away run an approval of the owner's (ADR 0007): the new state is derived and checked first,
// and only a passing one is recorded, as a live approval would be (request, journal line, decision), followed by the checks of the
// new tree and a new review. shown is the review entry the owner was looking at; a stale one is refused. An adoption that was
// interrupted after its decision is finished by adopting again.
func (r *Runner) Adopt(ctx context.Context, runID int64, seq int, shown string) (Review, error) {
	rv, err := r.ReviewOf(runID)
	if err != nil {
		return rv, err
	}
	if shown != rv.Entry {
		return rv, fmt.Errorf("this adoption was made on %.12s, which is not the review run #%d offers (%.12s): look at it again", shown, runID, rv.Entry)
	}
	release, err := claimRun(r.wsDir, runID)
	if err != nil {
		return rv, fmt.Errorf("run #%d is busy: %w", runID, err)
	}
	defer release()
	st, err := r.approvedNow(runID)
	if err != nil {
		return rv, err
	}
	defer st.cleanup()

	adopted := adoptedProposals(st.events)[seq]
	if adopted && reviewedSinceAdoption(st.events, seq) {
		return rv, fmt.Errorf("parked proposal %d of run #%d is already part of its change", seq, runID)
	}
	gate := st.gate(r.wsDir)
	var results []certificate.Check
	if !adopted {
		req, err := parkedRequest(st.events, seq)
		if err != nil {
			return rv, err
		}
		next, err := st.a.derive(req.ProposedEdits)
		if err != nil {
			return rv, fmt.Errorf("not adopted: it no longer applies on top of the change on offer: %w", err)
		}
		var failed string
		if results, failed = gate.runChecks(ctx, next); failed != "" || len(results) == 0 {
			return rv, fmt.Errorf("not adopted, nothing recorded: the checks do not pass with it: %s", failed)
		}
		if err := r.recordAdoption(runID, st, req, seq, next); err != nil {
			return rv, err
		}
		barrier.Hit(barrier.AdoptApproved)
		st.a.record(next)
	} else { // finishing an interrupted adoption: its approval is already part of the derived state
		var failed string
		if results, failed = gate.runChecks(ctx, nil); failed != "" || len(results) == 0 {
			return rv, fmt.Errorf("the change with the adopted proposal does not pass its checks: %s", failed)
		}
	}
	tree, err := st.a.tree()
	if err != nil {
		return rv, err
	}
	if err := r.audit(runID, "phase_checked", map[string]any{"tree": tree, "checks": checkEvidence(results), "passed": true, "failed": ""}); err != nil {
		return rv, fmt.Errorf("audit phase_checked: %w", err)
	}
	checked, err := r.store.GetLastEventHash(runID)
	if err != nil {
		return rv, err
	}
	stillParked := 0
	for _, p := range rv.ParkedProposals {
		if p.Seq != seq {
			stillParked++
		}
	}
	if err := r.audit(runID, "awaiting_review", map[string]any{"tree": tree, "files": digest(st.a.files), "phase_checked": checked, "parked": stillParked}); err != nil {
		return rv, fmt.Errorf("audit awaiting_review: %w", err)
	}
	return r.ReviewOf(runID)
}

// recordAdoption writes what a live approval writes, in the same order: the request, its journal line, the owner's decision.
func (r *Runner) recordAdoption(runID int64, st *approvedNow, parked domain.YieldRequest, seq int, next map[string]*approvedFile) error {
	req := domain.YieldRequest{Type: "yield_request", AgentName: "staircase", ActionType: domain.ActionFileEdit, ProposedEdits: parked.ProposedEdits,
		ReasoningTrace: fmt.Sprintf("the owner adopted parked proposal %d after a prototype", seq), Adopts: seq}
	fields := map[string]any{}
	b, _ := json.Marshal(req)
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	if err := r.audit(runID, "yield_request", fields); err != nil {
		return fmt.Errorf("record the adopted request: %w", err)
	}
	n := st.hist.LastDecided + 1
	if err := r.journalOnce(runID, req, n); err != nil {
		return err
	}
	decided := yieldDecided(n, "operator", req, domain.Decide(true, "adopted by the owner after a prototype"), st.hist.Bound.Base, next, "")
	decided["paths"], decided["adopts"] = slices.Sorted(maps.Keys(next)), seq
	if err := r.audit(runID, "yield_decided", decided); err != nil {
		return fmt.Errorf("record the owner's approval: %w", err)
	}
	return nil
}

// journalOnce appends the journal line of approval n, unless an adoption interrupted before its decision already wrote exactly it; a
// different line with that number refuses (two lines with one number would make the journal unreadable for recovery).
func (r *Runner) journalOnce(runID int64, req domain.YieldRequest, n int) error {
	j, err := readJournalFile(r.wsDir, runID)
	if err != nil {
		return err
	}
	want, _ := json.Marshal(req)
	for _, e := range j.Entries {
		if e.Seq == n {
			if bytes.Equal(e.Request, want) {
				return nil
			}
			return errors.New("the approval journal already holds another proposal under the number this adoption would take")
		}
	}
	return appendJournal(r.wsDir, runID, req, n, "operator")
}

// reviewedSinceAdoption: a review was offered after the owner adopted parked proposal seq (the adoption finished).
func reviewedSinceAdoption(events []domain.RunEventLog, seq int) bool {
	adoptedAt := -1
	pending := 0
	for i, e := range events {
		switch e.EventType {
		case "yield_request":
			var q struct {
				Adopts int `json:"adopts"`
			}
			_ = json.Unmarshal([]byte(e.Payload), &q)
			pending = q.Adopts
		case "yield_decided":
			if pending == seq {
				adoptedAt = i
			}
			pending = 0
		case "awaiting_review":
			if adoptedAt >= 0 && i > adoptedAt {
				return true
			}
		}
	}
	return false
}
