package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedRunWithDecisions creates a minimal DB fixture: vendor → project → topology
// → case → run, then appends n yield_decided events. Returns the run ID.
func seedRunWithDecisions(t *testing.T, store *persistence.Store, n int) int64 {
	t.Helper()
	vendor, err := store.CreateVendor("test-vendor")
	require.NoError(t, err)
	proj, err := store.CreateProject(vendor.ID, "test-project", "/tmp/repo")
	require.NoError(t, err)
	_, err = store.CreateSwarmTopology(proj.ID, "supervisor", "memory", "langgraph")
	require.NoError(t, err)
	c, err := store.CreateCase(proj.ID)
	require.NoError(t, err)
	run, err := store.CreateRun(c.ID, 1, "main")
	require.NoError(t, err)

	prevHash := ""
	for i := range n {
		payload, _ := json.Marshal(map[string]any{
			"type":        "yield_decided",
			"source":      "operator",
			"agent":       "coder",
			"action_type": "file_edit",
			"approved":    i%2 == 0,
			"feedback":    "",
		})
		entry, err := store.AppendEventLog(run.ID, "yield_decided", string(payload), prevHash, "")
		require.NoError(t, err)
		prevHash = entry.EventHash
	}
	return run.ID
}

func TestReplay_prints_yield_decisions(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	runID := seedRunWithDecisions(t, store, 3)

	var buf bytes.Buffer
	err = runReplay(&buf, store, runID)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Run #")
	assert.Contains(t, out, "coder", "agent name must appear in output")
	assert.Contains(t, out, "file_edit", "action type must appear")
	assert.Contains(t, out, "3 yield decision(s) replayed", "decision count must be reported")
	assert.Contains(t, out, "Audit chain intact", "chain verification message must appear")
}

func TestReplay_no_decisions_shows_empty_message(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	runID := seedRunWithDecisions(t, store, 0)

	var buf bytes.Buffer
	err = runReplay(&buf, store, runID)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "No yield decisions recorded")
}

func TestReplay_tampered_chain_returns_error(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	runID := seedRunWithDecisions(t, store, 2)

	// Corrupt the chain by overwriting the first entry's hash.
	_, err = db.Exec(
		`UPDATE run_event_logs SET event_hash = 'badhash'
		 WHERE id = (SELECT id FROM run_event_logs WHERE run_id = ? ORDER BY id LIMIT 1)`,
		runID,
	)
	require.NoError(t, err)

	var buf bytes.Buffer
	err = runReplay(&buf, store, runID)
	assert.Error(t, err, "tampered run must return an error")
	assert.Contains(t, err.Error(), "audit chain verification failed")
}

func TestReplay_run_not_found_returns_error(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := persistence.NewStore(db)

	var buf bytes.Buffer
	err = runReplay(&buf, store, 9999)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "9999") || strings.Contains(err.Error(), "not found"))
}
