// Package policy evaluates auto-approval rules against HITL yield requests.
//
// Design goals:
//   - Pure logic: no I/O, no external dependencies, fully unit-testable.
//   - Loaded once per run from $STAIRCASE_DIR/policy.json (or falls back to
//     a zero-rule engine that approves nothing automatically).
//   - Short-circuit: the first matching rule wins; rules are evaluated in
//     declaration order so operators can place more-specific rules first.
//
// # CEL expressions (deferred to Phase 8)
//
// The checklist (§7.1.1) calls for replacing the static struct conditions with
// CEL (github.com/google/cel-go) expressions so operators can write arbitrary
// predicates such as `request.confidence >= 0.9 && request.files.all(f, f.endsWith(".md"))`.
// The current struct-based rules are an intentional simplification that covers the
// common cases without the dependency; the upgrade path is additive (new Rule field
// `Expression string`; old conditions stay for backwards compatibility).
//
// Rule semantics (all conditions are AND-ed within a rule):
//
//	ActionTypes     — if non-empty, the yield's ActionType must be in the list.
//	MinConfidence   — if non-zero, the yield's ConfidenceScore must be >= this.
//	AllowedExtensions — if non-empty, every file in ProposedEdits must have an
//	                    extension from this list (prevents auto-approval of e.g.
//	                    go/py source from a high-confidence shell_exec rule).
//	Effect          — "approve" or "reject".  Defaults to "approve" when absent.
package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/domain"
)

// Effect is the outcome a rule produces when it matches.
type Effect string

const (
	EffectApprove Effect = "approve"
	EffectReject  Effect = "reject"
)

// Rule is a single auto-approval policy rule.
type Rule struct {
	// ActionTypes is the set of action types this rule applies to.
	// An empty slice matches any action type.
	ActionTypes []string `json:"action_types"`

	// MinConfidence is the minimum confidence score required for the rule to
	// match.  0 disables the condition (matches any score).
	MinConfidence float64 `json:"min_confidence"`

	// AllowedExtensions restricts which file extensions may be touched when
	// the rule is evaluated against a file_edit yield.
	// An empty slice matches any extension.
	AllowedExtensions []string `json:"allowed_extensions"`

	// Effect is the action taken when all conditions match.
	// Defaults to "approve" when the zero value is provided.
	Effect Effect `json:"effect"`
}

// Engine holds an ordered list of auto-approval rules.
type Engine struct {
	Rules []Rule `json:"rules"`
}

// LoadEngine reads $wsDir/policy.json and returns an Engine.
// If the file does not exist an empty (approve-nothing) Engine is returned.
// A parse error is returned as-is so the caller can surface it to the operator.
func LoadEngine(wsDir string) (*Engine, error) {
	path := filepath.Join(wsDir, "policy.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Engine{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read policy file: %w", err)
	}
	var e Engine
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("parse policy file %s: %w", path, err)
	}
	return &e, nil
}

// PolicyDecision is the result of evaluating a yield request against all rules.
type PolicyDecision struct {
	// Matched is true when at least one rule matched the yield request.
	// When false the yield requires human review; Approved is meaningless.
	Matched bool

	// Approved is the rule's Effect (true = EffectApprove, false = EffectReject).
	// Only meaningful when Matched is true.
	Approved bool

	// Reason is a human-readable explanation: which rule matched and what it did,
	// or "no matching rule" when Matched is false.
	Reason string
}

// Evaluate applies the engine's rules to req and returns a PolicyDecision.
// The first matching rule wins; rules are evaluated in declaration order.
func (e *Engine) Evaluate(req domain.YieldRequest) PolicyDecision {
	for i, r := range e.Rules {
		if !r.matchesActionType(req.ActionType) {
			continue
		}
		if !r.matchesConfidence(req.ConfidenceScore) {
			continue
		}
		if !r.matchesExtensions(req.ProposedEdits) {
			continue
		}
		effect := r.Effect
		if effect == "" {
			effect = EffectApprove
		}
		label := fmt.Sprintf("policy rule #%d", i)
		if effect == EffectApprove {
			return PolicyDecision{Matched: true, Approved: true, Reason: label + " auto-approved"}
		}
		return PolicyDecision{Matched: true, Approved: false, Reason: label + " auto-rejected"}
	}
	return PolicyDecision{Matched: false, Reason: "no matching rule"}
}

// ─── predicate helpers ────────────────────────────────────────────────────────

func (r Rule) matchesActionType(actionType string) bool {
	if len(r.ActionTypes) == 0 {
		return true
	}
	for _, t := range r.ActionTypes {
		if strings.EqualFold(t, actionType) {
			return true
		}
	}
	return false
}

func (r Rule) matchesConfidence(score float64) bool {
	return r.MinConfidence == 0 || score >= r.MinConfidence
}

// matchesExtensions returns true when either:
//   - the rule has no extension restriction, OR
//   - every file in the proposed edits has an allowed extension.
//
// If there are no proposed edits the condition is vacuously satisfied.
func (r Rule) matchesExtensions(edits []domain.ProposedEdit) bool {
	if len(r.AllowedExtensions) == 0 {
		return true
	}
	allowed := make(map[string]bool, len(r.AllowedExtensions))
	for _, ext := range r.AllowedExtensions {
		e := strings.ToLower(ext)
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		allowed[e] = true
	}
	for _, edit := range edits {
		ext := strings.ToLower(filepath.Ext(edit.File))
		if !allowed[ext] {
			return false
		}
	}
	return true
}
