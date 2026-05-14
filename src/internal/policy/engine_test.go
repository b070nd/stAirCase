package policy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
	dec := e.Evaluate(fileEditReq("shell_exec", 0.5))
	assert.False(t, dec.Matched)
	assert.Contains(t, dec.Reason, "no matching rule")
}

func TestEvaluate_empty_action_types_matches_any(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: nil, Effect: policy.EffectApprove},
	}}
	for _, at := range []string{"file_edit", "shell_exec", "custom"} {
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
	// shell_exec yield has no ProposedEdits; extension condition is vacuously true.
	req := domain.YieldRequest{Type: "yield_request", ActionType: "shell_exec", ConfidenceScore: 1.0}
	dec := e.Evaluate(req)
	assert.True(t, dec.Matched)
	assert.True(t, dec.Approved)
}

// ─── EffectReject ─────────────────────────────────────────────────────────────

func TestEvaluate_reject_effect_matched_is_true_approved_is_false(t *testing.T) {
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"shell_exec"}, Effect: policy.EffectReject},
	}}
	dec := e.Evaluate(fileEditReq("shell_exec", 1.0))
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
// The AUDIT_CHECKLIST.md specifies exact test names via grep.

// TestPolicyLoadValidation verifies that LoadEngine rejects files with invalid
// JSON so the caller can surface the error to the operator (CHECK 7.1.3).
func TestPolicyLoadValidation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`not-json`), 0o600))
	_, err := policy.LoadEngine(dir)
	require.Error(t, err, "invalid JSON must be rejected at load time")
	assert.Contains(t, err.Error(), "parse policy file")
}

// TestDenyBeatsAllow verifies that a reject-effect rule wins over any implicit
// approve path: once matched, EffectReject produces Approved=false (CHECK 7.1.4).
func TestDenyBeatsAllow(t *testing.T) {
	// Put a reject rule first; an approve rule after.  First-match wins, so reject
	// should be returned.
	e := &policy.Engine{Rules: []policy.Rule{
		{ActionTypes: []string{"shell_exec"}, Effect: policy.EffectReject},
		{Effect: policy.EffectApprove},
	}}
	req := domain.YieldRequest{ActionType: "shell_exec", ConfidenceScore: 0.99}
	dec := e.Evaluate(req)
	assert.True(t, dec.Matched, "rule must have matched")
	assert.False(t, dec.Approved, "reject effect must set Approved=false")
	assert.Contains(t, dec.Reason, "auto-rejected")
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
