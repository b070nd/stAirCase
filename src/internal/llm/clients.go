package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// OpenAI speaks the OpenAI chat-completions API, which the LLM gateway, xAI
// and Gemini's compatibility endpoint also serve.
type OpenAI struct {
	Key     string
	BaseURL string
}

// Chat implements Model.
func (c *OpenAI) Chat(ctx context.Context, r Request) (Response, error) {
	type function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	type toolCall struct {
		ID       string   `json:"id"`
		Type     string   `json:"type"`
		Function function `json:"function"`
	}
	type message struct {
		Role       string     `json:"role"`
		Content    *string    `json:"content"` // null when an assistant turn only calls tools
		ToolCalls  []toolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}
	var msgs []message
	if r.System != "" {
		msgs = append(msgs, message{Role: "system", Content: &r.System})
	}
	for _, m := range r.Messages {
		out := message{Role: m.Role, ToolCallID: m.ToolCallID}
		if m.Content != "" || len(m.ToolCalls) == 0 {
			content := m.Content
			out.Content = &content
		}
		for _, tc := range m.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, toolCall{ID: tc.ID, Type: "function",
				Function: function{Name: tc.Name, Arguments: string(tc.Arguments)}})
		}
		msgs = append(msgs, out)
	}
	body := map[string]any{"model": r.Model, "messages": msgs}
	if len(r.Tools) > 0 {
		var tools []map[string]any
		for _, t := range r.Tools {
			tools = append(tools, map[string]any{"type": "function",
				"function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Params}})
		}
		body["tools"] = tools
	}
	var out struct {
		Choices []struct {
			Message message `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := post(ctx, strings.TrimSuffix(c.BaseURL, "/")+"/chat/completions",
		map[string]string{"Authorization": "Bearer " + c.Key}, body, &out); err != nil {
		return Response{}, err
	}
	if len(out.Choices) == 0 {
		return Response{}, errors.New("chat completion returned no choices")
	}
	got := out.Choices[0].Message
	resp := Response{Message: Message{Role: "assistant"},
		InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens}
	if got.Content != nil {
		resp.Message.Content = *got.Content
	}
	for _, tc := range got.ToolCalls {
		resp.Message.ToolCalls = append(resp.Message.ToolCalls,
			ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: jsonArgs(tc.Function.Arguments)})
	}
	return resp, nil
}

// Anthropic speaks the Anthropic Messages API.
type Anthropic struct {
	Key       string
	BaseURL   string
	MaxTokens int // default 8192
}

// Chat implements Model.
func (c *Anthropic) Chat(ctx context.Context, r Request) (Response, error) {
	type block struct {
		Type      string          `json:"type"`
		Text      string          `json:"text,omitempty"`
		ID        string          `json:"id,omitempty"`
		Name      string          `json:"name,omitempty"`
		Input     json.RawMessage `json:"input,omitempty"`
		ToolUseID string          `json:"tool_use_id,omitempty"`
		Content   string          `json:"content,omitempty"`
	}
	type message struct {
		Role    string  `json:"role"`
		Content []block `json:"content"`
	}
	var msgs []message
	add := func(role string, b block) { // consecutive turns of one role become one turn
		if n := len(msgs); n > 0 && msgs[n-1].Role == role {
			msgs[n-1].Content = append(msgs[n-1].Content, b)
			return
		}
		msgs = append(msgs, message{Role: role, Content: []block{b}})
	}
	for _, m := range r.Messages {
		switch m.Role {
		case "user":
			if m.Content != "" {
				add("user", block{Type: "text", Text: m.Content})
			}
		case "assistant":
			if m.Content != "" {
				add("assistant", block{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				add("assistant", block{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: tc.Arguments})
			}
		case "tool":
			add("user", block{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content})
		}
	}
	maxTokens := c.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}
	body := map[string]any{"model": r.Model, "max_tokens": maxTokens, "messages": msgs}
	if r.System != "" {
		body["system"] = r.System
	}
	if len(r.Tools) > 0 {
		var tools []map[string]any
		for _, t := range r.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Params})
		}
		body["tools"] = tools
	}
	var out struct {
		Content []block `json:"content"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := post(ctx, strings.TrimSuffix(c.BaseURL, "/")+"/v1/messages",
		map[string]string{"x-api-key": c.Key, "anthropic-version": "2023-06-01"}, body, &out); err != nil {
		return Response{}, err
	}
	resp := Response{Message: Message{Role: "assistant"},
		InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}
	var text []string
	for _, b := range out.Content {
		switch b.Type {
		case "text":
			text = append(text, b.Text)
		case "tool_use":
			resp.Message.ToolCalls = append(resp.Message.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: jsonArgs(string(b.Input))})
		}
	}
	resp.Message.Content = strings.Join(text, "\n")
	return resp, nil
}

// jsonArgs keeps a model's tool arguments as JSON; malformed ones become a
// JSON string, which the tool then rejects with a message the model can read.
func jsonArgs(s string) json.RawMessage {
	if strings.TrimSpace(s) == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return b
}
