package policy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func fileEditReq(actionType string, confidence float64, files ...string) domain.YieldRequest {
	edits := make([]domain.ProposedEdit, len(files))
	for i, f := range files {
		edits[i] = domain.ProposedEdit{File: f}
	}
	return domain.YieldRequest{
		Type:            "yield_request",
		AgentName:       "test-agent",
		ActionType:      actionType,
		ConfidenceScore: confidence,
		ProposedEdits:   edits,
	}
}

func writePolicy(t *testing.T, dir string, e policy.Engine) {
	t.Helper()
	b, err := json.Marshal(e)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.json"), b, 0o600))
}

// ─── LoadEngine ───────────────────────────────────────────────────────────────

func TestLoadEngine_missing_file_returns_empty(t *testing.T) {
	e, err := policy.LoadEngine(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, e.Rules)
}

func TestLoadEngine_parses_valid_json(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, policy.Engine{
		Rules: []policy.Rule{
			{ActionTypes: []string{"file_edit"}, MinConfidence: 0.9, Effect: policy.EffectApprove},
		},
	})
	e, err := policy.LoadEngine(dir)
	require.NoError(t, err)
	require.Len(t, e.Rules, 1)
	assert.Equal(t, policy.EffectApprove, e.Rules[0].Effect)
}

func TestLoadEngine_invalid_json_returns_error(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.json"), []byte("not json"), 0o600))
	_, err := policy.LoadEngine(dir)
	assert.Error(t, err)
}

// ─── Evaluate — empty engine ──────────────────────────────────────────────────

func TestEvaluate_empty_engine_never_approves(t *testing.T) {
	e := &policy.Engine{}
	dec := e.Evaluate(fileEditReq("file_edit", 1.0))
	assert.False(t, dec.Matched)
	assert.False(t, dec.Approved)
	assert.Contains(t, dec.Reason, "no matching rule")
}

// ─── ActionType matching ──────────────────────────────────────────────────────

func TestEvaluate_action_type_match(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"file_edit"}, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 0.5))
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

func TestEvaluate_action_type_mismatch_falls_through(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"file_edit"}, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("custom", 0.5))
	assert.False(t, dec.Matched)
	assert.Contains(t, dec.Reason, "no matching rule")
}

func TestEvaluate_agent_name_match(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{AgentNames: []string{"coder"}, ActionTypes: []string{"file_edit"}, Effect: policy.EffectApprove},
	}}
	req := domain.YieldRequest{AgentName: "coder", ActionType: "file_edit", ConfidenceScore: 0.5}
	dec := e.Evaluate(req)
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

func TestEvaluate_agent_name_mismatch_falls_through(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{AgentNames: []string{"coder"}, ActionTypes: []string{"file_edit"}, Effect: policy.EffectApprove},
	}}
	req := domain.YieldRequest{AgentName: "reviewer", ActionType: "file_edit", ConfidenceScore: 0.5}
	dec := e.Evaluate(req)
	assert.False(t, dec.Matched, "different agent must not match")
	assert.Contains(t, dec.Reason, "no matching rule")
}

func TestEvaluate_empty_agent_names_matches_any(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"file_edit"}, Effect: policy.EffectApprove},
	}}
	for _, name := range []string{"coder", "reviewer", "architect", ""} {
		req := domain.YieldRequest{AgentName: name, ActionType: "file_edit", ConfidenceScore: 0.5}
		dec := e.Evaluate(req)
		assert.True(t, dec.Matched, "empty agent_names must match agent %q", name)
	}
}

func TestEvaluate_agent_name_case_insensitive(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{AgentNames: []string{"Coder"}, Effect: policy.EffectApprove},
	}}
	req := domain.YieldRequest{AgentName: "CODER", ActionType: "file_edit"}
	assert.True(t, e.Evaluate(req).Matched)
}

func TestEvaluate_empty_action_types_matches_any(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: nil, Effect: policy.EffectApprove},
	}}
	for _, at := range []string{"file_edit", "custom"} { // shell_exec is blocked at policy level — tested separately
		dec := e.Evaluate(fileEditReq(at, 0.5))
		assert.True(t, dec.Matched, "expected match for %q", at)
		assert.True(t, dec.Approved, "expected approve for %q", at)
	}
}

func TestEvaluate_action_type_case_insensitive(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"File_Edit"}, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 0.5))
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

// ─── Confidence matching ──────────────────────────────────────────────────────

func TestEvaluate_confidence_above_threshold_approves(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{MinConfidence: 0.8, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 0.95))
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

func TestEvaluate_confidence_exactly_at_threshold_approves(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{MinConfidence: 0.8, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 0.8))
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

func TestEvaluate_confidence_below_threshold_misses(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{MinConfidence: 0.8, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 0.5))
	assert.False(t, dec.Matched)
	assert.Contains(t, dec.Reason, "no matching rule")
}

func TestEvaluate_zero_min_confidence_matches_any(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{MinConfidence: 0, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 0.0))
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

// ─── Extension matching ───────────────────────────────────────────────────────

func TestEvaluate_allowed_extension_matches(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{AllowedExtensions: []string{".md", ".txt"}, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 1.0, "README.md", "notes.txt"))
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

func TestEvaluate_extension_without_dot_prefix_normalised(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{AllowedExtensions: []string{"md"}, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 1.0, "README.md"))
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

func TestEvaluate_disallowed_extension_misses(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{AllowedExtensions: []string{".md"}, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 1.0, "README.md", "main.go"))
	assert.False(t, dec.Matched)
	assert.Contains(t, dec.Reason, "no matching rule")
}

func TestEvaluate_empty_extensions_matches_any_file(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{AllowedExtensions: nil, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 1.0, "main.go", "config.yaml"))
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

func TestEvaluate_no_edits_vacuously_satisfies_extension_rule(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{AllowedExtensions: []string{".md"}, Effect: policy.EffectApprove},
	}}
	// custom yield has no ProposedEdits; extension condition is vacuously true.
	req := domain.YieldRequest{Type: "yield_request", ActionType: "custom", ConfidenceScore: 1.0}
	dec := e.Evaluate(req)
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

// ─── EffectReject ─────────────────────────────────────────────────────────────

func TestEvaluate_reject_effect_matched_is_true_approved_is_false(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"custom"}, Effect: policy.EffectReject},
	}}
	dec := e.Evaluate(fileEditReq("custom", 1.0))
	assert.True(t, dec.Matched)
	assert.False(t, dec.Approved)
	assert.Contains(t, dec.Reason, "auto-rejected")
}

// ─── Default effect ───────────────────────────────────────────────────────────

func TestEvaluate_zero_effect_defaults_to_approve(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"file_edit"}}, // Effect zero value
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 0.5))
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

// ─── First-match wins ─────────────────────────────────────────────────────────

func TestEvaluate_first_matching_rule_wins(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"file_edit"}, Effect: policy.EffectReject},
		{ActionTypes: []string{"file_edit"}, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 0.5))
	assert.True(t, dec.Matched)
	assert.False(t, dec.Approved)
	assert.Contains(t, dec.Reason, "rule #0")
}

// ─── Combined conditions ──────────────────────────────────────────────────────

func TestEvaluate_combined_conditions_all_must_match(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{
			ActionTypes:       []string{"file_edit"},
			MinConfidence:     0.9,
			AllowedExtensions: []string{".md"},
			Effect:            policy.EffectApprove,
		},
	}}

	// All conditions met.
	dec := e.Evaluate(fileEditReq("file_edit", 0.95, "README.md"))
	assert.True(t, dec.Matched, "all conditions met should match")
	assert.True(t, dec.Approved, "matched approve rule should approve")

	// Confidence too low → no match.
	dec = e.Evaluate(fileEditReq("file_edit", 0.5, "README.md"))
	assert.False(t, dec.Matched, "low confidence should not match")

	// Wrong extension → no match.
	dec = e.Evaluate(fileEditReq("file_edit", 0.95, "main.go"))
	assert.False(t, dec.Matched, "wrong extension should not match")
}

// ─── Reason string ────────────────────────────────────────────────────────────

func TestEvaluate_reason_contains_rule_index(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"other"}},
		{ActionTypes: []string{"file_edit"}, Effect: policy.EffectApprove},
	}}
	dec := e.Evaluate(fileEditReq("file_edit", 0.5))
	assert.True(t, dec.Matched)
	assert.Contains(t, dec.Reason, "rule #1")
}

// ─── approvalhttp integration (uses domain.YieldRequest directly) ─────────────

// TestEvaluate_type_compatible_with_domain verifies that domain.YieldRequest
// (the canonical type) is accepted by Evaluate without any conversion — i.e.
// that engine.go imports domain, not ipc.
func TestEvaluate_type_compatible_with_domain(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{Effect: policy.EffectApprove},
	}}
	req := domain.YieldRequest{
		ActionType:      "file_edit",
		ConfidenceScore: 0.9,
	}
	dec := e.Evaluate(req)
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

// ─── Checklist-named tests (CHECK 7.1.3–7.2.2) ───────────────────────────────
// The compliance checklist (docs/compliance-checklist.md) specifies exact test names via grep.

// TestPolicyLoadValidation verifies that LoadEngine rejects files with invalid
// JSON so the caller can surface the error to the operator (CHECK 7.1.3).
func TestPolicyLoadValidation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`not-json`), 0o600))
	_, err := policy.LoadEngine(dir)
	require.Error(t, err, "invalid JSON must be rejected at load time")
	assert.Contains(t, err.Error(), "parse policy file")
}

// TestDenyBeatsAllow verifies that a reject-effect rule wins when it appears
// first: the engine is first-match-wins, so rule order determines outcome
// (CHECK 7.1.4).
func TestDenyBeatsAllow(t *testing.T) {
	// Reject rule first → it matches custom before the catch-all approve.
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"custom"}, Effect: policy.EffectReject},
		{Effect: policy.EffectApprove},
	}}
	req := domain.YieldRequest{ActionType: "custom", ConfidenceScore: 0.99}
	dec := e.Evaluate(req)
	assert.True(t, dec.Matched, "rule must have matched")
	assert.False(t, dec.Approved, "first-matching reject rule must set Approved=false")
	assert.Contains(t, dec.Reason, "auto-rejected")
}

// TestAllowBeatesDeny verifies that the engine is first-match-wins, not
// deny-over-allow: when an approve rule appears before a reject rule for the
// same request, the approve rule wins (CHECK 7.1.4).
func TestAllowBeatesDeny(t *testing.T) {
	// Approve rule first → wins even though a reject rule also matches.
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"custom"}, Effect: policy.EffectApprove},
		{ActionTypes: []string{"custom"}, Effect: policy.EffectReject},
	}}
	req := domain.YieldRequest{ActionType: "custom", ConfidenceScore: 0.9}
	dec := e.Evaluate(req)
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved, "first-matching approve rule must win (first-match-wins, not deny-over-allow)")
}

// TestBlanketDenyRequiresFlag verifies that a policy file containing a rule
// that would reject ALL requests (no conditions + EffectReject) is refused by
// LoadEngine unless allow_blanket_deny is explicitly true (CHECK 7.1.5).
func TestBlanketDenyRequiresFlag(t *testing.T) {
	dir := t.TempDir()

	// Blanket-deny rule without the explicit flag → must error.
	data := []byte(`{"rules":[{"effect":"reject"}]}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.json"), data, 0o600))
	_, err := policy.LoadEngine(dir)
	require.Error(t, err, "blanket-deny without allow_blanket_deny must be rejected")
	assert.Contains(t, err.Error(), "blanket-deny")

	// Same rule WITH allow_blanket_deny: true → must succeed.
	allowed := []byte(`{"rules":[{"effect":"reject"}],"allow_blanket_deny":true}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.json"), allowed, 0o600))
	e, err := policy.LoadEngine(dir)
	require.NoError(t, err, "blanket-deny with allow_blanket_deny must be accepted")
	require.NotNil(t, e)
}

// TestLimitExhaustionAsksOperator verifies that CheckLimits returns exhausted=true
// when a counter reaches its cap, signalling that the operator must be consulted —
// the limit must NEVER be silently allowed or silently denied (CHECK 7.2.2).
func TestLimitExhaustionAsksOperator(t *testing.T) {
	e := &policy.Engine{Limits: policy.Limits{MaxAutoApproved: 5}}

	// At cap: operator review required.
	exhausted, reason := e.CheckLimits(5, 10)
	assert.True(t, exhausted, "at limit must return exhausted=true")
	assert.Contains(t, reason, "operator review required")

	// Below cap: policy rules still apply normally.
	exhausted, _ = e.CheckLimits(4, 10)
	assert.False(t, exhausted, "below limit must return exhausted=false")

	// Zero limit means unlimited.
	e2 := &policy.Engine{}
	exhausted, _ = e2.CheckLimits(1000, 1000)
	assert.False(t, exhausted, "zero limit (unlimited) must always return exhausted=false")
}

// ─── VerifyPolicySignature ────────────────────────────────────────────────────

// setupSignedPolicy creates a temp workspace with a valid policy.json + signing
// key pair + policy.json.sig so tests can exercise VerifyPolicySignature.
func setupSignedPolicy(t *testing.T) (wsDir string, policyData []byte) {
	t.Helper()
	wsDir = t.TempDir()
	policyData = []byte(`{"rules":[],"limits":{"max_auto_approved":5}}`)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), policyData, 0o600))
	require.NoError(t, crypto.GenerateSigningKey(wsDir))

	privKey, err := crypto.LoadSigningKey(wsDir)
	require.NoError(t, err)
	sigHex := crypto.Sign(privKey, policyData)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, policy.PolicySigFile), []byte(sigHex), 0o644))
	return wsDir, policyData
}

// TestPolicyEngine_shell_exec_never_auto_approved verifies the hard invariant:
// no policy rule — even a broad auto-approve wildcard — can auto-approve a
// shell_exec yield.  The operator must always personally review shell commands.
func TestPolicyEngine_shell_exec_never_auto_approved(t *testing.T) {
	// Wildcard rule: approve anything from any agent with any confidence.
	eng := &policy.Engine{Rules: []policy.Rule{
		{Effect: "approve", MinConfidence: 0.0},
	}}
	req := domain.YieldRequest{
		AgentName:       "any-agent",
		ActionType:      "shell_exec",
		ConfidenceScore: 1.0, // maximum confidence
	}
	dec := eng.Evaluate(req)
	assert.False(t, dec.Matched, "shell_exec must not match any policy rule")
	assert.False(t, dec.Approved, "shell_exec must never be auto-approved")
}

// TestPolicyEngine_non_shell_exec_still_auto_approved verifies that the
// shell_exec guard does not block auto-approval of other action types.
func TestPolicyEngine_non_shell_exec_still_auto_approved(t *testing.T) {
	eng := &policy.Engine{Rules: []policy.Rule{
		{Effect: "approve", MinConfidence: 0.0},
	}}
	req := domain.YieldRequest{
		AgentName:       "coder",
		ActionType:      "file_edit",
		ConfidenceScore: 0.9,
	}
	dec := eng.Evaluate(req)
	assert.True(t, dec.Matched, "file_edit should match the wildcard rule")
	assert.True(t, dec.Approved, "file_edit with high confidence should be auto-approved")
}

// TestVerifyPolicySignature_valid_signature returns (true, nil) when the
// sidecar matches the current policy.json content.
func TestVerifyPolicySignature_valid_signature(t *testing.T) {
	wsDir, _ := setupSignedPolicy(t)
	sigPresent, err := policy.VerifyPolicySignature(wsDir)
	require.NoError(t, err)
	assert.True(t, sigPresent)
}

// TestVerifyPolicySignature_tampered_policy returns (true, err) when
// policy.json is modified after signing.
func TestVerifyPolicySignature_tampered_policy(t *testing.T) {
	wsDir, _ := setupSignedPolicy(t)
	// Overwrite policy.json with different content (tamper).
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(`{"rules":[]}`), 0o600))

	sigPresent, err := policy.VerifyPolicySignature(wsDir)
	assert.True(t, sigPresent, "sig file is present even when tampered")
	assert.Error(t, err, "tampered policy must return an error")
}

// TestVerifyPolicySignature_absent_sig returns (false, nil) when policy.json
// exists but no .sig sidecar is present (unsigned policy).
func TestVerifyPolicySignature_absent_sig(t *testing.T) {
	wsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(`{}`), 0o600))

	sigPresent, err := policy.VerifyPolicySignature(wsDir)
	assert.NoError(t, err, "absent sig is not an error")
	assert.False(t, sigPresent, "absent sig must return sigPresent=false")
}

// TestVerifyPolicySignature_no_policy_file returns (false, nil) when neither
// policy.json nor the sidecar exist (empty workspace).
func TestVerifyPolicySignature_no_policy_file(t *testing.T) {
	sigPresent, err := policy.VerifyPolicySignature(t.TempDir())
	assert.NoError(t, err)
	assert.False(t, sigPresent)
}
