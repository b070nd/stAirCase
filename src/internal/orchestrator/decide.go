package orchestrator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

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
	task      bool          // ApproveInScope (or ApproveOnEvidence) with an agreed task
	evidence  *evidenceGate // with ApproveOnEvidence: in-scope changes are approved on evidence, not on the task alone
	signal    *Signal
	redact    func(string) string                                                 // removes delivered secret values from what a person is shown
	sign      *decisionSigning                                                    // checks and adds signatures on human decisions
	askHuman  func(req domain.YieldRequest, signText string) domain.YieldResponse // signText: what a person signs, up to the verb
	display   *monitor.Display
	tracker   *monitor.Tracker
	audit     func(event string, fields map[string]any) error

	total, autoApproved int  // proposals so far, and those the policy approved (limits)
	taskApproved        bool // something was approved as part of the task: a person reviews the result
}

// taskCheckpointEvery: with ApproveInScope, a person still sees at least one
// in every so many proposals.
const taskCheckpointEvery = 5

// ruling is how one proposal was decided.
type ruling struct {
	resp    domain.YieldResponse
	source  string                   // orchestrator, drift, policy, validator:<model>, operator
	next    map[string]*approvedFile // the derived state an approval commits (file edits)
	files   []string                 // its paths, sorted
	drift   string                   // why drift supervision asked a human
	halt    bool                     // the run has drifted too far and stops
	refused bool                     // the orchestrator refused it before anyone decided

	decideMS int64          // a person's time to decide
	lines    int            // lines the change adds or removes
	signed   map[string]any // the signature on a person's decision, for the audit record
	evidence map[string]any // what an evidence-based approval rests on, for the audit record
}

// decide rules on req, completing it with what the human is shown (Drift,
// Review). A signal can only turn an automatic approval into a person's
// decision.
func (d *deciders) decide(ctx context.Context, req *domain.YieldRequest) ruling {
	req.Drift, req.Guard, req.Review, req.Before = "", "", "", nil // the orchestrator's to fill, never the agent's
	for i := range req.ProposedEdits {                             // what a person is shown instead of unreadable bytes
		e := &req.ProposedEdits[i]
		e.BinaryBytes, e.BinarySHA256 = 0, ""
		if b, err := base64.StdEncoding.DecodeString(e.ContentB64); err == nil && e.ContentB64 != "" {
			e.BinaryBytes, e.BinarySHA256 = len(b), sha256Hex(b)
		}
	}
	rl := d.rule(ctx, req)
	if d.signal != nil && rl.resp.Approved && rl.source != "operator" && rl.next != nil {
		before := map[string]*approvedFile{}
		for p := range rl.next {
			before[p], _ = d.approvals.current(p)
		}
		if why := d.signal.escalate(ctx, before, rl.next, d.audit); why != "" {
			req.Review = why
			return d.human(req, rl)
		}
	}
	return rl
}

// rule decides req without the signal.
func (d *deciders) rule(ctx context.Context, req *domain.YieldRequest) ruling {
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
		for _, e := range req.ProposedEdits {
			if e.SearchBlock != MarkerNewFile {
				continue
			}
			if cur, _ := d.approvals.current(e.File); cur != nil && !cur.deleted && len(cur.content) <= validatorMaxFile {
				if req.Before == nil {
					req.Before = map[string]string{}
				}
				req.Before[e.File] = d.redact(string(cur.content))
			}
		}
	}

	// Drift supervision: a proposal reaching outside the stories' scope, past
	// a limit or at a checkpoint goes to a human, never to policy.
	rl.drift, rl.halt = d.drift.Check(rl.files)
	req.Drift = rl.drift
	if rl.next != nil {
		req.Guard = guard(rl.next, func(p string) *approvedFile { f, _ := d.approvals.current(p); return f })
	}
	if rl.halt {
		rl.resp, rl.source = domain.Decide(false, "refused: the run has drifted too far from its stories' scope and is halting"), "drift"
		return rl
	}
	// CHECK 7.2.1/7.2.2: once a session limit is hit, every proposal goes to a
	// human regardless of the policy's rules.
	if limitHit, limitReason := d.policy.CheckLimits(d.autoApproved, d.total); limitHit || rl.drift != "" || req.Guard != "" {
		why := strings.Trim(limitReason+"; "+rl.drift+"; "+req.Guard, "; ")
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
	// The agreed task: the proposal is in scope (no drift), no guard or limit
	// sent it to a person, and it is not a sensitive file.
	if d.task && req.ActionType == domain.ActionFileEdit && rl.next != nil {
		if f := sensitive(rl.files); f != "" {
			req.Review = "sensitive path " + f + " - a person decides even inside the agreed task"
			return d.human(req, rl)
		}
		if d.evidence != nil {
			return d.onEvidence(ctx, req, rl)
		}
		d.taskApproved = true
		d.display.AddActivity(fmt.Sprintf("%-14s TASK   %s → approved (inside the agreed task)", req.AgentName, req.ActionType))
		rl.resp, rl.source = domain.Decide(true, "inside the agreed task"), "task"
		return rl
	}
	note, resp, decided := d.validator.decide(ctx, *req, rl.files, rl.next, d.approvals, d.tracker)
	if decided {
		d.display.AddActivity(fmt.Sprintf("%-14s REVIEW %s → %v (%s)", req.AgentName, req.ActionType, resp.Approved, resp.Feedback))
		rl.resp, rl.source = resp, "validator:"+d.validator.Name()
		return rl
	}
	req.Review = note
	if note != "" {
		d.validator.humanDecided()
	}
	return d.human(req, rl)
}

func (d *deciders) human(req *domain.YieldRequest, rl ruling) ruling {
	start := time.Now()
	rl.source = "operator"
	rl.resp, rl.signed = d.sign.ask(*req, d.askHuman)
	rl.decideMS = time.Since(start).Milliseconds()
	if rl.next != nil {
		before := map[string]*approvedFile{}
		for p := range rl.next {
			before[p], _ = d.approvals.current(p)
		}
		rl.lines = changedLines(before, rl.next)
	}
	d.display.AddActivity(fmt.Sprintf("%-14s HITL   %s → %v", req.AgentName, req.ActionType, rl.resp.Approved))
	return rl
}

// finalReview has a human approve the run's whole change once, when the
// validator approved changes no human has seen; the decision is audited. The
// request is secret-scrubbed like every proposal.
func (d *deciders) finalReview(baseSHA string, delivered []string) (bool, error) {
	final := finalReviewRequest(d.approvals.files)
	final = scrubSecrets(final, delivered)
	start := time.Now()
	resp, signed := d.sign.ask(final, d.askHuman)
	decided := yieldDecided(d.total+1, "operator", final, resp, baseSHA, d.approvals.files, "")
	maps.Copy(decided, signed)
	decided["decide_ms"] = time.Since(start).Milliseconds()
	lines := 0
	for p, f := range d.approvals.files {
		base, _ := d.approvals.fromBase(p)
		lines += changedLines(map[string]*approvedFile{p: base}, map[string]*approvedFile{p: f})
	}
	decided["lines"] = lines
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
		if req.Sandboxed {
			fields["sandboxed"] = true
		}
	}
	if req.Guard != "" {
		fields["guard"] = req.Guard
	}
	return fields
}

// changedLines counts the lines a change adds or removes, file by file.
func changedLines(before, after map[string]*approvedFile) int {
	n := 0
	for p, a := range after {
		old := map[string]int{}
		if b := before[p]; b != nil && !b.deleted {
			for _, l := range strings.SplitAfter(string(b.content), "\n") {
				old[l]++
			}
		}
		if !a.deleted {
			for _, l := range strings.SplitAfter(string(a.content), "\n") {
				if old[l] > 0 {
					old[l]--
				} else if l != "" {
					n++ // added
				}
			}
		}
		for l, c := range old {
			if l != "" {
				n += c // removed
			}
		}
	}
	return n
}

// onEvidence decides an in-scope change on evidence: the checks must pass on
// the state it would produce, and the reviewer models, if any, must agree. A
// panel that unanimously rejects rejects it; anything else that is not clear
// evidence goes to a person, who is shown what was missing. Nothing is
// approved on the absence of evidence.
func (d *deciders) onEvidence(ctx context.Context, req *domain.YieldRequest, rl ruling) ruling {
	results, failed := d.evidence.runChecks(ctx, rl.next)
	ev := map[string]any{}
	if len(results) > 0 {
		ev["checks"] = checkEvidence(results)
	}
	if failed != "" {
		req.Review = "no evidence to approve on: " + failed
		d.display.AddActivity(fmt.Sprintf("%-14s EVIDENCE %s → HITL (%s)", req.AgentName, req.ActionType, failed))
		return d.human(req, rl)
	}
	if d.validator != nil {
		note, resp, decided := d.validator.decide(ctx, *req, rl.files, rl.next, d.approvals, d.tracker)
		switch {
		case decided && !resp.Approved:
			d.display.AddActivity(fmt.Sprintf("%-14s REVIEW %s → false (%s)", req.AgentName, req.ActionType, resp.Feedback))
			rl.resp, rl.source, rl.evidence = resp, "validator:"+d.validator.Name(), ev
			return rl
		case !decided:
			req.Review = note
			if note != "" {
				d.validator.humanDecided()
			}
			return d.human(req, rl)
		}
		ev["validator"] = d.validator.Name()
	}
	d.taskApproved = true // a person still reviews the whole change at the end
	d.display.AddActivity(fmt.Sprintf("%-14s EVIDENCE %s → approved", req.AgentName, req.ActionType))
	rl.resp, rl.source, rl.evidence = domain.Decide(true, "approved on evidence"), "evidence", ev
	return rl
}

// finalReviewRequest is the request to approve a run's whole change once, from
// the files it approved.
func finalReviewRequest(files map[string]*approvedFile) domain.YieldRequest {
	final := domain.YieldRequest{Type: "yield_request", AgentName: "staircase", ActionType: domain.ActionFinalReview,
		ReasoningTrace: "Final review: changes in this run were approved without you (by the validator or as part of the agreed task). Approve to commit exactly these files."}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		f := files[p]
		e := domain.ProposedEdit{File: p, SearchBlock: "(final content)", ReplaceBlock: string(f.content)}
		if f.deleted {
			e.SearchBlock, e.ReplaceBlock = MarkerDeleteFile, ""
		}
		final.ProposedEdits = append(final.ProposedEdits, e)
	}
	return final
}
