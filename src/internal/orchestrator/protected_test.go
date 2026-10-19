package orchestrator_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAway_an_agent_cannot_make_a_failing_check_pass_by_changing_its_test (V11-C01 scope 5): the check runs every test under
// src/tests, and one fails until src/feature.txt exists. Editing the script the check runs (which the check names), or deleting
// or editing the failing test (declared protected), would make the check pass on the state the change produces, and the policy
// approves every file edit. Each is parked all the same; only the change that makes the test pass is approved (by the policy), and
// it alone is offered for review. The protected paths are part of the terms the run saved, bound by digest on run_bound.
func TestAway_an_agent_cannot_make_a_failing_check_pass_by_changing_its_test(t *testing.T) {
	op := &operator{approve: true} // would approve anything: it must never be asked
	opts := awayOpts()
	opts.Checks, opts.ProtectedPaths = []string{"sh src/run_tests"}, []string{"src/tests/**"}
	r := runtest.Run(t, runtest.Options{
		Base: map[string]runtest.File{
			"src/run_tests": {Content: "for t in src/tests/*; do [ -e \"$t\" ] || continue; sh \"$t\" || exit 1; done\n", Mode: 0o644},
			"src/tests/one": {Content: "test -f src/feature.txt\n", Mode: 0o644},
		},
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			scoped(op, "")(s, wsDir, projectID) // the default policy: every file edit is approved
			cases, _ := s.ListCasesByProject(projectID)
			stories, _ := s.ListUserStoriesByCase(cases[0].ID)
			for _, st := range stories {
				_ = s.SetUserStoryScope(st.ID, `{"allow":["src/**"]}`)
			}
		},
		Run: opts,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/run_tests", "exit 0\n")
			env.Propose(ctx, domain.YieldRequest{AgentName: "coder", ActionType: domain.ActionFileEdit,
				ProposedEdits: []domain.ProposedEdit{{File: "src/tests/one", SearchBlock: orchestrator.MarkerDeleteFile}}})
			write(ctx, env, "src/tests/one", "exit 0\n")
			write(ctx, env, "src/feature.txt", "done\n")
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Empty(t, op.seen, "nobody is asked while away")
	assert.Equal(t, []string{"file_edit:parked", "file_edit:parked", "file_edit:parked", "file_edit:policy"}, sources(t, r))
	assert.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status)
	awaiting := payload(t, r, "awaiting_review")
	require.NotNil(t, awaiting)
	assert.Equal(t, []string{"src/feature.txt"}, slices.Sorted(maps.Keys(awaiting["files"].(map[string]any))), "only the honest change is offered")
	assert.EqualValues(t, 3, awaiting["parked"])

	saved, err := os.ReadFile(filepath.Join(r.WsDir, "journal", fmt.Sprintf("run-%d.options.json", r.Run.ID)))
	require.NoError(t, err)
	assert.Contains(t, string(saved), `"protected_paths":["src/run_tests","src/tests/**"]`)
	sum := sha256.Sum256(saved)
	assert.Equal(t, hex.EncodeToString(sum[:]), payload(t, r, "run_bound")["terms_sha256"], "bound by digest")
}

// TestDeclared_a_command_that_writes_a_protected_path_is_parked_like_any_change (ADR 0009, V11-C01 scope 5): a declared command is
// approved in advance, but what it writes is a change like any other. A file it overwrote under a protected path is parked, and
// the worktree goes back to its approved state, so a declared command cannot weaken the checks either.
func TestDeclared_a_command_that_writes_a_protected_path_is_parked_like_any_change(t *testing.T) {
	op := &operator{approve: true}
	opts := declaredOpts(t, "sh gen.sh")
	opts.Checks, opts.ProtectedPaths = []string{"sh src/run_tests"}, []string{"src/tests/**"}
	var cmd, wrote orchestrator.Approval
	var after string
	r := runtest.Run(t, runtest.Options{
		Base: map[string]runtest.File{
			"src/run_tests": {Content: "for t in src/tests/*; do sh \"$t\" || exit 1; done\n", Mode: 0o644},
			"src/tests/one": {Content: "true\n", Mode: 0o644},
		},
		Setup: func(s *persistence.Store, wsDir string, projectID int64) {
			scoped(op, "")(s, wsDir, projectID)
			cases, _ := s.ListCasesByProject(projectID)
			stories, _ := s.ListUserStoriesByCase(cases[0].ID)
			for _, st := range stories {
				_ = s.SetUserStoryScope(st.ID, `{"allow":["src/**"]}`)
			}
		},
		Run: opts,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			cmd = env.ProposeShell(ctx, "coder", "generate", ".", "sh gen.sh")
			// the approved command ran and wrote over a protected file
			if err := os.WriteFile(filepath.Join(env.Worktree, "src", "tests", "one"), []byte("exit 0\n"), 0o644); err != nil {
				return err
			}
			wrote = env.ShellRan(ctx, "coder", "sh gen.sh", true)
			b, err := os.ReadFile(filepath.Join(env.Worktree, "src", "tests", "one"))
			after = string(b)
			return err
		})})
	require.NoError(t, r.Err)
	assert.Empty(t, op.seen, "nobody is asked while away")
	assert.True(t, cmd.Approved, cmd.Feedback)
	assert.False(t, wrote.Approved, "what the command wrote under a protected path is not approved")
	assert.Equal(t, []string{"shell_exec:declared", "file_edit:parked"}, sources(t, r))
	assert.Equal(t, "true\n", after, "the protected file is back to its approved state")
}
