package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/certificate"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/history"
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
	hist    history.History
	terms   Terms
	cleanup func()
}

func (r *Runner) approvedNow(runID int64) (*approvedNow, error) {
	run, err := r.store.GetRun(runID)
	if err != nil || run == nil {
		return nil, fmt.Errorf("run #%d not found", runID)
	}
	opened, err := r.openRun(run)
	if err != nil {
		return nil, err
	}
	if !opened.journaled && len(opened.hist.Approvals()) > 0 { // an away run that approved nothing has no journal
		return nil, fmt.Errorf("run #%d has no approval journal: an away run that offers a review has one", runID)
	}
	terms, err := opened.terms(r.wsDir)
	if err != nil {
		return nil, fmt.Errorf("the terms run #%d was started with: %w", runID, err)
	}
	if len(terms.Checks) == 0 {
		return nil, fmt.Errorf("run #%d was started with no checks to run", runID)
	}
	repo, err := opened.repo()
	if err != nil {
		return nil, err
	}
	a, cleanup, err := opened.derive(repo)
	if err != nil {
		return nil, err
	}
	a.repo = repo // checkouts for the checks are made from the run's repository
	return &approvedNow{a: a, repo: repo, hist: opened.hist, terms: terms, cleanup: cleanup}, nil
}

func (s *approvedNow) gate(wsDir string) *evidenceGate {
	return &evidenceGate{approvals: s.a, checks: s.terms.Checks, sandbox: s.terms.Sandbox, wsDir: wsDir, timeout: s.terms.CheckTimeout}
}

// parkedRequest is the request of the parked proposal number seq, as the chain holds it (what the owner was shown).
func parkedRequest(h history.History, seq int) (domain.YieldRequest, error) {
	d, ok := h.Decision(seq)
	switch {
	case !ok:
		return domain.YieldRequest{}, fmt.Errorf("no parked proposal %d on the chain", seq)
	case d.Source != "parked":
		return domain.YieldRequest{}, fmt.Errorf("proposal %d was not parked", seq)
	case d.Request == nil || len(d.Request.ProposedEdits) == 0:
		return domain.YieldRequest{}, fmt.Errorf("the request of parked proposal %d is not on the chain whole (too large to keep?)", seq)
	case d.Request.ActionType != domain.ActionFileEdit:
		return domain.YieldRequest{}, fmt.Errorf("parked proposal %d is a %s: only a file change can be prototyped (a command would have to run)", seq, d.Request.ActionType)
	}
	return d.Request.YieldRequest, nil
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
	req, err := parkedRequest(st.hist, seq)
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

	_, adopted := st.hist.Adopted()[seq]
	if adopted && st.hist.ReviewedSinceAdoption(seq) {
		return rv, fmt.Errorf("parked proposal %d of run #%d is already part of its change", seq, runID)
	}
	gate := st.gate(r.wsDir)
	var results []certificate.Check
	if !adopted {
		req, err := parkedRequest(st.hist, seq)
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
	if err := r.log(runID).Append(history.PhaseChecked{Tree: tree, Checks: checkEvidence(results), Passed: true}); err != nil {
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
	if err := r.log(runID).Append(history.AwaitingReview{Tree: tree, Files: digest(st.a.files), PhaseChecked: checked, Parked: stillParked}); err != nil {
		return rv, fmt.Errorf("audit awaiting_review: %w", err)
	}
	return r.ReviewOf(runID)
}

// recordAdoption writes what a live approval writes, in the same order: the request, its journal line, the owner's decision.
func (r *Runner) recordAdoption(runID int64, st *approvedNow, parked domain.YieldRequest, seq int, next map[string]*approvedFile) error {
	req := domain.YieldRequest{Type: "yield_request", AgentName: "staircase", ActionType: domain.ActionFileEdit, ProposedEdits: parked.ProposedEdits,
		ReasoningTrace: fmt.Sprintf("the owner adopted parked proposal %d after a prototype", seq), Adopts: seq}
	if err := r.log(runID).Append(history.Request{YieldRequest: req}); err != nil {
		return fmt.Errorf("record the adopted request: %w", err)
	}
	entry, err := r.store.GetLastEventHash(runID) // the run's lock is held: nothing else writes in between
	if err != nil {
		return err
	}
	n := st.hist.LastDecided + 1
	rl := ruling{resp: domain.Decide(true, "adopted by the owner after a prototype"), source: "operator", next: next}
	// as a live approval is consumed; no run is live, so nothing ends it
	if err := r.consume(context.Background(), runID, n, req, &rl, func(bool) history.Decided {
		decided := yieldDecided(n, "operator", req, rl.resp, st.hist.Bound.BaseSHA, next, "")
		decided.RequestEntry, decided.Paths, decided.Adopts = entry, slices.Sorted(maps.Keys(next)), seq
		return decided
	}); err != nil {
		return fmt.Errorf("record the owner's approval: %w", err)
	}
	return nil
}
