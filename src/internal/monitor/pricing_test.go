package monitor_test

import (
	"testing"

	"github.com/b070nd/stAirCase/src/internal/monitor"
	"github.com/stretchr/testify/assert"
)

// TestEstimateCost_published_prices: what a million tokens in and a million out
// cost, from the providers' price pages as read on 2026-10-05 (see pricing.go).
// A model's price must not be below what its provider charges: a budget cap that
// counts too little stops nothing.
func TestEstimateCost_published_prices(t *testing.T) {
	for model, want := range map[string]float64{
		"claude-fable-5-1":  10 + 50,
		"claude-opus-5-5":   4 + 20,
		"claude-opus-4-8":   5 + 25,
		"claude-opus-4-6":   5 + 25,
		"claude-opus-4-1":   15 + 75,
		"claude-sonnet-5-5": 2 + 10,
		"claude-sonnet-4-6": 3 + 15,
		"claude-haiku-4-5":  1 + 5,
		"claude-haiku-3-5":  0.8 + 4,
		"gpt-5.5":           5 + 30,
		"gpt-5.4-mini":      0.75 + 4.5,
		"gpt-5":             1.25 + 10,
		"o3":                2 + 8,
		"o1-pro":            150 + 600,
		"gpt-4o-mini":       0.15 + 0.6,
		"gemini-3.5-flash":  1.5 + 9,
		"gemini-2.5-pro":    2.5 + 15, // the tier above 200k tokens: a cap counts the dearer rate
	} {
		assert.InDelta(t, want, monitor.EstimateCost(model, 1_000_000, 1_000_000), 0.0001, model)
	}
}

// TestEstimateCost_model_names: a dated or "-latest" name, and a gateway name
// (provider/model), cost what the model costs.
func TestEstimateCost_model_names(t *testing.T) {
	haiku := monitor.EstimateCost("claude-haiku-4-5", 1_000_000, 1_000_000)
	assert.InDelta(t, 6.0, haiku, 0.0001)
	for _, name := range []string{"claude-haiku-4-5-20251001", "anthropic/claude-haiku-4-5-20251001", "claude-haiku-4-5-latest", "vercel/claude-haiku-4-5"} {
		assert.InDelta(t, haiku, monitor.EstimateCost(name, 1_000_000, 1_000_000), 0.0001, name)
	}
}

// TestEstimateCost_unpriced_models_are_not_cheaper_than_priced_ones: a model this
// table does not know counts at a ceiling above every model it does (the
// specially priced pro reasoning models aside), so it cannot slip under a cap.
func TestEstimateCost_unpriced_models_are_not_cheaper_than_priced_ones(t *testing.T) {
	ceiling := monitor.EstimateCost("a-model-nobody-has-heard-of", 1_000_000, 1_000_000)
	for _, model := range []string{"claude-fable-5-1", "claude-opus-4-1", "gpt-6-astra", "o1", "gpt-5.5", "gemini-3.1-pro-preview"} {
		assert.GreaterOrEqual(t, ceiling, monitor.EstimateCost(model, 1_000_000, 1_000_000), model)
	}
}
