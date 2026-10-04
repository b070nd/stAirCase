package orchestrator_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inSrc scopes the case's story to src/** and sends what needs a person to op.
func inSrc(op http.Handler) func(*persistence.Store, string, int64) {
	return func(s *persistence.Store, wsDir string, projectID int64) {
		scoped(op, `{"rules":[]}`)(s, wsDir, projectID)
		cases, _ := s.ListCasesByProject(projectID)
		stories, _ := s.ListUserStoriesByCase(cases[0].ID)
		for _, st := range stories {
			_ = s.SetUserStoryScope(st.ID, `{"allow":["src/**"]}`)
		}
	}
}

// strictPerson is an operator that rejects every change put to them and
// approves the final review.
type strictPerson struct{ operator }

func (p *strictPerson) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req domain.YieldRequest
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &req)
	p.mu.Lock()
	p.seen = append(p.seen, req)
	p.mu.Unlock()
	_ = json.NewEncoder(w).Encode(domain.YieldResponse{Type: "yield_response", Approved: req.ActionType == "final_review", Feedback: "operator"})
}

func write(ctx context.Context, env *orchestrator.AgentEnv, file, content string) orchestrator.Approval {
	ap := env.Propose(ctx, domain.YieldRequest{AgentName: "coder", ActionType: "file_edit",
		ProposedEdits: []domain.ProposedEdit{{File: file, SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: content}}})
	if ap.Approved {
		_ = ap.Apply(env.Worktree)
	}
	return ap
}

// TestApproveOnEvidence_checks_decide_inside_the_scope: with checks as evidence,
// an in-scope change whose state passes them is approved as "evidence", with no
// sampled checkpoint; one that fails them goes to a person, who is shown the
// failing check; and the whole change is still reviewed once at the end.
func TestApproveOnEvidence_checks_decide_inside_the_scope(t *testing.T) {
	op := &strictPerson{} // rejects the changes that reach them, approves the final review
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(op),
		Run: orchestrator.RunOptions{ApproveInScope: true, ApproveOnEvidence: true, Agreed: "dev@example.com", // as the CLI sets both
			Checks: []string{`! grep -rq FORBIDDEN src || { echo "FORBIDDEN found"; exit 1; }`}},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			for i := 0; i < 7; i++ { // more than the old checkpoint of five
				write(ctx, env, fmt.Sprintf("src/ok%d.txt", i), "fine\n")
			}
			write(ctx, env, "src/bad.txt", "FORBIDDEN\n")
			write(ctx, env, "src/after.txt", "fine\n")
			return nil
		})})
	require.NoError(t, r.Err)
	want := []string{}
	for i := 0; i < 7; i++ {
		want = append(want, "file_edit:evidence")
	}
	want = append(want, "file_edit:operator", // the failing one, rejected by the person
		"file_edit:evidence", // the next one passes again: the rejected change is not part of the state
		"final_review:operator")
	assert.Equal(t, want, sources(t, r), "no sampled checkpoint: evidence replaces it")
	require.NotEmpty(t, op.seen)
	assert.Contains(t, op.seen[0].Review, "FORBIDDEN found", "the person is shown why the evidence was not enough")
	assert.Equal(t, "final_review", op.seen[len(op.seen)-1].ActionType)
	_, err := r.OnBranch("src/bad.txt")
	assert.Error(t, err)
	got, err := r.OnBranch("src/after.txt")
	require.NoError(t, err)
	assert.Equal(t, "fine\n", got)

	// What was decided on is on record: which check, its exit code and output digest.
	var decided map[string]any
	for _, e := range r.Events {
		if e.EventType == "yield_decided" {
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &decided))
			break
		}
	}
	ev, ok := decided["evidence"].(map[string]any)
	require.True(t, ok, "the decision carries its evidence: %v", decided)
	checks := ev["checks"].([]any)
	require.Len(t, checks, 1)
	assert.EqualValues(t, 0, checks[0].(map[string]any)["exit_code"])
	assert.NotEmpty(t, checks[0].(map[string]any)["output_sha256"])
}

// TestApproveOnEvidence_checks_see_the_proposed_state: a check runs on the
// base commit with the changes approved so far and this one applied, so it can
// depend on earlier approved files and on the change it judges.
func TestApproveOnEvidence_checks_see_the_proposed_state(t *testing.T) {
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(op),
		Run: orchestrator.RunOptions{ApproveOnEvidence: true, Agreed: "dev@example.com",
			Checks: []string{"test -f src/a.txt && test -f src/b.txt"}},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/a.txt", "a\n") // b does not exist yet: no evidence, a person decides
			write(ctx, env, "src/b.txt", "b\n") // a was approved, and b is in the state: evidence
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Equal(t, []string{"file_edit:operator", "file_edit:evidence", "final_review:operator"}, sources(t, r))
}

// TestApproveOnEvidence_panel_is_evidence: reviewer models that agree are
// evidence (and their own sampling is replaced by it); a unanimous rejection is
// a rejection; a split goes to a person.
func TestApproveOnEvidence_panel_is_evidence(t *testing.T) {
	rv := &reviewer{replies: []string{`{"approve": true, "reason": "fine"}`, `{"approve": false, "reason": "no"}`}}
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(op),
		Run: orchestrator.RunOptions{ApproveOnEvidence: true, Agreed: "dev@example.com",
			Validator: &orchestrator.Validator{Models: []string{"review/model"}, Chat: rv}},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/a.txt", "a\n") // approved by the panel
			write(ctx, env, "src/b.txt", "b\n") // rejected by the panel
			for i := 0; i < 6; i++ {            // the validator's own sampling would send one of these to a person
				write(ctx, env, fmt.Sprintf("src/c%d.txt", i), "c\n")
			}
			return nil
		})})
	require.NoError(t, r.Err)
	want := []string{"file_edit:evidence", "file_edit:validator:review/model"}
	for i := 0; i < 6; i++ {
		want = append(want, "file_edit:evidence")
	}
	assert.Equal(t, append(want, "final_review:operator"), sources(t, r))
}

// TestApproveOnEvidence_needs_evidence_to_exist: with no check and no reviewer
// there is nothing to decide on, so the run refuses to start.
func TestApproveOnEvidence_needs_evidence_to_exist(t *testing.T) {
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(&operator{approve: true}),
		Run:   orchestrator.RunOptions{ApproveOnEvidence: true, Agreed: "dev@example.com"},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
	require.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "--check")
}

// TestCheckTimeout: a check that does not finish within the run's check
// timeout counts as not passed: evidence-based approval then goes to a person
// (who is told it timed out) instead of waiting out the default, and the
// certificate records the check as one that could not run.
func TestCheckTimeout(t *testing.T) {
	start := time.Now()
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(op),
		Run: orchestrator.RunOptions{ApproveInScope: true, ApproveOnEvidence: true, Agreed: "dev@example.com",
			Checks: []string{"sleep 30"}, CheckTimeout: 300 * time.Millisecond},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/a.txt", "a\n")
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Less(t, time.Since(start), 20*time.Second, "the check was cut off, not waited for")
	assert.Equal(t, []string{"file_edit:operator"}, sources(t, r), "no evidence, so a person decided (and no final review: nothing was approved without one)")
	require.NotEmpty(t, op.seen)
	assert.Contains(t, op.seen[0].Review, "timed out")

	// After the commit the same timeout applies, and the certificate says the check could not run.
	_, _, s := certified(t, writeFile("health.txt", "ok\n"),
		orchestrator.RunOptions{Checks: []string{"sleep 30"}, CheckTimeout: 300 * time.Millisecond}, nil)
	require.Len(t, s.Predicate.Checks, 1)
	assert.Equal(t, -1, s.Predicate.Checks[0].ExitCode)
}

// TestFinalReview_shows_a_binary_file_by_its_digest: the whole-change review at
// the end lists a file that is not text by its size and digest, like the
// proposal did, never its bytes as garbled text.
func TestFinalReview_shows_a_binary_file_by_its_digest(t *testing.T) {
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(op),
		Run:   orchestrator.RunOptions{ApproveInScope: true, Agreed: "dev@example.com"},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/a.txt", "approved as part of the task\n") // so a final review is asked
			ap := env.ProposeEdit(ctx, "coder", "an image", domain.ProposedEdit{File: "src/logo.png", SearchBlock: orchestrator.MarkerNewFile,
				ContentB64: base64.StdEncoding.EncodeToString([]byte("\x89PNG\xff\x00\x80"))})
			if ap.Approved {
				_ = ap.Apply(env.Worktree)
			}
			return nil
		})})
	require.NoError(t, r.Err)
	require.NotEmpty(t, op.seen)
	final := op.seen[len(op.seen)-1]
	require.Equal(t, domain.ActionFinalReview, final.ActionType)
	var png *domain.ProposedEdit
	for i := range final.ProposedEdits {
		if final.ProposedEdits[i].File == "src/logo.png" {
			png = &final.ProposedEdits[i]
		}
	}
	require.NotNil(t, png)
	assert.Empty(t, png.ReplaceBlock, "no garbled bytes")
	assert.NotEmpty(t, png.BinarySHA256)
	assert.Equal(t, 7, png.BinaryBytes)
}
