// Package harness provides test infrastructure for stAirCase integration and
// E2E tests.  It contains test doubles, fixture loaders, and helpers that are
// used across L3–L5 tests.
package harness

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"
)

// DecisionStrategy controls how FakeOperator decides on yield requests.
type DecisionStrategy int

const (
	// ApproveAll approves every yield request. Use for happy-path tests.
	ApproveAll DecisionStrategy = iota
	// RejectAll rejects every yield request. Use for refusal-path tests.
	RejectAll
	// Pattern approves if the yield payload matches a regex, rejects otherwise.
	Pattern
	// Scripted uses a pre-determined list of decisions in order.
	// Panics if more yields arrive than Script entries.
	Scripted
	// Idle never decides. Use to test yield timeouts on the orchestrator side.
	Idle
	// Slow waits for SlowDelay before deciding according to BaseStrategy.
	Slow
)

// Decision is one operator decision: approve/reject + optional note.
type Decision struct {
	Approved bool
	Feedback string
}

// FakeOperator is an http.Handler that speaks the stAirCase webhook yield
// protocol (§3 IPC / webhook approval).  Wire it to an httptest.Server and
// pass the server URL as the project's webhook URL.
//
// Example:
//
//	op := &harness.FakeOperator{Strategy: harness.ApproveAll}
//	srv := httptest.NewServer(op)
//	defer srv.Close()
//	store.UpdateProjectWebhook(p.ID, srv.URL)
type FakeOperator struct {
	// Strategy controls the decision logic.  Default is ApproveAll.
	Strategy DecisionStrategy
	// Pattern is the regex used when Strategy == Pattern.
	PatternRx *regexp.Regexp
	// Script is the ordered list of decisions used when Strategy == Scripted.
	Script []Decision
	// SlowDelay is the synthetic think-time used when Strategy == Slow.
	SlowDelay time.Duration
	// BaseStrategy is the underlying strategy used after SlowDelay when
	// Strategy == Slow.  Defaults to ApproveAll.
	BaseStrategy DecisionStrategy

	mu       sync.Mutex
	scriptIdx int
	// Received holds the raw yield payloads for assertion in tests.
	Received []map[string]any
}

// ServeHTTP handles a webhook yield_request POST.
// Request body: JSON IpcYieldRequest
// Response body: JSON IpcYieldResponse
func (f *FakeOperator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.Received = append(f.Received, body)
	f.mu.Unlock()

	if f.Strategy == Idle {
		// Never respond — let the orchestrator time out.
		select {}
	}

	if f.Strategy == Slow {
		time.Sleep(f.SlowDelay)
	}

	dec := f.decide(body)

	resp := map[string]any{
		"type":     "yield_response",
		"approved": dec.Approved,
		"feedback": dec.Feedback,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// decide picks a Decision given the decoded yield body.
func (f *FakeOperator) decide(body map[string]any) Decision {
	strategy := f.Strategy
	if strategy == Slow {
		strategy = f.BaseStrategy
	}

	switch strategy {
	case RejectAll:
		return Decision{Approved: false, Feedback: "fake-operator: rejected by policy"}

	case Pattern:
		raw, _ := json.Marshal(body)
		if f.PatternRx != nil && f.PatternRx.Match(raw) {
			return Decision{Approved: true, Feedback: "fake-operator: pattern matched"}
		}
		return Decision{Approved: false, Feedback: "fake-operator: pattern not matched"}

	case Scripted:
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.scriptIdx >= len(f.Script) {
			panic(fmt.Sprintf("FakeOperator(Scripted): more yields (%d) than Script entries (%d)",
				f.scriptIdx+1, len(f.Script)))
		}
		dec := f.Script[f.scriptIdx]
		f.scriptIdx++
		return dec

	default: // ApproveAll
		return Decision{Approved: true, Feedback: "fake-operator: auto-approved"}
	}
}

// ReceivedCount returns the number of yield requests received so far.
// Safe to call concurrently.
func (f *FakeOperator) ReceivedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Received)
}

// NewFakeOperatorServer creates a FakeOperator with the given strategy and
// starts an httptest.Server.  The server is closed via t.Cleanup.
// Returns the operator (for assertions) and the server URL.
func NewFakeOperatorServer(t *testing.T, strategy DecisionStrategy) (*FakeOperator, string) {
	t.Helper()
	op := &FakeOperator{Strategy: strategy}
	srv := httptest.NewServer(op)
	t.Cleanup(srv.Close)
	return op, srv.URL
}
