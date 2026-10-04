package orchestrator_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interruptedWith is a run whose agent made the given writes (each approved by
// the policy) and was then cancelled before it could commit.
func interruptedWith(t *testing.T, writes [][2]string) runtest.Result {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := runtest.Run(t, runtest.Options{Ctx: ctx,
		Setup: func(_ *persistence.Store, ws string, _ int64) { require.NoError(t, crypto.GenerateSigningKey(ws)) },
		Agent: orchestrator.AgentFunc(func(c context.Context, env *orchestrator.AgentEnv) error {
			for _, w := range writes {
				if w[1] == badEdit { // refused by the orchestrator: the file does not exist
					env.ProposeEdit(c, "coder", "r", domain.ProposedEdit{File: w[0], SearchBlock: "absent\n", ReplaceBlock: "x\n"})
					continue
				}
				write(c, env, w[0], w[1])
			}
			cancel()
			<-c.Done()
			return c.Err()
		})})
	require.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	return r
}

// badEdit stands for an edit of a file that does not exist, which is refused.
const badEdit = "\x00bad"

func journalLines(t *testing.T, r runtest.Result) (string, []string) {
	path := filepath.Join(r.WsDir, "journal", fmt.Sprintf("run-%d.approved.jsonl", r.Run.ID))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return path, strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func recoverRun(t *testing.T, r runtest.Result) (orchestrator.RecoverResult, error) {
	return orchestrator.NewRunner(r.Store, r.WsDir).Recover(context.Background(), r.Run.ID, orchestrator.RecoverOptions{})
}

// branchUntouched asserts that recovery made no commit: the run's branch is
// still at the commit it started from.
func branchUntouched(t *testing.T, r runtest.Result) {
	t.Helper()
	tip := strings.TrimSpace(git(t, r.Repo, "rev-parse", "--verify", "-q", "staircase/run-1"))
	base := strings.TrimSpace(git(t, r.Repo, "rev-parse", "HEAD"))
	assert.Equal(t, base, tip, "recovery made a commit")
}

// TestRecover_refuses_a_tampered_audit_chain: the chain is verified before any
// row of it is believed, so changing a decision's text, type or hash is caught
// before recovery touches git.
func TestRecover_refuses_a_tampered_audit_chain(t *testing.T) {
	for name, sql := range map[string]string{
		"payload": `UPDATE run_event_logs SET payload = replace(payload, '"approved":true', '"approved":true ') WHERE event_type = 'yield_decided'`,
		"type":    `UPDATE run_event_logs SET event_type = 'validator_note' WHERE id = (SELECT MIN(id) FROM run_event_logs WHERE event_type = 'yield_decided')`,
		"hash":    `UPDATE run_event_logs SET event_hash = substr(event_hash, 2) || 'f' WHERE id = (SELECT MAX(id) FROM run_event_logs)`,
	} {
		t.Run(name, func(t *testing.T) {
			r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
			res, err := r.DB.Exec(sql)
			require.NoError(t, err)
			n, _ := res.RowsAffected()
			require.NotZero(t, n, "the damage must hit something")
			_, err = recoverRun(t, r)
			assert.ErrorContains(t, err, "does not verify")
			branchUntouched(t, r)
		})
	}
}

// TestRecover_applies_approvals_in_the_chains_order: the journal's line order is
// a file anyone can edit; the order of the approvals is the audit chain's.
func TestRecover_applies_approvals_in_the_chains_order(t *testing.T) {
	r := interruptedWith(t, [][2]string{{"src/x.txt", "first\n"}, {"src/x.txt", "second\n"}})
	path, lines := journalLines(t, r)
	require.Len(t, lines, 2)
	require.NoError(t, os.WriteFile(path, []byte(lines[1]+"\n"+lines[0]+"\n"), 0o600)) // reordered, both authentic

	_, err := recoverRun(t, r)
	require.NoError(t, err)
	got, err := r.OnBranch("src/x.txt")
	require.NoError(t, err)
	assert.Equal(t, "second\n", got, "the later approval wins, as it did in the run")
}

// TestRecover_refuses_a_repeated_proposal: the same authentic line twice is not
// the same approval given twice.
func TestRecover_refuses_a_repeated_proposal(t *testing.T) {
	r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}, {"src/b.txt", "two\n"}})
	path, lines := journalLines(t, r)
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(append(lines, lines[0]), "\n")+"\n"), 0o600))
	_, err := recoverRun(t, r)
	assert.ErrorContains(t, err, "repeats proposal")
	branchUntouched(t, r)
}

// TestRecover_refuses_to_certify_a_prefix: an approval the audit chain records
// and the journal lacks (or holds with other bytes) cannot be recovered, and a
// recovery without it would present less than the run approved as the whole.
func TestRecover_refuses_to_certify_a_prefix(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}, {"src/b.txt", "two\n"}})
		path, lines := journalLines(t, r)
		require.NoError(t, os.WriteFile(path, []byte(lines[0]+"\n"), 0o600)) // the second approval's line is gone
		_, err := recoverRun(t, r)
		assert.ErrorContains(t, err, "the journal does not have it")
		branchUntouched(t, r)
	})
	t.Run("other bytes", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}, {"src/b.txt", "two\n"}})
		path, lines := journalLines(t, r)
		var e map[string]any
		require.NoError(t, json.Unmarshal([]byte(lines[1]), &e))
		e["request"] = map[string]any{"type": "yield_request", "action_type": "file_edit", "proposed_edits": []map[string]string{
			{"file": "src/evil.txt", "search_block": "(new file)", "replace_block": "evil\n"}}}
		forged, _ := json.Marshal(e)
		require.NoError(t, os.WriteFile(path, []byte(lines[0]+"\n"+string(forged)+"\n"), 0o600))
		_, err := recoverRun(t, r)
		assert.ErrorContains(t, err, "not the one the audit chain recorded")
		branchUntouched(t, r)
	})
}

// TestRecover_journal_corruption_and_torn_tails: a crash cuts the last append,
// which was never audited and so never released: earlier approvals still recover.
// A cut line with entries after it, or a journal that cannot be read, is not that.
func TestRecover_journal_corruption_and_torn_tails(t *testing.T) {
	t.Run("a torn unaudited tail", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}, {"src/b.txt", "two\n"}})
		path, lines := journalLines(t, r)
		torn := strings.Join(lines, "\n") + "\n" + `{"seq":3,"source":"operator","requ` // no newline: the append was cut
		require.NoError(t, os.WriteFile(path, []byte(torn), 0o600))
		res, err := recoverRun(t, r)
		require.NoError(t, err)
		assert.Equal(t, 2, res.Proposals)
	})
	t.Run("a cut line with entries after it", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}, {"src/b.txt", "two\n"}})
		path, lines := journalLines(t, r)
		require.NoError(t, os.WriteFile(path, []byte(lines[0][:len(lines[0])/2]+"\n"+lines[1]+"\n"), 0o600))
		_, err := recoverRun(t, r)
		assert.ErrorContains(t, err, "corrupt")
		branchUntouched(t, r)
	})
	t.Run("a journal that cannot be read", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}})
		path, _ := journalLines(t, r)
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Mkdir(path, 0o700)) // opening works, reading does not
		_, err := recoverRun(t, r)
		assert.ErrorContains(t, err, "cannot be trusted")
		assert.NotContains(t, err.Error(), "no journal")
		branchUntouched(t, r)
	})
	t.Run("an unaudited entry before later decisions", func(t *testing.T) {
		r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}, {"src/gone.txt", badEdit}, {"src/b.txt", "two\n"}})
		path, lines := journalLines(t, r)
		require.Len(t, lines, 2, "the refused proposal was not journaled")
		var e map[string]any
		require.NoError(t, json.Unmarshal([]byte(lines[0]), &e))
		e["seq"] = 2.0 // numbered as the proposal the chain refused
		forged, _ := json.Marshal(e)
		require.NoError(t, os.WriteFile(path, []byte(lines[0]+"\n"+string(forged)+"\n"+lines[1]+"\n"), 0o600))
		_, err := recoverRun(t, r)
		assert.ErrorContains(t, err, "did not approve")
		branchUntouched(t, r)
	})
}

// TestAuditedHistory_refuses_what_no_run_can_have_written: the hashes of these
// histories are right (they are written through the store); their order is not.
func TestAuditedHistory_refuses_what_no_run_can_have_written(t *testing.T) {
	ev := func(typ string, payload map[string]any) domain.RunEventLog {
		b, _ := json.Marshal(payload)
		return domain.RunEventLog{EventType: typ, Payload: string(b)}
	}
	bound := ev("run_bound", map[string]any{"base_sha": "b", "branch": "refs/heads/x"})
	decided := func(seq int, approved bool) domain.RunEventLog {
		return ev("yield_decided", map[string]any{"seq": seq, "approved": approved, "action_type": domain.ActionFileEdit, "request_sha256": "h"})
	}
	good := []domain.RunEventLog{bound, ev("yield_request", nil), decided(1, true), ev("yield_request", nil), decided(2, false), decided(3, true)}
	approved, last, err := orchestrator.ExportedAuditedHistory(good)
	require.NoError(t, err, "an interrupted run's history is recoverable")
	assert.Equal(t, []int{1, 3}, approved)
	assert.Equal(t, 3, last)

	for name, events := range map[string][]domain.RunEventLog{
		"a decision before the run began":    {decided(1, true), bound},
		"two bindings":                       {bound, bound},
		"proposal numbers that do not grow":  {bound, decided(2, true), decided(2, true)},
		"numbers going backwards":            {bound, decided(3, true), decided(1, true)},
		"a decision after the certificate":   {bound, decided(1, true), ev("certificate_issued", nil), decided(2, true)},
		"a decision after a recovery":        {bound, decided(1, true), ev("run_recovered", nil), decided(2, true)},
		"an unreadable decision":             {bound, {EventType: "yield_decided", Payload: "not json"}},
		"a history that is not bound at all": {ev("yield_request", nil), bound},
	} {
		_, _, err := orchestrator.ExportedAuditedHistory(events)
		assert.Error(t, err, name)
	}
}
