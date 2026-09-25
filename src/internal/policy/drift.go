package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/b070nd/staircase-core/src/internal/engine"
	"github.com/b070nd/staircase-core/src/internal/plan"
)

// Supervisor watches a run for drift from its plan: changes outside the
// stories' scope, too many files, no human look for too long. It decides
// nothing itself: it tells the orchestrator which proposals a human must see
// and when the run must halt, and reports what happened.
type Supervisor struct {
	scope  []string // allow globs of the open stories; empty = no scope check
	limits plan.Limits
	yields int
	files  map[string]bool // distinct files of approved changes
	report DriftReport
}

// DriftReport is a run's drift record, audited when the run ends.
type DriftReport struct {
	Scope      []string `json:"scope,omitempty"`
	InScope    []string `json:"in_scope,omitempty"`     // approved files inside the scope
	OutOfScope []string `json:"out_of_scope,omitempty"` // approved outside it: human overrides
	Violations int      `json:"scope_violations"`       // proposals reaching outside the scope
	Overrides  int      `json:"overrides"`              // out-of-scope proposals a human approved
	Auto       int      `json:"auto_decided"`
	Human      int      `json:"human_decided"`
	Halted     string   `json:"halted,omitempty"`
}

// NewSupervisor supervises against scope (allow globs, ** spans directories)
// and limits (zero = unlimited).
func NewSupervisor(scope []string, limits plan.Limits) *Supervisor {
	return &Supervisor{scope: scope, limits: limits, files: map[string]bool{}, report: DriftReport{Scope: scope}}
}

func (s *Supervisor) inScope(f string) bool {
	return len(s.scope) == 0 || engine.MatchAny(s.scope, f)
}

// Check assesses a proposal touching files, before it is decided: reason says
// why a human must decide it ("" when nothing requires that), halt that the
// run has drifted too far to continue.
func (s *Supervisor) Check(files []string) (reason string, halt bool) {
	s.yields++
	var why, out []string
	for _, f := range files {
		if !s.inScope(f) {
			out = append(out, f)
		}
	}
	if len(out) > 0 {
		s.report.Violations++
		if s.limits.MaxScopeViolations > 0 && s.report.Violations > s.limits.MaxScopeViolations {
			return "", true
		}
		why = append(why, "outside the stories' scope: "+strings.Join(out, ", "))
	}
	if s.limits.MaxFilesChanged > 0 {
		n := len(s.files)
		for _, f := range files {
			if !s.files[f] {
				n++
			}
		}
		if n > s.limits.MaxFilesChanged {
			why = append(why, fmt.Sprintf("%d files changed (limit %d)", n, s.limits.MaxFilesChanged))
		}
	}
	if s.limits.CheckpointEvery > 0 && s.yields%s.limits.CheckpointEvery == 0 {
		why = append(why, checkpointNote(s.limits.CheckpointEvery))
	}
	return strings.Join(why, "; "), false
}

// Decided records how a checked proposal was decided; drift is its Check reason.
func (s *Supervisor) Decided(files []string, approved, byHuman bool, drift string) {
	if byHuman {
		s.report.Human++
	} else {
		s.report.Auto++
	}
	if !approved {
		return
	}
	for _, f := range files {
		if !s.inScope(f) {
			s.report.Overrides++ // a human approved a change outside the scope
			break
		}
	}
	for _, f := range files {
		if s.files[f] {
			continue
		}
		s.files[f] = true
		if s.inScope(f) {
			s.report.InScope = append(s.report.InScope, f)
		} else {
			s.report.OutOfScope = append(s.report.OutOfScope, f)
		}
	}
}

// Halt records why the run was stopped.
func (s *Supervisor) Halt(reason string) { s.report.Halted = reason }

// Report returns the drift record so far, files sorted.
func (s *Supervisor) Report() DriftReport {
	r := s.report
	r.InScope, r.OutOfScope = slices.Sorted(slices.Values(r.InScope)), slices.Sorted(slices.Values(r.OutOfScope))
	if len(r.InScope) == 0 {
		r.InScope = nil
	}
	if len(r.OutOfScope) == 0 {
		r.OutOfScope = nil
	}
	return r
}

func checkpointNote(n int) string {
	if n == 1 {
		return "checkpoint: every proposal is reviewed"
	}
	return fmt.Sprintf("checkpoint: one in every %d proposals is reviewed", n)
}
