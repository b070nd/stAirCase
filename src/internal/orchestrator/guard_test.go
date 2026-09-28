package orchestrator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGuards_send_risky_changes_to_a_person: a policy approves file edits
// automatically, but a change that adds hidden Unicode, changes dependencies
// or writes a secret goes to a person, with the reason; an ordinary change
// and something already in the file are left to the policy.
func TestGuards_send_risky_changes_to_a_person(t *testing.T) {
	base := map[string]runtest.File{
		"go.mod":    {Content: "module shop\n", Mode: 0o644},
		"legacy.go": {Content: "var s = \"\u202e\" // old\n", Mode: 0o644},
	}
	edits := []domain.ProposedEdit{
		{File: "notes.md", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "plain text\n"},
		{File: "admin.go", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "if isAdmin \u202e{ // }\n"},
		{File: "go.mod", SearchBlock: "module shop\n", ReplaceBlock: "module shop\n\nrequire example.com/x v1.0.0\n"},
		{File: "config.go", SearchBlock: orchestrator.MarkerNewFile, ReplaceBlock: "const key = \"AKIAABCDEFGHIJKLMNOP\"\n"},
		{File: "legacy.go", SearchBlock: "// old", ReplaceBlock: "// kept"},
	}
	agent := func(ctx context.Context, env *orchestrator.AgentEnv) error {
		for _, e := range edits {
			env.ProposeEdit(ctx, "coder", "r", e).Apply(env.Worktree)
		}
		return nil
	}
	r := runtest.Run(t, runtest.Options{Base: base, Agent: orchestrator.AgentFunc(agent),
		Setup: func(s *persistence.Store, _ string, projectID int64) {
			webhook(t, s, projectID, &operator{approve: true})
		}})
	require.NoError(t, r.Err)

	var got []struct{ Source, Guard string }
	for _, e := range r.Events {
		if e.EventType == "yield_decided" {
			var d struct{ Source, Guard string }
			require.NoError(t, json.Unmarshal([]byte(e.Payload), &d))
			got = append(got, d)
		}
	}
	require.Len(t, got, len(edits))
	assert.Equal(t, "policy", got[0].Source, "an ordinary change")
	assert.Equal(t, "operator", got[1].Source)
	assert.Contains(t, got[1].Guard, "hidden Unicode")
	assert.Equal(t, "operator", got[2].Source)
	assert.Contains(t, got[2].Guard, "dependencies")
	assert.Equal(t, "operator", got[3].Source)
	assert.Contains(t, got[3].Guard, "secret")
	assert.Equal(t, "policy", got[4].Source, "the hidden character was there before this change")
}
