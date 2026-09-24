"""staircase_runner.ipc — the one IPC client every stAirCase runtime uses.

Embedded in the Go binary and written into the workspace venv at every launch
(as staircase_runner/ipc.py), so the client always matches the orchestrator.
Wire format: JSON Lines over a Unix socket (TCP loopback on Windows), see
proto/ipc.v1.schema.json.

Guarantees:
- A pending approval waits as long as the operator needs: Go owns cancellation
  and closes the socket (or kills the process) when the run ends.
- Any transport failure ends the process (exit 70). Continuing could pair a
  request with a stale reply, and a tool-level exception handler (e.g.
  LangGraph's ToolNode) would otherwise swallow the error.
- A daemon thread sends heartbeats so Go's idle deadline only trips on a
  runtime that is really hung, not on a long LLM call.
"""
from __future__ import annotations

import json
import os
import socket
import sys
import threading
import time
from typing import Any

HEARTBEAT_SECS = 10.0  # well under the orchestrator's 30 s idle deadline
REPLY_TIMEOUT = 30.0  # auth and secret replies; approvals have no timeout
EXIT_IPC_FAILURE = 70
MAX_LINE_BYTES = 256 * 1024  # the orchestrator's line limit, newline included (ipc/server.go readBufferSize)


class IPCClient:
    def __init__(self, sock_path: str, token: str, heartbeat_secs: float = HEARTBEAT_SECS) -> None:
        self._lock = threading.Lock()  # one request/response (or send) at a time
        self._buf = b""
        self._closed = False  # set by close(): later socket errors are not failures
        try:
            if ":" in sock_path and not sock_path.startswith("/"):
                host, port = sock_path.rsplit(":", 1)
                self._sock = socket.create_connection((host, int(port)))
            else:
                self._sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                self._sock.connect(sock_path)
        except OSError as e:
            self._fail(f"connect {sock_path}: {e!r}")
        resp = self._request({"type": "auth", "token": token})
        if resp.get("type") != "auth_ok":
            self._fail(f"auth rejected: {resp}")
        if heartbeat_secs:
            threading.Thread(target=self._heartbeat, args=(heartbeat_secs,), daemon=True).start()

    def emit_state(self, agent: str, state: dict) -> None:
        with self._lock:
            self._send({"type": "state_emit", "active_agent": agent, "state": state})

    def yield_request(self, agent: str, action: str, edits: list, reasoning: str,
                      confidence: float = 0.9, batch_id: str = "") -> dict:
        msg: dict[str, Any] = {
            "type": "yield_request", "agent_name": agent, "action_type": action,
            "proposed_edits": edits, "reasoning_trace": reasoning, "confidence_score": confidence,
        }
        if batch_id:
            msg["batch_id"] = batch_id
        # JSON escaping can grow text up to 6x; a line over the limit would make
        # the orchestrator drop the connection, so refuse it here instead.
        size = len(json.dumps(msg)) + 1
        if size > MAX_LINE_BYTES:
            return {"type": "yield_response", "approved": False,
                    "feedback": f"proposal is {size:,} bytes encoded; at most {MAX_LINE_BYTES:,} can be sent "
                                "for approval — split it into smaller changes"}
        return self._request(msg, wait_forever=True)

    def get_secret(self, key: str, project_id: int | None = None) -> str | None:
        msg: dict[str, Any] = {"type": "secret_request", "key_name": key}
        if project_id is not None:
            msg["project_id"] = project_id
        return self._request(msg).get("plaintext_value")

    def close(self) -> None:
        self._closed = True
        try:
            self._sock.close()
        except OSError:
            pass

    # ── internals ─────────────────────────────────────────────────────────────

    def _fail(self, why: str) -> None:
        print(f"staircase ipc: {why} — exiting", file=sys.stderr, flush=True)
        if hasattr(self, "_sock"):
            self.close()
        os._exit(EXIT_IPC_FAILURE)

    def _send(self, msg: dict) -> None:
        try:
            self._sock.sendall((json.dumps(msg) + "\n").encode())
        except OSError as e:
            if not self._closed:
                self._fail(f"send {msg.get('type')}: {e!r}")

    def _request(self, msg: dict, wait_forever: bool = False) -> dict:
        with self._lock:
            self._send(msg)
            try:
                self._sock.settimeout(None if wait_forever else REPLY_TIMEOUT)
                while True:
                    while b"\n" not in self._buf:
                        chunk = self._sock.recv(65536)
                        if not chunk:
                            raise ConnectionError("socket closed by orchestrator")
                        self._buf += chunk
                    line, self._buf = self._buf.split(b"\n", 1)
                    reply = json.loads(line)
                    if reply.get("type") != "heartbeat_ack":  # acks of earlier beats
                        return reply
            except (OSError, ValueError) as e:  # timeout, closed socket, bad JSON
                if not self._closed:
                    self._fail(f"{msg.get('type')}: {e!r}")
        return {}  # unreachable: _fail exits

    def _heartbeat(self, every: float) -> None:
        # Skip a beat while a request is in flight: Go is then blocked on the
        # operator and not reading, and queued beats would trip its rate limit.
        while not self._closed:
            time.sleep(every)
            if not self._closed and self._lock.acquire(blocking=False):
                try:
                    self._send({"type": "heartbeat"})
                finally:
                    self._lock.release()
