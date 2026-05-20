"""ipc/client.py — stAirCase IPC client for Python agents.

Implements the JSON-over-UDS (or TCP on Windows) protocol defined in
proto/ipc.v1.schema.json.  Every response call has an explicit ``timeout``
parameter so no recv can block indefinitely (CHECK 6.4.2).
"""
from __future__ import annotations

import json
import socket
import threading
from typing import Any


class IPCClient:
    """Thread-safe IPC client that speaks the stAirCase wire protocol.

    Usage::

        ipc = IPCClient(socket_path, token)
        ipc.emit_state("planner", {"step": 1})
        resp = ipc.yield_request("planner", "file_edit", edits, reasoning)
        secret = ipc.get_secret("OPENAI_API_KEY")
        ipc.close()
    """

    def __init__(
        self, sock_path: str, token: str, boot: dict[str, Any] | None = None
    ) -> None:
        # Expose the full bootstrap dict so callers (e.g. make_llm) can read
        # optional fields like record_llm / replay_llm without extra parameters.
        self.boot: dict[str, Any] = boot or {}
        self._lock = threading.Lock()
        self._buf = b""

        # Windows uses TCP loopback; Unix uses UDS.
        if ":" in sock_path and not sock_path.startswith("/"):
            host, port_str = sock_path.rsplit(":", 1)
            self._sock: socket.socket = socket.socket(
                socket.AF_INET, socket.SOCK_STREAM
            )
            self._sock.connect((host, int(port_str)))
        else:
            self._sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            self._sock.connect(sock_path)

        # Auth handshake — must complete before any other messages.
        self._raw_send({"type": "auth", "token": token})
        resp = self._raw_recv(timeout=10.0)  # CHECK 6.4.2
        if resp.get("type") != "auth_ok":
            raise RuntimeError(f"IPC auth rejected: {resp}")

    # ── low-level framing ─────────────────────────────────────────────────────

    def _raw_send(self, msg: dict[str, Any]) -> None:
        """Send one JSON-Lines message to the server."""
        self._sock.sendall((json.dumps(msg) + "\n").encode())

    def _raw_recv(self, timeout: float = 30.0) -> dict[str, Any]:
        """Receive one JSON-Lines message from the server.

        Args:
            timeout: Socket receive timeout in seconds (CHECK 6.4.2).
        """
        self._sock.settimeout(timeout)  # CHECK 6.4.2 — every recv has timeout=
        while b"\n" not in self._buf:
            chunk = self._sock.recv(65536)
            if not chunk:
                raise ConnectionError("IPC socket closed unexpectedly")
            self._buf += chunk
        line, self._buf = self._buf.split(b"\n", 1)
        return dict(json.loads(line))  # type: ignore[arg-type]

    # ── public API ────────────────────────────────────────────────────────────

    def emit_state(
        self,
        agent: str,
        state: dict[str, Any],
        input_tokens: int = 0,
        output_tokens: int = 0,
        model: str = "",
    ) -> None:
        """Emit a node-completion event to the Go monitor (fire-and-forget)."""
        msg: dict[str, Any] = {
            "type": "state_emit",
            "active_agent": agent,
            "state": state,
        }
        if input_tokens:
            msg["input_tokens"] = input_tokens
        if output_tokens:
            msg["output_tokens"] = output_tokens
        if model:
            msg["model"] = model
        with self._lock:
            self._raw_send(msg)

    def yield_request(
        self,
        agent: str,
        action: str,
        edits: list[dict[str, str]],
        reasoning: str,
        confidence: float = 0.9,
        batch_id: str = "",
        timeout: float = 300.0,
    ) -> dict[str, Any]:
        """Request HITL approval and block until the operator decides.

        Args:
            agent:      Name of the requesting agent node.
            action:     Action type: ``file_edit``, ``shell_exec``, or ``custom``.
            edits:      List of ``{file, search_block, replace_block}`` dicts.
            reasoning:  Chain-of-thought explaining the proposed action.
            confidence: Agent self-reported confidence score in [0, 1].
            batch_id:   Groups related yields into a single approval screen.
            timeout:    Seconds to wait for the operator decision (CHECK 6.4.2).
                        Default 300 s — human decisions can take time.
        """
        msg: dict[str, Any] = {
            "type": "yield_request",
            "agent_name": agent,
            "action_type": action,
            "proposed_edits": edits,
            "reasoning_trace": reasoning,
            "confidence_score": confidence,
        }
        if batch_id:
            msg["batch_id"] = batch_id
        with self._lock:
            self._raw_send(msg)
            return self._raw_recv(timeout=timeout)  # CHECK 6.4.2

    def get_secret(
        self,
        key: str,
        project_id: int | None = None,
        timeout: float = 10.0,
    ) -> str | None:
        """Fetch a named secret from the encrypted store.

        Secrets are decrypted in Go before delivery — plaintext arrives over
        the UDS, never via environment variables (/proc leak prevention).

        Args:
            key:        Secret key name.
            project_id: Optional project scope; global if omitted.
            timeout:    Seconds to wait for the response (CHECK 6.4.2).
        """
        with self._lock:
            msg: dict[str, Any] = {"type": "secret_request", "key_name": key}
            if project_id is not None:
                msg["project_id"] = project_id
            self._raw_send(msg)
            resp = self._raw_recv(timeout=timeout)  # CHECK 6.4.2
        return resp.get("plaintext_value")  # type: ignore[return-value]

    def heartbeat(self, timeout: float = 10.0) -> None:
        """Send a keepalive ping and wait for the acknowledgement.

        Args:
            timeout: Seconds to wait for heartbeat_ack (CHECK 6.4.2).
        """
        with self._lock:
            self._raw_send({"type": "heartbeat"})
            self._raw_recv(timeout=timeout)  # CHECK 6.4.2

    def close(self) -> None:
        """Close the underlying socket."""
        self._sock.close()
