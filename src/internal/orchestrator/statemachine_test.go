package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunMoves: the run's state machine starts in PRE_FLIGHT (or, for a later segment of a
// continued run, in RESUME), reaches every other phase from there, never returns to a start, and
// ends in BRANCH_RESTORE.
func TestRunMoves(t *testing.T) {
	moves := orchestrator.RunMoves()
	reached := map[orchestrator.RunPhase]bool{orchestrator.PhasePreFlight: true, orchestrator.PhaseResume: true}
	next := []orchestrator.RunPhase{orchestrator.PhasePreFlight, orchestrator.PhaseResume}
	for len(next) > 0 {
		p := next[0]
		next = next[1:]
		for _, q := range moves[p] {
			assert.NotEqual(t, orchestrator.PhasePreFlight, q, "nothing moves back to PRE_FLIGHT")
			assert.NotEqual(t, orchestrator.PhaseResume, q, "nothing moves back to RESUME")
			if !reached[q] {
				reached[q] = true
				next = append(next, q)
			}
		}
	}
	for p := range moves {
		assert.True(t, reached[p], "%s is reachable", p)
	}
	assert.Empty(t, moves[orchestrator.PhaseBranchRestore], "BRANCH_RESTORE is the end")
}

func runPath(t *testing.T, r runtest.Result) []string {
	for _, e := range r.Events {
		if e.EventType == "run_path" {
			var p struct{ Phases []string }
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &p))
			return p.Phases
		}
	}
	t.Fatal("no run_path event")
	return nil
}

// TestRun_records_its_path: every run's evidence shows the phases it went
// through: the full path for a run that got to work, whether it succeeded
// or its agent failed, and a short one when setup stops it.
func TestRun_records_its_path(t *testing.T) {
	full := []string{"PRE_FLIGHT", "BRANCH_CREATE", "AGENT_START", "AGENT_LOOP", "FINALIZE", "BRANCH_RESTORE"}

	ok := runtest.Run(t, runtest.Options{Agent: idle})
	require.NoError(t, ok.Err)
	assert.Equal(t, full, runPath(t, ok))

	failing := runtest.Run(t, runtest.Options{Agent: orchestrator.AgentFunc(
		func(context.Context, *orchestrator.AgentEnv) error { return errors.New("model unavailable") })})
	require.Error(t, failing.Err)
	assert.Equal(t, full, runPath(t, failing))

	stopped := runtest.Run(t, runtest.Options{Agent: idle, Setup: func(_ *persistence.Store, wsDir string, _ int64) {
		require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(`{"not json`), 0o600))
	}})
	require.Error(t, stopped.Err)
	assert.Equal(t, []string{"PRE_FLIGHT", "BRANCH_CREATE", "BRANCH_RESTORE"}, runPath(t, stopped))
}
