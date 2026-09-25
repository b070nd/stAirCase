package monitor

import "strings"

// ModelPricing holds input/output cost per 1M tokens in USD (approximate).
type ModelPricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

// pricingTable — prices approximate, last updated 2026-03-25.
var pricingTable = map[string]ModelPricing{
	// Anthropic Claude
	"claude-opus-4-6":   {15.00, 75.00},
	"claude-sonnet-4-6": {3.00, 15.00},
	"claude-haiku-4-6":  {0.80, 4.00},
	"claude-opus-4-5":   {15.00, 75.00},
	"claude-sonnet-4-5": {3.00, 15.00},
	"claude-haiku-4-5":  {0.80, 4.00},
	// OpenAI
	"gpt-4o":      {2.50, 10.00},
	"gpt-4o-mini": {0.15, 0.60},
	"o1":          {15.00, 60.00},
	"o1-mini":     {3.00, 12.00},
	"o3":          {10.00, 40.00},
	"o3-mini":     {1.10, 4.40},
	"o4-mini":     {1.10, 4.40},
	// Google Gemini
	"gemini-2.0-flash": {0.10, 0.40},
	"gemini-2.5-pro":   {1.25, 10.00},
	"gemini-1.5-pro":   {1.25, 5.00},
	"gemini-1.5-flash": {0.075, 0.30},
	// xAI Grok
	"grok-3":      {3.00, 15.00},
	"grok-3-mini": {0.30, 0.50},
}

// EstimateCost returns the approximate USD cost. A gateway name
// ("provider/model") is priced by its model; a model without a price counts at
// the highest known rates, so an unpriced model never slips past a budget cap.
// ponytail: conservative ceiling; add pricing overrides if it over-trips.
func EstimateCost(model string, inputTokens, outputTokens int) float64 {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	p, ok := pricingTable[model]
	if !ok {
		for _, q := range pricingTable {
			p.InputPer1M, p.OutputPer1M = max(p.InputPer1M, q.InputPer1M), max(p.OutputPer1M, q.OutputPer1M)
		}
	}
	return float64(inputTokens)/1_000_000*p.InputPer1M +
		float64(outputTokens)/1_000_000*p.OutputPer1M
}
