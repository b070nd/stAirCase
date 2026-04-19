package template

import (
	"fmt"
	"os"
	"strings"
	"text/template"
)

// AgentParams describes one node in the swarm topology.
type AgentParams struct {
	Name               string
	Role               string
	Model              string
	Tools              []string // extra registered tool names (beyond built-ins)
	OutgoingConditions []string // non-empty → agent has conditional outgoing edges; ROUTE: parsed
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
	FromIdent        string           // pyIdent(From)
	FromIsSupervisor bool             // true when From == SupervisorName
	Routes           []ConditionalRoute
}

// ConditionalRoute is one branch within a conditional edge group.
type ConditionalRoute struct {
	Condition string
	To        string
	ToIsEND   bool // true when To is the graph END sentinel
}

// GraphExecParams holds all data needed to render graph_exec.py.
type GraphExecParams struct {
	RunID                 int64
	CaseID                int64
	ProjectPath           string
	PRDContext            string
	RepoContext           string
	Agents                []AgentParams
	Edges                 []EdgeParams
	SupervisorName        string
	CheckpointType        string
	RuntimeType           string
	ConditionalEdgeGroups []ConditionalEdgeGroup // populated by GenerateGraphExec
}

// GenerateGraphExec renders graph_exec.py to outPath from params.
func GenerateGraphExec(outPath string, p GraphExecParams) error {
	// Pre-compute ConditionalEdgeGroups from Edges.
	p.ConditionalEdgeGroups = buildConditionalGroups(p.Edges, p.SupervisorName)

	// Pre-populate OutgoingConditions per agent so the template can emit ROUTE: parsing.
	condsByAgent := make(map[string][]string)
	for _, e := range p.Edges {
		if e.Condition != "" {
			condsByAgent[e.From] = append(condsByAgent[e.From], e.Condition)
		}
	}
	for i := range p.Agents {
		p.Agents[i].OutgoingConditions = condsByAgent[p.Agents[i].Name]
	}

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
	defer f.Close()

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

// pyStr returns a Python string literal with proper escaping.
// Handles backslashes, quotes, and control characters that would break
// the single-line string syntax in the generated script.
func pyStr(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// pythonKeywords is the complete set of reserved words in Python 3.
// An identifier that matches one of these would produce a syntax error.
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
// Appends "_" if the result is a Python keyword; returns "_empty" for blank input.
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

// graphExecTmpl is the Go text/template (delimiters [[ ]]) that renders graph_exec.py.
// Key design constraints from spec §4.1 / §4.2:
//   - Constraint A: active_agents []string + Send drives parallel fan-out from the supervisor
//   - Constraint B: only exact search-and-replace via request_edit — no diffs
//   - Constraint C: no inline comment bloat
const graphExecTmpl = `#!/usr/bin/env python3
# graph_exec_[[.RunID]].py  —  stAirCase run #[[.RunID]] / case #[[.CaseID]]
# DO NOT EDIT  —  regenerate: staircase compile [[.CaseID]]
import json, os, re as _re, socket, sys, tempfile, threading
from typing import Annotated
[[if eq .RuntimeType "langgraph"]]
from langgraph.graph import StateGraph, END
from langgraph.constants import Send
[[else]]
raise NotImplementedError("Runtime '[[.RuntimeType]]' is not yet supported — only 'langgraph' is implemented.")
[[end]]
from pydantic import BaseModel
from langchain_core.messages import BaseMessage
from langgraph.graph.message import add_messages


# ── IPC client ────────────────────────────────────────────────────────────────

class _IPC:
    def __init__(self, sock_path: str, token: str):
        self._lock = threading.Lock()
        if ":" in sock_path and not sock_path.startswith("/"):
            host, port = sock_path.rsplit(":", 1)
            self._sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            self._sock.connect((host, int(port)))
        else:
            self._sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            self._sock.connect(sock_path)
        self._buf = b""
        self._raw_send({"type": "auth", "token": token})
        resp = self._raw_recv()
        if resp.get("type") != "auth_ok":
            raise RuntimeError(f"IPC auth rejected: {resp}")

    def _raw_send(self, msg: dict) -> None:
        self._sock.sendall((json.dumps(msg) + "\n").encode())

    def _raw_recv(self) -> dict:
        while b"\n" not in self._buf:
            chunk = self._sock.recv(65536)
            if not chunk:
                raise ConnectionError("IPC socket closed unexpectedly")
            self._buf += chunk
        line, self._buf = self._buf.split(b"\n", 1)
        return json.loads(line)

    def emit_state(self, agent: str, state: dict) -> None:
        with self._lock:
            self._raw_send({"type": "state_emit", "active_agent": agent, "state": state})

    def yield_request(self, agent: str, action: str, edits: list, reasoning: str, confidence: float = 0.9) -> dict:
        with self._lock:
            self._raw_send({"type": "yield_request", "agent_name": agent,
                            "action_type": action, "proposed_edits": edits,
                            "reasoning_trace": reasoning, "confidence_score": confidence})
            return self._raw_recv()

    def get_secret(self, key: str, project_id: int | None = None) -> str | None:
        with self._lock:
            msg: dict = {"type": "secret_request", "key_name": key}
            if project_id is not None:
                msg["project_id"] = project_id
            self._raw_send(msg)
            resp = self._raw_recv()
        return resp.get("encrypted_value")

    def heartbeat(self) -> None:
        with self._lock:
            self._raw_send({"type": "heartbeat"})
            self._raw_recv()

    def close(self) -> None:
        self._sock.close()


# ── Bootstrap ─────────────────────────────────────────────────────────────────
# Apply a 30-second timeout on stdin so Python does not block indefinitely
# if Go crashes before writing the bootstrap message (T-2).
try:
    import select as _select
    _rlist, _, _ = _select.select([sys.stdin], [], [], 30.0)
    if not _rlist:
        raise RuntimeError(
            "bootstrap timeout: Go orchestrator did not write the bootstrap "
            "message within 30 s — is staircase still running?"
        )
except (AttributeError, OSError):
    # select.select does not work on Windows raw stdin; fall through to
    # the plain readline() which will unblock once Go writes the message.
    pass

_boot = json.loads(sys.stdin.readline())
assert _boot["type"] == "bootstrap", f"unexpected: {_boot}"
_ipc = _IPC(_boot["socket_path"], _boot["token"])

PROJECT_PATH = [[.ProjectPath | pystr]]

PRD_CONTEXT = [[.PRDContext | pystr]]

REPO_CONTEXT = [[.RepoContext | pystr]]


# ── State ─────────────────────────────────────────────────────────────────────

class AgentState(BaseModel):
    messages: Annotated[list, add_messages] = []
    active_agents: list[str] = []
    next_agent: str = [[.SupervisorName | pystr]]


# ── Built-in tools ────────────────────────────────────────────────────────────

from langchain_core.tools import tool

@tool
def read_file(path: str) -> str:
    """Read a source file relative to the project root. Maximum 100 KB."""
    try:
        full = os.path.realpath(os.path.join(PROJECT_PATH, path))
        root = os.path.realpath(PROJECT_PATH)
        if not (full == root or full.startswith(root + os.sep)):
            return f"error: path escapes project root: {path}"
        size = os.path.getsize(full)
        if size > 102_400:
            return f"error: {path} is too large ({size} bytes); use a narrower path"
        with open(full) as _f:
            return _f.read()
    except FileNotFoundError:
        return f"error: file not found: {path}"
    except IsADirectoryError:
        return f"error: {path} is a directory, not a file"
    except Exception as _e:
        return f"error reading {path}: {_e}"

@tool
def request_edit(file: str, search_block: str, replace_block: str, reasoning: str) -> str:
    """Propose an exact search-and-replace edit. No diffs or line numbers."""
    # Validate path before presenting to operator — blocks escaping via symlinks or ../
    full_path = os.path.realpath(os.path.join(PROJECT_PATH, file))
    root = os.path.realpath(PROJECT_PATH)
    if not (full_path == root or full_path.startswith(root + os.sep)):
        return f"error: path escapes project root: {file}"
    resp = _ipc.yield_request(
        "agent", "file_edit",
        [{"file": file, "search_block": search_block, "replace_block": replace_block}],
        reasoning,
    )
    if not resp.get("approved"):
        return f"rejected: {resp.get('feedback', 'no feedback')}"
    try:
        src = open(full_path).read()
    except Exception as _e:
        return f"error opening {file}: {_e}"
    # Normalize CRLF → LF so Windows-edited files match Unix search blocks and vice-versa.
    src_n = src.replace("\r\n", "\n")
    search_n = search_block.replace("\r\n", "\n")
    replace_n = replace_block.replace("\r\n", "\n")
    if search_n not in src_n:
        return f"error: search_block not found in {file}"
    new_src = src_n.replace(search_n, replace_n, 1)
    # Atomic write: write to a temp file in the same directory, then rename.
    # Prevents a half-written file if the process is killed mid-write.
    tmp = None
    try:
        fd, tmp = tempfile.mkstemp(dir=os.path.dirname(full_path))
        with os.fdopen(fd, "w") as _f:
            _f.write(new_src)
        os.replace(tmp, full_path)
    except Exception as _e:
        if tmp:
            try:
                os.unlink(tmp)
            except OSError:
                pass
        return f"error writing {file}: {_e}"
    return "applied"

@tool
def list_dir(path: str) -> str:
    """List files and sub-directories inside a project directory.
    Returns one entry per line; directories are suffixed with '/'.
    Use this to explore the repo structure before reading specific files."""
    try:
        full = os.path.realpath(os.path.join(PROJECT_PATH, path if path else "."))
        root = os.path.realpath(PROJECT_PATH)
        if not (full == root or full.startswith(root + os.sep)):
            return f"error: path escapes project root: {path}"
        if not os.path.isdir(full):
            return f"error: {path} is not a directory"
        entries = []
        for entry in sorted(os.scandir(full), key=lambda e: (not e.is_dir(), e.name)):
            entries.append(entry.name + ("/" if entry.is_dir() else ""))
        return "\n".join(entries) if entries else "(empty directory)"
    except FileNotFoundError:
        return f"error: directory not found: {path}"
    except Exception as _e:
        return f"error listing {path}: {_e}"

_BUILTIN_TOOLS = [read_file, list_dir, request_edit]


# ── LLM provider dispatcher ───────────────────────────────────────────────────
# Provider is inferred from the model name prefix so each agent can use a
# different vendor without any extra configuration.
#
#   claude-*          → Anthropic          secret: ANTHROPIC_API_KEY
#   gpt-*, o1-*, o3-* → OpenAI             secret: OPENAI_API_KEY
#   o4-*              → OpenAI             secret: OPENAI_API_KEY
#   gemini-*          → Google             secret: GOOGLE_API_KEY
#   grok-*            → xAI               secret: XAI_API_KEY

def _make_llm(model: str, tools: list):
    if model.startswith("claude-"):
        from langchain_anthropic import ChatAnthropic
        api_key = _ipc.get_secret("ANTHROPIC_API_KEY")
        return ChatAnthropic(model=model, api_key=api_key).bind_tools(tools)
    elif model.startswith(("gpt-", "o1-", "o3-", "o4-")):
        from langchain_openai import ChatOpenAI
        api_key = _ipc.get_secret("OPENAI_API_KEY")
        return ChatOpenAI(model=model, api_key=api_key).bind_tools(tools)
    elif model.startswith("gemini-"):
        from langchain_google_genai import ChatGoogleGenerativeAI
        api_key = _ipc.get_secret("GOOGLE_API_KEY")
        return ChatGoogleGenerativeAI(model=model, google_api_key=api_key).bind_tools(tools)
    elif model.startswith("grok-"):
        from langchain_xai import ChatXAI
        api_key = _ipc.get_secret("XAI_API_KEY")
        return ChatXAI(model=model, xai_api_key=api_key).bind_tools(tools)
    else:
        raise ValueError(
            f"Unknown LLM provider for model {model!r}. "
            "Supported prefixes: claude-, gpt-, o1-, o3-, o4-, gemini-, grok-"
        )


# ── Agent nodes ───────────────────────────────────────────────────────────────
[[range .Agents]]
def _node_[[.Name | pyident]](state: AgentState) -> dict:
    _model = _make_llm([[.Model | pystr]], _BUILTIN_TOOLS)
    _sys = [[.Role | pystr]][[if .OutgoingConditions]] + "\n\nWhen finished, end your response with: ROUTE: <label> — valid labels: [[.OutgoingConditions | pyset]]"[[end]]
    _resp = _model.invoke([{"role": "system", "content": _sys}] + state.messages)
    _usage = getattr(_resp, 'usage_metadata', None) or {}
    _ipc.emit_state([[.Name | pystr]], {
        "content": str(_resp.content)[:300],
        "input_tokens": int(_usage.get("input_tokens", 0)),
        "output_tokens": int(_usage.get("output_tokens", 0)),
        "model": [[.Model | pystr]],
    })
    _next = [[$.SupervisorName | pystr]][[if .OutgoingConditions]]
    _m = _re.search(r'ROUTE:\s*(\S+)', str(_resp.content))
    if _m and _m.group(1) in [[.OutgoingConditions | pyset]]:
        _next = _m.group(1)[[end]]
    return {"messages": [_resp], "active_agents": [], "next_agent": _next}
[[end]]

# ── Supervisor fan-out router (Constraint A: active_agents + Send) ─────────────
# Routes to agents named in active_agents; falls back to all worker nodes.

_WORKER_NAMES = [[.Agents | pylist]]

def _supervisor_route(state: AgentState) -> list:
    targets = state.active_agents if state.active_agents else [
        t for t in _WORKER_NAMES if t != [[.SupervisorName | pystr]]
    ]
    return [Send(t, state) for t in targets]


# ── Path-map routers (non-supervisor conditional edges) ────────────────────────
# Each reads state.next_agent (set by the preceding node) to select the successor.
[[range .ConditionalEdgeGroups]][[if not .FromIsSupervisor]]
def _route_[[.FromIdent]](state: AgentState) -> str:
    return state.next_agent
[[end]][[end]]

# ── Graph assembly ────────────────────────────────────────────────────────────

_g = StateGraph(AgentState)
[[range .Agents]]
_g.add_node([[.Name | pystr]], _node_[[.Name | pyident]])[[end]]

[[range .Edges]][[if not .Condition]]
_g.add_edge([[.From | pystr]], [[.To | pystr]])[[end]][[end]]
[[range .ConditionalEdgeGroups]][[if .FromIsSupervisor]]
_g.add_conditional_edges([[.From | pystr]], _supervisor_route)[[else]]
_g.add_conditional_edges([[.From | pystr]], _route_[[.FromIdent]], {
    [[range .Routes]][[.Condition | pystr]]: [[if .ToIsEND]]END[[else]][[.To | pystr]][[end]],
    [[end]]
})[[end]][[end]]

_g.set_entry_point([[.SupervisorName | pystr]])
_app = _g.compile()


# ── Execute ───────────────────────────────────────────────────────────────────

try:
    _init = {"messages": [{"role": "user", "content": PRD_CONTEXT + "\n\n" + REPO_CONTEXT}]}
    _result = _app.invoke(_init)
    _ipc.emit_state("__end__", {"status": "success"})
except Exception as _exc:
    _ipc.emit_state("__error__", {"error": str(_exc)})
    raise
finally:
    _ipc.close()
`
