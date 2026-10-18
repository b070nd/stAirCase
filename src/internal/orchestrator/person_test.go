package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inProcess is a Person in the test's own process: it answers every proposal it is asked, and keeps what it was asked.
type inProcess struct {
	approve bool
	mu      sync.Mutex
	asked   []domain.YieldRequest
}

func (p *inProcess) Ask(_ context.Context, req domain.YieldRequest, _ string) domain.YieldResponse {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, req)
	return domain.Decide(p.approve, "answered in process")
}

// TestPerson_a_run_can_ask_one_in_process: a run's person need not be an HTTP stand-in. Given in RunOptions, it is asked what
// nothing else decides, its answer is the operator's decision on the chain, and what it approved is committed.
func TestPerson_a_run_can_ask_one_in_process(t *testing.T) {
	who := &inProcess{approve: true}
	r := runtest.Run(t, runtest.Options{
		Setup: func(_ *persistence.Store, ws string, _ int64) { // no rule approves anything: a person decides
			require.NoError(t, os.WriteFile(filepath.Join(ws, "policy.json"), []byte(`{"rules":[]}`), 0o600))
		},
		Run:   orchestrator.RunOptions{Person: who},
		Agent: writeFile("health.txt", "ok\n")})
	require.NoError(t, r.Err)
	require.Len(t, who.asked, 1)
	assert.Equal(t, "health.txt", who.asked[0].ProposedEdits[0].File)
	assert.Equal(t, []string{"file_edit:operator"}, sources(t, r))
	assert.Equal(t, "ok", trim(git(t, r.Repo, "show", "staircase/run-1:health.txt")))
}
