package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// a conversation with one tool round trip, as the agent loop builds it
var convo = Request{
	Model:  "m",
	System: "you are the coder",
	Messages: []Message{
		{Role: "user", Content: "PRD"},
		{Role: "assistant", Content: "reading", ToolCalls: []ToolCall{{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)}}},
		{Role: "tool", ToolCallID: "c1", Content: "A"},
	},
	Tools: []ToolSpec{{Name: "read_file", Description: "read", Params: json.RawMessage(`{"type":"object"}`)}},
}

// fakeAPI serves one canned reply and hands the request body to check.
func fakeAPI(t *testing.T, path string, check func(r *http.Request, body map[string]any), reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, path, r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		check(r, body)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestOpenAI_speaks_chat_completions catches a request the OpenAI-compatible
// APIs (gateway, OpenAI, xAI, Gemini) reject or misread, and a reply mapped wrong.
func TestOpenAI_speaks_chat_completions(t *testing.T) {
	srv := fakeAPI(t, "/v1/chat/completions", func(r *http.Request, body map[string]any) {
		assert.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		msgs := body["messages"].([]any)
		require.Len(t, msgs, 4)
		assert.Equal(t, map[string]any{"role": "system", "content": "you are the coder"}, msgs[0])
		call := msgs[2].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
		assert.Equal(t, "function", call["type"])
		assert.Equal(t, `{"path":"a.txt"}`, call["function"].(map[string]any)["arguments"], "arguments travel as a JSON string")
		assert.Equal(t, "c1", msgs[3].(map[string]any)["tool_call_id"])
		assert.Equal(t, "read_file", body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["name"])
	}, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[
		{"id":"c2","type":"function","function":{"name":"create_file","arguments":"{\"path\":\"b.txt\"}"}},
		{"id":"c3","type":"function","function":{"name":"list_dir","arguments":"not json"}}]}}],
		"usage":{"prompt_tokens":11,"completion_tokens":7}}`)
	resp, err := (&OpenAI{Key: "k", BaseURL: srv.URL + "/v1/"}).Chat(context.Background(), convo)
	require.NoError(t, err)
	assert.Equal(t, 11, resp.InputTokens)
	assert.Equal(t, 7, resp.OutputTokens)
	require.Len(t, resp.Message.ToolCalls, 2)
	assert.Equal(t, ToolCall{ID: "c2", Name: "create_file", Arguments: json.RawMessage(`{"path":"b.txt"}`)}, resp.Message.ToolCalls[0])
	assert.JSONEq(t, `"not json"`, string(resp.Message.ToolCalls[1].Arguments), "malformed arguments stay representable")
}

// TestAnthropic_speaks_messages catches a request the Messages API rejects -
// system prompt placement, tool results as user blocks, one turn per role -
// and a reply mapped wrong.
func TestAnthropic_speaks_messages(t *testing.T) {
	srv := fakeAPI(t, "/v1/messages", func(r *http.Request, body map[string]any) {
		assert.Equal(t, "k", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		assert.Equal(t, "you are the coder", body["system"])
		assert.EqualValues(t, 8192, body["max_tokens"])
		msgs := body["messages"].([]any)
		require.Len(t, msgs, 3, "user, assistant (text + tool_use), user (tool_result)")
		assistant := msgs[1].(map[string]any)["content"].([]any)
		assert.Equal(t, "tool_use", assistant[1].(map[string]any)["type"])
		assert.Equal(t, map[string]any{"path": "a.txt"}, assistant[1].(map[string]any)["input"])
		result := msgs[2].(map[string]any)
		assert.Equal(t, "user", result["role"])
		assert.Equal(t, "c1", result["content"].([]any)[0].(map[string]any)["tool_use_id"])
		assert.Contains(t, body["tools"].([]any)[0].(map[string]any), "input_schema")
	}, `{"content":[{"type":"text","text":"creating"},{"type":"tool_use","id":"t1","name":"create_file","input":{"path":"b.txt"}}],
		"usage":{"input_tokens":20,"output_tokens":5}}`)
	resp, err := (&Anthropic{Key: "k", BaseURL: srv.URL}).Chat(context.Background(), convo)
	require.NoError(t, err)
	assert.Equal(t, "creating", resp.Message.Content)
	assert.Equal(t, []ToolCall{{ID: "t1", Name: "create_file", Arguments: json.RawMessage(`{"path":"b.txt"}`)}}, resp.Message.ToolCalls)
	assert.Equal(t, 20, resp.InputTokens)
	assert.Equal(t, 5, resp.OutputTokens)
}

// TestPost_retries_only_what_is_worth_retrying catches a transient 429/5xx
// failing a run, and a hopeless 4xx being retried.
func TestPost_retries_only_what_is_worth_retrying(t *testing.T) {
	orig := retryDelay
	retryDelay = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { retryDelay = orig })
	serve := func(codes ...int) (*httptest.Server, *int32) {
		var n int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			i := atomic.AddInt32(&n, 1) - 1
			if int(i) < len(codes) && codes[i] != 200 {
				http.Error(w, "nope", codes[i])
				return
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		t.Cleanup(srv.Close)
		return srv, &n
	}
	var out map[string]any
	srv, n := serve(429, 503, 200)
	require.NoError(t, post(context.Background(), srv.URL, nil, map[string]any{}, &out))
	assert.EqualValues(t, 3, *n)

	srv, n = serve(400)
	err := post(context.Background(), srv.URL, nil, map[string]any{}, &out)
	assert.ErrorContains(t, err, "HTTP 400")
	assert.EqualValues(t, 1, *n)

	srv, n = serve(503, 503, 503, 200)
	assert.ErrorContains(t, post(context.Background(), srv.URL, nil, map[string]any{}, &out), "HTTP 503")
	assert.EqualValues(t, maxAttempts, *n)
}

// TestNew_routes_each_model_to_its_provider catches a model sent to the wrong
// API or with the wrong key; the provider-keys gate relies on SecretFor.
func TestNew_routes_each_model_to_its_provider(t *testing.T) {
	for model, secret := range map[string]string{
		"openai/gpt-6-astra": "LLM_GATEWAY_API_KEY", "claude-sonnet-4-6": "ANTHROPIC_API_KEY",
		"gpt-5": "OPENAI_API_KEY", "o3-mini": "OPENAI_API_KEY", "gemini-3-pro": "GOOGLE_API_KEY",
		"grok-4": "XAI_API_KEY", "llama-3": "",
	} {
		assert.Equal(t, secret, SecretFor(model), model)
	}
	secrets := map[string]string{"LLM_GATEWAY_API_KEY": "gk", "LLM_GATEWAY_URL": "https://gw.example/v1", "ANTHROPIC_API_KEY": "ak"}
	read := func(name string) (string, error) {
		if v, ok := secrets[name]; ok {
			return v, nil
		}
		return "", errors.New(name + " not found")
	}
	m, err := New("openai/gpt-6-astra", read)
	require.NoError(t, err)
	assert.Equal(t, &OpenAI{Key: "gk", BaseURL: "https://gw.example/v1"}, m)
	m, err = New("claude-sonnet-4-6", read)
	require.NoError(t, err)
	assert.Equal(t, &Anthropic{Key: "ak", BaseURL: "https://api.anthropic.com"}, m)
	_, err = New("gpt-5", read)
	assert.ErrorContains(t, err, "OPENAI_API_KEY not found")
	_, err = New("llama-3", read)
	assert.ErrorContains(t, err, "unknown LLM provider")
}

type scripted struct{ replies []Response }

func (s *scripted) Chat(context.Context, Request) (Response, error) {
	r := s.replies[0]
	s.replies = s.replies[1:]
	return r, nil
}

// TestRecordReplay catches a replay that answers a different request than
// was recorded, or loses the order of repeated identical requests.
func TestRecordReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.jsonl")
	a, b := Request{Model: "m", Messages: []Message{{Role: "user", Content: "a"}}}, Request{Model: "m", Messages: []Message{{Role: "user", Content: "b"}}}
	rec, err := NewRecorder(&scripted{replies: []Response{
		{Message: Message{Role: "assistant", Content: "a1"}}, {Message: Message{Role: "assistant", Content: "b1"}, InputTokens: 3},
		{Message: Message{Role: "assistant", Content: "a2"}},
	}}, path)
	require.NoError(t, err)
	for _, req := range []Request{a, b, a} {
		_, err := rec.Chat(context.Background(), req)
		require.NoError(t, err)
	}
	require.NoError(t, rec.Close())

	rp, err := LoadReplay(path)
	require.NoError(t, err)
	for _, want := range []struct {
		req  Request
		text string
	}{{b, "b1"}, {a, "a1"}, {a, "a2"}} {
		got, err := rp.Chat(context.Background(), want.req)
		require.NoError(t, err)
		assert.Equal(t, want.text, got.Message.Content)
	}
	_, err = rp.Chat(context.Background(), a)
	assert.ErrorContains(t, err, "no recorded response", "a request answered once more than recorded")
	_, err = rp.Chat(context.Background(), Request{Model: "m"})
	assert.ErrorContains(t, err, "no recorded response")
}
