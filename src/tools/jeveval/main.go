// Command jeveval measures an evaluation model (such as typesafe-ai/jev) on
// labelled code changes with the questions stAirCase's signal asks
// (signal.ReviewQuestions): accuracy, calibration (Brier score), missed
// risky changes, latency and cost. It prints a Markdown report.
//
//	LLM_GATEWAY_API_KEY=… go run ./src/tools/jeveval [-model typesafe-ai/jev] [-cases file]
//
// Every case is synthetic; nothing from a real repository is sent.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/b070nd/stAirCase/src/internal/signal"
)

//go:embed cases.json
var defaultCases []byte

type evalCase struct {
	ID     string  `json:"id"`
	Story  string  `json:"story"`
	Path   string  `json:"path"`
	Before *string `json:"before"`
	After  *string `json:"after"`
	Risky  bool    `json:"risky"`
	Serves bool    `json:"serves"`
	Kind   string  `json:"kind"`
}

func main() {
	model := flag.String("model", "typesafe-ai/jev", "evaluation model on the gateway")
	casesFile := flag.String("cases", "", "labelled cases (default: the built-in set)")
	base := flag.String("base", signal.DefaultBaseURL, "gateway URL")
	flag.Parse()
	key := os.Getenv("LLM_GATEWAY_API_KEY")
	if key == "" {
		key = os.Getenv("AI_GATEWAY_API_KEY")
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "jeveval: set LLM_GATEWAY_API_KEY")
		os.Exit(2)
	}
	b := defaultCases
	if *casesFile != "" {
		var err error
		if b, err = os.ReadFile(*casesFile); err != nil {
			fmt.Fprintln(os.Stderr, "jeveval:", err)
			os.Exit(2)
		}
	}
	var cases []evalCase
	if err := json.Unmarshal(b, &cases); err != nil {
		fmt.Fprintln(os.Stderr, "jeveval:", err)
		os.Exit(2)
	}
	c := &signal.Client{Model: *model, Key: key, BaseURL: *base}
	if err := run(context.Background(), c, *model, cases, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "jeveval:", err)
		os.Exit(1)
	}
}

// run evaluates every case and writes the report.
func run(ctx context.Context, ev signal.Evaluator, model string, cases []evalCase, w io.Writer) error {
	var (
		riskyOK, servesOK, kindOK, errs int
		brierRisky, brierServes, cost   float64
		latencies                       []time.Duration
		missed, flagged                 []string
	)
	for _, k := range cases {
		state := map[string]any{"stories": k.Story, "change": []map[string]any{{"path": k.Path, "before": k.Before, "after": k.After}}}
		res, err := ev.Evaluate(ctx, state, signal.ReviewQuestions)
		if err != nil {
			errs++
			fmt.Fprintf(w, "- %s: error: %v\n", k.ID, err)
			continue
		}
		risky, serves := res.Answers["risky"].Probability, res.Answers["serves_story"].Probability
		if (risky >= 0.5) == k.Risky {
			riskyOK++
		} else if k.Risky {
			missed = append(missed, fmt.Sprintf("%s (%.2f)", k.ID, risky))
		} else {
			flagged = append(flagged, fmt.Sprintf("%s (%.2f)", k.ID, risky))
		}
		if (serves >= 0.5) == k.Serves {
			servesOK++
		}
		if res.Answers["kind"].Choice == k.Kind {
			kindOK++
		}
		brierRisky += sq(risky - b2f(k.Risky))
		brierServes += sq(serves - b2f(k.Serves))
		cost += res.Cost
		latencies = append(latencies, res.Latency)
	}
	n := len(latencies)
	if n == 0 {
		return fmt.Errorf("no case could be evaluated (%d errors)", errs)
	}
	slices.Sort(latencies)
	pct := func(p float64) time.Duration { return latencies[min(n-1, int(p*float64(n)))] }
	fmt.Fprintf(w, "\n## %s on %d labelled changes (%s)\n\n", model, len(cases), time.Now().UTC().Format("2006-01-02"))
	fmt.Fprintf(w, "| Question | Accuracy | Brier (lower is better) |\n|---|---|---|\n")
	fmt.Fprintf(w, "| risky | %d/%d | %.3f |\n| serves_story | %d/%d | %.3f |\n| kind | %d/%d | - |\n\n",
		riskyOK, n, brierRisky/float64(n), servesOK, n, brierServes/float64(n), kindOK, n)
	fmt.Fprintf(w, "- risky changes missed (would not reach a person): %s\n", orNone(missed))
	fmt.Fprintf(w, "- safe changes flagged (a person asked needlessly): %s\n", orNone(flagged))
	fmt.Fprintf(w, "- latency p50 %s, p95 %s; cost $%.6f total; errors %d\n",
		pct(0.5).Round(time.Millisecond), pct(0.95).Round(time.Millisecond), cost, errs)
	return nil
}

func sq(x float64) float64 { return x * x }

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}
