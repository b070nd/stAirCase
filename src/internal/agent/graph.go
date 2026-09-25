package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/llm"
	"github.com/b070nd/stAirCase/src/internal/orchestrator"
	"github.com/b070nd/stAirCase/src/internal/plan"
)

const (
	maxAgentSteps = 25  // agent turns per run, like LangGraph's default recursion limit
	maxModelCalls = 100 // model calls per run, tool rounds included
)

// Graph runs a compiled plan in-process; it is an orchestrator.Agent.
//
// The supervisor goes first. A step is one agent's tool loop, run until the
// model answers without calling a tool. The answer's last "ROUTE: <next>" line
// picks the next step among the agent's outgoing edges — by condition label,
// else by target name — and the supervisor may always choose END. Without a
// valid route, an agent with a single next step takes it, a worker returns to
// the supervisor, and the supervisor ends the run. All agents share one
// conversation, which starts with the PRD and the repository context.
type Graph struct {
	Plan   plan.Plan
	Model  llm.Model // tests set it; nil means the providers
	Record string    // when set, every model exchange is recorded to this file
	Replay string    // when set, models are answered from this recording, offline
}

// route is a possible next step; target "" is END.
type route struct{ label, target string }

// Run implements orchestrator.Agent.
func (g *Graph) Run(ctx context.Context, env *orchestrator.AgentEnv) error {
	model := g.Model
	if model == nil {
		if g.Replay != "" {
			rp, err := llm.LoadReplay(g.Replay)
			if err != nil {
				return err
			}
			model = rp
		} else {
			model = &llm.Router{Secret: env.Secret}
			if g.Record != "" {
				rec, err := llm.NewRecorder(model, g.Record)
				if err != nil {
					return err
				}
				defer func() { _ = rec.Close() }()
				model = rec
			}
		}
	}
	agents := map[string]plan.Agent{}
	for _, a := range g.Plan.Agents {
		agents[a.Name] = a
	}
	history := []llm.Message{{Role: "user", Content: strings.TrimSpace(g.Plan.Brief() + "\n\n" + g.Plan.RepoContext)}}
	calls := 0
	for step, current := 1, g.Plan.Supervisor; ; step++ {
		if step > maxAgentSteps {
			return fmt.Errorf("stopped after %d agent steps without the supervisor routing to END", maxAgentSteps)
		}
		a := agents[current]
		routes := g.routes(a.Name)
		tools := Tools(env, a.Name)
		specs := make([]llm.ToolSpec, len(tools))
		for i, t := range tools {
			specs[i] = llm.ToolSpec{Name: t.Name, Description: t.Description, Params: t.Params}
		}
		req := llm.Request{Model: a.Model, System: a.Role + routeInstruction(routes), Tools: specs}
		var reply llm.Message
		for {
			if calls++; calls > maxModelCalls {
				return fmt.Errorf("stopped after %d model calls", maxModelCalls)
			}
			req.Messages = history
			resp, err := model.Chat(ctx, req)
			if err != nil {
				return fmt.Errorf("agent %s: %w", a.Name, err)
			}
			reply = resp.Message
			reply.Role = "assistant"
			env.Emit(ctx, orchestrator.Usage{Agent: a.Name, Model: a.Model, InputTokens: resp.InputTokens,
				OutputTokens: resp.OutputTokens, Content: preview(reply.Content), HasToolCalls: len(reply.ToolCalls) > 0})
			history = append(history, reply)
			if len(reply.ToolCalls) == 0 {
				break
			}
			for _, tc := range reply.ToolCalls {
				history = append(history, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: callTool(ctx, tools, tc)})
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		next, ok := pickRoute(routes, reply.Content)
		if !ok {
			switch {
			case len(routes) == 1:
				next = routes[0].target
			case a.Name == g.Plan.Supervisor:
				next = ""
			default:
				next = g.Plan.Supervisor
			}
		}
		if next == "" {
			return nil
		}
		current = next
	}
}

// routes lists an agent's possible next steps; the supervisor can always end.
func (g *Graph) routes(agent string) []route {
	var rs []route
	seen := map[string]bool{}
	add := func(r route) {
		if !seen[strings.ToLower(r.label)] {
			seen[strings.ToLower(r.label)] = true
			rs = append(rs, r)
		}
	}
	for _, e := range g.Plan.Edges {
		if e.From != agent {
			continue
		}
		r := route{label: e.Condition, target: e.To}
		if plan.IsEnd(e.To) {
			r.target = ""
		}
		if r.label == "" {
			r.label = e.To
			if plan.IsEnd(e.To) {
				r.label = "END"
			}
		}
		add(r)
	}
	if agent == g.Plan.Supervisor {
		add(route{label: "END"})
	}
	return rs
}

func routeInstruction(rs []route) string {
	if len(rs) < 2 {
		return ""
	}
	labels := make([]string, len(rs))
	for i, r := range rs {
		labels[i] = r.label
	}
	return "\n\nWhen your part is done, end your reply with one line `ROUTE: <next>`, where <next> is one of: " +
		strings.Join(labels, ", ") + "."
}

var routeLine = regexp.MustCompile(`ROUTE:\s*(\S+)`)

// pickRoute finds the reply's last ROUTE line among the valid routes.
func pickRoute(rs []route, reply string) (string, bool) {
	m := routeLine.FindAllStringSubmatch(reply, -1)
	if len(m) == 0 {
		return "", false
	}
	label := strings.Trim(m[len(m)-1][1], ".,;:!`'\"*")
	for _, r := range rs {
		if strings.EqualFold(r.label, label) {
			return r.target, true
		}
	}
	return "", false
}

func callTool(ctx context.Context, tools []Tool, tc llm.ToolCall) string {
	for _, t := range tools {
		if t.Name == tc.Name {
			return t.Call(ctx, tc.Arguments)
		}
	}
	return "error: unknown tool " + tc.Name
}

func preview(s string) string {
	if r := []rune(s); len(r) > 300 {
		return string(r[:300])
	}
	return s
}
