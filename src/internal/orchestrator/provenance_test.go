package orchestrator_test

import (
	"encoding/json"
	"testing"

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
