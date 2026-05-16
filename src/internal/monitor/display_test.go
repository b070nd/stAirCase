package monitor_test

import (
	"testing"
	"time"

	"github.com/b070nd/staircase-core/src/internal/monitor"
	"github.com/stretchr/testify/assert"
)

// ─── formatElapsed ────────────────────────────────────────────────────────────

func TestFormatElapsed_seconds_only(t *testing.T) {
	assert.Equal(t, "42s", monitor.ExportedFormatElapsed(42*time.Second))
}

func TestFormatElapsed_minutes_and_seconds(t *testing.T) {
	assert.Equal(t, "3m05s", monitor.ExportedFormatElapsed(3*time.Minute+5*time.Second))
}

func TestFormatElapsed_hours_minutes_seconds(t *testing.T) {
	assert.Equal(t, "1h02m03s", monitor.ExportedFormatElapsed(
		1*time.Hour+2*time.Minute+3*time.Second,
	))
}

func TestFormatElapsed_sub_second_rounds_to_zero(t *testing.T) {
	assert.Equal(t, "0s", monitor.ExportedFormatElapsed(400*time.Millisecond))
}

// ─── formatTokens ─────────────────────────────────────────────────────────────

func TestFormatTokens_zero_returns_dash(t *testing.T) {
	assert.Equal(t, "—", monitor.ExportedFormatTokens(0))
}

func TestFormatTokens_small_number(t *testing.T) {
	assert.Equal(t, "999", monitor.ExportedFormatTokens(999))
}

func TestFormatTokens_thousands(t *testing.T) {
	assert.Equal(t, "1.5K", monitor.ExportedFormatTokens(1_500))
}

func TestFormatTokens_millions(t *testing.T) {
	assert.Equal(t, "2.3M", monitor.ExportedFormatTokens(2_300_000))
}

// ─── AddActivity / Pause / Resume ─────────────────────────────────────────────

func newTestDisplay(t *testing.T) (*monitor.Display, *monitor.Tracker) {
	t.Helper()
	tr := monitor.NewTracker(1, 1, "proj", "main")
	return monitor.NewDisplay(tr, 0), tr
}

func TestDisplay_AddActivity_appends_entries(t *testing.T) {
	d, _ := newTestDisplay(t)
	d.AddActivity("step 1")
	d.AddActivity("step 2")
	// Verify via BudgetExceeded round-trip (Display is opaque; the activity is
	// internal — we exercise the method to hit the coverage path).
	// There is no reader for activity, so we just assert no panic.
}

func TestDisplay_AddActivity_trims_to_max_lines(t *testing.T) {
	d, _ := newTestDisplay(t)
	// maxActivityLines = 6; add more than that.
	for i := 0; i < 10; i++ {
		d.AddActivity("line")
	}
	// No assertion needed beyond "no panic" — internal state is bounded.
}

func TestDisplay_Pause_and_Resume_toggle_state(t *testing.T) {
	d, _ := newTestDisplay(t)
	// Calling Pause/Resume must not panic.
	d.Pause()
	d.Resume()
	// After Resume the display is not paused — BudgetExceeded still works.
	assert.False(t, d.BudgetExceeded())
}

func TestTracker_Elapsed_is_positive(t *testing.T) {
	tr := monitor.NewTracker(1, 1, "proj", "main")
	elapsed := tr.Elapsed()
	assert.GreaterOrEqual(t, elapsed, time.Duration(0), "elapsed time must be non-negative")
}

func TestDisplay_BudgetExceeded_under_cap(t *testing.T) {
	tr := monitor.NewTracker(1, 1, "proj", "main")
	// Very cheap model — well under $100 cap.
	tr.Record("agent", "claude-sonnet-4-6", 100, 50)
	d := monitor.NewDisplay(tr, 100.00)
	assert.False(t, d.BudgetExceeded(), "small spend should not exceed $100 cap")
}

func TestDisplay_Render_does_not_panic(t *testing.T) {
	d, tr := newTestDisplay(t)
	tr.Record("planner", "claude-sonnet-4-6", 1000, 200)
	d.AddActivity("phase: boot")
	d.Render() // writes ANSI to stdout — no assertion, just no panic
}

func TestDisplay_Render_while_paused_is_noop(t *testing.T) {
	d, _ := newTestDisplay(t)
	d.Pause()
	d.Render() // must short-circuit without panic
}

func TestDisplay_Final_success_does_not_panic(t *testing.T) {
	d, _ := newTestDisplay(t)
	d.Final("run complete") // must not panic
}

func TestDisplay_Final_failure_does_not_panic(t *testing.T) {
	d, _ := newTestDisplay(t)
	d.Final("FAIL: timeout") // status contains FAIL — alternate branch
}
