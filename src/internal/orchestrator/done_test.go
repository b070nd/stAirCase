package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDone_gives_up_after_a_few_attempts: an agent that cannot make its
// checks pass is let go after MaxDoneAttempts refused stops, so a session
// cannot loop forever; every attempt is on the audit chain. Without checks
// there is nothing to wait for.
func TestDone_gives_up_after_a_few_attempts(t *testing.T) {
	var answers []string
	r := runtest.Run(t, runtest.Options{Run: orchestrator.RunOptions{Checks: []string{"echo nope; exit 1"}},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			for range orchestrator.MaxDoneAttempts + 1 {
				answers = append(answers, env.Done(ctx))
			}
			return writeFile("x.txt", "x\n")(ctx, env)
		})})
	require.NoError(t, r.Err)
	require.Len(t, answers, orchestrator.MaxDoneAttempts+1)
	for _, a := range answers[:orchestrator.MaxDoneAttempts] {
		assert.Contains(t, a, "exit 1")
		assert.Contains(t, a, "nope")
	}
	assert.Empty(t, answers[orchestrator.MaxDoneAttempts], "let go")
	n := 0
	for _, e := range r.Types() {
		if e == "done_checked" {
			n++
		}
	}
	assert.Equal(t, orchestrator.MaxDoneAttempts, n)

	r = runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
		assert.Empty(t, env.Done(ctx), "no checks")
		return nil
	})})
	assert.False(t, strings.Contains(strings.Join(r.Types(), " "), "done_checked"))
}
