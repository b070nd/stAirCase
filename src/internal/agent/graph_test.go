package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/b070nd/staircase-core/src/internal/agent"
	"github.com/b070nd/staircase-core/src/internal/crypto"
	"github.com/b070nd/staircase-core/src/internal/llm"
	"github.com/b070nd/staircase-core/src/internal/orchestrator"
	"github.com/b070nd/staircase-core/src/internal/orchestrator/runtest"
	"github.com/b070nd/staircase-core/src/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptModel answers each agent from its own queue, keyed by the agent's
// role (the start of its system prompt), and keeps every request.
type scriptModel struct {
	mu      sync.Mutex
	replies map[string][]llm.Response
	calls   []llm.Request
}

func (s *scriptModel) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	for role, q := range s.replies {
		if strings.HasPrefix(req.System, role) && len(q) > 0 {
			s.replies[role] = q[1:]
			if q[0].Message.Content == "FAIL" {
				return llm.Response{}, fmt.Errorf("provider unavailable")
			}
			return q[0], nil
		}
	}
	return llm.Response{}, fmt.Errorf("no scripted reply left for %q", req.System)
}

// agentsCalled lists which agent (by role) each model call was for.
func (s *scriptModel) agentsCalled(roles ...string) []string {
	var out []string
	for _, c := range s.calls {
		for _, r := range roles {
			if strings.HasPrefix(c.System, r) {
				out = append(out, r)
			}
		}
	}
	return out
}

func say(text string) llm.Response {
	return llm.Response{Message: llm.Message{Role: "assistant", Content: text}, InputTokens: 10, OutputTokens: 5}
}

func use(tool string, args map[string]string) llm.Response {
	b, _ := json.Marshal(args)
	return llm.Response{Message: llm.Message{Role: "assistant",
		ToolCalls: []llm.ToolCall{{ID: "call-" + tool, Name: tool, Arguments: b}}}, InputTokens: 10, OutputTokens: 5}
}

// quickstart is the topology QUICKSTART.md documents: a supervisor and a coder
// handing work back and forth.
func quickstart() agent.Plan {
	return agent.Plan{Supervisor: "supervisor", PRD: "Add hello.txt.",
		Agents: []agent.AgentSpec{{Name: "supervisor", Role: "Route tasks.", Model: "claude-sonnet-4-6"},
			{Name: "coder", Role: "You write code.", Model: "claude-sonnet-4-6"}},
		Edges: []agent.Edge{{From: "supervisor", To: "coder"}, {From: "coder", To: "supervisor"}}}
}

func runGraph(t *testing.T, g *agent.Graph, setup func(*persistence.Store, string, int64)) runtest.Result {
	t.Helper()
	return runtest.Run(t, runtest.Options{Agent: g, Setup: setup})
}

// TestGraph_supervisor_delegates_and_ends catches the documented topology
// never finishing (F76): the supervisor hands off, the coder's tool loop
// changes the repository through an approval, and the supervisor ends it.
func TestGraph_supervisor_delegates_and_ends(t *testing.T) {
	m := &scriptModel{replies: map[string][]llm.Response{
		"Route tasks.":    {say("Coder, add hello.txt.\nROUTE: coder"), say("Done.\nROUTE: END")},
		"You write code.": {use("create_file", map[string]string{"path": "hello.txt", "content": "hi\n", "reasoning": "PRD"}), say("Created hello.txt.")},
	}}
	r := runGraph(t, &agent.Graph{Plan: quickstart(), Model: m}, nil)
	require.Equal(t, persistence.RunStatusSuccess, r.Run.Status, "%v", r.Err)
	got, err := r.OnBranch("hello.txt")
	require.NoError(t, err)
	assert.Equal(t, "hi\n", got)
	assert.Equal(t, []string{"Route tasks.", "You write code.", "You write code.", "Route tasks."}, m.agentsCalled("Route tasks.", "You write code."))
	assert.Contains(t, m.calls[0].System, "ROUTE: <next>`, where <next> is one of: coder, END.")
	assert.NotContains(t, m.calls[1].System, "ROUTE", "a single next step needs no choice")
	last := m.calls[2].Messages[len(m.calls[2].Messages)-1]
	assert.Equal(t, llm.Message{Role: "tool", ToolCallID: "call-create_file", Content: "created hello.txt"}, last)
	assert.Equal(t, "Add hello.txt.", m.calls[0].Messages[0].Content)
	var steps int
	for _, e := range r.Events {
		if e.EventType == "state_emit" {
			steps++
		}
	}
	assert.Equal(t, 4, steps, "every model call is reported for the budget")
}

// TestGraph_condition_labels_pick_the_route catches a label routed to the
// wrong agent, and END reached through a conditional edge.
func TestGraph_condition_labels_pick_the_route(t *testing.T) {
	plan := agent.Plan{Supervisor: "sup", PRD: "p",
		Agents: []agent.AgentSpec{{Name: "sup", Role: "S.", Model: "gpt-5"}, {Name: "coder", Role: "C.", Model: "gpt-5"}, {Name: "reviewer", Role: "R.", Model: "gpt-5"}},
		Edges: []agent.Edge{{From: "sup", To: "coder"}, {From: "coder", To: "reviewer", Condition: "review"},
			{From: "coder", To: "END", Condition: "ship"}, {From: "reviewer", To: "sup"}}}
	m := &scriptModel{replies: map[string][]llm.Response{
		"S.": {say("ROUTE: coder"), say("again\nROUTE: coder")},
		"C.": {say("first draft\nROUTE: review"), say("fixed. ROUTE: `ship`.")},
		"R.": {say("needs a fix")},
	}}
	r := runGraph(t, &agent.Graph{Plan: plan, Model: m}, nil)
	require.Equal(t, persistence.RunStatusSuccess, r.Run.Status, "%v", r.Err)
	assert.Equal(t, []string{"S.", "C.", "R.", "S.", "C."}, m.agentsCalled("S.", "C.", "R."))
	assert.Contains(t, m.calls[1].System, "one of: review, ship.")
}

// TestGraph_stops_a_loop catches agents handing work back and forth forever.
func TestGraph_stops_a_loop(t *testing.T) {
	var sup, coder []llm.Response
	for i := 0; i < 20; i++ {
		sup, coder = append(sup, say("ROUTE: coder")), append(coder, say("still working"))
	}
	r := runGraph(t, &agent.Graph{Plan: quickstart(), Model: &scriptModel{replies: map[string][]llm.Response{
		"Route tasks.": sup, "You write code.": coder}}}, nil)
	assert.Equal(t, persistence.RunStatusFailed, r.Run.Status)
	assert.ErrorContains(t, r.Err, "25 agent steps")
}

// TestGraph_tool_and_model_failures catches an unknown tool call derailing the
// loop, and a provider failure passing for success.
func TestGraph_tool_and_model_failures(t *testing.T) {
	m := &scriptModel{replies: map[string][]llm.Response{
		"Route tasks.":    {say("ROUTE: coder")},
		"You write code.": {use("rm_rf", nil), say("FAIL")},
	}}
	r := runGraph(t, &agent.Graph{Plan: quickstart(), Model: m}, nil)
	assert.Equal(t, persistence.RunStatusFailed, r.Run.Status)
	assert.ErrorContains(t, r.Err, "agent coder: provider unavailable")
	last := m.calls[2].Messages[len(m.calls[2].Messages)-1]
	assert.Equal(t, "error: unknown tool rm_rf", last.Content)
}

// fakeGateway serves OpenAI chat completions from a scriptModel.
func fakeGateway(t *testing.T, m *scriptModel) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string  `json:"role"`
				Content *string `json:"content"`
			} `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(raw, &body))
		resp, err := m.Chat(r.Context(), llm.Request{Model: body.Model, System: *body.Messages[0].Content})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		msg := map[string]any{"role": "assistant", "content": resp.Message.Content}
		var calls []map[string]any
		for _, tc := range resp.Message.ToolCalls {
			calls = append(calls, map[string]any{"id": tc.ID, "type": "function",
				"function": map[string]any{"name": tc.Name, "arguments": string(tc.Arguments)}})
		}
		if calls != nil {
			msg["tool_calls"] = calls
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg}},
			"usage": map[string]int{"prompt_tokens": resp.InputTokens, "completion_tokens": resp.OutputTokens}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestGraph_record_then_replay_offline catches a recording that cannot drive
// the same run again: the first run talks to a gateway (through the real
// router, client and workspace secrets) and records; the second runs offline.
func TestGraph_record_then_replay_offline(t *testing.T) {
	plan := quickstart()
	for i := range plan.Agents {
		plan.Agents[i].Model = "test/model" // a gateway model
	}
	gw := fakeGateway(t, &scriptModel{replies: map[string][]llm.Response{
		"Route tasks.":    {say("ROUTE: coder"), say("ROUTE: END")},
		"You write code.": {use("create_file", map[string]string{"path": "hello.txt", "content": "hi\n", "reasoning": "PRD"}), say("Created.")},
	}})
	rec := filepath.Join(t.TempDir(), "llm.jsonl")
	first := runGraph(t, &agent.Graph{Plan: plan, Record: rec}, func(s *persistence.Store, wsDir string, projectID int64) {
		key, err := crypto.LoadKey(wsDir)
		require.NoError(t, err)
		for name, v := range map[string]string{"LLM_GATEWAY_API_KEY": "gk", "LLM_GATEWAY_URL": gw.URL} {
			enc, err := crypto.Encrypt(key, v)
			require.NoError(t, err)
			_, err = s.CreateSecret(name, enc, &projectID)
			require.NoError(t, err)
		}
	})
	require.Equal(t, persistence.RunStatusSuccess, first.Run.Status, "%v", first.Err)

	gw.Close() // the replay must not need it
	second := runGraph(t, &agent.Graph{Plan: plan, Replay: rec}, nil)
	require.Equal(t, persistence.RunStatusSuccess, second.Run.Status, "%v", second.Err)
	got, err := second.OnBranch("hello.txt")
	require.NoError(t, err)
	assert.Equal(t, "hi\n", got)
}

// TestGraph_replay_refuses_a_different_run catches a replay quietly answering
// a run whose prompts differ from the recording.
func TestGraph_replay_refuses_a_different_run(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "llm.jsonl")
	m := &scriptModel{replies: map[string][]llm.Response{"Route tasks.": {say("ROUTE: END")}}}
	recorder, err := llm.NewRecorder(m, rec)
	require.NoError(t, err)
	first := runGraph(t, &agent.Graph{Plan: quickstart(), Model: recorder}, nil)
	require.NoError(t, recorder.Close())
	require.Equal(t, persistence.RunStatusSuccess, first.Run.Status)

	changed := quickstart()
	changed.PRD = "Something else."
	r := runGraph(t, &agent.Graph{Plan: changed, Replay: rec}, nil)
	assert.Equal(t, persistence.RunStatusFailed, r.Run.Status)
	assert.ErrorContains(t, r.Err, "no recorded response")
}

var _ orchestrator.Agent = (*agent.Graph)(nil)
