package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/b070nd/stAirCase/src/internal/blueprint"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/plan"
)

func init() {
	Register(&runtimePlanCompiledGate{})
	Register(&runtimePlanPinnedGate{})
	Register(&runtimeSourcePathGate{})
	Register(&runtimeNoConcurrentRunGate{})
	Register(&runtimeGitAvailableGate{})
}

// planPath is where compile writes the case's plan.
func planPath(ctx Context) string {
	return filepath.Join(ctx.WsDir, "tmp", fmt.Sprintf("plan_case%d.json", ctx.CaseID))
}

// ─── runtime.plan_compiled ───────────────────────────────────────────────────

type runtimePlanCompiledGate struct{}

func (*runtimePlanCompiledGate) Name() string       { return "runtime.plan_compiled" }
func (*runtimePlanCompiledGate) Category() string   { return "runtime" }
func (*runtimePlanCompiledGate) Severity() Severity { return SeverityBlock }

// Run checks that the case has a plan that will run as compiled: present,
// unmodified, of this staircase's plan version, valid, for this case - and
// warns when the topology changed after compile.
func (*runtimePlanCompiledGate) Run(ctx Context) Result {
	const name = "runtime.plan_compiled"
	p, err := plan.Load(planPath(ctx))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("no compiled plan - run 'staircase compile %d'", ctx.CaseID))
	case err != nil:
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("plan cannot run: %v - run 'staircase compile %d --force'", err, ctx.CaseID))
	case p.CaseID != ctx.CaseID:
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("plan was compiled for case #%d - run 'staircase compile %d --force'", p.CaseID, ctx.CaseID))
	case p.Harness != "":
		return pass(name, "runtime", SeverityBlock, "plan runs "+p.Harness)
	}
	if c, _ := ctx.Store.GetCase(ctx.CaseID); c != nil {
		if cur, _ := ctx.Store.GetLatestTopology(c.ProjectID); cur != nil && p.TopologyVersion < cur.Version {
			return warn(name, "runtime", fmt.Sprintf("plan was compiled for topology v%d but current topology is v%d - re-run 'staircase compile %d --force'",
				p.TopologyVersion, cur.Version, ctx.CaseID))
		}
	}
	return pass(name, "runtime", SeverityBlock, fmt.Sprintf("plan for topology v%d, %d agents", p.TopologyVersion, len(p.Agents)))
}

// ─── runtime.plan_pinned ─────────────────────────────────────────────────────

type runtimePlanPinnedGate struct{}

func (*runtimePlanPinnedGate) Name() string       { return "runtime.plan_pinned" }
func (*runtimePlanPinnedGate) Category() string   { return "runtime" }
func (*runtimePlanPinnedGate) Severity() Severity { return SeverityBlock }

// Run checks that a case bound to a blueprint runs exactly that blueprint:
// the plan names it and holds its topology, PRD and stories. Anything changed
// through the imperative CLI after binding blocks the run.
func (*runtimePlanPinnedGate) Run(ctx Context) Result {
	const name = "runtime.plan_pinned"
	hash, slug, err := ctx.Store.CaseBlueprint(ctx.CaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return skip(name, "runtime", SeverityBlock, "case not found")
	}
	if err != nil {
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("read case binding: %v", err))
	}
	if hash == "" {
		return pass(name, "runtime", SeverityBlock, "case is not bound to a blueprint")
	}
	p, err := plan.Load(filepath.Join(ctx.WsDir, "tmp", fmt.Sprintf("plan_case%d.json", ctx.CaseID)))
	if err != nil {
		return skip(name, "runtime", SeverityBlock, "no usable plan (see runtime.plan_compiled)")
	}
	if p.BlueprintHash != hash {
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("plan was not compiled from blueprint %.12s - run 'staircase compile %d --force' to recompile", hash, ctx.CaseID))
	}
	stored, err := ctx.Store.FindBlueprint(hash)
	if err != nil {
		return fail(name, "runtime", SeverityBlock, err.Error())
	}
	b, err := blueprint.Parse([]byte(stored.Content))
	if err == nil && b.Hash() != hash {
		err = errors.New("stored content does not match its hash")
	}
	if err != nil {
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("blueprint %.12s: %v", hash, err))
	}
	if err := b.Check(p, slug); err != nil {
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("case #%d drifted from blueprint %s %.12s (%v) - re-bind the project with 'staircase project bind'", ctx.CaseID, b.Name, hash, err))
	}
	return pass(name, "runtime", SeverityBlock, fmt.Sprintf("plan is blueprint %s %.12s, case %s", b.Name, hash, slug))
}

// ─── runtime.source_path ─────────────────────────────────────────────────────

type runtimeSourcePathGate struct{}

func (*runtimeSourcePathGate) Name() string       { return "runtime.source_path" }
func (*runtimeSourcePathGate) Category() string   { return "runtime" }
func (*runtimeSourcePathGate) Severity() Severity { return SeverityBlock }

func (*runtimeSourcePathGate) Run(ctx Context) Result {
	const name = "runtime.source_path"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	if c == nil {
		return skip(name, "runtime", SeverityBlock, "case not found")
	}
	p, _ := ctx.Store.GetProject(c.ProjectID)
	if p == nil || p.SourcePath == "" {
		return pass(name, "runtime", SeverityBlock, "no source path configured")
	}
	if _, err := os.Stat(p.SourcePath); err != nil {
		return fail(name, "runtime", SeverityBlock,
			fmt.Sprintf("source path %q does not exist or is inaccessible", p.SourcePath))
	}
	return pass(name, "runtime", SeverityBlock,
		fmt.Sprintf("source path %q exists", p.SourcePath))
}

// ─── runtime.no_concurrent_run ────────────────────────────────────────────────

type runtimeNoConcurrentRunGate struct{}

func (*runtimeNoConcurrentRunGate) Name() string       { return "runtime.no_concurrent_run" }
func (*runtimeNoConcurrentRunGate) Category() string   { return "runtime" }
func (*runtimeNoConcurrentRunGate) Severity() Severity { return SeverityBlock }

func (*runtimeNoConcurrentRunGate) Run(ctx Context) Result {
	const name = "runtime.no_concurrent_run"
	runs, err := ctx.Store.ListRunsByCase(ctx.CaseID)
	if err != nil {
		return fail(name, "runtime", SeverityBlock, "cannot load runs: "+err.Error())
	}
	for _, r := range runs {
		if r.Status == persistence.RunStatusRunning {
			return fail(name, "runtime", SeverityBlock,
				fmt.Sprintf("run #%d is already RUNNING for this case - wait or kill it", r.ID))
		}
	}
	return pass(name, "runtime", SeverityBlock, "no concurrent runs")
}

// ─── runtime.git_available ────────────────────────────────────────────────────

type runtimeGitAvailableGate struct{}

func (*runtimeGitAvailableGate) Name() string       { return "runtime.git_available" }
func (*runtimeGitAvailableGate) Category() string   { return "runtime" }
func (*runtimeGitAvailableGate) Severity() Severity { return SeverityBlock }

func (*runtimeGitAvailableGate) Run(ctx Context) Result {
	const name = "runtime.git_available"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	if c == nil {
		return skip(name, "runtime", SeverityBlock, "case not found")
	}
	p, _ := ctx.Store.GetProject(c.ProjectID)
	if p == nil || p.SourcePath == "" {
		// No source path - git not required for this project.
		return pass(name, "runtime", SeverityBlock, "no source path, git not required")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fail(name, "runtime", SeverityBlock,
			"git not found in PATH - install git or set source_path to empty")
	}
	return pass(name, "runtime", SeverityBlock, "git found in PATH")
}
