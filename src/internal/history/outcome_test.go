package history

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func (c *chain) ended(status string) *chain {
	return c.add("run_path", map[string]any{"phases": []string{"PRE_FLIGHT", "BRANCH_RESTORE"}, "status": status})
}

func (c *chain) offered() *chain {
	c.add("phase_checked", map[string]any{"tree": "t", "passed": true})
	return c.add("awaiting_review", map[string]any{"tree": "t", "phase_checked": fmt.Sprintf("e%d", len(c.events))})
}

// TestOutcome_maps_each_history_to_one_outcome (V11-C01 scope 6): one outcome per run, read from its history alone, with the
// reason the chain gives for it.
func TestOutcome_maps_each_history_to_one_outcome(t *testing.T) {
	approved := func() *chain { return bound().edit("a").decided(1, "evidence", true) }
	delivered := func() *chain {
		return approved().add("commit_prepared", map[string]any{"commit": "c"}).add("certificate_issued", map[string]any{"commit": "c"})
	}
	for name, tc := range map[string]struct {
		c   *chain
		out Outcome
		why string
	}{
		"its segment has not ended":                 {approved(), OutcomeRunning, "does not say it ended"},
		"continued after it stopped":                {approved().ended("KILLED").add("run_resumed", map[string]any{"segment": 2}), OutcomeRunning, ""},
		"a review a resumed session withdrew":       {approved().offered().ended("AWAITING_REVIEW").add("run_resumed", map[string]any{"segment": 2}), OutcomeRunning, ""},
		"a review offered":                          {approved().offered().ended("AWAITING_REVIEW"), OutcomeAwaitingReview, "staircase decide"},
		"accepted, its delivery not finished":       {approved().offered().ended("AWAITING_REVIEW").add("review_accepted", nil), OutcomeAwaitingReview, "accept it again"},
		"a review the owner rejected":               {approved().offered().ended("AWAITING_REVIEW").add("review_rejected", map[string]any{"feedback": "no"}), OutcomeFailed, "rejected"},
		"checks that failed on the approved tree":   {approved().add("phase_checked", map[string]any{"tree": "t", "passed": false, "failed": "go test: exit 1"}).ended("FAILED"), OutcomeFailed, "go test: exit 1"},
		"a final review a person rejected":          {approved().final(2, false, nil).ended("FAILED"), OutcomeFailed, "final review"},
		"a run that failed":                         {approved().ended("FAILED"), OutcomeFailed, "failed"},
		"halted for drift":                          {approved().add("drift_halt", map[string]any{"reason": "the run exceeded its 60 s limit"}).ended("KILLED"), OutcomeBlocked, "60 s limit"},
		"stopped before it finished":                {approved().ended("KILLED"), OutcomeBlocked, "stopped"},
		"everything parked":                         {bound().edit("a").decided(1, "parked", false).ended("SUCCESS"), OutcomeBlocked, "1 proposal(s) parked"},
		"nothing approved, nothing parked":          {bound().edit("a").decided(1, "operator", false).ended("SUCCESS"), OutcomeFailed, "without a change"},
		"a commit named, never delivered":           {approved().add("commit_prepared", map[string]any{"commit": "c"}).ended("KILLED"), OutcomeBlocked, "staircase recover"},
		"delivered with its evidence":               {delivered().ended("SUCCESS"), OutcomeDelivered, "c"},
		"delivered, evidence failed":                {delivered().add("evidence_failed", map[string]any{"commit": "c", "errors": []string{"ledger: disk full"}}).ended("SUCCESS"), OutcomeDeliveredIncomplete, "ledger: disk full"},
		"delivered, no certificate":                 {approved().add("commit_prepared", map[string]any{"commit": "c"}).add("certificate_failed", map[string]any{"commit": "c"}).add("evidence_failed", map[string]any{"commit": "c", "errors": []string{"certificate: no key"}}).ended("SUCCESS"), OutcomeDeliveredIncomplete, "certificate: no key"},
		"an accepted away run delivered":            {approved().offered().ended("AWAITING_REVIEW").add("review_accepted", nil).add("certificate_issued", map[string]any{"commit": "c"}).add("review_delivered", map[string]any{"commit": "c"}), OutcomeDelivered, "c"},
		"an away delivery with incomplete evidence": {approved().offered().ended("AWAITING_REVIEW").add("review_accepted", nil).add("review_delivered", map[string]any{"commit": "c", "evidence_errors": []string{"ledger note: x"}}), OutcomeDeliveredIncomplete, "ledger note: x"},
		"recovered": {approved().add("run_recovered", map[string]any{"commit": "c"}), OutcomeDelivered, "recovered"},
		"recovered, its evidence repaired later": {approved().add("run_recovered", map[string]any{"commit": "c", "evidence_errors": []string{"ledger: x"}}).
			add("recovery_repaired", map[string]any{"commit": "c"}), OutcomeDelivered, "c"},
		"recovered, a repair still incomplete": {approved().add("run_recovered", map[string]any{"commit": "c"}).
			add("recovery_repaired", map[string]any{"commit": "c", "evidence_errors": []string{"certificate: x"}}), OutcomeDeliveredIncomplete, "certificate: x"},
		"an older run's end, after a halt": {approved().add("drift_halt", map[string]any{"reason": "drift"}).add("run_path", map[string]any{"phases": []string{}}), OutcomeBlocked, "drift"},
		"an older run's end, parked":       {bound().edit("a").decided(1, "parked", false).add("run_path", map[string]any{"phases": []string{}}), OutcomeBlocked, "parked"},
		"an older run's end, nothing else": {approved().add("run_path", map[string]any{"phases": []string{}}), OutcomeFailed, "does not say"},
	} {
		t.Run(name, func(t *testing.T) {
			out, why := tc.c.replay(t).Outcome()
			assert.Equal(t, tc.out, out, why)
			assert.Contains(t, why, tc.why)
		})
	}
}

// TestOutcome_names_the_lifecycle_class_of_a_launch_that_ended_badly (V11-C01 scope 8): a run whose agent was refused, exited
// with an error, did not stop when asked or was lost says so, in the class's own words, and is never reported as a failed check or
// a rejected review. A run that failed for those reasons keeps them first.
func TestOutcome_names_the_lifecycle_class_of_a_launch_that_ended_badly(t *testing.T) {
	approved := func() *chain { return bound().edit("a").decided(1, "evidence", true) }
	for name, tc := range map[string]struct {
		c   *chain
		out Outcome
		why []string
	}{
		"rejected": {approved().claim(1, "g").launchClassed(1, "g", LaunchFailed, ClassRejected).ended("FAILED"), OutcomeFailed,
			[]string{"rejected", "nothing was delivered"}},
		"a host exit": {approved().claim(1, "g").launchEnded(1, "g", LaunchFailed).ended("FAILED"), OutcomeFailed,
			[]string{"host exit", "nothing was delivered"}},
		"storage trouble": {approved().claim(1, "g").launchClassed(1, "g", LaunchFailed, ClassStorage).ended("FAILED"), OutcomeFailed,
			[]string{"storage"}},
		"a cleanup failure": {approved().claim(1, "g").launchEnded(1, "g", LaunchUnresponsive).ended("KILLED"), OutcomeBlocked,
			[]string{"cleanup", "stopped"}},
		"an uncertain dispatch": {approved().claim(1, "g").launchEnded(1, "g", LaunchUnknown).ended("KILLED"), OutcomeBlocked,
			[]string{"uncertain dispatch"}},
		"a clean exit says nothing of it": {approved().claim(1, "g").launchEnded(1, "g", LaunchExited).ended("FAILED"), OutcomeFailed,
			[]string{"the run failed: nothing was delivered"}},
		"a check failure outranks a host exit": {approved().claim(1, "g").launchEnded(1, "g", LaunchFailed).
			add("phase_checked", map[string]any{"tree": "t", "passed": false, "failed": "go test: exit 1"}).ended("FAILED"), OutcomeFailed,
			[]string{"its checks failed", "go test: exit 1"}},
	} {
		t.Run(name, func(t *testing.T) {
			out, why := tc.c.replay(t).Outcome()
			assert.Equal(t, tc.out, out, why)
			for _, w := range tc.why {
				assert.Contains(t, why, w)
			}
		})
	}
	_, why := approved().claim(1, "g").launchEnded(1, "g", LaunchFailed).add("phase_checked", map[string]any{"tree": "t", "passed": false, "failed": "x"}).ended("FAILED").replay(t).Outcome()
	assert.NotContains(t, why, "host exit", "a failed check is not blamed on the host")
}
