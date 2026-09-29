package orchestrator_test

import (
	"context"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApproveInScope_approves_the_agreed_task_not_every_step: with the task
// agreed and ApproveInScope, in-scope changes are approved as part of the
// task; a person still sees every 5th one (checkpoint), anything outside the
// scope, sensitive files, and the whole change once before it is committed.
func TestApproveInScope_approves_the_agreed_task_not_every_step(t *testing.T) {
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			scoped(op, `{"rules":[]}`)(s, wsDir, projectID)
			cases, _ := s.ListCasesByProject(projectID)
			stories, _ := s.ListUserStoriesByCase(cases[0].ID)
			for _, st := range stories {
				_ = s.SetUserStoryScope(st.ID, `{"allow":["src/**"]}`)
			}
		},
		Run: orchestrator.RunOptions{ApproveInScope: true, Agreed: "dev@example.com"},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			for _, f := range []string{"src/a.txt", "src/b.txt", "src/c.txt", "src/d.txt", "src/e.txt", "README.md", "src/run.sh", "src/f.txt"} {
				create(ctx, env, f)
			}
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Equal(t, []string{"file_edit:task", "file_edit:task", "file_edit:task", "file_edit:task",
		"file_edit:operator", // checkpoint
		"file_edit:operator", // outside the scope
		"file_edit:operator", // sensitive
		"file_edit:task", "final_review:operator"}, sources(t, r))
	require.Len(t, op.seen, 4)
	assert.Contains(t, op.seen[2].Review, "sensitive path src/run.sh")
	got, err := r.OnBranch("src/f.txt")
	require.NoError(t, err)
	assert.Equal(t, "x\n", got)
}

// TestApproveInScope_needs_an_agreed_task: without an agreement the option
// approves nothing.
func TestApproveInScope_needs_an_agreed_task(t *testing.T) {
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{
		Setup: scoped(op, `{"rules":[]}`),
		Run:   orchestrator.RunOptions{ApproveInScope: true},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			create(ctx, env, "GREETING.md")
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Equal(t, []string{"file_edit:operator"}, sources(t, r))
}
