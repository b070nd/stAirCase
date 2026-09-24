"""Behavior of the file tools shipped in the generated runtime.

The tools section of src/internal/template/graph_exec.py.tmpl contains no Go
template directives, so it is loaded here as a module with a fake IPC client —
testing the exact code agents run, without LangGraph.
"""
from __future__ import annotations

import importlib.util
import os
import subprocess
import sys
import tempfile
import threading
from pathlib import Path

TEMPLATE = Path(__file__).resolve().parents[2] / "src" / "internal" / "template" / "graph_exec.py.tmpl"


class FakeIPC:
    def __init__(self, approve: bool = True):
        self.approve = approve
        self.requests: list[dict] = []

    def yield_request(self, agent, action, edits, reasoning, confidence=0.9, batch_id=""):
        self.requests.append({"action": action, "edits": edits})
        return {"approved": self.approve, "feedback": "" if self.approve else "no"}


def load_tools(project: Path, ipc: FakeIPC):
    src = TEMPLATE.read_text(encoding="utf-8")
    section = src[src.index("@tool\ndef read_file"):src.index("_BUILTIN_TOOLS =")]
    assert "[[" not in section, "tools section must stay free of template directives"
    module_file = Path(tempfile.mkdtemp()) / "shipped_tools.py"
    module_file.write_text("import hashlib, os, subprocess, tempfile\n\ndef tool(f):\n    return f\n\n" + section,
                           encoding="utf-8")
    spec = importlib.util.spec_from_file_location("shipped_tools", module_file)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    mod._ipc, mod.PROJECT_PATH, mod._agent_ctx = ipc, str(project), threading.local()
    return mod


def test_create_sends_the_full_content_it_writes(tmp_path):
    ipc = FakeIPC()
    tools = load_tools(tmp_path, ipc)
    content = "x" * 50_000  # well past the old 10,000-char preview
    assert tools.create_file("big.txt", content, "r").startswith("created")
    assert ipc.requests[0]["edits"][0]["replace_block"] == content, "the operator must see what is written"
    assert (tmp_path / "big.txt").read_text() == content


def test_create_refuses_what_cannot_be_approved(tmp_path):
    ipc = FakeIPC()
    tools = load_tools(tmp_path, ipc)
    out = tools.create_file("huge.txt", "x" * (200 * 1024 + 1), "r")
    assert out.startswith("error"), out
    assert ipc.requests == [], "nothing is sent for approval"
    assert not (tmp_path / "huge.txt").exists()


def test_create_reports_a_directory_failure_instead_of_crashing(tmp_path):
    (tmp_path / "blocker").write_text("a file where a directory is needed")
    tools = load_tools(tmp_path, FakeIPC())
    out = tools.create_file("blocker/child.txt", "x", "r")
    assert out.startswith("error"), out


def test_delete_file_needs_approval(tmp_path):
    (tmp_path / "old.txt").write_text("bye\n")
    rejecting = FakeIPC(approve=False)
    assert load_tools(tmp_path, rejecting).delete_file("old.txt", "r").startswith("rejected")
    assert (tmp_path / "old.txt").exists()
    assert rejecting.requests[0]["edits"][0]["search_block"] == "(delete file)"

    approving = FakeIPC()
    assert load_tools(tmp_path, approving).delete_file("old.txt", "r").startswith("deleted")
    assert not (tmp_path / "old.txt").exists()


def test_delete_file_stays_inside_the_project(tmp_path):
    outside = tmp_path.parent / "outside.txt"
    outside.write_text("keep")
    ipc = FakeIPC()
    tools = load_tools(tmp_path, ipc)
    assert tools.delete_file("../outside.txt", "r").startswith("error")
    assert outside.exists() and ipc.requests == []


def test_edit_normalizes_newlines_like_the_orchestrator(tmp_path):
    (tmp_path / "f.txt").write_bytes(b"a\r\nb\r\n")
    tools = load_tools(tmp_path, FakeIPC())
    assert tools.request_edit("f.txt", "b\n", "B\n", "r") == "applied"
    assert (tmp_path / "f.txt").read_bytes() == b"a\nB\n"


def test_file_tools_write_utf8_whatever_the_locale(tmp_path):
    # The orchestrator derives UTF-8 bytes; tools using the locale's encoding
    # write other bytes (or fail) under a non-UTF-8 locale, which LANG/LC_*
    # pass through to the runtime. C with UTF-8 mode off is ASCII everywhere.
    (tmp_path / "f.txt").write_bytes("café\n".encode())
    script = (
        "import sys; from pathlib import Path; sys.path.insert(0, sys.argv[1])\n"
        "from test_template_tools import FakeIPC, load_tools\n"
        "tools = load_tools(Path(sys.argv[2]), FakeIPC())\n"
        "print(tools.create_file('new.txt', 'é\\n', 'r'))\n"
        "print(tools.request_edit('f.txt', 'café', 'crème', 'r'))\n"
    )
    env = {**os.environ, "LC_ALL": "C", "LANG": "C", "PYTHONUTF8": "0"}
    out = subprocess.run([sys.executable, "-c", script, str(Path(__file__).parent), str(tmp_path)],
                         env=env, capture_output=True, text=True, check=True).stdout.split("\n")
    assert out[:2] == ["created new.txt", "applied"], out
    assert (tmp_path / "new.txt").read_bytes() == "é\n".encode()
    assert (tmp_path / "f.txt").read_bytes() == "crème\n".encode()
