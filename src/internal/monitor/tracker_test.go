package monitor_test

import (
	"testing"

	"github.com/b070nd/staircase-core/src/internal/monitor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTracker_Record_accumulates(t *testing.T) {
	tr := monitor.NewTracker(1, 1, "proj", "main")
	tr.Record("coder", "claude-sonnet-4-6", 100, 50)
	tr.Record("coder", "claude-sonnet-4-6", 200, 75)

	agents, _ := tr.Snapshot()
	require.Len(t, agents, 1)
	assert.Equal(t, 2, agents[0].Steps)
	assert.Equal(t, 300, agents[0].InputTokens)
	assert.Equal(t, 125, agents[0].OutputTokens)
}

func TestTracker_Record_active_flag(t *testing.T) {
	tr := monitor.NewTracker(1, 1, "proj", "main")
	tr.Record("supervisor", "claude-opus-4-6", 50, 20)
	tr.Record("coder", "claude-sonnet-4-6", 100, 40)

	agents, _ := tr.Snapshot()
	require.Len(t, agents, 2)
	assert.False(t, agents[0].Active, "supervisor should be inactive after coder runs")
	assert.True(t, agents[1].Active, "coder should be active")
}

func TestTracker_Totals(t *testing.T) {
	tr := monitor.NewTracker(1, 1, "proj", "main")
	tr.Record("supervisor", "claude-opus-4-6", 100, 30)
	tr.Record("coder", "claude-sonnet-4-6", 500, 200)

	totals := tr.Totals()
	assert.Equal(t, 2, totals.Steps)
	assert.Equal(t, 600, totals.InputTokens)
	assert.Equal(t, 230, totals.OutputTokens)
}

func TestEstimateCost_known_model(t *testing.T) {
	cost := monitor.EstimateCost("claude-sonnet-4-6", 1_000_000, 500_000)
	// $3/M input + $7.50/M output = $10.50
	assert.InDelta(t, 10.50, cost, 0.01)
}

func TestEstimateCost_unknown_model_returns_zero(t *testing.T) {
	assert.Equal(t, 0.0, monitor.EstimateCost("unknown-model-xyz", 1000000, 1000000))
}

func TestDisplay_BudgetExceeded_no_cap(t *testing.T) {
	tr := monitor.NewTracker(1, 1, "proj", "main")
	tr.Record("agent", "claude-opus-4-6", 1_000_000, 1_000_000) // very expensive
	d := monitor.NewDisplay(tr, 0)                               // no cap
	assert.False(t, d.BudgetExceeded())
}

func TestDisplay_BudgetExceeded_over_cap(t *testing.T) {
	tr := monitor.NewTracker(1, 1, "proj", "main")
	// claude-opus-4-6: $15/M in + $75/M out
	// 1M in + 1M out = $90
	tr.Record("agent", "claude-opus-4-6", 1_000_000, 1_000_000)
	d := monitor.NewDisplay(tr, 1.00) // $1 cap
	assert.True(t, d.BudgetExceeded())
}
