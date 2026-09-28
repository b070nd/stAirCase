// export_test.go - exposes unexported orchestrator functions for whitebox testing.
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// ExportedTimePtr wraps the unexported timePtr helper.
func ExportedTimePtr(t time.Time) *time.Time { return timePtr(t) }

// ExportedSendWebhookYield wraps sendWebhookYield for round-trip testing.
// secret may be nil for the unauthenticated path.
func ExportedSendWebhookYield(url string, secret []byte, req domain.YieldRequest) domain.YieldResponse {
	return sendWebhookYield(url, secret, req)
}

// ExportedScrubSecrets exposes scrubSecrets for whitebox tests (CHECK 4.4.3).
func ExportedScrubSecrets(req domain.YieldRequest, activeValues []string) domain.YieldRequest {
	return scrubSecrets(req, activeValues)
}

// ExportedRunGates exposes runGates for whitebox testing of the quality-gate
// pre-flight path without requiring a full Run() invocation.
func ExportedRunGates(r *Runner, caseID int64) error { return r.runGates(caseID) }

// ExportedWriteSummary exposes writeSummary for unit testing (CHECK 10.4.1).
func ExportedWriteSummary(wsDir string, s RunSummary) error { return writeSummary(wsDir, s) }

// CleanApprovedPathForTest exposes cleanApprovedPath for trust-boundary tests.
func CleanApprovedPathForTest(root, rel string) (string, error) { return cleanApprovedPath(root, rel) }

// SetLostGraceForTest shortens how long a run waits for an agent to stop after
// cancellation before it gives up on it. Restore with the returned func.
func SetLostGraceForTest(d time.Duration) (restore func()) {
	orig := lostGrace
	lostGrace = d
	return func() { lostGrace = orig }
}

// CommitApprovedForTest drives finalize's commit step on repoPath's current
// branch: it approves creating file with content through the real derivation,
// writes it like an honest agent, checks it with verify, then runs tamper - a
// process still alive after verify - before committing the approvals.
func CommitApprovedForTest(repoPath, file, content string, tamper func()) (string, error) {
	gr, err := OpenGitRepo(repoPath)
	if err != nil {
		return "", err
	}
	base, err := gr.HeadSHA()
	if err != nil {
		return "", err
	}
	branch, err := gr.CurrentBranch()
	if err != nil {
		return "", err
	}
	a, err := newApprovals(gr, base)
	if err != nil {
		return "", err
	}
	next, err := a.derive([]domain.ProposedEdit{{File: file, SearchBlock: MarkerNewFile, ReplaceBlock: content}})
	if err != nil {
		return "", err
	}
	a.record(next)
	if err := os.WriteFile(filepath.Join(repoPath, file), []byte(content), 0o644); err != nil {
		return "", err
	}
	if v, err := a.verify(); err != nil || len(v) > 0 {
		return "", fmt.Errorf("verify: %v %v", v, err)
	}
	tamper()
	return a.commit(branch, "test")
}
