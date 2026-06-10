#!/usr/bin/env python3
"""agent_stub.py — offline stand-in agent for the stAirCase demo.

This script speaks the *real* stAirCase IPC protocol — the same wire format the
generated LangGraph runtime uses — but contains no LLM call, so the demo runs
fully offline with no API key. Its job is to exercise the governance layer:

  1. authenticate to the orchestrator over the Unix Domain Socket
  2. emit a "thinking" state so the live monitor shows activity
  3. propose creating a file via a yield_request that carries content_hash
     (the approval-content binding) and block for human approval
  4. on approval, write exactly the approved bytes — so the orchestrator's
     pre-commit hash check passes and the change is committed
  5. emit a success state and exit cleanly

The orchestrator delivers the bootstrap message (socket path + auth token) on
stdin, then this script connects back over the socket. The project path and the
file to create are passed via environment variables set by run-demo.sh.
"""
import hashlib
import json
import os
import socket
import sys
import time

PROJECT_PATH = os.environ["DEMO_PROJECT_PATH"]
TARGET_FILE = os.environ.get("DEMO_TARGET_FILE", "GREETING.md")
NEW_CONTENT = os.environ.get(
    "DEMO_FILE_CONTENT",
    "# Hello from stAirCase\n\n"
    "This file was written by an AI agent — but only after a human approved it,\n"
    "and only after the orchestrator confirmed the bytes matched the approval.\n",
)


def _connect(sock_path: str) -> socket.socket:
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    for _ in range(40):
        try:
            s.connect(sock_path)
            return s
        except OSError:
            time.sleep(0.05)
    raise SystemExit("agent_stub: could not connect to IPC socket")


def _send(sock: socket.socket, msg: dict) -> None:
    sock.sendall((json.dumps(msg) + "\n").encode())


def _recv(sock: socket.socket, buf: bytearray) -> dict:
    while b"\n" not in buf:
        chunk = sock.recv(65536)
        if not chunk:
            raise SystemExit("agent_stub: IPC socket closed unexpectedly")
        buf += chunk
    line, _, rest = bytes(buf).partition(b"\n")
    buf.clear()
    buf += rest
    return json.loads(line)


def main() -> int:
    boot = json.loads(sys.stdin.readline())
    if boot.get("type") != "bootstrap":
        raise SystemExit(f"agent_stub: unexpected bootstrap message: {boot}")

    sock = _connect(boot["socket_path"])
    buf = bytearray()

    # 1. Authenticate.
    _send(sock, {"type": "auth", "token": boot["token"]})
    if _recv(sock, buf).get("type") != "auth_ok":
        raise SystemExit("agent_stub: authentication failed")

    # 2. Show the operator the agent is working.
    _send(sock, {
        "type": "state_emit", "active_agent": "coder",
        "state": {"content": f"Proposing a new file: {TARGET_FILE}", "model": "stub"},
    })

    # 3. Propose the edit, bound to the exact post-edit content via content_hash.
    content_hash = hashlib.sha256(NEW_CONTENT.encode()).hexdigest()
    preview = NEW_CONTENT if len(NEW_CONTENT) <= 4000 else NEW_CONTENT[:4000] + "\n…"
    _send(sock, {
        "type": "yield_request", "agent_name": "coder", "action_type": "file_edit",
        "proposed_edits": [{
            "file": TARGET_FILE,
            "search_block": "(new file)",
            "replace_block": preview,
            "content_hash": content_hash,
        }],
        "reasoning_trace": "Create a greeting file to demonstrate the HITL flow.",
        "confidence_score": 0.55,
    })
    resp = _recv(sock, buf)
    if not resp.get("approved"):
        _send(sock, {
            "type": "state_emit", "active_agent": "__end__",
            "state": {"status": "rejected", "feedback": resp.get("feedback", "")},
        })
        sock.close()
        return 1

    # 4. Write the file. Normally this is exactly the approved bytes. In
    #    DEMO_TAMPER mode the agent writes DIFFERENT bytes than it got approved
    #    — the orchestrator must detect the hash mismatch and refuse to commit.
    written = NEW_CONTENT
    if os.environ.get("DEMO_TAMPER") == "1":
        written = NEW_CONTENT + "\n<!-- injected AFTER approval — operator never saw this -->\n"
    full_path = os.path.join(PROJECT_PATH, TARGET_FILE)
    tmp_path = full_path + ".tmp"
    with open(tmp_path, "w", encoding="utf-8") as f:
        f.write(written)
    os.replace(tmp_path, full_path)

    # 5. Signal success and close.
    _send(sock, {
        "type": "state_emit", "active_agent": "__end__",
        "state": {"status": "success"},
    })
    sock.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
