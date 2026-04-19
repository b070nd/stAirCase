package gate

import (
	"fmt"

	"github.com/b070nd/staircase-core/src/internal/engine"
)

func init() {
	Register(&depNoCycleGate{})
	Register(&depDepsCompletedGate{})
}

// ─── deps.no_cycle ────────────────────────────────────────────────────────────

type depNoCycleGate struct{}

func (*depNoCycleGate) Name() string       { return "deps.no_cycle" }
func (*depNoCycleGate) Category() string   { return "dependency" }
func (*depNoCycleGate) Severity() Severity { return SeverityBlock }

func (*depNoCycleGate) Run(ctx Context) Result {
	const name = "deps.no_cycle"
	projects, err := ctx.Store.ListAllProjects()
	if err != nil {
		return skip(name, "dependency", SeverityBlock, "cannot load projects: "+err.Error())
	}
	deps, err := ctx.Store.ListAllProjectDependencies()
	if err != nil {
		return skip(name, "dependency", SeverityBlock, "cannot load dependencies: "+err.Error())
	}
	if _, err := engine.TopoSort(projects, deps); err != nil {
		return fail(name, "dependency", SeverityBlock, err.Error())
	}
	return pass(name, "dependency", SeverityBlock,
		fmt.Sprintf("dependency graph is acyclic (%d projects, %d edges)", len(projects), len(deps)))
}

// ─── deps.deps_completed ─────────────────────────────────────────────────────

type depDepsCompletedGate struct{}

func (*depDepsCompletedGate) Name() string       { return "deps.deps_completed" }
func (*depDepsCompletedGate) Category() string   { return "dependency" }
func (*depDepsCompletedGate) Severity() Severity { return SeverityWarn }

func (*depDepsCompletedGate) Run(ctx Context) Result {
	const name = "deps.deps_completed"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	if c == nil {
		return skip(name, "dependency", SeverityWarn, "case not found")
	}
	deps, err := ctx.Store.ListProjectDependencies(c.ProjectID)
	if err != nil {
		return skip(name, "dependency", SeverityWarn, "cannot load dependencies: "+err.Error())
	}
	if len(deps) == 0 {
		return pass(name, "dependency", SeverityWarn, "no project dependencies configured")
	}
	var stale, missing []string
	for _, dep := range deps {
		// Fetch the upstream project's *current* topology version so we can
		// verify that a SUCCESS run was executed against it — not an outdated topology.
		upstreamTopo, _ := ctx.Store.GetLatestTopology(dep.TargetProjectID)
		if upstreamTopo == nil {
			missing = append(missing, fmt.Sprintf("project %d (no topology)", dep.TargetProjectID))
			continue
		}
		ok, err := ctx.Store.HasSuccessfulRunAtTopologyVersion(dep.TargetProjectID, upstreamTopo.Version)
		if err != nil {
			return skip(name, "dependency", SeverityWarn, "cannot check runs: "+err.Error())
		}
		if !ok {
			stale = append(stale,
				fmt.Sprintf("project %d (no SUCCESS run at topology v%d)", dep.TargetProjectID, upstreamTopo.Version))
		}
	}
	if len(missing) > 0 || len(stale) > 0 {
		all := append(missing, stale...)
		return warn(name, "dependency",
			fmt.Sprintf("upstream projects lack a SUCCESS run at their current topology: %v", all))
	}
	return pass(name, "dependency", SeverityWarn,
		fmt.Sprintf("all %d upstream dependencies have a SUCCESS run at their current topology version", len(deps)))
}
