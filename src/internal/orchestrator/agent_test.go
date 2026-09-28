package orchestrator_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runInProcess runs agent on a fresh workspace and repo; setup may add secrets
// or limits to the case's project first.
func runInProcess(t *testing.T, setup func(s *persistence.Store, wsDir string, projectID int64), agent orchestrator.AgentFunc) (*persistence.Store, persistence.Run, []domain.RunEventLog, error) {
	t.Helper()
	r := runtest.Run(t, runtest.Options{Setup: setup, Agent: agent})
	return r.Store, r.Run, r.Events, r.Err
}

// TestAgentEnv_secret_delivery catches a secret leaking out of the run: the
// orchestrator's reserved keys must stay unreachable, and a delivered value
// must never reach the audit log or the run branch - even when the agent
// writes it into a file it proposes.
func TestAgentEnv_secret_delivery(t *testing.T) {
	const secret = "sk-test-SECRET-4711"
	var got string
	var reservedErr, missingErr error
	s, run, logs, _ := runInProcess(t, func(s *persistence.Store, wsDir string, projectID int64) {
		key, err := crypto.LoadKey(wsDir)
		require.NoError(t, err)
		for name, value := range map[string]string{"OPENAI_API_KEY": secret, "__webhook_hmac_secret__": "hmac"} {
			enc, err := crypto.Encrypt(key, value)
			require.NoError(t, err)
			_, err = s.CreateSecret(name, enc, &projectID)
			require.NoError(t, err)
		}
	}, func(ctx context.Context, env *orchestrator.AgentEnv) error {
		got, _ = env.Secret("OPENAI_API_KEY")
		_, reservedErr = env.Secret("__webhook_hmac_secret__")
		_, missingErr = env.Secret("NOPE")
		env.Propose(ctx, domain.YieldRequest{AgentName: "coder", ActionType: "file_edit",
			ProposedEdits: []domain.ProposedEdit{{File: "leak.txt", SearchBlock: "(new file)", ReplaceBlock: "key=" + got + "\n"}}})
		return os.WriteFile(env.Worktree+"/leak.txt", []byte("key="+got+"\n"), 0o644)
	})
	assert.Equal(t, secret, got)
	assert.ErrorContains(t, reservedErr, "reserved")
	assert.ErrorContains(t, missingErr, "not found")
	n, err := s.CountSecretAccesses(run.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, n, "every access is logged, whatever its outcome")
	for _, l := range logs {
		assert.NotContains(t, l.Payload, secret, "audit event %s carries the secret", l.EventType)
	}
	assert.Equal(t, persistence.RunStatusFailed, run.Status, "the operator approved <REDACTED>, not the secret")
	assert.Empty(t, run.GitCommitHash)
}

// TestAgentEnv_budget_overrun_stops_the_agent catches usage that is not
// counted, or a run that keeps going past its budget: the agent reports one
// expensive step and then waits; the run must stop it.
func TestAgentEnv_budget_overrun_stops_the_agent(t *testing.T) {
	stopped := make(chan struct{})
	started := time.Now()
	_, run, logs, runErr := runInProcess(t, func(s *persistence.Store, _ string, projectID int64) {
		require.NoError(t, s.SetProjectBudgetCap(projectID, 0.00001))
	}, func(ctx context.Context, env *orchestrator.AgentEnv) error {
		defer close(stopped)
		env.Emit(ctx, orchestrator.Usage{Agent: "coder", Model: "claude-sonnet-4-6", InputTokens: 100000, OutputTokens: 100000})
		<-ctx.Done()
		return ctx.Err()
	})
	assert.Equal(t, persistence.RunStatusKilled, run.Status)
	assert.ErrorIs(t, runErr, orchestrator.ErrRunNotSuccessful)
	assert.Less(t, time.Since(started), 10*time.Second)
	select {
	case <-stopped:
	default:
		t.Fatal("the agent was still running after the run ended")
	}
	var emitted bool
	for _, l := range logs {
		emitted = emitted || (l.EventType == "state_emit" && strings.Contains(l.Payload, `"input_tokens":100000`))
	}
	assert.True(t, emitted, "the step's usage is on the audit chain")
}

// TestAgentEnv_shell_exec_needs_allow_shell catches a shell command reaching a
// decision in a run started without --allow-shell-exec.
func TestAgentEnv_shell_exec_needs_allow_shell(t *testing.T) {
	var resp orchestrator.Approval
	_, run, logs, _ := runInProcess(t, nil, func(ctx context.Context, env *orchestrator.AgentEnv) error {
		resp = env.Propose(ctx, domain.YieldRequest{AgentName: "coder", ActionType: "shell_exec",
			ProposedEdits: []domain.ProposedEdit{{File: ".", SearchBlock: "(shell)", ReplaceBlock: "rm -rf /"}}})
		return nil
	})
	assert.False(t, resp.Approved)
	assert.Contains(t, resp.Feedback, "--allow-shell-exec")
	var types []string
	for _, l := range logs {
		types = append(types, l.EventType)
	}
	assert.Contains(t, types, "shell_exec_rejected")
	assert.NotContains(t, types, "yield_decided", "it never reached a decision")
	assert.Equal(t, persistence.RunStatusSuccess, run.Status)
}

// TestAgentEnv_panic_fails_the_run catches an agent bug taking the whole
// orchestrator down instead of failing its run.
func TestAgentEnv_panic_fails_the_run(t *testing.T) {
	_, run, _, runErr := runInProcess(t, nil, func(context.Context, *orchestrator.AgentEnv) error {
		panic("boom")
	})
	assert.Equal(t, persistence.RunStatusFailed, run.Status)
	assert.ErrorContains(t, runErr, "agent panicked: boom")
}
