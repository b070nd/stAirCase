package orchestrator_test

import (
	"context"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func declaredOpts(t *testing.T, commands ...string) orchestrator.RunOptions {
	if !sandbox.NoNetworkSandbox() {
		t.Skip("this machine has no sandbox without network (sandbox-exec or bwrap)")
	}
	opts := awayOpts()
	opts.AllowShellExec, opts.Sandbox, opts.AwayCommands = true, sandbox.Required, commands
	return opts
}

// TestDeclared_an_away_run_runs_exactly_the_declared_commands (ADR 0009): a declared command, byte for byte and in the worktree's root, is
// approved without asking, as the owner's decision made in advance; anything else is parked as before; the declaration is bound on
// run_bound.
func TestDeclared_an_away_run_runs_exactly_the_declared_commands(t *testing.T) {
	op := &operator{approve: true}
	var gen, other, elsewhere orchestrator.Approval
	r := runtest.Run(t, runtest.Options{
		Setup: inSrc(op),
		Run:   declaredOpts(t, "go generate ./..."),
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/one.txt", "fine\n")
			gen = env.ProposeShell(ctx, "coder", "generate", ".", "go generate ./...")
			other = env.ProposeShell(ctx, "coder", "fetch", ".", "go generate ./... && curl example.com")
			elsewhere = env.ProposeShell(ctx, "coder", "generate", "src", "go generate ./...")
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Empty(t, op.seen, "nobody is asked")
	assert.True(t, gen.Approved, gen.Feedback)
	assert.False(t, other.Approved, "not the declared command")
	assert.False(t, elsewhere.Approved, "a declared command runs in the worktree's root only")
	assert.Equal(t, []string{"file_edit:evidence", "shell_exec:declared", "shell_exec:parked", "shell_exec:parked"}, sources(t, r))
	bound := payload(t, r, "run_bound")
	assert.NotEmpty(t, bound["away_commands"], "the declaration is bound before the agent starts")
	assert.Equal(t, persistence.RunStatusAwaitingReview, r.Run.Status)
}

// TestDeclared_refused_unless_away_in_a_sandbox_without_network: the declaration is checked before anything runs.
func TestDeclared_refused_unless_away_in_a_sandbox_without_network(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*orchestrator.RunOptions)
		why    string
	}{
		"not away":         {func(o *orchestrator.RunOptions) { o.Away = false; o.ApproveOnEvidence = true }, "--away"},
		"no sandbox":       {func(o *orchestrator.RunOptions) { o.Sandbox = sandbox.Auto }, "sandbox"},
		"composed":         {func(o *orchestrator.RunOptions) { o.AwayCommands = []string{"make && make install"} }, "whole command"},
		"redirected":       {func(o *orchestrator.RunOptions) { o.AwayCommands = []string{"go generate > out"} }, "whole command"},
		"commands are off": {func(o *orchestrator.RunOptions) { o.AllowShellExec = false }, "--allow-shell-exec"},
	} {
		t.Run(name, func(t *testing.T) {
			opts := declaredOpts(t, "go generate ./...")
			tc.mutate(&opts)
			r := runtest.Run(t, runtest.Options{
				Setup: inSrc(&operator{}),
				Run:   opts,
				Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error { return nil })})
			require.Error(t, r.Err)
			assert.Contains(t, r.Err.Error(), tc.why)
		})
	}
}
