package signal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClient_Evaluate: the request is the gateway's documented /v1/evaluate
// shape, and the answers and cost come back; a missing answer or an HTTP
// error is an error, never a default answer.
func TestClient_Evaluate(t *testing.T) {
	var got map[string]any
	reply := `{"model":"typesafe-ai/jev","answers":{"risky":{"type":"boolean","probability":0.93}},` +
		`"usage":{"inputTokens":275,"outputTokens":20},"providerMetadata":{"gateway":{"cost":"0.00001155"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/evaluate", r.URL.Path)
		assert.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	c := &Client{Model: "typesafe-ai/jev", Key: "k", BaseURL: srv.URL}
	q := map[string]Question{"risky": ReviewQuestions["risky"]}

	res, err := c.Evaluate(context.Background(), map[string]any{"change": "x"}, q)
	require.NoError(t, err)
	assert.InDelta(t, 0.93, res.Answers["risky"].Probability, 1e-9)
	assert.InDelta(t, 0.00001155, res.Cost, 1e-12)
	assert.Equal(t, "typesafe-ai/jev", got["model"])
	assert.Contains(t, got, "state")
	assert.Equal(t, "boolean", got["questions"].(map[string]any)["risky"].(map[string]any)["type"])

	_, err = c.Evaluate(context.Background(), "s", map[string]Question{"risky": q["risky"], "kind": ReviewQuestions["kind"]})
	assert.ErrorContains(t, err, `no answer to "kind"`)

	reply = `{"error":{"message":"card required"}}`
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(reply))
	}))
	defer fail.Close()
	c.BaseURL = fail.URL
	_, err = c.Evaluate(context.Background(), "s", q)
	assert.ErrorContains(t, err, "HTTP 403")
}

// TestClient_systemone: against a TypeSafe-compatible server (a local Laya, or
// TypeSafe itself) the client uses its native shape: POST /v1/systemone,
// booleans as "noul" with true/false criteria, and flat answers, and it reads
// them into the same answers. No key is sent when there is none.
func TestClient_systemone(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/systemone", r.URL.Path)
		auth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = w.Write([]byte(`{"model":"laya-1","answers":{` +
			`"risky":{"noul":0.87},` +
			`"serves_story":{"noul":0.12},` +
			`"kind":{"choice":"config","probabilities":{"feature":0.1,"test":0.05,"docs":0.05,"config":0.7,"dependency":0.1},"confidence":0.7}},` +
			`"usage":{"input_tokens":200,"output_tokens":10}}`))
	}))
	defer srv.Close()
	c := &Client{Model: "laya", BaseURL: srv.URL, API: SystemOne}

	res, err := c.Evaluate(context.Background(), map[string]any{"change": "x"}, ReviewQuestions)
	require.NoError(t, err)
	assert.InDelta(t, 0.87, res.Answers["risky"].Probability, 1e-9)
	assert.InDelta(t, 0.12, res.Answers["serves_story"].Probability, 1e-9)
	assert.Equal(t, "config", res.Answers["kind"].Choice)
	assert.Empty(t, auth, "no key, no Authorization header")
	assert.Equal(t, "laya", got["model"])
	qs := got["questions"].(map[string]any)
	risky := qs["risky"].(map[string]any)
	assert.Equal(t, "noul", risky["type"])
	// a noul question's criteria are keyed "true" and "false": a real Laya (0.4.0) answers 422 to any other key
	assert.Equal(t, map[string]any{"true": "the change adds at least one of these", "false": "it adds none of these"}, risky["criteria"])
	assert.Equal(t, "choice", qs["kind"].(map[string]any)["type"])

	c.Key = "k"
	_, err = c.Evaluate(context.Background(), "s", map[string]Question{"risky": ReviewQuestions["risky"]})
	require.NoError(t, err)
	assert.Equal(t, "Bearer k", auth)

	_, err = c.Evaluate(context.Background(), "s", ReviewQuestions)
	require.NoError(t, err)
	c.API = "bogus"
	_, err = c.Evaluate(context.Background(), "s", ReviewQuestions)
	assert.ErrorContains(t, err, "unknown")
}

// TestClient_an_invalid_answer_is_an_error_never_a_safe_one: a model's answer that is not what was asked (a probability
// missing, outside 0..1 or of the wrong type, a choice that is none of the offered ones, an answer cut off, a server
// that never answers or answers without end) is an error, so the change goes to a person. Above all it is never read
// as "not risky": a missing probability must not become 0.
func TestClient_an_invalid_answer_is_an_error_never_a_safe_one(t *testing.T) {
	kind := `"kind":{"choice":"feature","probabilities":{"feature":1}}`
	for name, tc := range map[string]struct{ api, answers, want string }{
		"gateway: probability missing":     {Gateway, `"risky":{"type":"boolean"},"serves_story":{"type":"boolean","probability":0.9},` + kind, "risky"},
		"gateway: probability above 1":     {Gateway, `"risky":{"type":"boolean","probability":1.7},"serves_story":{"type":"boolean","probability":0.9},` + kind, "risky"},
		"gateway: probability below 0":     {Gateway, `"risky":{"type":"boolean","probability":0.1},"serves_story":{"type":"boolean","probability":-0.2},` + kind, "serves_story"},
		"gateway: probability is a string": {Gateway, `"risky":{"type":"boolean","probability":"0.1"},"serves_story":{"type":"boolean","probability":0.9},` + kind, "unreadable"},
		"gateway: answer of another type":  {Gateway, `"risky":{"type":"choice","choice":"feature"},"serves_story":{"type":"boolean","probability":0.9},` + kind, "risky"},
		"gateway: choice not offered":      {Gateway, `"risky":{"type":"boolean","probability":0.1},"serves_story":{"type":"boolean","probability":0.9},"kind":{"choice":"banana"}`, "kind"},
		"gateway: choice empty":            {Gateway, `"risky":{"type":"boolean","probability":0.1},"serves_story":{"type":"boolean","probability":0.9},"kind":{"type":"choice"}`, "kind"},
		"systemone: noul missing":          {SystemOne, `"risky":{},"serves_story":{"noul":0.9},` + kind, "risky"},
		"systemone: noul above 1":          {SystemOne, `"risky":{"noul":2},"serves_story":{"noul":0.9},` + kind, "risky"},
		"systemone: noul null":             {SystemOne, `"risky":{"noul":null},"serves_story":{"noul":0.9},` + kind, "risky"},
		"systemone: choice not offered":    {SystemOne, `"risky":{"noul":0.1},"serves_story":{"noul":0.9},"kind":{"choice":"Feature"}`, "kind"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"answers":{` + tc.answers + `}}`))
			}))
			defer srv.Close()
			_, err := (&Client{Model: "m", BaseURL: srv.URL, API: tc.api}).Evaluate(context.Background(), "s", ReviewQuestions)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}

	t.Run("a valid answer at the edges of the range is accepted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"answers":{"risky":{"type":"boolean","probability":0},"serves_story":{"type":"boolean","probability":1},` + kind + `}}`))
		}))
		defer srv.Close()
		res, err := (&Client{Model: "m", BaseURL: srv.URL}).Evaluate(context.Background(), "s", ReviewQuestions)
		require.NoError(t, err)
		assert.Zero(t, res.Answers["risky"].Probability, "an explicit 0 is an answer")
	})

	t.Run("an answer cut off, or without end", func(t *testing.T) {
		cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"answers":{"risky":{"type":"boolean","probab`))
		}))
		defer cut.Close()
		_, err := (&Client{Model: "m", BaseURL: cut.URL}).Evaluate(context.Background(), "s", ReviewQuestions)
		assert.ErrorContains(t, err, "unreadable")

		endless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"answers":{"risky":{"type":"boolean","probability":0.1}},"pad":"`))
			junk := make([]byte, 1<<16)
			for i := range junk {
				junk[i] = 'x'
			}
			for range 64 { // 4 MiB, over what the client reads
				if _, err := w.Write(junk); err != nil {
					return
				}
			}
		}))
		defer endless.Close()
		_, err = (&Client{Model: "m", BaseURL: endless.URL}).Evaluate(context.Background(), "s", ReviewQuestions)
		assert.Error(t, err, "an oversized reply is not read as an answer")
	})

	t.Run("a server that is too slow", func(t *testing.T) {
		release := make(chan struct{})
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer slow.Close()
		defer close(release)
		c := &Client{Model: "m", BaseURL: slow.URL, HTTP: &http.Client{Timeout: 100 * time.Millisecond}}
		start := time.Now()
		_, err := c.Evaluate(context.Background(), "s", ReviewQuestions)
		require.Error(t, err)
		assert.Less(t, time.Since(start), 5*time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err = (&Client{Model: "m", BaseURL: slow.URL}).Evaluate(ctx, "s", ReviewQuestions)
		assert.Error(t, err, "the run's own deadline reaches the call")
	})
}
