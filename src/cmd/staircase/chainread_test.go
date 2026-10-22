package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReplay_refuses_a_run_whose_chain_was_altered: replay verifies and reads in one step, and names the run and the entry.
func TestReplay_refuses_a_run_whose_chain_was_altered(t *testing.T) {
	db, err := persistence.InitDB(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	s := persistence.NewStore(db)
	v, _ := s.CreateVendor("V")
	p, _ := s.CreateProject(v.ID, "P", "")
	c, _ := s.CreateCase(p.ID)
	_, err = s.CreateSwarmTopology(p.ID, "sup", "memory", "langgraph")
	require.NoError(t, err)
	run, err := s.CreateRun(c.ID, 1, "main")
	require.NoError(t, err)
	_, err = s.AppendEventLogChained(run.ID, "yield_decided", `{"seq":1,"source":"operator","agent":"coder","action_type":"file_edit","approved":false}`, "")
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, runReplay(&out, s, run.ID))
	assert.Contains(t, out.String(), "REJECTED")

	_, err = db.Exec(`UPDATE run_event_logs SET payload = replace(payload, '"approved":false', '"approved":true') WHERE run_id = ?`, run.ID)
	require.NoError(t, err)
	out.Reset()
	err = runReplay(&out, s, run.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit chain verification failed")
	assert.Contains(t, err.Error(), "yield_decided")
	assert.NotContains(t, out.String(), "APPROVED", "nothing of a tampered run is replayed")
}

// TestCommands_read_the_chain_through_history (ADR 0014): a command that shows a chain reads its rows with history.ReadRows, so what
// it shows and what it says of it are one read. None lists raw rows itself, and none hashes entries by itself.
func TestCommands_read_the_chain_through_history(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.NotContains(t, string(b), ".ListEventLogs(", f)
		assert.NotContains(t, string(b), "chain.Expected(", f)
	}
}
