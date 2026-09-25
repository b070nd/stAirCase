package gate_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/blueprint"
	"github.com/b070nd/staircase-core/src/internal/domain"
	"github.com/b070nd/staircase-core/src/internal/gate"
	"github.com/b070nd/staircase-core/src/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var hello = blueprint.Blueprint{Name: "hello", Supervisor: "sup",
	Agents: []blueprint.Agent{{Name: "sup", Model: "claude-sonnet-4-6", Prompt: "Route."}, {Name: "coder", Model: "claude-sonnet-4-6", Prompt: "Code."}},
	Edges:  []blueprint.Edge{{From: "sup", To: "coder"}},
	Cases: []blueprint.Case{{Slug: "greet", PRD: "Say hello.",
		Stories: []blueprint.Story{{Text: "greeting", Scope: blueprint.Scope{Allow: []string{"GREETING.md"}}}}}}}

// pinnedPlan is what compile writes for the bound case.
func pinnedPlan(caseID int64, topo int) plan.Plan {
	return plan.Plan{CaseID: caseID, TopologyVersion: topo, BlueprintHash: hello.Hash(), Supervisor: "sup", PRD: "Say hello.",
		Agents:  []plan.Agent{{Name: "sup", Role: "Route.", Model: "claude-sonnet-4-6"}, {Name: "coder", Role: "Code.", Model: "claude-sonnet-4-6"}},
		Edges:   []plan.Edge{{From: "sup", To: "coder"}},
		Stories: []plan.Story{{ID: 1, Text: "greeting", Allow: []string{"GREETING.md"}}}}
}

// TestRuntimePlanPinnedGate: a case bound to a blueprint runs only a plan
// that is exactly that blueprint's; changes made through the imperative CLI
// after binding block the run.
func TestRuntimePlanPinnedGate(t *testing.T) {
	ctx, wsDir := newGateEnv(t)
	pID, unbound := makeCase(t, ctx.Store)
	_, err := ctx.Store.ImportBlueprint(domain.Blueprint{Hash: hello.Hash(), Name: hello.Name, Content: string(hello.JSON())})
	require.NoError(t, err)
	topo, cases, err := ctx.Store.BindBlueprint(pID, hello.Binding())
	require.NoError(t, err)
	bound := cases[0]
	write := func(p plan.Plan) {
		require.NoError(t, os.MkdirAll(filepath.Join(wsDir, "tmp"), 0o755))
		require.NoError(t, plan.Write(filepath.Join(wsDir, "tmp", fmt.Sprintf("plan_case%d.json", p.CaseID)), p))
	}
	run := func(caseID int64) gate.Result {
		ctx.CaseID = caseID
		return gate.RuntimePlanPinnedGate.Run(ctx)
	}

	assert.Equal(t, gate.StatusPass, run(unbound).Status, "cases made by hand are not pinned")

	write(pinnedPlan(bound, topo))
	r := run(bound)
	assert.Equal(t, gate.StatusPass, r.Status, r.Message)

	drifted := pinnedPlan(bound, topo)
	drifted.Agents = append(drifted.Agents, plan.Agent{Name: "tester", Role: "x", Model: "claude-sonnet-4-6"})
	write(drifted)
	r = run(bound)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Equal(t, gate.SeverityBlock, r.Severity)
	assert.Contains(t, r.Message, "re-bind")

	unpinned := pinnedPlan(bound, topo)
	unpinned.BlueprintHash = ""
	write(unpinned)
	r = run(bound)
	assert.Equal(t, gate.StatusFail, r.Status)
	assert.Contains(t, r.Message, "recompile")
}
