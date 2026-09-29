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
