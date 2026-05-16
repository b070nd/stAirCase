// Package gate implements the stAirCase pre-run quality gate system.
//
// Architecture:
//   - Gate     — interface each check implements (Name, Category, Severity, Run)
//   - Register — adds a gate to the global registry (called from each file's init)
//   - RunAll   — executes every registered gate and returns a Report
//   - Report   — JSON-serialisable, machine- and human-readable result set
//
// New gates: implement the Gate interface and call Register(&myGate{}) in an init()
// function inside any file in this package. No other wiring required.
package gate

import (
	"time"

	"github.com/b070nd/staircase-core/src/internal/persistence"
)

// ─── Core types ───────────────────────────────────────────────────────────────

// Severity classifies whether a failing gate blocks the run or is advisory.
type Severity string

const (
	SeverityBlock Severity = "BLOCK" // failing this gate prevents staircase run
	SeverityWarn  Severity = "WARN"  // failing this gate is advisory only
)

// Status is the outcome of a single gate execution.
type Status string

const (
	StatusPass Status = "PASS"
	StatusWarn Status = "WARN" // advisory failure
	StatusFail Status = "FAIL" // hard failure
	StatusSkip Status = "SKIP" // prerequisite unavailable; gate could not run
)

// Result is the full outcome of one gate execution.
type Result struct {
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Severity Severity `json:"severity"`
	Status   Status   `json:"status"`
	Message  string   `json:"message"`
}

// Context carries all inputs a gate may need.
type Context struct {
	CaseID int64
	WsDir  string
	Store  *persistence.Store
}

// Gate is the interface every quality check must implement.
type Gate interface {
	Name() string
	Category() string
	Severity() Severity
	Run(ctx Context) Result
}

// ─── Registry ─────────────────────────────────────────────────────────────────

var registry []Gate

// Register adds a gate to the global registry. Call it from init() functions.
func Register(g Gate) { registry = append(registry, g) }

// ─── Runner ───────────────────────────────────────────────────────────────────

// Report is the complete, JSON-serialisable output of a quality gate run.
type Report struct {
	CaseID  int64     `json:"case_id"`
	RunAt   time.Time `json:"run_at"`
	Overall Status    `json:"overall"`
	Gates   []Result  `json:"gates"`
	Summary Summary   `json:"summary"`
}

// Summary counts gate outcomes.
type Summary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

// Blocking returns true when the report has at least one hard (BLOCK) failure.
func (r Report) Blocking() bool { return r.Overall == StatusFail }

// RunAll executes every registered gate plus any plugin gates from
// $wsDir/gates.json in insertion order and returns a Report.
func RunAll(ctx Context) Report {
	allGates := append(append([]Gate{}, registry...), loadPluginGates(ctx.WsDir)...)
	report := Report{CaseID: ctx.CaseID, RunAt: time.Now().UTC()}
	for _, g := range allGates {
		res := g.Run(ctx)
		report.Gates = append(report.Gates, res)
		switch res.Status {
		case StatusPass:
			report.Summary.Pass++
		case StatusWarn:
			report.Summary.Warn++
		case StatusFail:
			report.Summary.Fail++
		default:
			report.Summary.Skip++
		}
	}
	// Overall = FAIL if any BLOCK gate failed; WARN if any warning; else PASS.
	report.Overall = StatusPass
	for _, res := range report.Gates {
		if res.Status == StatusFail && res.Severity == SeverityBlock {
			report.Overall = StatusFail
			return report
		}
	}
	for _, res := range report.Gates {
		if res.Status == StatusWarn || res.Status == StatusFail {
			report.Overall = StatusWarn
			break
		}
	}
	return report
}

// ─── Result constructors (used by gate implementations) ──────────────────────

func pass(name, category string, sev Severity, msg string) Result {
	return Result{Name: name, Category: category, Severity: sev, Status: StatusPass, Message: msg}
}

func fail(name, category string, sev Severity, msg string) Result {
	return Result{Name: name, Category: category, Severity: sev, Status: StatusFail, Message: msg}
}

// warn always uses SeverityWarn — advisory failures never block a run.
func warn(name, category, msg string) Result {
	return Result{Name: name, Category: category, Severity: SeverityWarn, Status: StatusWarn, Message: msg}
}

func skip(name, category string, sev Severity, msg string) Result {
	return Result{Name: name, Category: category, Severity: sev, Status: StatusSkip, Message: msg}
}

// Ensure persistence is used (Store is referenced by Context above).
var _ *persistence.Store
