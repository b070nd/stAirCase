package orchestrator_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/wslock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writesThenEnds is a continuing agent: it proposes the given files (each approved by the policy) and ends.
func writesThenEnds(files ...[2]string) orchestrator.Agent {
	return orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
		for _, f := range files {
			write(c, env, f[0], f[1])
		}
		return nil
	})
}

func resume(t *testing.T, r runtest.Result, opts orchestrator.ResumeOptions) error {
	if opts.Agent == nil {
		opts.Agent = writesThenEnds()
	}
	return orchestrator.NewRunner(r.Store, r.WsDir).Resume(context.Background(), r.Run.ID, opts)
}

func eventsOf(t *testing.T, r runtest.Result, typ string) []domain.RunEventLog {
	events, err := r.Store.ListEventLogs(r.Run.ID)
	require.NoError(t, err)
	var out []domain.RunEventLog
	for _, e := range events {
		if e.EventType == typ {
			out = append(out, e)
		}
	}
	return out
}

// TestResume_continues_an_interrupted_run: the second segment is the same run, branch and worktree, starts from the
// approved state, continues proposal numbers, finishes, and delivers one commit holding the first segment's approval
// and its own, with a certificate on the same chain.
func TestResume_continues_an_interrupted_run(t *testing.T) {
	r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
	var brief string
	err := resume(t, r, orchestrator.ResumeOptions{Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
		brief = env.Continuation
		write(c, env, "src/b.txt", "two\n")
		return nil
	})})
	require.NoError(t, err)

	run, err := r.Store.GetRun(r.Run.ID)
	require.NoError(t, err)
	assert.Equal(t, persistence.RunStatusSuccess, run.Status)
	require.NotEmpty(t, run.GitCommitHash)
	assert.Equal(t, "one", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
	assert.Equal(t, "two", trim(git(t, r.Repo, "show", "staircase/run-1:src/b.txt")))
	assert.Equal(t, 1, strings.Count(git(t, r.Repo, "log", "--format=%H", "main..staircase/run-1"), "\n"), "one commit")

	assert.Contains(t, brief, "continuing it (segment 2)")
	assert.Contains(t, brief, "src/a.txt", "the agent is told what is already approved")

	resumed := eventsOf(t, r, "run_resumed")
	require.Len(t, resumed, 1)
	var p struct {
		Segment int    `json:"segment"`
		NextSeq int    `json:"next_seq"`
		Context string `json:"context"`
	}
	require.NoError(t, json.Unmarshal([]byte(resumed[0].Payload), &p))
	assert.Equal(t, 2, p.Segment)
	assert.Equal(t, 2, p.NextSeq, "proposal numbers continue from the chain")
	assert.Equal(t, "fresh_grounded", p.Context)
	var seqs []int
	for _, e := range eventsOf(t, r, "yield_decided") {
		var d struct{ Seq int }
		_ = json.Unmarshal([]byte(e.Payload), &d)
		seqs = append(seqs, d.Seq)
	}
	assert.Equal(t, []int{1, 2}, seqs)
	assert.NotEmpty(t, eventsOf(t, r, "certificate_issued"))
	require.NoError(t, r.Store.VerifyChain(r.Run.ID))
}

// firstSegment runs an agent that writes files (and takes hold of the policy and webhook the test sets up), and is then
// cancelled: an interrupted run, whose first segment is what the test needs.
func firstSegment(t *testing.T, policyJSON string, op *operator, files ...[2]string) runtest.Result {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := runtest.Run(t, runtest.Options{Ctx: ctx,
		Setup: func(s *persistence.Store, ws string, p int64) {
			require.NoError(t, crypto.GenerateSigningKey(ws))
			if op != nil {
				webhook(t, s, p, op)
			}
			if policyJSON != "" {
				require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), []byte(policyJSON), 0o600))
			}
		},
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			for _, f := range files {
				write(c, env, f[0], f[1])
			}
			cancel()
			<-c.Done()
			return c.Err()
		})})
	require.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	return r
}

func decidedBy(t *testing.T, r runtest.Result) (sources []string) {
	for _, e := range eventsOf(t, r, "yield_decided") {
		var d struct {
			Source, ActionType string
			Approved           bool
		}
		_ = json.Unmarshal([]byte(e.Payload), &d)
		if d.ActionType == domain.ActionFinalReview {
			continue
		}
		sources = append(sources, fmt.Sprintf("%s:%v", d.Source, d.Approved))
	}
	return
}

// TestResume_delivers_what_recovery_would: for the same history, a continuation whose agent does nothing delivers the
// tree that `recover` commits (one derivation of what was approved).
func TestResume_delivers_what_recovery_would(t *testing.T) {
	writes := [][2]string{{"src/a.txt", "one\n"}, {"src/b.txt", "two\n"}}
	viaResume := interruptedWith(t, writes)
	require.NoError(t, resume(t, viaResume, orchestrator.ResumeOptions{}))
	viaRecover := interruptedWith(t, writes)
	_, err := recoverRun(t, viaRecover)
	require.NoError(t, err)
	assert.Equal(t, trim(git(t, viaRecover.Repo, "rev-parse", "staircase/run-1^{tree}")), trim(git(t, viaResume.Repo, "rev-parse", "staircase/run-1^{tree}")))
}

// TestResume_the_policys_limits_are_cumulative: a limit that was reached in the first segment is still reached in the
// second. max_auto_approved is 2 and the first segment used both; a third proposal after the restart goes to a person,
// where a fresh run would have approved it by rule. The policy that applies is the saved one, not today's file.
func TestResume_the_policys_limits_are_cumulative(t *testing.T) {
	op := &operator{approve: false}
	r := firstSegment(t, `{"rules":[{"action_types":["file_edit"],"effect":"approve"}],"limits":{"max_auto_approved":2}}`, op,
		[2]string{"src/a.txt", "1\n"}, [2]string{"src/b.txt", "2\n"})
	// the policy file is changed after the kill: it would approve everything, with no limit
	require.NoError(t, os.WriteFile(filepath.Join(r.WsDir, "policy.json"), []byte(`{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}`), 0o600))

	var answered []bool
	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
		answered = append(answered, write(c, env, "src/c.txt", "3\n").Approved)
		return nil
	})}))
	assert.Equal(t, []bool{false}, answered, "the third proposal was not auto-approved: the limit of the first segment still holds")
	assert.Equal(t, []string{"policy:true", "policy:true", "operator:false"}, decidedBy(t, r))
	require.Len(t, op.seen, 1, "a person was asked")
}

// TestResume_the_supervisor_remembers_the_files_of_the_first_segment: max_files_changed counts every distinct file the
// run has changed, in both segments.
func TestResume_the_supervisor_remembers_the_files_of_the_first_segment(t *testing.T) {
	op := &operator{approve: true}
	r := firstSegment(t, `{"rules":[{"action_types":["file_edit"],"effect":"approve"}],"limits":{"max_files_changed":2}}`, op,
		[2]string{"src/a.txt", "1\n"}, [2]string{"src/b.txt", "2\n"})
	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{Agent: writesThenEnds([2]string{"src/c.txt", "3\n"})}))
	require.Len(t, op.seen, 1, "the third file went to a person")
	assert.Contains(t, op.seen[0].Drift, "3 files changed (limit 2)")
}

// TestResume_the_time_limit_is_for_the_whole_run: a run whose time limit the first segment used is not given a fresh
// one; it is refused, changed in no way, and can still be recovered.
func TestResume_the_time_limit_is_for_the_whole_run(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := runtest.Run(t, runtest.Options{Ctx: ctx,
		Setup: func(_ *persistence.Store, ws string, _ int64) {
			require.NoError(t, crypto.GenerateSigningKey(ws))
			require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), []byte(`{"rules":[{"action_types":["file_edit"],"effect":"approve"}],"limits":{"max_run_secs":2}}`), 0o600))
		},
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			write(c, env, "src/a.txt", "1\n")
			time.Sleep(2200 * time.Millisecond) // more than the run may take; its own limit ends it too
			write(c, env, "src/b.txt", "2\n")
			cancel()
			<-c.Done()
			return c.Err()
		})})
	_ = r
	err := resume(t, r, orchestrator.ResumeOptions{})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
	assert.ErrorContains(t, err, "used up its time limit")
	run, _ := r.Store.GetRun(r.Run.ID)
	assert.NotEqual(t, persistence.RunStatusRunning, run.Status, "the refusal changed nothing")
	assert.Empty(t, eventsOf(t, r, "run_resumed"))
	_, rerr := recoverRun(t, r)
	require.NoError(t, rerr, "it can still be recovered")
}

// TestResume_refuses_a_worktree_that_holds_what_nobody_approved: the agent changed files after its last approval, or
// left new ones. The continuation refuses, names them, changes nothing (no event, nothing staged); with
// --discard-unapproved the worktree is put back to the approved state and the run goes on.
func TestResume_refuses_a_worktree_that_holds_what_nobody_approved(t *testing.T) {
	r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
	wt := filepath.Join(r.WsDir, "worktrees", "run-1")
	require.NoError(t, os.WriteFile(filepath.Join(wt, "src", "a.txt"), []byte("changed after the approval\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wt, "stray.txt"), []byte("left behind\n"), 0o644))
	before := git(t, wt, "status", "--porcelain=v1")

	err := resume(t, r, orchestrator.ResumeOptions{})
	require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
	assert.ErrorContains(t, err, "src/a.txt")
	assert.ErrorContains(t, err, "stray.txt")
	assert.ErrorContains(t, err, "--discard-unapproved")
	assert.Empty(t, eventsOf(t, r, "run_resumed"), "nothing was written to the chain")
	assert.Equal(t, before, git(t, wt, "status", "--porcelain=v1"), "nothing was staged, reverted or removed")
	run, _ := r.Store.GetRun(r.Run.ID)
	assert.Equal(t, persistence.RunStatusKilled, run.Status)

	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{DiscardUnapproved: true}))
	assert.Equal(t, "one", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
	assert.NotContains(t, git(t, r.Repo, "ls-tree", "-r", "--name-only", "staircase/run-1"), "stray.txt")
}

// TestResume_a_repeated_proposal_is_a_new_proposal: an agent that proposes again what the first segment already had
// approved gets a new sequence number and a new decision: it is never matched to the old one.
func TestResume_a_repeated_proposal_is_a_new_proposal(t *testing.T) {
	r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{Agent: writesThenEnds([2]string{"src/a.txt", "one\n"})}))
	var seqs []int
	for _, e := range eventsOf(t, r, "yield_decided") {
		var d struct{ Seq int }
		_ = json.Unmarshal([]byte(e.Payload), &d)
		seqs = append(seqs, d.Seq)
	}
	assert.Equal(t, []int{1, 2}, seqs)
	assert.Equal(t, "one", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
}

// TestResume_refuses_a_run_that_cannot_be_continued: each case changes nothing and says why.
func TestResume_refuses_a_run_that_cannot_be_continued(t *testing.T) {
	t.Run("a run that already has its commit", func(t *testing.T) {
		r := runtest.Run(t, runtest.Options{Agent: writesThenEnds([2]string{"src/a.txt", "1\n"})})
		require.NoError(t, r.Err)
		assert.ErrorIs(t, resume(t, r, orchestrator.ResumeOptions{}), orchestrator.ErrNotContinuable)
	})
	t.Run("a recovery has begun", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		_, err := recoverRun(t, r)
		require.NoError(t, err)
		_, err = r.DB.Exec(`UPDATE runs SET status = 'KILLED', git_commit_hash = '' WHERE id = 1`)
		require.NoError(t, err)
		err = resume(t, r, orchestrator.ResumeOptions{})
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "recovery") || strings.Contains(err.Error(), "commit"), "%v", err)
	})
	t.Run("the run's saved policy is gone", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		require.NoError(t, os.Remove(filepath.Join(r.WsDir, "journal", "run-1.policy.json")))
		err := resume(t, r, orchestrator.ResumeOptions{})
		require.ErrorIs(t, err, orchestrator.ErrNotContinuable)
		assert.ErrorContains(t, err, "saved no policy")
	})
	t.Run("the saved policy is not the one the chain names", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		require.NoError(t, os.WriteFile(filepath.Join(r.WsDir, "journal", "run-1.policy.json"), []byte(`{"rules":[]}`), 0o600))
		assert.ErrorContains(t, resume(t, r, orchestrator.ResumeOptions{}), "not the one the audit chain names")
	})
	t.Run("a run of an older version saved nothing", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		require.NoError(t, os.Remove(filepath.Join(r.WsDir, "journal", "run-1.options.json")))
		assert.ErrorContains(t, resume(t, r, orchestrator.ResumeOptions{}), "recover it instead")
	})
	t.Run("the branch moved", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		git(t, r.Repo, "commit", "--allow-empty", "-q", "-m", "elsewhere")
		git(t, r.Repo, "update-ref", "refs/heads/staircase/run-1", "HEAD")
		assert.ErrorContains(t, resume(t, r, orchestrator.ResumeOptions{}), "moved")
	})
	t.Run("the audit chain was changed", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		_, err := r.DB.Exec(`UPDATE run_event_logs SET payload = replace(payload, '"approved":true', '"approved":true ') WHERE event_type = 'yield_decided'`)
		require.NoError(t, err)
		assert.ErrorContains(t, resume(t, r, orchestrator.ResumeOptions{}), "does not verify")
	})
	t.Run("a run that is alive", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		held, err := os.OpenFile(filepath.Join(r.WsDir, "journal", "run-1.owner.lock"), os.O_RDWR, 0)
		require.NoError(t, err)
		require.NoError(t, wslock.LockExclusive(held.Fd()))
		defer func() { _ = wslock.Unlock(held.Fd()); _ = held.Close() }()
		assert.ErrorContains(t, resume(t, r, orchestrator.ResumeOptions{}), "still running")
	})
}

// TestResume_only_the_time_that_is_left_is_given: the first segment used part of max_run_secs; the continuation is
// given the rest, so an agent that would take longer is ended at the run's limit, not at a fresh one.
func TestResume_only_the_time_that_is_left_is_given(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := runtest.Run(t, runtest.Options{Ctx: ctx,
		Setup: func(_ *persistence.Store, ws string, _ int64) {
			require.NoError(t, crypto.GenerateSigningKey(ws))
			require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), []byte(`{"rules":[{"action_types":["file_edit"],"effect":"approve"}],"limits":{"max_run_secs":3}}`), 0o600))
		},
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			write(c, env, "src/a.txt", "1\n")
			time.Sleep(1500 * time.Millisecond) // the first segment uses about half of the run's time
			write(c, env, "src/b.txt", "2\n")
			cancel()
			<-c.Done()
			return c.Err()
		})})
	var late bool
	start := time.Now()
	err := resume(t, r, orchestrator.ResumeOptions{Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
		select {
		case <-time.After(10 * time.Second): // would run past the limit
			late = write(c, env, "src/c.txt", "3\n").Approved
		case <-c.Done():
		}
		return nil
	})})
	assert.Error(t, err, "a run that ran out of time is not a success")
	assert.Less(t, time.Since(start), 2500*time.Millisecond, "the continuation was ended at what was left, not at a fresh limit")
	assert.False(t, late)
}

// TestResume_applies_an_approval_the_agent_died_before_writing: the agent was killed between an approval and the write.
// The file is as it was at the base, so exactly the approved bytes are written (that is what was approved); a file the
// agent changed some other way is not.
func TestResume_applies_an_approval_the_agent_died_before_writing(t *testing.T) {
	r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
	wt := filepath.Join(r.WsDir, "worktrees", "run-1")
	require.NoError(t, os.Remove(filepath.Join(wt, "src", "a.txt"))) // approved, never written
	require.NoError(t, resume(t, r, orchestrator.ResumeOptions{}))
	assert.Equal(t, "one", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
	assert.Empty(t, eventsOf(t, r, "unapproved_worktree_change"))
}
