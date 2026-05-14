package gate

import (
	"fmt"

	"github.com/b070nd/staircase-core/src/internal/persistence"
)

func init() {
	Register(&caseProjectExistsGate{})
	Register(&caseHasStoriesGate{})
	Register(&caseHasPRDGate{})
	Register(&topologyExistsGate{})
	Register(&topologyHasAgentsGate{})
	Register(&topologySupervisorRegisteredGate{})
	Register(&topologyEdgesValidGate{})
	Register(&topologyNoOrphanAgentsGate{})
	Register(&topologyRuntimeValidGate{})
}

// ─── case.project_exists ──────────────────────────────────────────────────────

type caseProjectExistsGate struct{}

func (*caseProjectExistsGate) Name() string       { return "case.project_exists" }
func (*caseProjectExistsGate) Category() string   { return "structural" }
func (*caseProjectExistsGate) Severity() Severity { return SeverityBlock }

func (*caseProjectExistsGate) Run(ctx Context) Result {
	const name = "case.project_exists"
	c, err := ctx.Store.GetCase(ctx.CaseID)
	if err != nil {
		return fail(name, "structural", SeverityBlock, "store error: "+err.Error())
	}
	if c == nil {
		return fail(name, "structural", SeverityBlock, fmt.Sprintf("case %d not found", ctx.CaseID))
	}
	if c.DeletedAt != nil {
		return fail(name, "structural", SeverityBlock,
			fmt.Sprintf("case %d has been deleted (soft-deleted at %s)", ctx.CaseID, c.DeletedAt.Format("2006-01-02")))
	}
	p, err := ctx.Store.GetProject(c.ProjectID)
	if err != nil || p == nil {
		return fail(name, "structural", SeverityBlock, fmt.Sprintf("project %d not found", c.ProjectID))
	}
	return pass(name, "structural", SeverityBlock,
		fmt.Sprintf("case %d → project %q (id=%d)", ctx.CaseID, p.Name, p.ID))
}

// ─── case.has_stories ─────────────────────────────────────────────────────────

type caseHasStoriesGate struct{}

func (*caseHasStoriesGate) Name() string       { return "case.has_stories" }
func (*caseHasStoriesGate) Category() string   { return "structural" }
func (*caseHasStoriesGate) Severity() Severity { return SeverityBlock }

func (*caseHasStoriesGate) Run(ctx Context) Result {
	const name = "case.has_stories"
	stories, err := ctx.Store.ListUserStoriesByCase(ctx.CaseID)
	if err != nil {
		return fail(name, "structural", SeverityBlock, "store error: "+err.Error())
	}
	pending := 0
	for _, s := range stories {
		if s.Status == persistence.StoryStatusPending {
			pending++
		}
	}
	if pending == 0 {
		return fail(name, "structural", SeverityBlock,
			fmt.Sprintf("no PENDING user stories — use 'staircase story add %d <desc>'", ctx.CaseID))
	}
	return pass(name, "structural", SeverityBlock, fmt.Sprintf("%d PENDING user stories", pending))
}

// ─── case.has_prd ─────────────────────────────────────────────────────────────

type caseHasPRDGate struct{}

func (*caseHasPRDGate) Name() string       { return "case.has_prd" }
func (*caseHasPRDGate) Category() string   { return "structural" }
func (*caseHasPRDGate) Severity() Severity { return SeverityWarn }

func (*caseHasPRDGate) Run(ctx Context) Result {
	const name = "case.has_prd"
	c, err := ctx.Store.GetCase(ctx.CaseID)
	if err != nil || c == nil {
		return skip(name, "structural", SeverityWarn, "case not found")
	}
	if c.PrdJSON == "" {
		return warn(name, "structural",
			"no PRD context — agents run without product requirements; use 'staircase case set-prd'")
	}
	return pass(name, "structural", SeverityWarn, "PRD context present")
}

// ─── topology.exists ──────────────────────────────────────────────────────────

type topologyExistsGate struct{}

func (*topologyExistsGate) Name() string       { return "topology.exists" }
func (*topologyExistsGate) Category() string   { return "structural" }
func (*topologyExistsGate) Severity() Severity { return SeverityBlock }

func (*topologyExistsGate) Run(ctx Context) Result {
	const name = "topology.exists"
	c, err := ctx.Store.GetCase(ctx.CaseID)
	if err != nil || c == nil {
		return skip(name, "structural", SeverityBlock, "case not found")
	}
	topo, err := ctx.Store.GetLatestTopology(c.ProjectID)
	if err != nil {
		return fail(name, "structural", SeverityBlock, "store error: "+err.Error())
	}
	if topo == nil {
		return fail(name, "structural", SeverityBlock,
			"no topology — use 'staircase topology register'")
	}
	return pass(name, "structural", SeverityBlock,
		fmt.Sprintf("topology v%d (id=%d, supervisor=%q)", topo.Version, topo.ID, topo.SupervisorName))
}

// ─── topology.has_agents ──────────────────────────────────────────────────────

type topologyHasAgentsGate struct{}

func (*topologyHasAgentsGate) Name() string       { return "topology.has_agents" }
func (*topologyHasAgentsGate) Category() string   { return "structural" }
func (*topologyHasAgentsGate) Severity() Severity { return SeverityBlock }

func (*topologyHasAgentsGate) Run(ctx Context) Result {
	const name = "topology.has_agents"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	if c == nil {
		return skip(name, "structural", SeverityBlock, "case not found")
	}
	topo, _ := ctx.Store.GetLatestTopology(c.ProjectID)
	if topo == nil {
		return skip(name, "structural", SeverityBlock, "no topology")
	}
	agents, err := ctx.Store.ListAgentNodes(topo.ID)
	if err != nil {
		return fail(name, "structural", SeverityBlock, "store error: "+err.Error())
	}
	if len(agents) == 0 {
		return fail(name, "structural", SeverityBlock,
			"topology has no agents — use 'staircase topology agent add'")
	}
	return pass(name, "structural", SeverityBlock, fmt.Sprintf("%d agent nodes", len(agents)))
}

// ─── topology.supervisor_registered ──────────────────────────────────────────

type topologySupervisorRegisteredGate struct{}

func (*topologySupervisorRegisteredGate) Name() string       { return "topology.supervisor_registered" }
func (*topologySupervisorRegisteredGate) Category() string   { return "structural" }
func (*topologySupervisorRegisteredGate) Severity() Severity { return SeverityBlock }

func (*topologySupervisorRegisteredGate) Run(ctx Context) Result {
	const name = "topology.supervisor_registered"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	if c == nil {
		return skip(name, "structural", SeverityBlock, "case not found")
	}
	topo, _ := ctx.Store.GetLatestTopology(c.ProjectID)
	if topo == nil {
		return skip(name, "structural", SeverityBlock, "no topology")
	}
	agents, _ := ctx.Store.ListAgentNodes(topo.ID)
	for _, a := range agents {
		if a.Name == topo.SupervisorName {
			return pass(name, "structural", SeverityBlock,
				fmt.Sprintf("supervisor %q is a registered agent node", topo.SupervisorName))
		}
	}
	return fail(name, "structural", SeverityBlock,
		fmt.Sprintf("supervisor %q not registered as an agent node", topo.SupervisorName))
}

// ─── topology.edges_valid ─────────────────────────────────────────────────────

type topologyEdgesValidGate struct{}

func (*topologyEdgesValidGate) Name() string       { return "topology.edges_valid" }
func (*topologyEdgesValidGate) Category() string   { return "structural" }
func (*topologyEdgesValidGate) Severity() Severity { return SeverityBlock }

func (*topologyEdgesValidGate) Run(ctx Context) Result {
	const name = "topology.edges_valid"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	if c == nil {
		return skip(name, "structural", SeverityBlock, "case not found")
	}
	topo, _ := ctx.Store.GetLatestTopology(c.ProjectID)
	if topo == nil {
		return skip(name, "structural", SeverityBlock, "no topology")
	}
	agents, _ := ctx.Store.ListAgentNodes(topo.ID)
	known := map[string]bool{"END": true, "__end__": true}
	for _, a := range agents {
		known[a.Name] = true
	}
	edges, err := ctx.Store.ListEdges(topo.ID)
	if err != nil {
		return fail(name, "structural", SeverityBlock, "store error: "+err.Error())
	}
	var bad []string
	for _, e := range edges {
		if !known[e.FromNode] {
			bad = append(bad, fmt.Sprintf("from=%q (edge id=%d)", e.FromNode, e.ID))
		}
		if !known[e.ToNode] {
			bad = append(bad, fmt.Sprintf("to=%q (edge id=%d)", e.ToNode, e.ID))
		}
	}
	if len(bad) > 0 {
		return fail(name, "structural", SeverityBlock,
			fmt.Sprintf("edges reference unknown nodes: %v", bad))
	}
	return pass(name, "structural", SeverityBlock,
		fmt.Sprintf("%d edges all reference registered nodes", len(edges)))
}

// ─── topology.runtime_valid ───────────────────────────────────────────────────
//
// Three tiers:
//   - implementedRuntimes  – langgraph is fully executable today → PASS
//   - recognisedRuntimes   – crewai/autogen are spec'd for a future phase;
//                            registered in the schema but the code-generator
//                            raises NotImplementedError at runtime → WARN so
//                            the operator knows before wasting a run
//   - anything else        → BLOCK (truly unknown)

var implementedRuntimes = map[string]bool{"langgraph": true}

// recognisedRuntimes are accepted by the schema CHECK constraint and will be
// implemented in a future release, but the Python harness currently raises
// NotImplementedError for them. Allowing them silently would be a false
// affordance — we emit a WARN instead so the operator sees it before running.
var recognisedRuntimes = map[string]bool{"crewai": true, "autogen": true}

type topologyRuntimeValidGate struct{}

func (*topologyRuntimeValidGate) Name() string       { return "topology.runtime_valid" }
func (*topologyRuntimeValidGate) Category() string   { return "structural" }
func (*topologyRuntimeValidGate) Severity() Severity { return SeverityBlock }

func (*topologyRuntimeValidGate) Run(ctx Context) Result {
	const name = "topology.runtime_valid"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	if c == nil {
		return skip(name, "structural", SeverityBlock, "case not found")
	}
	t, _ := ctx.Store.GetLatestTopology(c.ProjectID)
	if t == nil {
		return skip(name, "structural", SeverityBlock, "no topology")
	}
	if implementedRuntimes[t.RuntimeType] {
		return pass(name, "structural", SeverityBlock,
			fmt.Sprintf("runtime_type=%q", t.RuntimeType))
	}
	if recognisedRuntimes[t.RuntimeType] {
		return warn(name, "structural",
			fmt.Sprintf("runtime_type=%q is registered but not yet executable — "+
				"the code generator will raise NotImplementedError at run time; "+
				"only 'langgraph' is currently supported", t.RuntimeType))
	}
	return fail(name, "structural", SeverityBlock,
		fmt.Sprintf("unknown runtime_type %q — valid values: langgraph (implemented), crewai/autogen (future)", t.RuntimeType))
}

// ─── topology.no_orphan_agents ────────────────────────────────────────────────

type topologyNoOrphanAgentsGate struct{}

func (*topologyNoOrphanAgentsGate) Name() string       { return "topology.no_orphan_agents" }
func (*topologyNoOrphanAgentsGate) Category() string   { return "structural" }
func (*topologyNoOrphanAgentsGate) Severity() Severity { return SeverityWarn }

func (*topologyNoOrphanAgentsGate) Run(ctx Context) Result {
	const name = "topology.no_orphan_agents"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	if c == nil {
		return skip(name, "structural", SeverityWarn, "case not found")
	}
	topo, _ := ctx.Store.GetLatestTopology(c.ProjectID)
	if topo == nil {
		return skip(name, "structural", SeverityWarn, "no topology")
	}
	agents, _ := ctx.Store.ListAgentNodes(topo.ID)
	edges, _ := ctx.Store.ListEdges(topo.ID)
	connected := map[string]bool{}
	for _, e := range edges {
		connected[e.FromNode] = true
		connected[e.ToNode] = true
	}
	var orphans []string
	for _, a := range agents {
		if !connected[a.Name] {
			orphans = append(orphans, a.Name)
		}
	}
	if len(orphans) > 0 {
		return warn(name, "structural",
			fmt.Sprintf("agents not connected by any edge (will never execute): %v", orphans))
	}
	return pass(name, "structural", SeverityWarn, "all agents are connected")
}
