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

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/domain"
)

// Limits caps automatic behaviour within a run (CHECK 7.2.1).
// Zero values mean "unlimited".
type Limits struct {
	// MaxAutoApproved is the maximum number of yields that may be automatically
	// approved (by policy rules) before the engine falls back to operator review.
	MaxAutoApproved int `json:"max_auto_approved"`

	// MaxTotalYields is the maximum total number of yields (auto + manual) in a
	// single run.  Once reached, every further yield must be reviewed by the
	// operator regardless of matching rules.
	MaxTotalYields int `json:"max_total_yields"`

	// MaxRunDurationSecs is the maximum wall-clock duration of a run in seconds.
	// Enforced externally by the orchestrator; stored here for audit purposes.
	MaxRunDurationSecs int `json:"max_run_duration"`
}

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

	// AgentNames restricts the rule to yields from the named agents.
	// An empty slice matches any agent (same semantics as ActionTypes).
	AgentNames []string `json:"agent_names,omitempty"`

	// Effect is the action taken when all conditions match.
	// Defaults to "approve" when the zero value is provided.
	Effect Effect `json:"effect"`
}

// Engine holds an ordered list of auto-approval rules and optional session limits.
//
// Policy file format (CHECK 7.1.2):
//
//	{
//	  "version": 1,
//	  "rules": [...],
//	  "limits": {"max_auto_approved": 10, "max_total_yields": 50, "max_run_duration": 3600},
//	  "allow_blanket_deny": false
//	}
type Engine struct {
	// Version is a format version for forward compatibility (CHECK 7.1.2).
	Version int `json:"version"`

	// Rules is the ordered list of auto-approval rules.
	Rules []Rule `json:"rules"`

	// Limits caps automatic behaviour within a run (CHECK 7.2.1).
	Limits Limits `json:"limits"`

	// AllowBlanketDeny must be explicitly true when any rule has EffectReject
	// with no conditions (would silently reject every yield).  LoadEngine returns
	// an error if a blanket-deny rule is found and this flag is false (CHECK 7.1.5).
	AllowBlanketDeny bool `json:"allow_blanket_deny"`
}

// LoadEngine reads $wsDir/policy.json and returns an Engine.
// If the file does not exist an empty (approve-nothing) Engine is returned.
// Returns an error when:
//   - the JSON cannot be parsed (caller can surface it to the operator), or
//   - a blanket-deny rule is present but AllowBlanketDeny is false (CHECK 7.1.5).
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
	// Blanket-deny guard: a rule that matches everything and rejects is dangerous
	// because it silently blocks all automation.  Require an explicit opt-in.
	if !e.AllowBlanketDeny {
		for i, r := range e.Rules {
			if r.Effect == EffectReject &&
				len(r.ActionTypes) == 0 &&
				r.MinConfidence == 0 &&
				len(r.AllowedExtensions) == 0 {
				return nil, fmt.Errorf(
					"policy rule #%d is a blanket-deny (rejects everything) — "+
						"set allow_blanket_deny: true to permit this", i,
				)
			}
		}
	}
	return &e, nil
}

// PolicySigFile is the sidecar written by 'staircase policy sign'.
const PolicySigFile = "policy.json.sig"

// VerifyPolicySignature checks the Ed25519 signature sidecar (policy.json.sig)
// against the current policy.json content using the workspace signing public key.
//
// Returns (false, nil) when either policy.json or the signature sidecar are
// absent — the caller should warn the operator but continue.  Returns
// (true, nil) when the signature is present and valid.  Returns (true, err)
// when the signature is present but invalid (tamper detected).
func VerifyPolicySignature(wsDir string) (sigPresent bool, err error) {
	policyPath := filepath.Join(wsDir, "policy.json")
	data, err := os.ReadFile(policyPath)
	if os.IsNotExist(err) {
		return false, nil // no policy file — nothing to verify
	}
	if err != nil {
		return false, fmt.Errorf("read policy.json for verification: %w", err)
	}

	sigPath := filepath.Join(wsDir, PolicySigFile)
	sigHex, err := os.ReadFile(sigPath)
	if os.IsNotExist(err) {
		return false, nil // no sig sidecar — unsigned policy
	}
	if err != nil {
		return true, fmt.Errorf("read policy signature: %w", err)
	}

	pub, err := crypto.LoadSigningPublicKey(wsDir)
	if err != nil {
		return true, fmt.Errorf("load signing public key: %w", err)
	}
	if err := crypto.Verify(pub, data, strings.TrimSpace(string(sigHex))); err != nil {
		return true, fmt.Errorf("policy.json signature invalid (file may have been tampered): %w", err)
	}
	return true, nil
}

// CheckLimits returns (true, reason) when the supplied counters have reached or
// exceeded one of the Engine's session limits (CHECK 7.2.1).
// Returns (false, "") when no limit is breached.
// Callers must route to human operator review when the result is true — the
// limit exhaustion must never be silently allowed or silently denied (CHECK 7.2.2).
func (e *Engine) CheckLimits(autoApproved, totalYields int) (exhausted bool, reason string) {
	if e.Limits.MaxAutoApproved > 0 && autoApproved >= e.Limits.MaxAutoApproved {
		return true, fmt.Sprintf(
			"auto-approval limit reached (%d/%d) — operator review required",
			autoApproved, e.Limits.MaxAutoApproved,
		)
	}
	if e.Limits.MaxTotalYields > 0 && totalYields >= e.Limits.MaxTotalYields {
		return true, fmt.Sprintf(
			"total yield limit reached (%d/%d) — operator review required",
			totalYields, e.Limits.MaxTotalYields,
		)
	}
	return false, ""
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
//
// shell_exec actions are never auto-approved: the operator must personally
// review and approve every shell command regardless of policy configuration.
func (e *Engine) Evaluate(req domain.YieldRequest) PolicyDecision {
	// Hard invariant: shell commands must always reach a human operator.
	// No policy rule can override this — a broad auto-approve rule could
	// otherwise silently execute arbitrary OS commands.
	if req.ActionType == "shell_exec" {
		return PolicyDecision{}
	}
	for i, r := range e.Rules {
		if !r.matchesAgentName(req.AgentName) {
			continue
		}
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

// ─── predicate helpers ──────────��────────────────────────────��────────────────

func (r Rule) matchesAgentName(agentName string) bool {
	if len(r.AgentNames) == 0 {
		return true
	}
	for _, n := range r.AgentNames {
		if strings.EqualFold(n, agentName) {
			return true
		}
	}
	return false
}

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
