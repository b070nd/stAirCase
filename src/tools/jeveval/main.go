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
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
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
	base := flag.String("base", signal.DefaultBaseURL, "server URL (the gateway, or for -api systemone a local Laya such as http://127.0.0.1:8000)")
	api := flag.String("api", "", `request shape: "" for the gateway's /v1/evaluate, "systemone" for TypeSafe's and Laya's /v1/systemone`)
	out := flag.String("out", "", "also write the report to this file, to be kept as the evidence (docs/evaluation/)")
	toolVersion := flag.String("tool-version", "", "the version of this tool the numbers come from (make eval-jev passes the git revision)")
	flag.Parse()
	key := os.Getenv("SIGNAL_API_KEY") // a local server gets its own key only, never the gateway's
	if *api == signal.Gateway {
		if key = os.Getenv("LLM_GATEWAY_API_KEY"); key == "" {
			key = os.Getenv("AI_GATEWAY_API_KEY")
		}
		if key == "" {
			fmt.Fprintln(os.Stderr, "jeveval: set LLM_GATEWAY_API_KEY")
			os.Exit(2)
		}
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
	c := &signal.Client{Model: *model, Key: key, BaseURL: *base, API: *api}
	w := io.Writer(os.Stdout)
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "jeveval:", err)
			os.Exit(2)
		}
		defer func() { _ = f.Close() }()
		w = io.MultiWriter(os.Stdout, f)
	}
	m := meta{Command: "jeveval " + strings.Join(os.Args[1:], " "), Base: *base, API: *api, Tool: *toolVersion, Cases: b}
	if err := run(context.Background(), c, *model, cases, w, m); err != nil {
		fmt.Fprintln(os.Stderr, "jeveval:", err)
		os.Exit(1)
	}
}

// meta is where a report's numbers come from, kept with them.
type meta struct {
	Command, Base, API, Tool string
	Cases                    []byte // the labelled cases as they were read
}

// limitations are printed with every report: what the numbers are not.
const limitations = `- The cases are synthetic and few (see the counts above); they are not a benchmark and say nothing about other kinds of change.
- One run per model and date: no repeats, no confidence interval. The decision threshold is the fixed 0.5 the signal uses.
- The model version is whatever the server named; a hosted model can change without notice.
- The signal can only send a change to a person (see docs/approvals.md): "missed" means the change would have stayed an automatic approval, which the policy had already allowed.
`

// run evaluates every case and writes the report.
func run(ctx context.Context, ev signal.Evaluator, model string, cases []evalCase, w io.Writer, m meta) error {
	var (
		riskyOK, servesOK, kindOK, errs int
		brierRisky, brierServes, cost   float64
		latencies                       []time.Duration
		missed, flagged                 []string
		rows                            []string
	)
	for _, k := range cases {
		state := map[string]any{"stories": k.Story, "change": []map[string]any{{"path": k.Path, "before": k.Before, "after": k.After}}}
		res, err := ev.Evaluate(ctx, state, signal.ReviewQuestions)
		if err != nil {
			errs++
			fmt.Fprintf(w, "- %s: error: %v\n", k.ID, err)
			rows = append(rows, fmt.Sprintf("| %s | %s | - | %s | - | %s | - | error |", k.ID, yn(k.Risky), yn(k.Serves), k.Kind))
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
		verdict := "ok"
		if (risky >= 0.5) != k.Risky {
			verdict = map[bool]string{true: "risky missed", false: "safe flagged"}[k.Risky]
		}
		rows = append(rows, fmt.Sprintf("| %s | %s | %.2f | %s | %.2f | %s | %s | %s |", k.ID, yn(k.Risky), risky, yn(k.Serves), serves, k.Kind, res.Answers["kind"].Choice, verdict))
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
	risky := 0
	for _, k := range cases {
		if k.Risky {
			risky++
		}
	}
	sum := sha256.Sum256(m.Cases)
	fmt.Fprintf(w, "- command: `%s`\n- server: %s (API %q), tool version %s, Go %s\n- cases: %d (%d labelled risky), sha256 %x\n\n",
		m.Command, m.Base, m.API, orNone([]string{m.Tool}), runtime.Version(), len(cases), risky, sum)
	fmt.Fprintf(w, "| Question | Accuracy | Brier (lower is better) |\n|---|---|---|\n")
	fmt.Fprintf(w, "| risky | %d/%d | %.3f |\n| serves_story | %d/%d | %.3f |\n| kind | %d/%d | - |\n\n",
		riskyOK, n, brierRisky/float64(n), servesOK, n, brierServes/float64(n), kindOK, n)
	fmt.Fprintf(w, "- risky changes missed (would not reach a person): %s\n", orNone(missed))
	fmt.Fprintf(w, "- safe changes flagged (a person asked needlessly): %s\n", orNone(flagged))
	fmt.Fprintf(w, "\n| case | risky (label) | risky (p) | serves (label) | serves (p) | kind (label) | kind (model) | result |\n|---|---|---|---|---|---|---|---|\n%s\n\n", strings.Join(rows, "\n"))
	fmt.Fprintf(w, "- latency p50 %s, p95 %s; cost $%.6f total; errors %d\n",
		pct(0.5).Round(time.Millisecond), pct(0.95).Round(time.Millisecond), cost, errs)
	fmt.Fprintf(w, "\n### Limitations\n\n%s", limitations)
	return nil
}

func yn(b bool) string { return map[bool]string{true: "yes", false: "no"}[b] }

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
