package orchestrator

import (
	"testing"
	"time"
)

// TestSavedOptions: a continuation runs with the terms the run saved, and a continuation's own options cannot
// change them.
func TestSavedOptions(t *testing.T) {
	orig := RunOptions{Terms: Terms{Agreed: "dev@example.com", ApproveInScope: true, AllowShellExec: true, Sandbox: "required",
		Checks: []string{"go test ./..."}, CheckTimeout: 3 * time.Minute, SignKey: "/k", SignAs: "dev@example.com",
		SignalModel: "m", SignalURL: "http://127.0.0.1:1", ValidatorModels: []string{"x", "y"}, RequireEvidence: true}}
	got := optionsToSave(orig).apply(RunOptions{AckDrift: true, ApprovalPort: 9, Terms: Terms{ApproveInScope: false, AllowShellExec: false, Sandbox: "off"}})
	if !got.ApproveInScope || !got.AllowShellExec || got.Sandbox != "required" || got.Agreed != "dev@example.com" || !got.RequireEvidence {
		t.Errorf("the saved terms did not win: %+v", got)
	}
	if got.CheckTimeout != 3*time.Minute || len(got.Checks) != 1 || got.SignalModel != "m" || got.SignalURL != "http://127.0.0.1:1" || len(got.ValidatorModels) != 2 {
		t.Errorf("checks, signal or validator lost: %+v", got)
	}
	if !got.AckDrift || got.ApprovalPort != 9 {
		t.Errorf("what belongs to the continuation was lost: %+v", got)
	}
}
