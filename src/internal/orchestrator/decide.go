package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/monitor"
	"github.com/b070nd/stAirCase/src/internal/policy"
)

// deciders are who may decide a run's proposals, asked in a fixed order: the
// orchestrator refuses what cannot be applied as shown; drift supervision
// halts the run or sends the proposal to a human; the policy's limits send it
// to a human, its rules decide it; the validator decides in-scope edits; a
// human decides the rest.
type deciders struct {
	approvals *approvals // nil without a git repository
	drift     *policy.Supervisor
	policy    *policy.Engine
	validator *Validator
	askHuman  func(domain.YieldRequest) domain.YieldResponse
	display   *monitor.Display
	tracker   *monitor.Tracker
	audit     func(event string, fields map[string]any) error

	total, autoApproved int // proposals so far, and those the policy approved (limits)
}

// ruling is how one proposal was decided.
type ruling struct {
	resp    domain.YieldResponse
	source  string                   // orchestrator, drift, policy, validator:<model>, operator
	next    map[string]*approvedFile // the derived state an approval commits (file edits)
	files   []string                 // its paths, sorted
	drift   string                   // why drift supervision asked a human
	halt    bool                     // the run has drifted too far and stops
	refused bool                     // the orchestrator refused it before anyone decided
}

// decide rules on req, completing it with what the human is shown (Drift,
// Review).
func (d *deciders) decide(ctx context.Context, req *domain.YieldRequest) ruling {
	d.total++
	var rl ruling
	// The orchestrator derives exactly what approving this proposal would
	// commit. A proposal that cannot be applied as shown (bad path, search
	// text not found, too large) is refused before policy or human.
	if req.ActionType == domain.ActionFileEdit && d.approvals != nil {
		next, err := d.approvals.derive(req.ProposedEdits)
		if err != nil {
			if errors.Is(err, errBadPath) {
				_ = d.audit("approval_path_escape", map[string]any{"agent": req.AgentName, "detail": err.Error()})
			}
			d.display.AddActivity(fmt.Sprintf("%-14s REFUSE %s (%s)", req.AgentName, req.ActionType, err))
			return ruling{resp: domain.Decide(false, "refused by the orchestrator: "+err.Error()), source: "orchestrator", refused: true}
		}
		rl.next = next
		rl.files = slices.Sorted(maps.Keys(next))
	}

	// Drift supervision: a proposal reaching outside the stories' scope, past
	// a limit or at a checkpoint goes to a human, never to policy.
	rl.drift, rl.halt = d.drift.Check(rl.files)
	req.Drift = rl.drift
	if rl.halt {
		rl.resp, rl.source = domain.Decide(false, "refused: the run has drifted too far from its stories' scope and is halting"), "drift"
		return rl
	}
	// CHECK 7.2.1/7.2.2: once a session limit is hit, every proposal goes to a
	// human regardless of the policy's rules.
	if limitHit, limitReason := d.policy.CheckLimits(d.autoApproved, d.total); limitHit || rl.drift != "" {
		why := strings.Trim(limitReason+"; "+rl.drift, "; ")
		d.display.AddActivity(fmt.Sprintf("%-14s DRIFT  %s → HITL (%s)", req.AgentName, req.ActionType, why))
		return d.human(req, rl)
	}
	if dec := d.policy.Evaluate(*req); dec.Matched {
		if dec.Approved {
			d.autoApproved++
		}
		verb := "rejected"
		if dec.Approved {
			verb = "approved"
		}
		d.display.AddActivity(fmt.Sprintf("%-14s AUTO   %s → %s (%s)", req.AgentName, req.ActionType, verb, dec.Reason))
		rl.resp, rl.source = domain.Decide(dec.Approved, dec.Reason), "policy"
		return rl
	}
	note, resp, decided := d.validator.decide(ctx, *req, rl.files, rl.next, d.approvals, d.tracker)
	if decided {
		d.display.AddActivity(fmt.Sprintf("%-14s REVIEW %s → %v (%s)", req.AgentName, req.ActionType, resp.Approved, resp.Feedback))
		rl.resp, rl.source = resp, "validator:"+d.validator.Model
		return rl
	}
	req.Review = note
	if note != "" {
		d.validator.humanDecided()
	}
	return d.human(req, rl)
}

func (d *deciders) human(req *domain.YieldRequest, rl ruling) ruling {
	rl.resp, rl.source = d.askHuman(*req), "operator"
	d.display.AddActivity(fmt.Sprintf("%-14s HITL   %s → %v", req.AgentName, req.ActionType, rl.resp.Approved))
	return rl
}

// finalReview has a human approve the run's whole change once, when the
// validator approved changes no human has seen; the decision is audited. The
// request is secret-scrubbed like every proposal.
func (d *deciders) finalReview(baseSHA string, delivered []string) (bool, error) {
	final := domain.YieldRequest{Type: "yield_request", AgentName: "staircase", ActionType: domain.ActionFinalReview,
		ReasoningTrace: "Final review: the validator approved changes in this run. Approve to commit exactly these files."}
	for _, p := range slices.Sorted(maps.Keys(d.approvals.files)) {
		f := d.approvals.files[p]
		e := domain.ProposedEdit{File: p, SearchBlock: "(final content)", ReplaceBlock: string(f.content)}
		if f.deleted {
			e.SearchBlock, e.ReplaceBlock = MarkerDeleteFile, ""
		}
		final.ProposedEdits = append(final.ProposedEdits, e)
	}
	final = scrubSecrets(final, delivered)
	resp := d.askHuman(final)
	decided := yieldDecided(d.total+1, "operator", final, resp, baseSHA, d.approvals.files, "")
	decided["files"] = digest(d.approvals.files) // what was offered, approved or not
	if err := d.audit("yield_decided", decided); err != nil {
		return false, fmt.Errorf("audit final review: %w", err)
	}
	return resp.Approved, nil
}

// yieldDecided is the audit record of a decision: the request's hash, and for
// an approved change the digest of exactly the approved state.
func yieldDecided(seq int, source string, req domain.YieldRequest, resp domain.YieldResponse, baseSHA string, approved map[string]*approvedFile, drift string) map[string]any {
	reqJSON, _ := json.Marshal(req)
	fields := map[string]any{"seq": seq, "source": source, "agent": req.AgentName, "action_type": req.ActionType,
		"approved": resp.Approved, "feedback": resp.Feedback, "request_sha256": sha256Hex(reqJSON), "base_sha": baseSHA}
	if resp.Approved && approved != nil {
		fields["files"] = digest(approved)
	}
	if drift != "" {
		fields["drift"] = drift
	}
	if req.ReviewAfter {
		fields["review_after"] = true
	}
	return fields
}
