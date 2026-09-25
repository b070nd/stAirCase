package orchestrator_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/orchestrator/runtest"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/b070nd/staircase-core/src/internal/plan"
	"github.com/b070nd/staircase-core/src/internal/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// operator is a webhook approver that answers every yield with approve and
// records what it was shown.
type operator struct {
	approve bool
	mu      sync.Mutex
	seen    []domain.YieldRequest
}

func (o *operator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req domain.YieldRequest
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &req)
	o.mu.Lock()
	o.seen = append(o.seen, req)
	o.mu.Unlock()
	_ = json.NewEncoder(w).Encode(domain.YieldResponse{Type: "yield_response", Approved: o.approve, Feedback: "operator"})
}

// scoped gives the case one story allowed to change only GREETING.md and
// sends HITL yields to op; policy (from runtest) auto-approves file edits.
func scoped(op *operator, policyJSON string) func(*persistence.Store, string, int64) {
	return func(s *persistence.Store, wsDir string, projectID int64) {
		srv := httptest.NewServer(op)
		cases, _ := s.ListCasesByProject(projectID)
		st, _ := s.CreateUserStory(cases[0].ID, "Greet")
		_ = s.SetUserStoryScope(st.ID, `{"allow":["GREETING.md"]}`)
		_ = s.UpdateProjectWebhook(projectID, srv.URL)
		if policyJSON != "" {
			_ = os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(policyJSON), 0o600)
		}
	}
}

func create(ctx context.Context, env *orchestrator.AgentEnv, path string) orchestrator.Approval {
	ap := env.Propose(ctx, domain.YieldRequest{AgentName: "coder", ActionType: "file_edit",
		ProposedEdits: []domain.ProposedEdit{{File: path, SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "x\n"}}})
	if ap.Approved {
		_ = ap.Apply(env.Worktree)
	}
	return ap
}

func eventPayload(t *testing.T, r runtest.Result, typ string) map[string]any {
	t.Helper()
	for _, e := range r.Events {
		if e.EventType == typ {
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &m))
			return m
		}
	}
	t.Fatalf("no %s event in %v", typ, r.Types())
	return nil
}

// TestDrift_out_of_scope_goes_to_a_human: the policy would auto-approve any
// edit, but one outside the stories' scope is shown to the operator with the
// reason; the override is audited and reported.
func TestDrift_out_of_scope_goes_to_a_human(t *testing.T) {
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{Setup: scoped(op, ""),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			create(ctx, env, "GREETING.md")
			create(ctx, env, "src/other.go")
			return nil
		})})
	require.NoError(t, r.Err)
	require.Len(t, op.seen, 1, "only the out-of-scope edit reaches the operator")
	assert.Contains(t, op.seen[0].Drift, "outside the stories' scope: src/other.go")

	var sources []string
	for _, e := range r.Events {
		if e.EventType == "yield_decided" {
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &m))
			sources = append(sources, m["source"].(string))
			if m["source"] == "operator" {
				assert.Contains(t, m["drift"], "src/other.go", "the override records why it was asked")
			}
		}
	}
	assert.Equal(t, []string{"policy", "operator"}, sources)
	rep := eventPayload(t, r, "drift_report")["report"].(map[string]any)
	assert.Equal(t, []any{"GREETING.md"}, rep["in_scope"])
	assert.Equal(t, []any{"src/other.go"}, rep["out_of_scope"])
	assert.EqualValues(t, 1, rep["overrides"])

	b, err := os.ReadFile(filepath.Join(r.WsDir, "runs", fmt.Sprint(r.Run.ID), "summary.json"))
	require.NoError(t, err)
	var sum orchestrator.RunSummary
	require.NoError(t, json.Unmarshal(b, &sum))
	require.NotNil(t, sum.Drift, "the summary carries the same report")
	assert.Equal(t, []string{"src/other.go"}, sum.Drift.OutOfScope)
}

// TestDrift_halt_and_ack: past max_scope_violations the run halts (KILLED,
// drift_halt audited) and the case does not run again until the operator
// acknowledges it.
func TestDrift_halt_and_ack(t *testing.T) {
	op := &operator{approve: false}
	var second orchestrator.Approval
	r := runtest.Run(t, runtest.Options{
		Setup: scoped(op, `{"rules":[{"action_types":["file_edit"],"effect":"approve"}],"limits":{"max_scope_violations":1}}`),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			create(ctx, env, "a.txt")
			second = create(ctx, env, "b.txt")
			<-ctx.Done()
			return ctx.Err()
		})})
	require.Error(t, r.Err)
	assert.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	assert.False(t, second.Approved)
	assert.Contains(t, second.Feedback, "drifted")
	assert.Contains(t, eventPayload(t, r, "drift_halt")["reason"], "more than 1")
	assert.Contains(t, eventPayload(t, r, "drift_report")["report"].(map[string]any)["halted"], "more than 1")

	caseID := r.Run.CaseID
	wsDir := r.WsDir
	err := orchestrator.NewRunner(r.Store, wsDir).Run(context.Background(), caseID, orchestrator.RunOptions{SkipGates: true, Agent: idle})
	require.ErrorContains(t, err, "--ack-drift")
	runs, err := r.Store.ListRunsByCase(caseID)
	require.NoError(t, err)
	assert.Len(t, runs, 1, "no run was started")

	require.NoError(t, orchestrator.NewRunner(r.Store, wsDir).Run(context.Background(), caseID, orchestrator.RunOptions{SkipGates: true, Agent: idle, AckDrift: true}))
	runs, err = r.Store.ListRunsByCase(caseID)
	require.NoError(t, err)
	events, err := r.Store.ListEventLogs(runs[0].ID)
	require.NoError(t, err)
	assert.Contains(t, events[0].Payload, `"ack_drift":1`)
}

// TestDrift_run_time_limit: a run past max_run_secs halts even while its
// agent keeps working.
func TestDrift_run_time_limit(t *testing.T) {
	started := time.Now()
	r := runtest.Run(t, runtest.Options{
		Setup: func(_ *persistence.Store, wsDir string, _ int64) {
			_ = os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(`{"rules":[],"limits":{"max_run_secs":1}}`), 0o600)
		},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, _ *orchestrator.AgentEnv) error {
			<-ctx.Done()
			return ctx.Err()
		})})
	assert.Less(t, time.Since(started), 10*time.Second)
	assert.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	assert.Contains(t, eventPayload(t, r, "drift_halt")["reason"], "1 s limit")
}

// TestDrift_checkpoint_and_plan_limits: a plan's limits apply (tightened by
// the workspace policy), so every 2nd proposal is reviewed by a human.
func TestDrift_checkpoint_and_plan_limits(t *testing.T) {
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{Setup: scoped(op, ""),
		Run: orchestrator.RunOptions{Plan: &plan.Plan{TopologyVersion: 1, Limits: plan.Limits{CheckpointEvery: 2}}},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			for range 4 {
				create(ctx, env, "GREETING.md")
			}
			return nil
		})})
	require.NoError(t, r.Err)
	require.Len(t, op.seen, 2)
	assert.Contains(t, op.seen[0].Drift, "checkpoint")
	var rep policy.DriftReport
	b, _ := json.Marshal(eventPayload(t, r, "drift_report")["report"])
	require.NoError(t, json.Unmarshal(b, &rep))
	assert.Equal(t, 2, rep.Auto)
	assert.Equal(t, 2, rep.Human)
}

// TestDrift_story_max_files: a story's max_files caps the distinct files the
// run may change before a human must look.
func TestDrift_story_max_files(t *testing.T) {
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			scoped(op, "")(s, wsDir, projectID)
			cases, _ := s.ListCasesByProject(projectID)
			stories, _ := s.ListUserStoriesByCase(cases[0].ID)
			_ = s.SetUserStoryScope(stories[0].ID, `{"allow":["docs/**"],"max_files":1}`)
		},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			create(ctx, env, "docs/a.md")
			create(ctx, env, "docs/b.md")
			return nil
		})})
	require.NoError(t, r.Err)
	require.Len(t, op.seen, 1)
	assert.Contains(t, op.seen[0].Drift, "2 files changed (limit 1)")
}

// TestBudget_unpriced_model_is_not_free: usage of a model without a price
// still counts against the budget cap and kills the run.
func TestBudget_unpriced_model_is_not_free(t *testing.T) {
	started := time.Now()
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, _ string, projectID int64) {
			require.NoError(t, s.SetProjectBudgetCap(projectID, 0.01))
		},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			env.Emit(ctx, orchestrator.Usage{Agent: "coder", Model: "someprovider/brand-new-model", InputTokens: 10_000, OutputTokens: 10_000})
			<-ctx.Done()
			return ctx.Err()
		})})
	assert.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	assert.Less(t, time.Since(started), 10*time.Second, "killed by the budget, not by the test's deadline")
}
