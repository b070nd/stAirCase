package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/b070nd/stAirCase/src/internal/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rater answers by the changed file's name.
type rater struct {
	mu   sync.Mutex
	seen []string
}

func (r *rater) Evaluate(_ context.Context, state any, _ map[string]signal.Question) (signal.Result, error) {
	b, _ := json.Marshal(state)
	r.mu.Lock()
	r.seen = append(r.seen, string(b))
	r.mu.Unlock()
	a := func(risky, serves float64) signal.Result {
		return signal.Result{Answers: map[string]signal.Answer{"risky": {Probability: risky}, "serves_story": {Probability: serves}, "kind": {Choice: "feature"}}}
	}
	switch {
	case strings.Contains(string(b), "upload.sh"):
		return a(0.91, 0.8), nil
	case strings.Contains(string(b), "unrelated.txt"):
		return a(0.05, 0.1), nil
	case strings.Contains(string(b), "flaky.txt"):
		return signal.Result{}, errors.New("HTTP 403")
	}
	return a(0.02, 0.97), nil
}

// TestSignal_only_sends_changes_to_a_person: a decision model's signal
// turns an automatic approval into a person's decision when it rates the
// change risky or off the stories, or cannot answer; it never approves or
// rejects anything itself. Its ratings are recorded.
func TestSignal_only_sends_changes_to_a_person(t *testing.T) {
	op := &operator{approve: true}
	rt := &rater{}
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, wsDir string, projectID int64) { webhook(t, s, projectID, op) },
		Run:   orchestrator.RunOptions{Signal: &orchestrator.Signal{Model: "typesafe-ai/jev", Eval: rt}},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			for _, f := range []string{"ok.txt", "upload.sh", "unrelated.txt", "flaky.txt"} {
				create(ctx, env, f)
			}
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Equal(t, []string{"file_edit:policy", "file_edit:operator", "file_edit:operator", "file_edit:operator"}, sources(t, r))
	require.Len(t, op.seen, 3)
	assert.Contains(t, op.seen[0].Review, "risky (0.91)")
	assert.Contains(t, op.seen[1].Review, "serves a story (0.10)")
	assert.Contains(t, op.seen[2].Review, "unavailable")
	assert.Contains(t, rt.seen[0], `"after":"x\n"`, "it sees the derived change")
	n := 0
	for _, e := range r.Types() {
		if e == "signal_rated" {
			n++
		}
	}
	assert.Equal(t, 4, n)
}

// TestSignal_url_talks_to_a_local_server: --signal-url asks a
// TypeSafe-compatible server (a local Laya) in its own shape, sends no key it
// was not given, and its answers turn an automatic approval into a person's
// decision just like the gateway's.
func TestSignal_url_talks_to_a_local_server(t *testing.T) {
	var auth []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		auth = append(auth, r.URL.Path+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		risky := 0.02
		if strings.Contains(string(body), "upload.sh") {
			risky = 0.9
		}
		_, _ = fmt.Fprintf(w, `{"model":"laya","answers":{"risky":{"noul":%v},"serves_story":{"noul":0.9},"kind":{"choice":"feature","probabilities":{"feature":1},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`, risky)
	}))
	defer srv.Close()
	op := &operator{approve: true}
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, wsDir string, projectID int64) { webhook(t, s, projectID, op) },
		Run:   orchestrator.RunOptions{Signal: &orchestrator.Signal{Model: "laya", URL: srv.URL}},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			for _, f := range []string{"ok.txt", "upload.sh"} {
				create(ctx, env, f)
			}
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Equal(t, []string{"file_edit:policy", "file_edit:operator"}, sources(t, r))
	require.Len(t, op.seen, 1)
	assert.Contains(t, op.seen[0].Review, "laya rates this change risky (0.90)")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"/v1/systemone ", "/v1/systemone "}, auth, "its own path, and no key")
}

// TestSignal_a_wrong_late_or_invalid_answer_only_sends_the_change_to_a_person: a model that answers with a missing or
// out-of-range probability, something that is not an answer, nothing in time or an error, never lets a change through
// that a person would not have seen; only an answer that is valid and calm leaves the automatic approval in place. The
// model can send a change to a person, never approve or reject it.
func TestSignal_a_wrong_late_or_invalid_answer_only_sends_the_change_to_a_person(t *testing.T) {
	calm := `{"answers":{"risky":{"noul":0.02},"serves_story":{"noul":0.97},"kind":{"choice":"feature"}}}`
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(string(body), "missing.txt"):
			_, _ = w.Write([]byte(`{"answers":{"risky":{},"serves_story":{"noul":0.97},"kind":{"choice":"feature"}}}`))
		case strings.Contains(string(body), "range.txt"):
			_, _ = w.Write([]byte(`{"answers":{"risky":{"noul":-3},"serves_story":{"noul":0.97},"kind":{"choice":"feature"}}}`))
		case strings.Contains(string(body), "garbage.txt"):
			_, _ = w.Write([]byte(`<html>try again later</html>`))
		case strings.Contains(string(body), "late.txt"):
			select {
			case <-release:
			case <-r.Context().Done():
			}
		case strings.Contains(string(body), "denied.txt"):
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			_, _ = w.Write([]byte(calm))
		}
	}))
	defer srv.Close()
	defer close(release)
	op := &operator{approve: true}
	files := []string{"ok.txt", "missing.txt", "range.txt", "garbage.txt", "late.txt", "denied.txt"}
	r := runtest.Run(t, runtest.Options{
		Setup: func(s *persistence.Store, wsDir string, projectID int64) { webhook(t, s, projectID, op) },
		Run: orchestrator.RunOptions{Signal: &orchestrator.Signal{Model: "laya", Eval: &signal.Client{Model: "laya", BaseURL: srv.URL,
			API: signal.SystemOne, HTTP: &http.Client{Timeout: 300 * time.Millisecond}}}},
		Agent: orchestrator.AgentFunc(func(ctx context.Context, env *orchestrator.AgentEnv) error {
			for _, f := range files {
				create(ctx, env, f)
			}
			return nil
		})})
	require.NoError(t, r.Err)
	assert.Equal(t, []string{"file_edit:policy", "file_edit:operator", "file_edit:operator", "file_edit:operator", "file_edit:operator", "file_edit:operator"}, sources(t, r))
	require.Len(t, op.seen, 5, "everything but the valid, calm answer reached a person")
	for _, s := range op.seen {
		assert.Contains(t, s.Review, "laya unavailable", "it says the model could not be used, not that the change is fine: %s", s.Review)
	}
	assert.Contains(t, op.seen[0].Review, "no probability")
	assert.Contains(t, op.seen[1].Review, "not within 0 and 1")
}
