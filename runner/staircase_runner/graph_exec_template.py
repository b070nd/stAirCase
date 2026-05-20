"""graph_exec_template.py — contract skeleton for generated graph_exec_*.py scripts.

This file documents the exact structure that ``staircase compile`` renders for
each case.  It is a valid, mypy-clean Python file with no template variables;
the real generation uses ``src/internal/template/graph_exec.py.tmpl``.

Relationship to the runner package:
  - ``staircase_runner.ipc.IPCClient``      — IPC wire protocol implementation
  - ``staircase_runner.agent_base.AgentState``     — shared TypedDict state schema
  - ``staircase_runner.agent_base.make_tools``     — built-in HITL tool factory
  - ``staircase_runner.agent_base.make_agent_node``— LangGraph node function factory
  - ``staircase_runner.agent_base.make_llm``       — LLM provider dispatcher

To regenerate a script for a case::

    staircase compile <case-id>

The generated script is written to ``$STAIRCASE_DIR/tmp/graph_exec_case<id>.py``
and copied to ``$STAIRCASE_DIR/tmp/graph_exec_<run-id>.py`` at run time.
"""
from __future__ import annotations

import json
import sys

from staircase_runner.ipc import IPCClient
from staircase_runner.agent_base import AgentState, make_tools, make_agent_node

# ── The three values below are injected by Go template rendering. ─────────────
# They are intentionally empty here so the skeleton compiles cleanly with mypy.
PROJECT_PATH: str = ""
PRD_CONTEXT: str = ""
REPO_CONTEXT: str = ""

# ── Bootstrap (generated): read socket_path + token from stdin, connect. ──────
# The real generated script does:
#
#   try:
#       import select as _select
#       _rlist, _, _ = _select.select([sys.stdin], [], [], 30.0)
#       if not _rlist:
#           raise RuntimeError("bootstrap timeout")
#   except (AttributeError, OSError):
#       pass
#   _boot = json.loads(sys.stdin.readline())
#   _ipc = IPCClient(_boot["socket_path"], _boot["token"])

# ── Tool set (generated): one tool list per agent. ───────────────────────────
# The real generated script does (per agent):
#
#   _tools_<agent_ident> = make_tools(PROJECT_PATH, _ipc)

# ── Graph assembly (generated, topology-specific). ───────────────────────────
# Example for a two-agent topology (supervisor + coder):
#
#   from langgraph.graph import StateGraph, END
#   from langgraph.prebuilt import ToolNode
#
#   _g: StateGraph[AgentState] = StateGraph(AgentState)
#   _g.add_node("supervisor", make_agent_node("supervisor", MODEL, ROLE, ...))
#   _g.add_node("coder",      make_agent_node("coder",      MODEL, ROLE, ...))
#   _g.add_node("tools_coder", ToolNode(_tools_coder))
#   _g.add_edge("tools_coder", "coder")
#   _g.set_entry_point("supervisor")
#   _app = _g.compile()
#
#   _result = _app.invoke({"messages": [{"role": "user", "content": PRD_CONTEXT}]})
#   _ipc.close()


def _skeleton_main() -> None:
    """Placeholder entry point — never called by the real generated script."""
    boot = json.loads(sys.stdin.readline())
    ipc = IPCClient(boot["socket_path"], boot["token"], boot=boot)
    tools = make_tools(PROJECT_PATH, ipc)
    node = make_agent_node("agent", "claude-opus-4-5", "You are a helpful assistant.",
                           tools, ipc, "agent")
    state: AgentState = {
        "messages": [],
        "active_agents": [],
        "next_agent": "agent",
    }
    node(state)
    ipc.close()


if __name__ == "__main__":
    _skeleton_main()
