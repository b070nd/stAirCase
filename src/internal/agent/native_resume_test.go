package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/agent"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interruptedHarness is a run of an agent harness that was killed after its first approval: the agent recorded its
// vendor session (sessionID, "" for an agent that never got as far as recording one), had a file approved, and the
// run was then cancelled.
func interruptedHarness(t *testing.T, harness, sessionID string) runtest.Result {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := runtest.Run(t, runtest.Options{Ctx: ctx, Run: orchestrator.RunOptions{Plan: &plan.Plan{Harness: harness}},
		Setup: func(_ *persistence.Store, ws string, _ int64) { require.NoError(t, crypto.GenerateSigningKey(ws)) },
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			if sessionID != "" {
				env.RecordSession(harness, sessionID, "started")
			}
			ap := env.Propose(c, domain.YieldRequest{AgentName: harness, ActionType: "file_edit",
				ProposedEdits: []domain.ProposedEdit{{File: "src/a.txt", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "one\n"}}})
			if ap.Approved {
				_ = ap.Apply(env.Worktree)
			}
			cancel()
			<-c.Done()
			return c.Err()
		})})
	require.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	return r
}

func argsOf(t *testing.T, file string) []string {
	b, err := os.ReadFile(file)
	require.NoError(t, err)
	var args []string
	require.NoError(t, json.Unmarshal(b, &args))
	return args
}

func contextOf(t *testing.T, r runtest.Result) (kind string, sessions []string) {
	events, err := r.Store.ListEventLogs(r.Run.ID)
	require.NoError(t, err)
	for _, e := range events {
		var p struct{ Context, Mode, ID string }
		_ = json.Unmarshal([]byte(e.Payload), &p)
		switch e.EventType {
		case "run_resumed":
			kind = p.Context
		case "agent_session":
			sessions = append(sessions, p.Mode+":"+p.ID)
		}
	}
	return
}

func resumeWith(t *testing.T, r runtest.Result, ag orchestrator.Agent, opts orchestrator.ResumeOptions) error {
	opts.Agent = ag
	return orchestrator.NewRunner(r.Store, r.WsDir).Resume(context.Background(), r.Run.ID, opts)
}

// TestNativeResume_each_agent_resumes_its_own_session: a run of Claude Code, Gemini CLI or Codex that was killed is continued by
// resuming the vendor's own session, whose id the chain named; the chain says so (`run_resumed` context and the resumed
// `agent_session`), and the agent is started with the vendor's resume arguments and not as a new session.
func TestNativeResume_each_agent_resumes_its_own_session(t *testing.T) {
	const id = "0f9e8d7c-6b5a-4938-8271-605f4e3d2c1b"
	t.Run("claude-code", func(t *testing.T) {
		r := interruptedHarness(t, "claude-code", id)
		bin, _, args := claudeBinWithArgs(t)
		require.NoError(t, resumeWith(t, r, &agent.ClaudeCode{Prompt: "p", Bin: bin}, orchestrator.ResumeOptions{}))
		got := argsOf(t, args)
		assert.Contains(t, got, "--resume")
		assert.Contains(t, got, id)
		assert.NotContains(t, got, "--session-id", "a resumed session is not started under an id")
		kind, sessions := contextOf(t, r)
		assert.Equal(t, "native_resume", kind)
		assert.Equal(t, []string{"started:" + id, "resumed:" + id}, sessions)
	})
	t.Run("gemini", func(t *testing.T) {
		r := interruptedHarness(t, "gemini", id)
		bin, _, args := geminiBin(t, "1", nil)
		require.NoError(t, resumeWith(t, r, &agent.Gemini{Prompt: "p", Bin: bin}, orchestrator.ResumeOptions{}))
		got := argsOf(t, args)
		assert.Contains(t, got, "--resume")
		assert.Contains(t, got, id)
		assert.NotContains(t, got, "--session-id")
		kind, _ := contextOf(t, r)
		assert.Equal(t, "native_resume", kind)
	})
	t.Run("codex", func(t *testing.T) {
		r := interruptedHarness(t, "codex", id)
		bin, args := codexBin(t, "1")
		require.NoError(t, resumeWith(t, r, &agent.Codex{Prompt: "p", Bin: bin}, orchestrator.ResumeOptions{}))
		got := argsOf(t, args)
		assert.Equal(t, []string{"exec", "resume"}, got[:2], "codex exec resume <id>")
		assert.Contains(t, got, id)
		assert.Contains(t, got, `sandbox_mode="workspace-write"`, "exec resume takes no -s: the sandbox is set through the configuration")
		assert.Contains(t, got, "--dangerously-bypass-hook-trust")
		kind, _ := contextOf(t, r)
		assert.Equal(t, "native_resume", kind)
	})
}

// TestNativeResume_a_first_segment_puts_its_session_on_the_chain: the id of the agent's vendor session is chosen (Claude
// Code, Gemini) or learned from the first event of its stream (Codex) and audited before the agent can be killed.
func TestNativeResume_a_first_segment_puts_its_session_on_the_chain(t *testing.T) {
	sessionOf := func(t *testing.T, r runtest.Result) (agentName, id, mode string) {
		for _, e := range r.Events {
			if e.EventType == "agent_session" {
				var p struct{ Agent, ID, Mode string }
				require.NoError(t, json.Unmarshal([]byte(e.Payload), &p))
				return p.Agent, p.ID, p.Mode
			}
		}
		t.Fatalf("no agent_session on the chain: %v", r.Types())
		return
	}
	t.Run("claude-code: chosen by staircase, passed as --session-id", func(t *testing.T) {
		bin, _, args := claudeBinWithArgs(t)
		r := runtest.Run(t, runtest.Options{Agent: &agent.ClaudeCode{Prompt: "p", Bin: bin}})
		require.NoError(t, r.Err)
		name, id, mode := sessionOf(t, r)
		assert.Equal(t, []string{"claude-code", "started"}, []string{name, mode})
		assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, id, "a version 4 UUID")
		got := argsOf(t, args)
		assert.Contains(t, got, "--session-id")
		assert.Contains(t, got, id)
	})
	t.Run("gemini: chosen by staircase", func(t *testing.T) {
		bin, _, args := geminiBin(t, "1", nil)
		r := runtest.Run(t, runtest.Options{Agent: &agent.Gemini{Prompt: "p", Bin: bin}})
		require.NoError(t, r.Err)
		_, id, _ := sessionOf(t, r)
		got := argsOf(t, args)
		assert.Contains(t, got, "--session-id")
		assert.Contains(t, got, id)
	})
	t.Run("codex: learned from thread.started", func(t *testing.T) {
		bin, _ := codexBin(t, "1")
		r := runtest.Run(t, runtest.Options{Agent: &agent.Codex{Prompt: "p", Bin: bin}})
		require.NoError(t, r.Err)
		name, id, mode := sessionOf(t, r)
		assert.Equal(t, []string{"codex", "started", "019aaaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee"}, []string{name, mode, id})
	})
}

// TestNativeResume_refuses_what_it_cannot_do_and_never_falls_back_silently: an agent that cannot resume its session, or a
// chain that names none, refuses the continuation, says why and offers --fresh-context; with it the run continues in a new
// session grounded by the audit chain, and the chain says "fresh_grounded".
func TestNativeResume_refuses_what_it_cannot_do_and_never_falls_back_silently(t *testing.T) {
	t.Run("an agent that cannot resume", func(t *testing.T) {
		r := interruptedHarness(t, "opencode", "some-id")
		stand := orchestrator.AgentFunc(func(context.Context, *orchestrator.AgentEnv) error { return nil })
		err := resumeWith(t, r, stand, orchestrator.ResumeOptions{})
		require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
		assert.ErrorContains(t, err, "cannot be resumed")
		assert.ErrorContains(t, err, "--fresh-context")
		events, _ := r.Store.ListEventLogs(r.Run.ID)
		for _, e := range events {
			assert.NotEqual(t, "run_resumed", e.EventType, "a refusal changes nothing")
		}
		require.NoError(t, resumeWith(t, r, stand, orchestrator.ResumeOptions{Context: "fresh_grounded"}))
		kind, _ := contextOf(t, r)
		assert.Equal(t, "fresh_grounded", kind)
	})
	t.Run("the chain names no session", func(t *testing.T) {
		r := interruptedHarness(t, "claude-code", "")
		bin, _, _ := claudeBinWithArgs(t)
		err := resumeWith(t, r, &agent.ClaudeCode{Prompt: "p", Bin: bin}, orchestrator.ResumeOptions{})
		require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
		assert.ErrorContains(t, err, "names no claude-code session")
		assert.ErrorContains(t, err, "--fresh-context")
	})
	t.Run("a resume that was demanded and cannot be done", func(t *testing.T) {
		r := interruptedHarness(t, "claude-code", "")
		bin, _, _ := claudeBinWithArgs(t)
		err := resumeWith(t, r, &agent.ClaudeCode{Prompt: "p", Bin: bin}, orchestrator.ResumeOptions{Context: "native_resume"})
		assert.ErrorIs(t, err, orchestrator.ErrNotContinuable)
	})
	t.Run("a fresh context is chosen over a resume", func(t *testing.T) {
		r := interruptedHarness(t, "claude-code", "0f9e8d7c-6b5a-4938-8271-605f4e3d2c1b")
		bin, _, args := claudeBinWithArgs(t)
		require.NoError(t, resumeWith(t, r, &agent.ClaudeCode{Prompt: "p", Bin: bin}, orchestrator.ResumeOptions{Context: "fresh_grounded"}))
		got := argsOf(t, args)
		assert.NotContains(t, got, "--resume", "a person chose a new session")
		assert.Contains(t, got, "--session-id")
		kind, sessions := contextOf(t, r)
		assert.Equal(t, "fresh_grounded", kind)
		assert.Len(t, sessions, 2)
		assert.True(t, strings.HasPrefix(sessions[1], "started:"), "the new session is a new one, recorded as started: %v", sessions)
	})
}
