package persistence_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChainVectors: the conformance vectors of docs/spec/audit-chain-vectors.json
// (also checked by audit_chain_vectors.py, which shares no code with this
// package) are decided as the file says.
func TestChainVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "spec", "audit-chain-vectors.json"))
	require.NoError(t, err)
	var vectors map[string]struct {
		Valid   bool                 `json:"valid"`
		Entries []domain.RunEventLog `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(raw, &vectors))
	require.NotEmpty(t, vectors)
	for name, v := range vectors {
		t.Run(name, func(t *testing.T) {
			err := persistence.VerifyEntries(v.Entries)
			if v.Valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// chainStore is a store with one run to write events to.
func chainStore(t *testing.T) (*persistence.Store, *sql.DB, int64) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := persistence.NewStore(db)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")
	c, _ := s.CreateCase(p.ID)
	_, err = s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	require.NoError(t, err)
	run, err := s.CreateRun(c.ID, 1, "main")
	require.NoError(t, err)
	return s, db, run.ID
}

// TestChain_covers_the_event_type: what an event is called is as much part of
// the record as what it says; relabelling one breaks the chain.
func TestChain_covers_the_event_type(t *testing.T) {
	s, db, run := chainStore(t)
	for _, e := range []string{"run_started", "yield_decided", "run_finished"} {
		_, err := s.AppendEventLogChained(run, e, `{"n":1}`, "")
		require.NoError(t, err)
	}
	require.NoError(t, s.VerifyChain(run))
	logs, err := s.ListEventLogs(run)
	require.NoError(t, err)
	for _, l := range logs {
		assert.Equal(t, 2, l.HashVersion, "new entries are written under chain version 2")
	}

	mustExec(t, db, `UPDATE run_event_logs SET event_type = 'validator_note' WHERE id = ?`, logs[1].ID)
	assert.Error(t, s.VerifyChain(run), "a relabelled event is detected")
}

// TestChain_old_entries_still_verify: a run written before version 2 keeps
// verifying, also when later entries of the same run are version 2, and an
// entry cannot be passed off as an older version, nor as one that is unknown.
func TestChain_old_entries_still_verify(t *testing.T) {
	s, db, run := chainStore(t)
	first := persistence.ComputeEventHash(`{"a":1}`, "", "")
	mustExec(t, db, `INSERT INTO run_event_logs (run_id, event_type, payload, timestamp, event_hash, git_commit_hash, hash_version)
		VALUES (?, 'run_started', '{"a":1}', ?, ?, '', 1)`, run, time.Now(), first)
	_, err := s.AppendEventLogChained(run, "yield_decided", `{"b":2}`, "")
	require.NoError(t, err)
	require.NoError(t, s.VerifyChain(run), "version 1 followed by version 2")

	logs, err := s.ListEventLogs(run)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, 1, logs[0].HashVersion)

	mustExec(t, db, `UPDATE run_event_logs SET hash_version = 1 WHERE id = ?`, logs[1].ID)
	assert.Error(t, s.VerifyChain(run), "a version 2 entry presented as version 1")
	mustExec(t, db, `UPDATE run_event_logs SET hash_version = 3 WHERE id = ?`, logs[1].ID)
	assert.ErrorContains(t, s.VerifyChain(run), "version 3")
}

// TestChain_appended_entry_is_the_documented_hash: the stored hash is the one
// docs/audit.md describes, which audit_chain_vectors.py computes on its own.
func TestChain_appended_entry_is_the_documented_hash(t *testing.T) {
	s, _, run := chainStore(t)
	_, err := s.AppendEventLogChained(run, "run_started", `{}`, "")
	require.NoError(t, err)
	logs, _ := s.ListEventLogs(run)
	assert.Equal(t, persistence.ComputeEventHashV2("run_started", `{}`, "", ""), logs[0].EventHash)
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	require.NoError(t, err)
}

// TestPrunableEventLogs_are_whole_runs: retention removes runs, never the front
// of a run's chain, which would leave a chain that can no longer be verified.
func TestPrunableEventLogs_are_whole_runs(t *testing.T) {
	s, db, a := chainStore(t)
	c, _ := s.GetRun(a)
	mk := func() int64 {
		r, err := s.CreateRun(c.CaseID, 1, "main")
		require.NoError(t, err)
		require.NoError(t, s.FinishRun(r.ID, persistence.RunStatusSuccess, time.Now(), "c"))
		return r.ID
	}
	require.NoError(t, s.FinishRun(a, persistence.RunStatusSuccess, time.Now(), "c"))
	b, cRun := mk(), mk()
	add := func(run int64, n int) {
		for i := 0; i < n; i++ {
			_, err := s.AppendEventLogChained(run, "state_emit", `{}`, "")
			require.NoError(t, err)
		}
	}
	add(a, 3)
	add(b, 3)
	add(cRun, 3)
	rowsOf := func(rows []domain.RunEventLog) map[int64]int {
		m := map[int64]int{}
		for _, r := range rows {
			m[r.RunID]++
		}
		return m
	}

	rows, err := s.PrunableEventLogs(5) // 9 rows, 4 over: the first run (3) fits, the second is cut in the middle
	require.NoError(t, err)
	assert.Equal(t, map[int64]int{a: 3}, rowsOf(rows))
	n, err := s.PruneEventLogsOfRuns([]int64{a})
	require.NoError(t, err)
	assert.EqualValues(t, 3, n)
	require.NoError(t, s.VerifyChain(b), "a run that was not pruned still verifies")
	require.NoError(t, s.VerifyChain(cRun))

	// a late entry on an old run (a story accepted afterwards) keeps that run whole
	add(b, 1)
	rows, err = s.PrunableEventLogs(4) // 7 rows, 3 over: b's last row is newer than the cut
	require.NoError(t, err)
	assert.Empty(t, rows)

	// a run that is still running is never pruned
	mustExec(t, db, `UPDATE runs SET status = 'RUNNING' WHERE id = ?`, b)
	rows, err = s.PrunableEventLogs(0)
	require.NoError(t, err)
	assert.Equal(t, map[int64]int{cRun: 3}, rowsOf(rows))
}
