// Package llm talks to chat models that can call tools, over plain HTTPS:
// one OpenAI-compatible client (the LLM gateway, OpenAI, xAI, Gemini) and one
// Anthropic client, plus recording and offline replay of their exchanges.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Message is one turn of a conversation.
type Message struct {
	Role       string     `json:"role"` // user | assistant | tool
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant
	ToolCallID string     `json:"tool_call_id,omitempty"` // tool: the call it answers
}

// ToolCall is the model asking to run a tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"` // a JSON object
}

// ToolSpec offers a tool to the model.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Params      json.RawMessage `json:"parameters"` // JSON Schema of the arguments object
}

// Request is one chat completion request.
type Request struct {
	Model    string     `json:"model"`
	System   string     `json:"system,omitempty"`
	Messages []Message  `json:"messages"`
	Tools    []ToolSpec `json:"tools,omitempty"`
}

// Response is the model's next turn and what it cost.
type Response struct {
	Message      Message `json:"message"` // role assistant
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
}

// Model is a chat model that can call tools.
type Model interface {
	Chat(ctx context.Context, req Request) (Response, error)
}

// GatewayURL is the default OpenAI-compatible LLM gateway (Vercel AI
// Gateway); the LLM_GATEWAY_URL secret overrides it.
const GatewayURL = "https://ai-gateway.vercel.sh/v1"

// provider is where a model is served and which secret holds its key.
type provider struct {
	secret    string
	baseURL   string // OpenAI-compatible; "" means Anthropic
	anthropic bool
}

// providerFor routes a model name: provider/model goes to the gateway, known
// prefixes to their vendor.
func providerFor(model string) (provider, bool) {
	switch {
	case strings.Contains(model, "/"):
		return provider{secret: "LLM_GATEWAY_API_KEY", baseURL: GatewayURL}, true
	case strings.HasPrefix(model, "claude-"):
		return provider{secret: "ANTHROPIC_API_KEY", anthropic: true}, true
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "o1-"),
		strings.HasPrefix(model, "o3-"), strings.HasPrefix(model, "o4-"):
		return provider{secret: "OPENAI_API_KEY", baseURL: "https://api.openai.com/v1"}, true
	case strings.HasPrefix(model, "gemini-"):
		return provider{secret: "GOOGLE_API_KEY", baseURL: "https://generativelanguage.googleapis.com/v1beta/openai"}, true
	case strings.HasPrefix(model, "grok-"):
		return provider{secret: "XAI_API_KEY", baseURL: "https://api.x.ai/v1"}, true
	}
	return provider{}, false
}

// SecretFor returns the workspace secret that holds model's API key, or ""
// for a model no provider serves. The secret.provider_keys gate uses it.
func SecretFor(model string) string {
	p, _ := providerFor(model)
	return p.secret
}

// New returns a client for model; secret reads workspace secrets.
func New(model string, secret func(name string) (string, error)) (Model, error) {
	p, ok := providerFor(model)
	if !ok {
		return nil, fmt.Errorf("unknown LLM provider for model %q - supported: claude-, gpt-, o1-, o3-, o4-, gemini-, grok-, or provider/model (LLM gateway)", model)
	}
	key, err := secret(p.secret)
	if err != nil {
		return nil, err
	}
	if p.anthropic {
		return &Anthropic{Key: key, BaseURL: "https://api.anthropic.com"}, nil
	}
	base := p.baseURL
	if p.secret == "LLM_GATEWAY_API_KEY" {
		if u, err := secret("LLM_GATEWAY_URL"); err == nil && u != "" {
			base = u
		}
	}
	return &OpenAI{Key: key, BaseURL: base}, nil
}

// httpClient has no overall timeout: requests carry the run's context, and a
// model may take minutes to answer.
var httpClient = &http.Client{}

// retryDelay is the wait before retry n (0-based); tests shorten it.
var retryDelay = func(n int) time.Duration { return time.Duration(1+3*n) * time.Second }

const maxAttempts = 3

// post sends body as JSON and decodes a 2xx reply into out, retrying rate
// limits, overloads and network errors a few times.
func post(ctx context.Context, url string, headers map[string]string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := httpClient.Do(req)
		retry := err == nil || ctx.Err() == nil // network errors are worth another try
		if err == nil {
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
			_ = resp.Body.Close()
			switch {
			case rerr != nil:
				err = rerr
			case resp.StatusCode/100 == 2:
				if err := json.Unmarshal(data, out); err != nil {
					return fmt.Errorf("%s: decode reply: %w", url, err)
				}
				return nil
			default:
				err = fmt.Errorf("%s: HTTP %d: %s", url, resp.StatusCode, excerpt(data))
				retry = resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500
			}
		}
		if !retry || attempt == maxAttempts-1 {
			return err
		}
		select {
		case <-time.After(retryDelay(attempt)):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func excerpt(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

// Router sends each request to the client for its model, creating clients on
// first use, so one Model (and one recording) serves every agent of a run.
type Router struct {
	Secret func(name string) (string, error)

	mu      sync.Mutex
	clients map[string]Model
}

// Chat implements Model.
func (r *Router) Chat(ctx context.Context, req Request) (Response, error) {
	r.mu.Lock()
	m, ok := r.clients[req.Model]
	if !ok {
		var err error
		if m, err = New(req.Model, r.Secret); err != nil {
			r.mu.Unlock()
			return Response{}, err
		}
		if r.clients == nil {
			r.clients = map[string]Model{}
		}
		r.clients[req.Model] = m
	}
	r.mu.Unlock()
	return m.Chat(ctx, req)
}
