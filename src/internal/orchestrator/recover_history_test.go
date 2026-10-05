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

// TestAuditedHistory_refuses_what_no_run_can_have_written is a unit test of the pure
// history check: its events are bare (no hashes, nothing in a store), so it shows what
// the check refuses and nothing about hash-valid chains. Real-store, hash-valid histories
// are refused (and legitimate ones recovered) in recover_semantics_test.go and
// TestRecover_* above, which run Recover on a run that was really made.
func TestAuditedHistory_refuses_what_no_run_can_have_written(t *testing.T) {
	ev := func(typ string, payload map[string]any) domain.RunEventLog {
		b, _ := json.Marshal(payload)
		return domain.RunEventLog{EventType: typ, Payload: string(b)}
	}
	bound := ev("run_bound", map[string]any{"base_sha": "b", "branch": "refs/heads/x"})
	decided := func(seq int, approved bool) domain.RunEventLog {
		return ev("yield_decided", map[string]any{"seq": seq, "approved": approved, "action_type": domain.ActionFileEdit, "request_sha256": "h"})
	}
	request := ev("yield_request", map[string]any{"action_type": domain.ActionFileEdit})
	good := []domain.RunEventLog{bound, request, request, decided(1, true), decided(2, false), request, decided(3, true)} // requests may be audited ahead of their decisions
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
		"a history that is not bound at all": {request, bound},
		"a decision with no request":         {bound, decided(1, true)},
		"more decisions than requests":       {bound, request, decided(1, true), decided(2, true)},
		"a request of another kind":          {bound, ev("yield_request", map[string]any{"action_type": domain.ActionShellExec}), decided(1, true)},
	} {
		_, _, err := orchestrator.ExportedAuditedHistory(events)
		assert.Error(t, err, name)
	}
}

// TestAuditedHistory_names_the_policy_and_the_initiator: what a run recorded about
// the policy it loaded and who started it is what a recovered certificate names.
func TestAuditedHistory_names_the_policy_and_the_initiator(t *testing.T) {
	ev := func(typ string, payload map[string]any) domain.RunEventLog {
		b, _ := json.Marshal(payload)
		return domain.RunEventLog{EventType: typ, Payload: string(b)}
	}
	policy, who, sig, err := orchestrator.ExportedRunContext([]domain.RunEventLog{
		ev("run_bound", map[string]any{"base_sha": "b", "branch": "x"}),
		ev("initiator_signed", map[string]any{"principal": "dev@example.com", "signature": "c2ln"}),
		ev("policy_snapshot", map[string]any{"digest": "abc123", "signed": true}),
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"abc123", "dev@example.com", "c2ln"}, []string{policy, who, sig})

	policy, who, _, err = orchestrator.ExportedRunContext([]domain.RunEventLog{
		ev("run_bound", map[string]any{"base_sha": "b", "branch": "x"}),
		ev("initiator_signed", map[string]any{"principal": "old@example.com"}), // a record from before the signature was kept
	})
	require.NoError(t, err)
	assert.Empty(t, policy)
	assert.Empty(t, who, "without a signature there is nothing to carry into a certificate")
}

// TestRecover_only_a_cut_append_is_a_torn_tail: appendJournal writes a line and its
// newline in one write, so a crash leaves a line that stops short of its newline
// and is not complete JSON. A last record that is complete (terminated, or valid
// JSON) but not an entry is corruption, whatever it looks like: it is refused,
// no ref moves, and nothing is committed.
func TestRecover_only_a_cut_append_is_a_torn_tail(t *testing.T) {
	cases := []struct {
		name   string
		tail   string // appended after the two journaled lines
		recovs bool
	}{
		{"cut in the middle of the line, no newline", `{"seq":3,"source":"operator","requ`, true},
		{"cut right after the opening brace", `{`, true},
		{"a complete {} with no newline", `{}`, false},
		{"a complete null with no newline", `null`, false},
		{"a complete {} with its newline", "{}\n", false},
		{"null with its newline", "null\n", false},
		{"a non-positive sequence number", `{"seq":0,"source":"operator","request":{}}` + "\n", false},
		{"a negative sequence number", `{"seq":-4,"source":"operator","request":{}}` + "\n", false},
		{"garbage that ends in a newline", "not json at all\n", false},
		{"a cut line that ends in a newline", `{"seq":3,"source":"operator","requ` + "\n", false},
		{"a complete entry with no request", `{"seq":3,"source":"operator"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}, {"src/b.txt", "two\n"}})
			path, lines := journalLines(t, r)
			require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"+c.tail), 0o600))
			res, err := recoverRun(t, r)
			if !c.recovs {
				require.Error(t, err, "a complete record that is not an entry was taken for a torn tail: %+v", res)
				assert.Contains(t, err.Error(), "corrupt")
				branchUntouched(t, r)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 2, res.Proposals, "every approval the chain holds is recovered")
			assert.Equal(t, "one", strings.TrimSpace(git(t, r.Repo, "show", "staircase/run-1:src/a.txt")))
			assert.Equal(t, "two", strings.TrimSpace(git(t, r.Repo, "show", "staircase/run-1:src/b.txt")))
			assert.Equal(t, "src/a.txt\nsrc/b.txt", strings.TrimSpace(git(t, r.Repo, "diff", "--name-only", "HEAD", "staircase/run-1")), "exactly the approved files")
		})
	}
}

// TestRecover_a_cut_line_cannot_hide_an_acknowledged_approval: a line that really is
// cut short is tolerated only while it was never audited. When the chain says the
// approval was acknowledged, the cut line is a loss and recovery refuses, committing nothing.
func TestRecover_a_cut_line_cannot_hide_an_acknowledged_approval(t *testing.T) {
	r := interruptedWith(t, [][2]string{{"src/a.txt", "one\n"}, {"src/b.txt", "two\n"}})
	path, lines := journalLines(t, r)
	require.NoError(t, os.WriteFile(path, []byte(lines[0]+"\n"+lines[1][:len(lines[1])/2]), 0o600))
	_, err := recoverRun(t, r)
	assert.ErrorContains(t, err, "the journal does not have it")
	branchUntouched(t, r)
}
