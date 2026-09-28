package orchestrator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRun_records_plan_provenance: the first chain event names the plan the
// run executed (digest, blueprint, topology version), not the latest topology.
func TestRun_records_plan_provenance(t *testing.T) {
	r := runtest.Run(t, runtest.Options{Agent: idle,
		Setup: func(s *persistence.Store, _ string, projectID int64) { // latest is now v2
			_, err := s.CreateSwarmTopology(projectID, "other", "memory", "langgraph")
			require.NoError(t, err)
		},
		Run: orchestrator.RunOptions{Plan: &plan.Plan{TopologyVersion: 1, Digest: "d1g35t", BlueprintHash: "b1u3"}}})
	require.NoError(t, r.Err)
	assert.Equal(t, 1, r.Run.TopologyVersion)
	require.NotEmpty(t, r.Events)
	assert.Equal(t, "run_bound", r.Events[0].EventType)
	var bound map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.Events[0].Payload), &bound))
	assert.Equal(t, "d1g35t", bound["plan_digest"])
	assert.Equal(t, "b1u3", bound["blueprint_hash"])
	assert.EqualValues(t, 1, bound["topology_version"])
}

// TestRun_harness_plan_needs_no_topology: a case run by an agent harness has
// no topology; the run still starts, records the harness as provenance, and
// commits what was approved.
func TestRun_harness_plan_needs_no_topology(t *testing.T) {
	r := runtest.Run(t, runtest.Options{NoTopology: true,
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			ap := env.ProposeEdit(ctx, "claude-code", "health check",
				domain.ProposedEdit{File: "health.txt", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "ok\n"})
			return ap.Apply(env.Worktree)
		}),
		Run: orchestrator.RunOptions{Plan: &plan.Plan{Harness: "claude-code", Digest: "d1g35t"}}})
	require.NoError(t, r.Err)
	assert.Equal(t, persistence.RunStatusSuccess, r.Run.Status)
	assert.Equal(t, 0, r.Run.TopologyVersion, "0: no topology")
	var bound map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.Events[0].Payload), &bound))
	assert.Equal(t, "claude-code", bound["harness"])
	got, err := r.OnBranch("health.txt")
	require.NoError(t, err)
	assert.Equal(t, "ok\n", got)
}
