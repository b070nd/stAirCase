package agent

import (
	"strings"
	"testing"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
)

// TestWithContinuation: a first segment's prompt is untouched; a continuation's is followed by what the audit chain says.
func TestWithContinuation(t *testing.T) {
	if got := withContinuation("task", &orchestrator.AgentEnv{}); got != "task" {
		t.Errorf("a first segment's prompt changed: %q", got)
	}
	if got := withContinuation("task", nil); got != "task" {
		t.Errorf("no env: %q", got)
	}
	got := withContinuation("task", &orchestrator.AgentEnv{Continuation: "you are continuing"})
	if got != "task\n\nyou are continuing" {
		t.Errorf("a continuation's prompt: %q", got)
	}
}

// TestWithContinuation_puts_the_away_brief_first: an away run's brief comes before the task in the agent's first message.
func TestWithContinuation_puts_the_away_brief_first(t *testing.T) {
	got := withContinuation("add a test", &orchestrator.AgentEnv{Brief: "BRIEF"})
	if !strings.HasPrefix(got, "BRIEF\n\n") || !strings.Contains(got, "add a test") {
		t.Fatalf("got %q", got)
	}
}
