package orchestrator_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRun_an_automatic_decision_ends_with_the_run_limit: a check that decides a
// change (evidence) is bound by the run's time limit like a person's wait is; the
// run does not sit in a long check while its limit has passed, and nothing is
// approved on the strength of work that finished too late.
func TestRun_an_automatic_decision_ends_with_the_run_limit(t *testing.T) {
	setup := func(s *persistence.Store, ws string, projectID int64) {
		scoped(&operator{approve: true}, `{"rules":[],"limits":{"max_run_secs":1}}`)(s, ws, projectID)
		cases, _ := s.ListCasesByProject(projectID)
		stories, _ := s.ListUserStoriesByCase(cases[0].ID)
		for _, st := range stories {
			_ = s.SetUserStoryScope(st.ID, `{"allow":["src/**"]}`)
		}
	}
	start := time.Now()
	r := runtest.Run(t, runtest.Options{
		Setup: setup,
		Run: orchestrator.RunOptions{ApproveInScope: true, ApproveOnEvidence: true, Agreed: "dev@example.com",
			Checks: []string{"sleep 25"}},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			write(ctx, env, "src/a.txt", "x\n")
			return nil
		})})
	assert.Less(t, time.Since(start), 12*time.Second, "the run waited for a check past its limit")
	assert.Empty(t, r.Run.GitCommitHash, "nothing was delivered")
	for _, e := range r.Events {
		if e.EventType == "yield_decided" {
			assert.NotContains(t, e.Payload, `"approved":true`, "no approval was consumed after the limit")
		}
	}
	assert.Contains(t, r.Types(), "drift_halt")
}

// TestRecover_cancellation: a recovery that is cancelled delivers nothing and begins
// no operation; a cancelled wait for the final review does not turn into an approval.
func TestRecover_cancellation(t *testing.T) {
	t.Run("before it starts", func(t *testing.T) {
		r := interrupted(t, nil, orchestrator.RunOptions{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(ctx, r.Run.ID, orchestrator.RecoverOptions{})
		assert.ErrorIs(t, err, context.Canceled)
		branchUntouched(t, r)
	})
	review := func(t *testing.T) runtest.Result {
		return interrupted(t, inSrc(&operator{approve: true}), orchestrator.RunOptions{ApproveInScope: true, Agreed: "dev@example.com"})
	}
	t.Run("while a person is being asked", func(t *testing.T) {
		r := review(t)
		ctx, cancel := context.WithCancel(context.Background())
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		go func() { time.Sleep(200 * time.Millisecond); cancel() }()
		start := time.Now()
		_, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(ctx, r.Run.ID, orchestrator.RecoverOptions{
			Confirm: func(domain.YieldRequest) domain.YieldResponse { <-release; return domain.Decide(true, "late") }})
		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, time.Since(start), 3*time.Second, "the recovery did not wait for a person who is no longer asked")
		branchUntouched(t, r)
		_, statErr := os.Stat(filepath.Join(r.WsDir, "journal", "run-1.recovery.json"))
		assert.True(t, os.IsNotExist(statErr), "no operation was begun")
	})
	t.Run("an approval that arrives as it is cancelled", func(t *testing.T) {
		r := review(t)
		ctx, cancel := context.WithCancel(context.Background())
		_, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(ctx, r.Run.ID, orchestrator.RecoverOptions{
			Confirm: func(domain.YieldRequest) domain.YieldResponse { cancel(); return domain.Decide(true, "ok") }})
		assert.ErrorIs(t, err, context.Canceled, "an answer to a cancelled recovery counts for nothing")
		branchUntouched(t, r)
		// and a recovery that is not cancelled still works afterwards
		res, err := orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{Confirm: approveAll})
		require.NoError(t, err)
		assert.NotEmpty(t, res.Commit)
	})
}

var _ = strings.TrimSpace
var _ = exec.Command
