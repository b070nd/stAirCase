package gate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/engine"
	"github.com/b070nd/stAirCase/src/internal/history"
	"github.com/b070nd/stAirCase/src/internal/persistence"
)

func init() {
	Register(&depNoCycleGate{})
	Register(&depDepsCompletedGate{})
}

// deps.no_cycle

type depNoCycleGate struct{}

func (*depNoCycleGate) Name() string       { return "deps.no_cycle" }
func (*depNoCycleGate) Category() string   { return "dependency" }
func (*depNoCycleGate) Severity() Severity { return SeverityBlock }

func (*depNoCycleGate) Run(ctx Context) Result {
	const name = "deps.no_cycle"
	projects, err := ctx.Store.ListAllProjects()
	if err != nil {
		return fail(name, "dependency", SeverityBlock, "cannot load projects: "+err.Error())
	}
	deps, err := ctx.Store.ListAllProjectDependencies()
	if err != nil {
		return fail(name, "dependency", SeverityBlock, "cannot load dependencies: "+err.Error())
	}
	if _, err := engine.TopoSort(projects, deps); err != nil {
		return fail(name, "dependency", SeverityBlock, err.Error())
	}
	return pass(name, "dependency", SeverityBlock,
		fmt.Sprintf("dependency graph is acyclic (%d projects, %d edges)", len(projects), len(deps)))
}

// deps.deps_completed

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
		// verify that a run was delivered against it - not an outdated topology.
		upstreamTopo, _ := ctx.Store.GetLatestTopology(dep.TargetProjectID)
		if upstreamTopo == nil {
			missing = append(missing, fmt.Sprintf("project %d (no topology)", dep.TargetProjectID))
			continue
		}
		delivered, why, err := deliveredAt(ctx.Store, dep.TargetProjectID, upstreamTopo.Version)
		if err != nil {
			return skip(name, "dependency", SeverityWarn, "cannot check runs: "+err.Error())
		}
		if !delivered {
			stale = append(stale, fmt.Sprintf("project %d (no delivered run at topology v%d%s)", dep.TargetProjectID, upstreamTopo.Version, why))
		}
	}
	if len(missing) > 0 || len(stale) > 0 {
		all := slices.Concat(missing, stale)
		return warn(name, "dependency",
			fmt.Sprintf("upstream projects lack a delivered run at their current topology: %v", all))
	}
	return pass(name, "dependency", SeverityWarn,
		fmt.Sprintf("all %d upstream dependencies have a delivered run at their current topology version", len(deps)))
}

// deliveredAt says whether a run of the project was delivered against the topology version, and when it was not, what the runs it looked
// at said that a person should know (a history that cannot be read, a delivery whose evidence is incomplete). A run is delivered when
// its own verified history says so (history.Outcome): a status column nobody verified decides nothing (ADR 0005, ADR 0010).
func deliveredAt(store *persistence.Store, projectID int64, topoVersion int) (delivered bool, why string, err error) {
	cases, err := store.ListCasesByProject(projectID)
	if err != nil {
		return false, "", err
	}
	var notes []string
	for _, c := range cases {
		runs, err := store.ListRunsByCase(c.ID)
		if err != nil {
			return false, "", err
		}
		for _, r := range runs {
			h, err := history.Read(store, r.ID)
			if err != nil { // unknown, never satisfied
				notes = append(notes, fmt.Sprintf("the history of run #%d cannot be read: %v", r.ID, err))
				continue
			}
			if h.Bound.TopologyVersion != topoVersion {
				continue
			}
			switch outcome, _ := h.Outcome(); outcome {
			case history.OutcomeDelivered:
				return true, "", nil
			case history.OutcomeDeliveredIncomplete:
				notes = append(notes, fmt.Sprintf("run #%d was delivered with incomplete evidence", r.ID))
			}
		}
	}
	if len(notes) > 0 {
		why = "; " + strings.Join(notes, "; ")
	}
	return false, why, nil
}
