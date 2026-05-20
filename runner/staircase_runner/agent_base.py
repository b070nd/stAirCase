"""agent_base.py — stAirCase agent base layer for Python swarm agents.

Provides:
  - ``AgentState``   — typed state schema shared across all graph nodes
  - ``make_tools``   — factory that creates the built-in HITL tool set
  - ``make_llm``     — factory that wires the correct LLM provider
  - ``make_agent_node`` — factory that creates a LangGraph node function

All factories receive ``project_path`` and ``ipc`` as arguments so that the
generated graph script can remain thin and topology-focused.
"""
from __future__ import annotations

import os
import re
import subprocess
import tempfile
from typing import TYPE_CHECKING, Annotated, Any, Callable

from typing_extensions import TypedDict

# LangGraph imports — type: ignore because langgraph lacks complete py.typed.
from langgraph.graph.message import add_messages  # type: ignore[import-untyped]
from langchain_core.messages import BaseMessage  # type: ignore[import-untyped]
from langchain_core.tools import tool  # type: ignore[import-untyped]

if TYPE_CHECKING:
    from staircase_runner.ipc import IPCClient


# ── State schema ──────────────────────────────────────────────────────────────


class AgentState(TypedDict):
    """Shared state passed through every node in the LangGraph StateGraph."""

    messages: Annotated[list[BaseMessage], add_messages]
    active_agents: list[str]
    next_agent: str


# ── Built-in tool factory ─────────────────────────────────────────────────────


def make_tools(project_path: str, ipc: "IPCClient") -> list[Any]:
    """Return the five built-in HITL-aware tools bound to *project_path* and *ipc*.

    Tools are created as closures so each invocation uses the correct runtime
    state without relying on module-level globals.

    Args:
        project_path: Absolute path to the project root on disk.
        ipc:          Live :class:`IPCClient` instance for this run.
    """
    _root = os.path.realpath(project_path)

    def _check_path(path: str) -> str | None:
        """Return None if *path* is safe, or an error string if it escapes root."""
        full = os.path.realpath(os.path.join(_root, path))
        if full == _root or full.startswith(_root + os.sep):
            return None
        return f"error: path escapes project root: {path}"

    @tool  # type: ignore[misc]
    def read_file(path: str) -> str:
        """Read a source file relative to the project root (max 100 KB)."""
        err = _check_path(path)
        if err:
            return err
        full = os.path.realpath(os.path.join(_root, path))
        try:
            size = os.path.getsize(full)
            if size > 102_400:
                return f"error: {path} is too large ({size} bytes); use a narrower path"
            with open(full) as f:
                return f.read()
        except FileNotFoundError:
            return f"error: file not found: {path}"
        except IsADirectoryError:
            return f"error: {path} is a directory, not a file"
        except OSError as exc:
            return f"error reading {path}: {exc}"

    @tool  # type: ignore[misc]
    def list_dir(path: str) -> str:
        """List files and sub-directories inside a project directory."""
        err = _check_path(path if path else ".")
        if err:
            return err
        full = os.path.realpath(os.path.join(_root, path if path else "."))
        try:
            if not os.path.isdir(full):
                return f"error: {path} is not a directory"
            entries = [
                e.name + ("/" if e.is_dir() else "")
                for e in sorted(os.scandir(full), key=lambda e: (not e.is_dir(), e.name))
            ]
            return "\n".join(entries) if entries else "(empty directory)"
        except FileNotFoundError:
            return f"error: directory not found: {path}"
        except OSError as exc:
            return f"error listing {path}: {exc}"

    @tool  # type: ignore[misc]
    def request_edit(
        file: str,
        search_block: str,
        replace_block: str,
        reasoning: str,
        caller: str = "agent",
    ) -> str:
        """Propose an exact search-and-replace edit inside an existing file.

        Requires HITL approval before the edit is applied.
        """
        err = _check_path(file)
        if err:
            return err
        full = os.path.realpath(os.path.join(_root, file))
        resp = ipc.yield_request(
            caller,
            "file_edit",
            [{"file": file, "search_block": search_block, "replace_block": replace_block}],
            reasoning,
        )
        if not resp.get("approved"):
            return f"rejected: {resp.get('feedback', 'no feedback')}"
        try:
            src = open(full).read()
        except OSError as exc:
            return f"error opening {file}: {exc}"
        src_n = src.replace("\r\n", "\n")
        search_n = search_block.replace("\r\n", "\n")
        replace_n = replace_block.replace("\r\n", "\n")
        if search_n not in src_n:
            return f"error: search_block not found in {file}"
        new_src = src_n.replace(search_n, replace_n, 1)
        tmp: str | None = None
        try:
            fd, tmp = tempfile.mkstemp(dir=os.path.dirname(full))
            with os.fdopen(fd, "w") as f:
                f.write(new_src)
            os.replace(tmp, full)
        except OSError as exc:
            if tmp:
                try:
                    os.unlink(tmp)
                except OSError:
                    pass
            return f"error writing {file}: {exc}"
        return "applied"

    @tool  # type: ignore[misc]
    def create_file(
        path: str,
        content: str,
        reasoning: str,
        caller: str = "agent",
    ) -> str:
        """Create a new file inside the project root (requires HITL approval)."""
        err = _check_path(path)
        if err:
            return err
        full = os.path.realpath(os.path.join(_root, path))
        preview = content[:200] + ("…" if len(content) > 200 else "")
        resp = ipc.yield_request(
            caller,
            "file_edit",
            [{"file": path, "search_block": "(new file)", "replace_block": preview}],
            reasoning,
        )
        if not resp.get("approved"):
            return f"rejected: {resp.get('feedback', 'no feedback')}"
        tmp2: str | None = None
        try:
            os.makedirs(os.path.dirname(full), exist_ok=True)
            fd, tmp2 = tempfile.mkstemp(dir=os.path.dirname(full))
            with os.fdopen(fd, "w") as f:
                f.write(content)
            os.replace(tmp2, full)
        except OSError as exc:
            if tmp2:
                try:
                    os.unlink(tmp2)
                except OSError:
                    pass
            return f"error creating {path}: {exc}"
        return f"created {path}"

    @tool  # type: ignore[misc]
    def run_shell(
        command: str,
        reasoning: str,
        working_dir: str = "",
        caller: str = "agent",
    ) -> str:
        """Execute a shell command inside the project root (requires HITL approval)."""
        resp = ipc.yield_request(
            caller,
            "shell_exec",
            [{"file": working_dir or ".", "search_block": "(shell)", "replace_block": command}],
            reasoning,
        )
        if not resp.get("approved"):
            return f"rejected: {resp.get('feedback', 'no feedback')}"
        cwd = (
            os.path.realpath(os.path.join(_root, working_dir)) if working_dir else _root
        )
        if not (cwd == _root or cwd.startswith(_root + os.sep)):
            return "error: working_dir escapes project root"
        try:
            result = subprocess.run(
                command,
                shell=True,
                cwd=cwd,
                capture_output=True,
                text=True,
                timeout=120,
            )
            out = result.stdout[-4096:] if len(result.stdout) > 4096 else result.stdout
            err = result.stderr[-2048:] if len(result.stderr) > 2048 else result.stderr
            return f"exit={result.returncode}\n{out}" + (f"STDERR: {err}" if err else "")
        except subprocess.TimeoutExpired:
            return "error: command timed out after 120 s"
        except OSError as exc:
            return f"error running command: {exc}"

    return [read_file, list_dir, request_edit, create_file, run_shell]


# ── LLM factory ───────────────────────────────────────────────────────────────


def make_llm(model: str, ipc: "IPCClient", tools: list[Any]) -> Any:
    """Return a bound LLM for *model*, fetching API keys via *ipc*.

    When ``ipc.boot["replay_llm"]`` is set the call returns a :class:`ReplayClient`
    that replays recorded responses without touching the real API.

    When ``ipc.boot["record_llm"]`` is set the real LLM is wrapped in a
    :class:`RecordingClient` that appends every exchange to a per-run
    :class:`RecordingSession` stored on ``ipc``.  Call
    ``ipc._recording_session.close()`` (or use it as a context manager) at
    script exit to flush the recording to disk.

    Supported model prefixes:
      ``claude-*``               → Anthropic (ANTHROPIC_API_KEY)
      ``gpt-*``, ``o1-*``, ``o3-*``, ``o4-*`` → OpenAI (OPENAI_API_KEY)
      ``gemini-*``               → Google (GOOGLE_API_KEY)
      ``grok-*``                 → xAI (XAI_API_KEY)
    """
    boot = getattr(ipc, "boot", {})
    replay_path = boot.get("replay_llm", "")
    record_path = boot.get("record_llm", "")

    if replay_path:
        from staircase_runner.recording import ReplayClient

        return ReplayClient(replay_path)

    # Build the real (provider-specific) LLM.
    if model.startswith("claude-"):
        from langchain_anthropic import ChatAnthropic  # type: ignore[import-untyped]

        real_llm: Any = ChatAnthropic(  # type: ignore[call-arg]
            model=model, api_key=ipc.get_secret("ANTHROPIC_API_KEY")
        ).bind_tools(tools)
    elif model.startswith(("gpt-", "o1-", "o3-", "o4-")):
        from langchain_openai import ChatOpenAI  # type: ignore[import-untyped]

        real_llm = ChatOpenAI(  # type: ignore[call-arg]
            model=model, api_key=ipc.get_secret("OPENAI_API_KEY")
        ).bind_tools(tools)
    elif model.startswith("gemini-"):
        from langchain_google_genai import ChatGoogleGenerativeAI  # type: ignore[import-untyped]

        real_llm = ChatGoogleGenerativeAI(  # type: ignore[call-arg]
            model=model, google_api_key=ipc.get_secret("GOOGLE_API_KEY")
        ).bind_tools(tools)
    elif model.startswith("grok-"):
        from langchain_xai import ChatXAI  # type: ignore[import-untyped]

        real_llm = ChatXAI(  # type: ignore[call-arg]
            model=model, xai_api_key=ipc.get_secret("XAI_API_KEY")
        ).bind_tools(tools)
    else:
        raise ValueError(
            f"Unknown LLM provider for model {model!r}. "
            "Supported prefixes: claude-, gpt-, o1-, o3-, o4-, gemini-, grok-"
        )

    if record_path:
        from staircase_runner.recording import RecordingClient, RecordingSession

        # Lazily create one shared session per ipc instance (covers multi-model runs).
        if not hasattr(ipc, "_recording_session"):
            ipc._recording_session = RecordingSession(record_path)  # type: ignore[attr-defined]
        return RecordingClient(real_llm, model, ipc._recording_session)  # type: ignore[attr-defined]

    return real_llm


# ── Agent node factory ────────────────────────────────────────────────────────

_ROUTE_PATTERN = re.compile(r"ROUTE:\s*(\S+)")


def make_agent_node(
    name: str,
    model: str,
    role: str,
    tools: list[Any],
    ipc: "IPCClient",
    supervisor_name: str,
    outgoing_conditions: list[str] | None = None,
) -> Callable[[AgentState], dict[str, Any]]:
    """Return a LangGraph node function for an agent.

    Args:
        name:                 Agent node name (matches topology).
        model:                LLM model identifier.
        role:                 System prompt / role description.
        tools:                Tool list from :func:`make_tools`.
        ipc:                  Live :class:`IPCClient` for this run.
        supervisor_name:      Name of the supervisor node (routing default).
        outgoing_conditions:  Condition labels for conditional edges, if any.
    """
    conditions: list[str] = outgoing_conditions or []
    llm: Any = None  # lazy-init on first call to avoid auth at import time

    def agent_node(state: AgentState) -> dict[str, Any]:
        nonlocal llm
        if llm is None:
            llm = make_llm(model, ipc, tools)

        sys_prompt = role
        if conditions:
            labels = "{" + ", ".join(repr(c) for c in conditions) + "}"
            sys_prompt += f"\n\nWhen finished, end your response with exactly: ROUTE: <label> — valid labels: {labels}"

        resp: Any = llm.invoke(
            [{"role": "system", "content": sys_prompt}] + list(state["messages"])
        )
        usage: dict[str, Any] = getattr(resp, "usage_metadata", None) or {}
        has_tools = bool(getattr(resp, "tool_calls", None))

        ipc.emit_state(
            name,
            {
                "content": str(resp.content)[:300],
                "has_tool_calls": has_tools,
            },
            input_tokens=int(usage.get("input_tokens", 0)),
            output_tokens=int(usage.get("output_tokens", 0)),
            model=model,
        )

        if has_tools:
            return {"messages": [resp]}

        next_agent = supervisor_name
        if conditions:
            m = _ROUTE_PATTERN.search(str(resp.content))
            if m and m.group(1) in conditions:
                next_agent = m.group(1)
        return {"messages": [resp], "next_agent": next_agent}

    return agent_node
