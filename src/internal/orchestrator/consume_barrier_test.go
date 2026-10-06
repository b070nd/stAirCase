//go:build barriers

package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/barrier"
	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These run only in a build with -tags barriers: they end a run at an exact point (the
// hook fires in the run's own goroutine at the point, so there is no timing), where the
// kill drill ends a process there. The run's context is the one cancelled: the run limit
// (max_run_secs) ends the same waitCtx, so it takes the same path.

// cancelAt cancels the run when the n-th visit of point is reached.
func cancelAt(t *testing.T, point string, n int, cancel context.CancelFunc) {
	var mu sync.Mutex
	seen := 0
	restore := barrier.SetHook(func(name string) {
		if name != point {
			return
		}
		mu.Lock()
		seen++
		fire := seen == n
		mu.Unlock()
		if fire {
			cancel()
		}
	})
	t.Cleanup(restore)
}

// twoWrites is a run whose agent has two files approved by the policy and then, if it is
// still going, ends. The agent also reports what the second answer was.
func twoWrites(t *testing.T, ctx context.Context, answers *[]bool) runtest.Result {
	return runtest.Run(t, runtest.Options{Ctx: ctx,
		Setup: func(_ *persistence.Store, ws string, _ int64) { require.NoError(t, crypto.GenerateSigningKey(ws)) },
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			for _, f := range [][2]string{{"src/a.txt", "first\n"}, {"src/b.txt", "second\n"}} {
				*answers = append(*answers, write(c, env, f[0], f[1]).Approved)
			}
			return nil
		})})
}

func journalCount(t *testing.T, r runtest.Result) int {
	b, err := os.ReadFile(filepath.Join(r.WsDir, "journal", "run-1.approved.jsonl"))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(b), "\n")
}

func decisions(t *testing.T, r runtest.Result) (approved, rejected int) {
	events, err := r.Store.ListEventLogs(r.Run.ID)
	require.NoError(t, err)
	for _, e := range events {
		if e.EventType != "yield_decided" {
			continue
		}
		if strings.Contains(e.Payload, `"approved":true`) {
			approved++
		} else {
			rejected++
		}
	}
	return
}

// TestConsumption_expiry_before_the_journal: the run ends after the second proposal was
// decided and before anything of it is kept. It is not journaled, not on the chain as an
// approval and not released; the first approval, consumed earlier, is recovered once with
// exactly its bytes.
func TestConsumption_expiry_before_the_journal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelAt(t, barrier.DecisionMade, 2, cancel)
	var answers []bool
	r := twoWrites(t, ctx, &answers)
	assert.Equal(t, []bool{true, false}, answers, "the agent was told yes to the first and no to the second")
	assert.Equal(t, 1, journalCount(t, r), "only the consumed approval is in the journal")
	a, rej := decisions(t, r)
	assert.Equal(t, 1, a)
	assert.Equal(t, 1, rej)
	branchUntouched(t, r)

	res, err := recoverRun(t, r)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Proposals)
	assert.Equal(t, "first", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
	assert.Equal(t, "src/a.txt", trim(git(t, r.Repo, "diff", "--name-only", "HEAD", "staircase/run-1")), "exactly the earlier approval")
}

// TestConsumption_expiry_between_the_journal_and_the_chain: the run ends after the journal
// line was synced and before the decision was put on the chain. The authorization was not
// consumed in time: the chain records it as rejected (and says its journal line is an
// unconsumed one), the agent is told no, and recovery commits the earlier approval only; the
// unfinished line never becomes an approval and is not read as corruption.
func TestConsumption_expiry_between_the_journal_and_the_chain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelAt(t, barrier.JournalSynced, 2, cancel)
	var answers []bool
	r := twoWrites(t, ctx, &answers)
	assert.Equal(t, []bool{true, false}, answers)
	assert.Equal(t, 2, journalCount(t, r), "the second line was written before the run ended")
	a, rej := decisions(t, r)
	assert.Equal(t, 1, a, "the chain holds one approval")
	assert.Equal(t, 1, rej)
	events, _ := r.Store.ListEventLogs(r.Run.ID)
	unconsumed := 0
	for _, e := range events {
		if strings.Contains(e.Payload, `"unconsumed":true`) {
			unconsumed++
		}
	}
	assert.Equal(t, 1, unconsumed, "the rejection says it was a journaled authorization that was not consumed")
	branchUntouched(t, r)

	res, err := recoverRun(t, r)
	require.NoError(t, err, "an unconsumed journal line is not corruption")
	assert.Equal(t, 1, res.Proposals)
	assert.Equal(t, "first", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
	assert.Equal(t, "src/a.txt", trim(git(t, r.Repo, "diff", "--name-only", "HEAD", "staircase/run-1")))
}

// TestConsumption_an_unconsumed_line_is_not_a_licence: the tolerance is for a line the chain
// itself says expired. A journal line the chain rejected without that mark, or one whose
// request is not the one the chain recorded, still refuses recovery.
func TestConsumption_an_unconsumed_line_is_not_a_licence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelAt(t, barrier.JournalSynced, 2, cancel)
	var answers []bool
	r := twoWrites(t, ctx, &answers)
	// the chain's mark removed: the same history without "unconsumed"
	rewriteChain(t, r, func(e domain.RunEventLog, p map[string]any) []domain.RunEventLog {
		if _, ok := p["unconsumed"]; ok {
			delete(p, "unconsumed")
			return []domain.RunEventLog{as(e, p)}
		}
		return []domain.RunEventLog{e}
	})
	_, err := recoverRun(t, r)
	assert.ErrorContains(t, err, "did not approve", "a rejected proposal's journal line without the mark is corruption")
	branchUntouched(t, r)
}

// TestConsumption_expiry_before_the_branch_moves: the run is cancelled after its commit
// was made and named on the chain and before the branch moved. Nothing is delivered (the
// branch is where it was, the run is KILLED) and the commit the chain names is not what a
// recovery then delivers: recovery makes its own commit of the approved bytes.
func TestConsumption_expiry_before_the_branch_moves(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelAt(t, barrier.CommitPrepared, 1, cancel)
	var answers []bool
	r := twoWrites(t, ctx, &answers)
	assert.Equal(t, []bool{true, true}, answers)
	assert.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	assert.Empty(t, r.Run.GitCommitHash)
	branchUntouched(t, r)
	named := ""
	for _, e := range r.Events {
		if e.EventType == "commit_prepared" {
			named = e.Payload
		}
	}
	require.NotEmpty(t, named, "the commit was named before it could be delivered")

	res, err := recoverRun(t, r)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Proposals)
	assert.NotContains(t, named, res.Commit, "recovery delivered its own commit, not the one that was only named")
	assert.Equal(t, "first", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
	assert.Equal(t, "second", trim(git(t, r.Repo, "show", "staircase/run-1:src/b.txt")))
}

// TestConsumption_the_final_review_is_not_bound_by_the_run_limit: positive control for the
// deliberate semantics. The agent ends, and the person (the final review, since the task
// approved the change) answers after max_run_secs has passed: the review is bound by
// cancellation only, so the commit is delivered.
func TestConsumption_the_final_review_is_not_bound_by_the_run_limit(t *testing.T) {
	person, _ := slowPerson(t, 2500*time.Millisecond)
	r := runtest.Run(t, runtest.Options{
		Run: orchestrator.RunOptions{ApproveInScope: true, Agreed: "dev@example.com"},
		Setup: func(st *persistence.Store, ws string, p int64) {
			require.NoError(t, crypto.GenerateSigningKey(ws))
			require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), []byte(`{"rules":[],"limits":{"max_run_secs":1}}`), 0o600))
			require.NoError(t, st.UpdateProjectWebhook(p, person.URL))
			cases, _ := st.ListCasesByProject(p)
			stories, _ := st.ListUserStoriesByCase(cases[0].ID)
			if len(stories) == 0 {
				_, _ = st.CreateUserStory(cases[0].ID, "Greet")
				stories, _ = st.ListUserStoriesByCase(cases[0].ID)
			}
			for _, s := range stories {
				_ = st.SetUserStoryScope(s.ID, `{"allow":["src/**"]}`)
			}
		},
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			write(c, env, "src/a.txt", "first\n")
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	assert.NotEmpty(t, r.Run.GitCommitHash, "a final review answered after the run limit still delivers")
	assert.Equal(t, "first", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
}

// TestConsumption_expiry_while_waiting_for_the_chain: the approval has been journaled and every earlier
// check passed; the run ends while the decision's append holds the chain lock, before it has checked.
// That check, under the lock and right before the insert, is the last one: the authorization is not
// consumed, the chain records a rejection marked unconsumed, the agent is told no, and recovery
// commits the earlier approval only.
func TestConsumption_expiry_while_waiting_for_the_chain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelAt(t, barrier.AuditAppend, 2, cancel)
	var answers []bool
	r := twoWrites(t, ctx, &answers)
	assert.Equal(t, []bool{true, false}, answers)
	assert.Equal(t, 2, journalCount(t, r), "the second line was journaled before the run ended")
	a, rej := decisions(t, r)
	assert.Equal(t, 1, a, "the chain holds one approval")
	assert.Equal(t, 1, rej)
	unconsumed := 0
	for _, e := range r.Events {
		if strings.Contains(e.Payload, `"unconsumed":true`) {
			unconsumed++
		}
	}
	assert.Equal(t, 1, unconsumed)
	branchUntouched(t, r)
	res, err := recoverRun(t, r)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Proposals)
	assert.Equal(t, "src/a.txt", trim(git(t, r.Repo, "diff", "--name-only", "HEAD", "staircase/run-1")), "exactly the earlier approval")
}

// TestConsumption_an_append_that_has_begun_is_consumed: the run ends right after the decision was
// written. It was consumed: it is on the chain, and recovery commits it, once, with exactly its bytes. The
// agent is told yes only for what is on the chain (its own end may make it hear "the run ended" instead).
func TestConsumption_an_append_that_has_begun_is_consumed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancelAt(t, barrier.AuditCommitted, 2, cancel)
	var answers []bool
	r := twoWrites(t, ctx, &answers)
	a, rej := decisions(t, r)
	// The agent may be told "the run ended" for a decision that was consumed (its own context ended too),
	// which is safe: it then does not act. What can never happen is a yes for what is not on the chain.
	yes := 0
	for _, ok := range answers {
		if ok {
			yes++
		}
	}
	assert.LessOrEqual(t, yes, a, "the agent was told yes only for what is on the chain")
	assert.Equal(t, 2, a)
	assert.Equal(t, 0, rej)
	assert.Equal(t, 2, journalCount(t, r))
	assert.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	branchUntouched(t, r)
	res, err := recoverRun(t, r)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Proposals)
	assert.Equal(t, "first", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
	assert.Equal(t, "second", trim(git(t, r.Repo, "show", "staircase/run-1:src/b.txt")))
	_, err = recoverRun(t, r)
	assert.Error(t, err, "recovered once: a second recovery has nothing to do")
}
