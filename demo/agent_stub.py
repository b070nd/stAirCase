#!/usr/bin/env python3
"""agent_stub.py — offline stand-in agent for the stAirCase demo.

This script uses the *real* stAirCase IPC client (staircase_runner.ipc, the one
the generated LangGraph runtime imports) but contains no LLM call, so the demo
runs fully offline with no API key. Its job is to exercise the governance layer:

  1. authenticate to the orchestrator over the Unix Domain Socket
  2. emit a "thinking" state so the live monitor shows activity
  3. propose creating a file via a yield_request that carries content_hash
     (the approval-content binding) and block for human approval
  4. on approval, write exactly the approved bytes — so the orchestrator's
     pre-commit hash check passes and the change is committed
  5. emit a success state and exit cleanly

The orchestrator delivers the bootstrap message (socket path, auth token and
the run's worktree) on stdin, then this script connects back over the socket.
The runtime gets no inherited environment, so run-demo.sh prepends a SETTINGS
line (target file, tamper mode) to this script instead.
"""
import hashlib
import json
import os
import sys

SETTINGS = globals().get("SETTINGS", {})
TARGET_FILE = SETTINGS.get("target_file", "GREETING.md")
TAMPER = SETTINGS.get("tamper", False)
NEW_CONTENT = (
    "# Hello from stAirCase\n\n"
    "This file was written by an AI agent — but only after a human approved it,\n"
    "and only after the orchestrator confirmed the bytes matched the approval.\n"
)


def main() -> int:
    boot = json.loads(sys.stdin.readline())
    if boot.get("type") != "bootstrap":
        raise SystemExit(f"agent_stub: unexpected bootstrap message: {boot}")
    sys.path.insert(0, boot["runner_path"])  # written by the orchestrator at launch
    from staircase_runner.ipc import IPCClient

    # 1. Authenticate (the client does it on connect).
    ipc = IPCClient(boot["socket_path"], boot["token"])

    # 2. Show the operator the agent is working.
    ipc.emit_state("coder", {"content": f"Proposing a new file: {TARGET_FILE}", "model": "stub"})

    # 3. Propose the edit, bound to the exact post-edit content via content_hash.
    content_hash = hashlib.sha256(NEW_CONTENT.encode()).hexdigest()
    preview = NEW_CONTENT if len(NEW_CONTENT) <= 4000 else NEW_CONTENT[:4000] + "\n…"
    resp = ipc.yield_request("coder", "file_edit", [{
        "file": TARGET_FILE,
        "search_block": "(new file)",
        "replace_block": preview,
        "content_hash": content_hash,
    }], "Create a greeting file to demonstrate the HITL flow.", confidence=0.55)
    if not resp.get("approved"):
        ipc.emit_state("__end__", {"status": "rejected", "feedback": resp.get("feedback", "")})
        ipc.close()
        return 1

    # 4. Write the file. Normally this is exactly the approved bytes. In
    #    DEMO_TAMPER mode the agent writes DIFFERENT bytes than it got approved
    #    — the orchestrator must detect the hash mismatch and refuse to commit.
    written = NEW_CONTENT
    if TAMPER:
        written = NEW_CONTENT + "\n<!-- injected AFTER approval — operator never saw this -->\n"
    full_path = os.path.join(boot["project_path"], TARGET_FILE)  # the run's worktree
    tmp_path = full_path + ".tmp"
    with open(tmp_path, "w", encoding="utf-8") as f:
        f.write(written)
    os.replace(tmp_path, full_path)

    # 5. Signal success and close.
    ipc.emit_state("__end__", {"status": "success"})
    ipc.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
