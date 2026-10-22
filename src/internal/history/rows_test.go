package history_test

import (
	"testing"

	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rowsStore is a store with a run whose chain holds five entries.
func rowsStore(t *testing.T) (*persistence.Store, int64) {
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
	for _, typ := range []string{"run_bound", "yield_request", "yield_decided", "phase_checked", "run_path"} {
		_, err := s.AppendEventLogChained(run.ID, typ, `{"type":"`+typ+`"}`, "")
		require.NoError(t, err)
	}
	return s, run.ID
}

// TestReadRows: a chain is shown as stored with each entry judged on its own, so a tampered one is marked and shown, not refused.
func TestReadRows_marks_what_is_broken_and_shows_it(t *testing.T) {
	s, runID := rowsStore(t)
	rows, err := history.ReadRows(s, runID)
	require.NoError(t, err)
	assert.Len(t, rows.Entries, 5)
	assert.True(t, rows.Intact())
	assert.NoError(t, rows.First())

	_, err = s.DB().Exec(`UPDATE run_event_logs SET payload = '{"type":"yield_decided","approved":true}' WHERE run_id = ? AND event_type = 'yield_decided'`, runID)
	require.NoError(t, err)
	rows, err = history.ReadRows(s, runID)
	require.NoError(t, err, "a tampered chain can be shown")
	assert.Len(t, rows.Entries, 5)
	assert.False(t, rows.Intact())
	require.Error(t, rows.First())
	assert.Contains(t, rows.First().Error(), "yield_decided")
	for i, e := range rows.Broken {
		if i == 2 {
			assert.Error(t, e, "the tampered entry")
		} else {
			assert.NoError(t, e, "entry %d is judged on its own", i+1)
		}
	}
	_, err = history.Read(s, runID)
	require.Error(t, err, "but nothing is decided from it")
}
