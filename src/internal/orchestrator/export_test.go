// export_test.go — exposes unexported orchestrator functions for whitebox testing.
package orchestrator

import (
	"time"

	"github.com/b070nd/staircase-core/src/internal/ipc"
)

// ExportedHandleDirtyTree wraps handleDirtyTree so it can be called from
// package orchestrator_test without making the symbol part of the public API.
var ExportedHandleDirtyTree = handleDirtyTree

// ExportedTimePtr wraps the unexported timePtr helper.
func ExportedTimePtr(t time.Time) *time.Time { return timePtr(t) }

// ExportedSendWebhookYield wraps sendWebhookYield for round-trip testing.
// secret may be nil for the unauthenticated path.
func ExportedSendWebhookYield(url string, secret []byte, req ipc.IpcYieldRequest) ipc.IpcYieldResponse {
	return sendWebhookYield(url, secret, req)
}

// ExportedScrubSecrets exposes scrubSecrets for whitebox tests (CHECK 4.4.3).
func ExportedScrubSecrets(req ipc.IpcYieldRequest, activeValues []string) ipc.IpcYieldRequest {
	return scrubSecrets(req, activeValues)
}

// ExportedRunGates exposes runGates for whitebox testing of the quality-gate
// pre-flight path without requiring a full Run() invocation.
func ExportedRunGates(r *Runner, caseID int64) error { return r.runGates(caseID) }

// ExportedWriteSummary exposes writeSummary for unit testing (CHECK 10.4.1).
func ExportedWriteSummary(wsDir string, s RunSummary) error { return writeSummary(wsDir, s) }

// PathWithinRootForTest exposes pathWithinRoot for sandbox-predicate unit tests.
func PathWithinRootForTest(root, rel string) bool { return pathWithinRoot(root, rel) }
