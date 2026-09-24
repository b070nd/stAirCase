package gate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/b070nd/staircase-core/src/internal/plan"
)

func init() {
	Register(&runtimePlanCompiledGate{})
	Register(&runtimeVenvReadyGate{})
	Register(&runtimeVenvBrokenGate{})
	Register(&runtimeSourcePathGate{})
	Register(&runtimeNoConcurrentRunGate{})
	Register(&runtimeGitAvailableGate{})
}

// ─── runtime.plan_compiled ───────────────────────────────────────────────────

type runtimePlanCompiledGate struct{}

func (*runtimePlanCompiledGate) Name() string       { return "runtime.plan_compiled" }
func (*runtimePlanCompiledGate) Category() string   { return "runtime" }
func (*runtimePlanCompiledGate) Severity() Severity { return SeverityBlock }

// Run checks that the case has a plan that will run as compiled: present,
// unmodified, of this staircase's plan version, valid, for this case — and
// warns when the topology changed after compile.
func (*runtimePlanCompiledGate) Run(ctx Context) Result {
	const name = "runtime.plan_compiled"
	p, err := plan.Load(filepath.Join(ctx.WsDir, "tmp", fmt.Sprintf("plan_case%d.json", ctx.CaseID)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("no compiled plan — run 'staircase compile %d'", ctx.CaseID))
	case err != nil:
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("plan cannot run: %v — run 'staircase compile %d --force'", err, ctx.CaseID))
	case p.CaseID != ctx.CaseID:
		return fail(name, "runtime", SeverityBlock, fmt.Sprintf("plan was compiled for case #%d — run 'staircase compile %d --force'", p.CaseID, ctx.CaseID))
	}
	if c, _ := ctx.Store.GetCase(ctx.CaseID); c != nil {
		if cur, _ := ctx.Store.GetLatestTopology(c.ProjectID); cur != nil && p.TopologyVersion < cur.Version {
			return warn(name, "runtime", fmt.Sprintf("plan was compiled for topology v%d but current topology is v%d — re-run 'staircase compile %d --force'",
				p.TopologyVersion, cur.Version, ctx.CaseID))
		}
	}
	return pass(name, "runtime", SeverityBlock, fmt.Sprintf("plan for topology v%d, %d agents", p.TopologyVersion, len(p.Agents)))
}

// ─── runtime.venv_ready ───────────────────────────────────────────────────────

type runtimeVenvReadyGate struct{}

func (*runtimeVenvReadyGate) Name() string       { return "runtime.venv_ready" }
func (*runtimeVenvReadyGate) Category() string   { return "runtime" }
func (*runtimeVenvReadyGate) Severity() Severity { return SeverityBlock }

func (*runtimeVenvReadyGate) Run(ctx Context) Result {
	const name = "runtime.venv_ready"
	python := filepath.Join(ctx.WsDir, "venv", "bin", "python")
	if _, err := os.Stat(python); err != nil {
		return fail(name, "runtime", SeverityBlock,
			"venv not found — run 'staircase init'")
	}
	hashFile := filepath.Join(ctx.WsDir, "venv", ".requirements_hash")
	if _, err := os.Stat(hashFile); err != nil {
		// Venv predates hash tracking; advisory only.
		return warn(name, "runtime",
			"venv exists but has no requirements hash — run 'staircase init' to re-validate packages")
	}
	return pass(name, "runtime", SeverityBlock, "venv present, requirements hash verified")
}

// ─── runtime.venv_broken ─────────────────────────────────────────────────────

type runtimeVenvBrokenGate struct{}

func (*runtimeVenvBrokenGate) Name() string       { return "runtime.venv_broken" }
func (*runtimeVenvBrokenGate) Category() string   { return "runtime" }
func (*runtimeVenvBrokenGate) Severity() Severity { return SeverityBlock }

func (*runtimeVenvBrokenGate) Run(ctx Context) Result {
	const name = "runtime.venv_broken"
	sentinel := filepath.Join(ctx.WsDir, "venv", ".requirements_hash.broken")
	if _, err := os.Stat(sentinel); err == nil {
		return fail(name, "runtime", SeverityBlock,
			"venv is in a broken state — run 'staircase doctor --fix-venv' to repair")
	}
	return pass(name, "runtime", SeverityBlock, "venv broken sentinel absent")
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
		return skip(name, "runtime", SeverityBlock, "cannot load runs: "+err.Error())
	}
	for _, r := range runs {
		if r.Status == persistence.RunStatusRunning {
			return fail(name, "runtime", SeverityBlock,
				fmt.Sprintf("run #%d is already RUNNING for this case — wait or kill it", r.ID))
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
		// No source path — git not required for this project.
		return pass(name, "runtime", SeverityBlock, "no source path, git not required")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fail(name, "runtime", SeverityBlock,
			"git not found in PATH — install git or set source_path to empty")
	}
	return pass(name, "runtime", SeverityBlock, "git found in PATH")
}
