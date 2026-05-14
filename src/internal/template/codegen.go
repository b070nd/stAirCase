package template

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
	"text/template"
)

//go:embed graph_exec.py.tmpl
var graphExecTmpl string

// AgentParams describes one node in the swarm topology.
type AgentParams struct {
	Name               string
	Role               string
	Model              string
	Tools              []string // extra registered tool names beyond built-ins
	OutgoingConditions []string // non-empty → ROUTE: pattern routing; labels for conditional edges
}

// EdgeParams describes one directed edge in the topology graph.
type EdgeParams struct {
	From      string
	To        string
	Condition string // non-empty → conditional edge; label returned by routing function
}

// ConditionalEdgeGroup groups all conditional edges leaving the same source node.
// Pre-computed from Edges before template rendering.
type ConditionalEdgeGroup struct {
	From             string
	FromIdent        string // pyIdent(From)
	FromIsSupervisor bool   // true when From == SupervisorName
	Routes           []ConditionalRoute
}

// ConditionalRoute is one branch within a conditional edge group.
type ConditionalRoute struct {
	Condition string
	To        string
	ToIsEND   bool // true when To is the graph END sentinel
}

// WorkerRouteEntry is one entry in the conditional-edge path-map for a worker
// node. Each entry maps a value that state.next_agent may hold to a graph node.
type WorkerRouteEntry struct {
	Label  string // path-map key  (= value state.next_agent will hold)
	Target string // graph node name
	IsEND  bool   // true when target is an END sentinel
}

// WorkerPathMap carries the complete routing information for one non-supervisor
// agent. Pre-computed by GenerateGraphExec so the template does not have to
// reason across slices.
type WorkerPathMap struct {
	AgentName  string
	AgentIdent string // pyIdent(AgentName)
	Routes     []WorkerRouteEntry
}

// GraphExecParams holds all data needed to render graph_exec.py.
type GraphExecParams struct {
	RunID          int64
	CaseID         int64
	ProjectPath    string
	PRDContext     string
	RepoContext    string
	Agents         []AgentParams
	Edges          []EdgeParams
	SupervisorName string
	CheckpointType string
	RuntimeType    string

	// Pre-computed by GenerateGraphExec — do not set manually.
	ConditionalEdgeGroups []ConditionalEdgeGroup
	WorkerPathMaps        []WorkerPathMap
}

// GenerateGraphExec renders graph_exec.py to outPath from params.
func GenerateGraphExec(outPath string, p GraphExecParams) error {
	// Pre-compute ConditionalEdgeGroups from Edges.
	p.ConditionalEdgeGroups = buildConditionalGroups(p.Edges, p.SupervisorName)

	// Pre-populate OutgoingConditions per agent.
	condsByAgent := make(map[string][]string)
	for _, e := range p.Edges {
		if e.Condition != "" {
			condsByAgent[e.From] = append(condsByAgent[e.From], e.Condition)
		}
	}
	for i := range p.Agents {
		p.Agents[i].OutgoingConditions = condsByAgent[p.Agents[i].Name]
	}

	// Pre-compute WorkerPathMaps: one per non-supervisor agent.
	p.WorkerPathMaps = buildWorkerPathMaps(p.Agents, p.Edges, p.SupervisorName)

	tmpl, err := template.New("graph_exec").
		Delims("[[", "]]").
		Funcs(template.FuncMap{
			"pystr":   pyStr,
			"pyident": pyIdent,
			"indent":  indentBlock,
			"isLast":  func(i, n int) bool { return i == n-1 },
			"pyset": func(ss []string) string {
				quoted := make([]string, len(ss))
				for i, s := range ss {
					quoted[i] = pyStr(s)
				}
				return "{" + strings.Join(quoted, ", ") + "}"
			},
			"pylist": func(agents []AgentParams) string {
				names := make([]string, len(agents))
				for i, a := range agents {
					names[i] = pyStr(a.Name)
				}
				return "[" + strings.Join(names, ", ") + "]"
			},
		}).
		Parse(graphExecTmpl)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	f, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open output: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := tmpl.Execute(f, p); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}
	return nil
}

// buildConditionalGroups groups conditional edges by From node, preserving
// insertion order. Groups whose From matches supervisorName are flagged so
// the template can use _supervisor_route (Send fan-out) for them instead of
// the path-map routing used for all other conditional edges.
func buildConditionalGroups(edges []EdgeParams, supervisorName string) []ConditionalEdgeGroup {
	endSentinels := map[string]bool{"END": true, "__end__": true}
	index := map[string]int{} // From → position in result slice
	var groups []ConditionalEdgeGroup
	for _, e := range edges {
		if e.Condition == "" {
			continue
		}
		idx, exists := index[e.From]
		if !exists {
			idx = len(groups)
			index[e.From] = idx
			groups = append(groups, ConditionalEdgeGroup{
				From:             e.From,
				FromIdent:        pyIdent(e.From),
				FromIsSupervisor: e.From == supervisorName,
			})
		}
		groups[idx].Routes = append(groups[idx].Routes, ConditionalRoute{
			Condition: e.Condition,
			To:        e.To,
			ToIsEND:   endSentinels[e.To],
		})
	}
	return groups
}

// buildWorkerPathMaps constructs the complete conditional-edge path-map for
// every non-supervisor agent. Each map always contains:
//   - "tools_<ident>"  → tools node (for LangGraph ToolNode loop)
//   - supervisorName   → supervisor (default when the worker is done)
//
// Additional entries come from any conditional edges FROM that agent (the ROUTE:
// pattern labels). Unconditional edges FROM worker nodes are subsumed here; the
// template omits them from the static add_edge calls to avoid conflicts.
func buildWorkerPathMaps(agents []AgentParams, edges []EdgeParams, supervisorName string) []WorkerPathMap {
	endSentinels := map[string]bool{"END": true, "__end__": true}

	// Collect conditional routes per non-supervisor agent.
	condRoutes := make(map[string][]WorkerRouteEntry)
	for _, e := range edges {
		if e.Condition == "" || e.From == supervisorName {
			continue
		}
		condRoutes[e.From] = append(condRoutes[e.From], WorkerRouteEntry{
			Label:  e.Condition,
			Target: e.To,
			IsEND:  endSentinels[e.To],
		})
	}

	var maps []WorkerPathMap
	for _, a := range agents {
		if a.Name == supervisorName {
			continue
		}
		ident := pyIdent(a.Name)
		routes := []WorkerRouteEntry{
			{Label: "tools_" + ident, Target: "tools_" + ident},
			{Label: supervisorName, Target: supervisorName},
		}
		// Add conditional routes, skipping duplicates (supervisor already present).
		seen := map[string]bool{
			"tools_" + ident: true,
			supervisorName:   true,
		}
		for _, r := range condRoutes[a.Name] {
			if !seen[r.Label] {
				routes = append(routes, r)
				seen[r.Label] = true
			}
		}
		maps = append(maps, WorkerPathMap{
			AgentName:  a.Name,
			AgentIdent: ident,
			Routes:     routes,
		})
	}
	return maps
}

// pyStr returns a Python string literal with proper escaping.
func pyStr(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// pythonKeywords is the complete set of reserved words in Python 3.
var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true,
	"and": true, "as": true, "assert": true, "async": true, "await": true,
	"break": true, "class": true, "continue": true, "def": true, "del": true,
	"elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true,
	"is": true, "lambda": true, "nonlocal": true, "not": true, "or": true,
	"pass": true, "raise": true, "return": true, "try": true, "while": true,
	"with": true, "yield": true,
}

// pyIdent sanitises a string for use as a Python identifier.
func pyIdent(s string) string {
	var b strings.Builder
	for i, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	ident := b.String()
	if ident == "" {
		return "_empty"
	}
	if pythonKeywords[ident] {
		return ident + "_"
	}
	return ident
}

// indentBlock prefixes every non-empty line of a multi-line string with spaces.
func indentBlock(spaces int, s string) string {
	pad := strings.Repeat(" ", spaces)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}
