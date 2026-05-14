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
func ExportedSendWebhookYield(url string, req ipc.IpcYieldRequest) ipc.IpcYieldResponse {
	return sendWebhookYield(url, req)
}
