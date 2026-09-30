package signal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
// booleans as "noul" with yes/no criteria, and flat answers, and it reads
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
	assert.Equal(t, map[string]any{"yes": "the change adds at least one of these", "no": "it adds none of these"}, risky["criteria"])
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
