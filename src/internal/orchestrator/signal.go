package orchestrator

import (
	"context"
	"fmt"
	"github.com/b070nd/stAirCase/src/internal/history"

	"github.com/b070nd/stAirCase/src/internal/signal"
)

// Signal is a decision model (such as typesafe-ai/jev) asked about every
// change approved without a person. It can only send the change to a person:
// when it rates it risky, doubts it serves a story, or cannot answer.
type Signal struct {
	Model string
	// URL, when set, is a TypeSafe-compatible server (such as a local Laya at
	// http://127.0.0.1:8000) instead of the gateway; the change is sent there.
	URL   string
	Eval  signal.Evaluator // nil: built from the gateway or URL
	brief string
}

// signalThreshold: a probability at or past it counts.
// Fixed for now; calibrate it per project once there are local numbers (ROADMAP phase 3).
const signalThreshold = 0.5

// escalate returns why a person must decide the change, or "".
func (s *Signal) escalate(ctx context.Context, before, after map[string]*approvedFile, audit func(history.Event) error) string {
	change, err := changeFiles(before, after)
	if err != nil {
		return "signal: " + err.Error() + " - a person decides"
	}
	if s.Eval == nil {
		return s.Model + " unavailable (no key) - a person decides"
	}
	res, err := s.Eval.Evaluate(ctx, map[string]any{"stories": s.brief, "change": change}, signal.ReviewQuestions)
	if err != nil {
		_ = audit(history.SignalFailed{Model: s.Model, Error: err.Error()})
		return fmt.Sprintf("%s unavailable (%v) - a person decides", s.Model, err)
	}
	risky, serves := res.Answers["risky"].Probability, res.Answers["serves_story"].Probability
	_ = audit(history.SignalRated{Model: s.Model, Risky: risky, ServesStory: serves, Kind: res.Answers["kind"].Choice, CostUSD: res.Cost})
	switch {
	case risky >= signalThreshold:
		return fmt.Sprintf("%s rates this change risky (%.2f) - a person decides", s.Model, risky)
	case serves < signalThreshold:
		return fmt.Sprintf("%s doubts it serves a story (%.2f) - a person decides", s.Model, serves)
	}
	return ""
}
