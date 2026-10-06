package orchestrator

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
)

// TestReplayChain: what a continuation derives from a history, event by event: the policy-approved count, the task
// flag, the supervisor's decisions (not the orchestrator's refusals, not the final review), token usage, how much
// time earlier segments used, how many segments there were and the requests nobody decided.
func TestReplayChain(t *testing.T) {
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	ev := func(typ string, sec int, p map[string]any) domain.RunEventLog {
		b, _ := json.Marshal(p)
		return domain.RunEventLog{EventType: typ, Payload: string(b), Timestamp: at(sec)}
	}
	decided := func(sec, seq int, source string, approved bool, paths ...string) domain.RunEventLog {
		return ev("yield_decided", sec, map[string]any{"seq": seq, "source": source, "approved": approved, "action_type": "file_edit", "paths": paths})
	}
	req := func(sec int) domain.RunEventLog {
		return ev("yield_request", sec, map[string]any{"action_type": "file_edit"})
	}
	events := []domain.RunEventLog{
		ev("run_bound", 0, nil),
		req(1), decided(2, 1, "policy", true, "a"),
		req(3), decided(4, 2, "orchestrator", false), // refused: never reached the supervisor
		req(5), decided(6, 3, "task", true, "b"),
		ev("state_emit", 7, map[string]any{"active_agent": "coder", "state": map[string]any{"model": "m", "input_tokens": 10, "output_tokens": 4}}),
		req(8),                      // a request nobody decided: the segment ended
		ev("run_resumed", 100, nil), // the second segment began a long time after
		req(101), decided(102, 4, "operator", false, "c"),
		ev("yield_decided", 103, map[string]any{"seq": 5, "source": "operator", "approved": true, "action_type": domain.ActionFinalReview}),
		ev("drift_halt", 104, nil),
	}
	c := &continuation{}
	if err := replayChain(events, c); err != nil {
		t.Fatal(err)
	}
	if c.autoApproved != 1 || !c.taskApproved || !c.halted {
		t.Errorf("autoApproved=%d taskApproved=%v halted=%v", c.autoApproved, c.taskApproved, c.halted)
	}
	if len(c.decisions) != 3 || c.decisions[0].Paths[0] != "a" || c.decisions[2].Source != "operator" {
		t.Errorf("decisions %+v: the orchestrator's refusal and the final review are not the supervisor's", c.decisions)
	}
	if len(c.usage) != 1 || c.usage[0] != (usageRecord{"coder", "m", 10, 4}) {
		t.Errorf("usage %+v", c.usage)
	}
	// segment 1 lasted from 0 to 8 s (its last event); segment 2 from 100 to 104 s
	if c.segment != 2 || c.elapsed != 12*time.Second {
		t.Errorf("segment %d elapsed %v, want 2 and 12s (the gap between the segments is not run time)", c.segment, c.elapsed)
	}
	if c.undecided != 0 {
		t.Errorf("undecided %d: the request left pending by segment 1 is counted when the segment is resumed, not twice", c.undecided)
	}

	// a history that ends with two requests nobody decided
	c2 := &continuation{}
	if err := replayChain(append(events[:9:9], req(9)), c2); err != nil {
		t.Fatal(err)
	}
	if c2.undecided != 2 || c2.segment != 1 {
		t.Errorf("undecided %d segment %d, want 2 and 1", c2.undecided, c2.segment)
	}
}

// TestSavedOptions: a continuation runs with the terms the run saved, and a continuation's own options cannot
// change them.
func TestSavedOptions(t *testing.T) {
	orig := RunOptions{Agreed: "dev@example.com", ApproveInScope: true, AllowShellExec: true, Sandbox: "required",
		Checks: []string{"go test ./..."}, CheckTimeout: 3 * time.Minute, SignKey: "/k", SignAs: "dev@example.com",
		Signal: &Signal{Model: "m", URL: "http://127.0.0.1:1"}, Validator: &Validator{Models: []string{"x", "y"}}, RequireEvidence: true}
	got := optionsToSave(orig).apply(RunOptions{ApproveInScope: false, AllowShellExec: false, Sandbox: "off", AckDrift: true, ApprovalPort: 9})
	if !got.ApproveInScope || !got.AllowShellExec || got.Sandbox != "required" || got.Agreed != "dev@example.com" || !got.RequireEvidence {
		t.Errorf("the saved terms did not win: %+v", got)
	}
	if got.CheckTimeout != 3*time.Minute || len(got.Checks) != 1 || got.Signal == nil || got.Signal.URL != "http://127.0.0.1:1" || len(got.Validator.Models) != 2 {
		t.Errorf("checks, signal or validator lost: %+v", got)
	}
	if !got.AckDrift || got.ApprovalPort != 9 {
		t.Errorf("what belongs to the continuation was lost: %+v", got)
	}
}
