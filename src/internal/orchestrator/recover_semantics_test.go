package orchestrator_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rewriteChain replaces a run's audit chain, through the store, with what mutate makes of
// it: every event it keeps is appended again, so the hashes are right and only the history
// is wrong. mutate returns the event(s) to write in place of e (none drops it).
func rewriteChain(t *testing.T, r runtest.Result, mutate func(e domain.RunEventLog, payload map[string]any) []domain.RunEventLog) {
	t.Helper()
	events, err := r.Store.ListEventLogs(r.Run.ID)
	require.NoError(t, err)
	_, err = r.DB.Exec(`DELETE FROM run_event_logs WHERE run_id = ?`, r.Run.ID)
	require.NoError(t, err)
	for _, e := range events {
		p := map[string]any{}
		_ = json.Unmarshal([]byte(e.Payload), &p)
		for _, out := range mutate(e, p) {
			_, err := r.Store.AppendEventLogChained(r.Run.ID, out.EventType, out.Payload, "")
			require.NoError(t, err)
		}
	}
}

func appendEvent(t *testing.T, r runtest.Result, typ string, payload map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	_, err = r.Store.AppendEventLogChained(r.Run.ID, typ, string(b), "")
	require.NoError(t, err)
}

// as returns e with its payload replaced by p.
func as(e domain.RunEventLog, p map[string]any) domain.RunEventLog {
	b, _ := json.Marshal(p)
	e.Payload = string(b)
	return e
}

func trim(s string) string { return strings.TrimSpace(s) }

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// TestRecover_refuses_histories_with_a_wrong_shape_even_when_their_hashes_are_right:
// real store, hash-valid chains. Live runs audit a yield_request before the decision it
// leads to (the final review is the exception: it is a decision by itself), and record the
// digest of exactly the approved state with every approval. A history without those is not
// one a run of any version that keeps a journal (v0.6.0 on) writes; recovery refuses it
// before it touches git.
func TestRecover_refuses_histories_with_a_wrong_shape_even_when_their_hashes_are_right(t *testing.T) {
	firstDecision := func(e domain.RunEventLog) bool {
		return e.EventType == "yield_decided"
	}
	for name, change := range map[string]func(e domain.RunEventLog, p map[string]any, n *int) []domain.RunEventLog{
		"a decision that has no request": func(e domain.RunEventLog, p map[string]any, n *int) []domain.RunEventLog {
			if e.EventType == "yield_request" {
				*n++
				if *n == 1 {
					return nil
				}
			}
			return []domain.RunEventLog{e}
		},
		"a request of another kind than the decision": func(e domain.RunEventLog, p map[string]any, n *int) []domain.RunEventLog {
			if e.EventType == "yield_request" {
				*n++
				if *n == 1 {
					p["action_type"] = domain.ActionShellExec
					return []domain.RunEventLog{as(e, p)}
				}
			}
			return []domain.RunEventLog{e}
		},
		"an approval with no digest of the approved state": func(e domain.RunEventLog, p map[string]any, n *int) []domain.RunEventLog {
			if firstDecision(e) {
				*n++
				if *n == 1 {
					delete(p, "files")
					return []domain.RunEventLog{as(e, p)}
				}
			}
			return []domain.RunEventLog{e}
		},
		"an approval with an empty digest": func(e domain.RunEventLog, p map[string]any, n *int) []domain.RunEventLog {
			if firstDecision(e) {
				*n++
				if *n == 1 {
					p["files"] = map[string]string{}
					return []domain.RunEventLog{as(e, p)}
				}
			}
			return []domain.RunEventLog{e}
		},
		"an approval whose digest is of other bytes": func(e domain.RunEventLog, p map[string]any, n *int) []domain.RunEventLog {
			if firstDecision(e) {
				*n++
				if *n == 1 {
					p["files"] = map[string]string{"src/a.txt": sha("not what was approved\n")}
					return []domain.RunEventLog{as(e, p)}
				}
			}
			return []domain.RunEventLog{e}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}, {"src/b.txt", "two\n"}})
			n := 0
			rewriteChain(t, r, func(e domain.RunEventLog, p map[string]any) []domain.RunEventLog { return change(e, p, &n) })
			_, err := recoverRun(t, r)
			require.Error(t, err, "recovered from a history no run writes")
			branchUntouched(t, r)
		})
	}
}

// TestRecover_the_final_review_on_the_chain_is_the_review: a run killed after its
// person approved the whole change and before the commit already has the review recovery
// would ask for, so it is not asked again; a review of other bytes than the approvals
// derive is refused.
func TestRecover_the_final_review_on_the_chain_is_the_review(t *testing.T) {
	taskRun := func(t *testing.T) runtest.Result {
		return interrupted(t, inSrc(&operator{approve: true}), orchestrator.RunOptions{ApproveInScope: true, Agreed: "dev@example.com"})
	}
	finalReview := func(approved bool, files map[string]string) map[string]any {
		return map[string]any{"seq": 3, "source": "operator", "agent": "staircase", "action_type": domain.ActionFinalReview,
			"approved": approved, "request_sha256": "x", "files": files}
	}
	whole := map[string]string{"src/a.txt": sha("first\n"), "src/b.txt": sha("second\n")}

	t.Run("a review of exactly the approved state is not asked for again", func(t *testing.T) {
		r := taskRun(t)
		appendEvent(t, r, "yield_decided", finalReview(true, whole))
		res, err := recoverRun(t, r) // no Confirm: asking would fail
		require.NoError(t, err)
		assert.Equal(t, 2, res.Proposals)
		assert.Equal(t, "first", trim(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
		assert.Equal(t, "second", trim(git(t, r.Repo, "show", "staircase/run-1:src/b.txt")))
	})
	t.Run("a review of other bytes is refused", func(t *testing.T) {
		r := taskRun(t)
		appendEvent(t, r, "yield_decided", finalReview(true, map[string]string{"src/a.txt": sha("first\n"), "src/b.txt": sha("something else\n")}))
		_, err := recoverRun(t, r)
		assert.ErrorContains(t, err, "final review")
		branchUntouched(t, r)
	})
	t.Run("a review that was not approved is not one", func(t *testing.T) {
		r := taskRun(t)
		appendEvent(t, r, "yield_decided", finalReview(false, whole))
		_, err := recoverRun(t, r) // still needs a person's review: none can be asked
		assert.ErrorContains(t, err, "final review")
		branchUntouched(t, r)
	})
	t.Run("without a review on the chain one is still needed", func(t *testing.T) {
		r := taskRun(t)
		_, err := recoverRun(t, r)
		assert.ErrorContains(t, err, "final review")
		branchUntouched(t, r)
	})
}
