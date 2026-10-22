package orchestrator

import (
	"context"
	"fmt"
	"github.com/b070nd/stAirCase/src/internal/history"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/certificate"
)

// evidenceGate produces the evidence an in-scope change is approved on: the
// run's checks, run in the sandbox on the exact state the change would
// produce (the base commit, the changes approved so far, and this one).
type evidenceGate struct {
	approvals *approvals
	checks    []string
	sandbox   string // as for approved commands
	wsDir     string
	timeout   time.Duration // for each check; 0 is the default
}

// runChecks runs every check on the proposed state and returns the results and,
// when one did not pass, why: what a person is shown instead of a quiet approval.
func (g *evidenceGate) runChecks(ctx context.Context, next map[string]*approvedFile) (results []certificate.Check, failed string) {
	if len(g.checks) == 0 {
		return nil, ""
	}
	dir, cleanup, err := g.state(next)
	if err != nil {
		return nil, "the checks could not run on the proposed state: " + err.Error()
	}
	defer cleanup()
	var why []string
	for _, command := range g.checks {
		c, tail := runCheck(ctx, dir, command, g.sandbox, g.wsDir, g.timeout)
		results = append(results, c)
		if c.ExitCode != 0 {
			why = append(why, fmt.Sprintf("check %q failed (exit %d): %s", command, c.ExitCode, strings.TrimSpace(tail)))
		}
	}
	return results, strings.Join(why, "; ")
}

// state checks out the base commit in a scratch directory and lays the approved
// changes and next over it.
func (g *evidenceGate) state(next map[string]*approvedFile) (string, func(), error) {
	dir, cleanup, err := scratchCheckout(g.approvals.repo.path, g.approvals.base.Hash.String(), "staircase-evidence-")
	if err != nil {
		return "", nil, err
	}
	merged := maps.Clone(g.approvals.files)
	maps.Copy(merged, next)
	for p, f := range merged {
		dst := filepath.Join(dir, filepath.FromSlash(p)) // p was cleaned when it was derived
		if f.deleted {
			_ = os.Remove(dst)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			cleanup()
			return "", nil, err
		}
		_ = os.Remove(dst)
		if err := os.WriteFile(dst, f.content, f.mode.Perm()); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return dir, cleanup, nil
}

// checkEvidence is how a check result appears in a decision's record.
func checkEvidence(results []certificate.Check) []history.Check {
	out := make([]history.Check, len(results))
	for i, c := range results {
		out[i] = history.Check{Command: c.Command, ExitCode: c.ExitCode, OutputSHA256: c.OutputSHA256, Sandboxed: c.Sandboxed}
	}
	return out
}
