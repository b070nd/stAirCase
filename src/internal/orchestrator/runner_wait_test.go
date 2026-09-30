package orchestrator_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b070nd/stAirCase/src/internal/domain"
	"github.com/b070nd/stAirCase/src/internal/orchestrator/runtest"
	"github.com/b070nd/stAirCase/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowPerson is a webhook whose person takes a while: it tells the test when
// it was asked, then answers approve after wait (or never, when wait is 0 and
// the test ends first).
func slowPerson(t *testing.T, wait time.Duration) (srv *httptest.Server, asked <-chan struct{}) {
	ch := make(chan struct{}, 4)
	release := make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ch <- struct{}{}
		var after <-chan time.Time
		if wait > 0 {
			after = time.After(wait)
		}
		select {
		case <-after:
		case <-release:
			return
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(domain.YieldResponse{Type: "yield_response", Approved: true, Feedback: "at last"})
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs first: lets a waiting handler go
	return srv, ch
}

func waitingRun(t *testing.T, ctx context.Context, person *httptest.Server, policy string) runtest.Result {
	return runtest.Run(t, runtest.Options{Ctx: ctx, Agent: writeFile("health.txt", "ok\n"),
		Setup: func(st *persistence.Store, wsDir string, projectID int64) {
			require.NoError(t, os.WriteFile(filepath.Join(wsDir, "policy.json"), []byte(policy), 0o600)) // people decide
			require.NoError(t, st.UpdateProjectWebhook(projectID, person.URL))
		}})
}

// TestRun_cancelling_reaches_a_run_waiting_for_a_person: a run blocked on a
// person's answer (here a webhook that never replies) ends when it is
// cancelled, KILLED, with nothing approved or committed (F98).
func TestRun_cancelling_reaches_a_run_waiting_for_a_person(t *testing.T) {
	person, asked := slowPerson(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-asked; cancel() }()

	start := time.Now()
	r := waitingRun(t, ctx, person, `{"rules":[]}`)
	assert.Less(t, time.Since(start), 10*time.Second, "the run kept waiting for a person after it was cancelled")
	assert.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	assert.Empty(t, r.Run.GitCommitHash)
	for _, e := range r.Events {
		assert.NotContains(t, e.Payload, `"approved":true`)
	}
}

// TestRun_an_answer_after_the_run_limit_approves_nothing: the person answers
// only after max_run_secs has passed; the run is over, so that approval is not
// recorded or released (F98).
func TestRun_an_answer_after_the_run_limit_approves_nothing(t *testing.T) {
	person, _ := slowPerson(t, 2500*time.Millisecond)
	r := waitingRun(t, context.Background(), person, `{"rules":[],"limits":{"max_run_secs":1}}`)
	assert.Equal(t, persistence.RunStatusKilled, r.Run.Status)
	assert.Empty(t, r.Run.GitCommitHash)
	for _, e := range r.Events {
		if e.EventType == "yield_decided" {
			assert.NotContains(t, e.Payload, `"approved":true`, "a late approval must not be recorded as given")
		}
	}
}
