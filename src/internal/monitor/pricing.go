package monitor

import (
	"regexp"
	"strings"
)

// ModelPricing holds input/output cost per 1M tokens in USD (approximate).
type ModelPricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

// pricingTable - read from the providers' price pages on 2026-10-05:
//   - Anthropic: https://platform.claude.com/docs/en/about-claude/pricing
//   - OpenAI:    https://developers.openai.com/api/docs/pricing
//   - Google:    https://ai.google.dev/gemini-api/docs/pricing
//
// Standard (non-batch, non-cached) rates. Where a price depends on the prompt's
// size or a promotion ends, the higher rate is used: a budget cap that counts
// too little stops nothing. xAI's entries and the retired Gemini 1.5/2.0 ones
// were not on a page read that day. Re-read the pages when a provider ships
// a model; a model that is not here counts at unpricedCeiling.
var pricingTable = map[string]ModelPricing{
	// Anthropic Claude
	"claude-fable-5-1":  {10.00, 50.00},
	"claude-fable-5":    {10.00, 50.00},
	"claude-mythos-5-1": {10.00, 50.00},
	"claude-mythos-5":   {10.00, 50.00},
	"claude-opus-5-5":   {4.00, 20.00},
	"claude-opus-5":     {5.00, 25.00},
	"claude-opus-4-8":   {5.00, 25.00},
	"claude-opus-4-7":   {5.00, 25.00},
	"claude-opus-4-6":   {5.00, 25.00},
	"claude-opus-4-5":   {5.00, 25.00},
	"claude-opus-4-1":   {15.00, 75.00},
	"claude-opus-4":     {15.00, 75.00},
	"claude-sonnet-5-5": {2.00, 10.00},
	"claude-sonnet-5":   {2.00, 10.00},
	"claude-sonnet-4-6": {3.00, 15.00},
	"claude-sonnet-4-5": {3.00, 15.00},
	"claude-sonnet-4":   {3.00, 15.00},
	"claude-haiku-4-5":  {1.00, 5.00},
	"claude-haiku-3-5":  {0.80, 4.00},
	// OpenAI
	"gpt-6-astra":   {10.00, 50.00},
	"gpt-6.1-sol":   {2.00, 10.00},
	"gpt-6-sol":     {2.00, 10.00},
	"gpt-6-luna":    {0.10, 0.50},
	"gpt-5.6-sol":   {4.00, 20.00},
	"gpt-5.6-terra": {2.00, 12.00},
	"gpt-5.6-luna":  {0.20, 1.20},
	"gpt-5.5":       {5.00, 30.00},
	"gpt-5.4":       {2.50, 15.00},
	"gpt-5.4-mini":  {0.75, 4.50},
	"gpt-5.4-nano":  {0.20, 1.25},
	"gpt-5.2":       {1.75, 14.00},
	"gpt-5.1":       {1.25, 10.00},
	"gpt-5":         {1.25, 10.00},
	"gpt-5-mini":    {0.25, 2.00},
	"gpt-5-nano":    {0.05, 0.40},
	"o1":            {15.00, 60.00},
	"o1-pro":        {150.00, 600.00},
	"o3-pro":        {20.00, 80.00},
	"o3":            {2.00, 8.00},
	"o4-mini":       {1.10, 4.40},
	"o3-mini":       {1.10, 4.40},
	"gpt-4o":        {2.50, 10.00},
	"gpt-4o-mini":   {0.15, 0.60},
	"gpt-4-turbo":   {10.00, 30.00},
	"gpt-3.5-turbo": {0.50, 1.50},
	// Google Gemini (the 3.6 to 3.8 flash models are cheaper until 2026-12-31; the later price is used)
	"gemini-3.8-flash":       {1.50, 7.50},
	"gemini-3.7-flash":       {1.50, 7.50},
	"gemini-3.6-flash":       {1.50, 7.50},
	"gemini-3.5-flash":       {1.50, 9.00},
	"gemini-3.5-flash-lite":  {0.30, 2.50},
	"gemini-3.1-flash-lite":  {0.25, 1.50},
	"gemini-3.1-pro-preview": {4.00, 18.00}, // above 200k tokens; 2.00/12.00 below
	"gemini-2.5-pro":         {2.50, 15.00}, // above 200k tokens; 1.25/10.00 below
	"gemini-2.5-flash":       {0.30, 2.50},
	"gemini-2.5-flash-lite":  {0.10, 0.40},
	"gemini-2.0-flash":       {0.10, 0.40},
	"gemini-1.5-pro":         {1.25, 5.00},
	"gemini-1.5-flash":       {0.075, 0.30},
	// xAI Grok
	"grok-3":      {3.00, 15.00},
	"grok-3-mini": {0.30, 0.50},
}

// unpricedCeiling is what a model that is not in the table counts at: above every
// model in it except the specially priced pro reasoning models, which are listed.
var unpricedCeiling = ModelPricing{InputPer1M: 15.00, OutputPer1M: 75.00}

// datedName is the end of a model name that pins a release, "-20251001", or
// follows the newest, "-latest".
var datedName = regexp.MustCompile(`(-\d{8}|-latest)$`)

// EstimateCost returns the approximate USD cost. A gateway name
// ("provider/model") is priced by its model, and a dated or "-latest" name by the
// model it pins; a model without a price counts at unpricedCeiling, so an
// unpriced model never slips past a budget cap.
func EstimateCost(model string, inputTokens, outputTokens int) float64 {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	p, ok := pricingTable[model]
	if !ok {
		p, ok = pricingTable[datedName.ReplaceAllString(model, "")]
	}
	if !ok {
		p = unpricedCeiling
	}
	return float64(inputTokens)/1_000_000*p.InputPer1M +
		float64(outputTokens)/1_000_000*p.OutputPer1M
}
