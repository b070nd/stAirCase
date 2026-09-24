"""Behavior of the single IPC client (src/internal/runtime/ipc_embed.py).

A fake orchestrator on a real Unix socket plays the Go side. Failure paths run
the client in a subprocess because they end the process (exit 70) by design.
"""
from __future__ import annotations

import json
import os
import socket
import subprocess
import sys
import tempfile
import threading
import time

from staircase_runner import ipc

RUNTIME_DIR = os.path.dirname(ipc.__file__)  # src/internal/runtime (see conftest)


class FakeOrchestrator:
    """Accepts one client, answers auth, then runs the test's script."""

    def __init__(self, script):
        self.dir = tempfile.mkdtemp()  # short path: unix socket length limit
        self.path = os.path.join(self.dir, "s.sock")
        self._srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self._srv.bind(self.path)
        self._srv.listen(1)
        self._buf = b""
        self.error = None
        self._t = threading.Thread(target=self._serve, args=(script,), daemon=True)
        self._t.start()

    def _serve(self, script):
        try:
            self.conn, _ = self._srv.accept()
            assert self.read()["type"] == "auth"
            self.send({"type": "auth_ok"})
            script(self)
        except Exception as e:  # surfaced by join()
            self.error = e

    def read(self):
        while b"\n" not in self._buf:
            chunk = self.conn.recv(65536)
            if not chunk:
                return None
            self._buf += chunk
        line, self._buf = self._buf.split(b"\n", 1)
        return json.loads(line)

    def quiet_for(self, secs) -> bool:
        """True if the client sends nothing for secs."""
        if self._buf:
            return False
        self.conn.settimeout(secs)
        try:
            return self.conn.recv(1) == b""
        except socket.timeout:
            return True
        finally:
            self.conn.settimeout(None)

    def send(self, msg):
        self.conn.sendall((json.dumps(msg) + "\n").encode())

    def join(self):
        self._t.join(5)
        if self.error:
            raise self.error


def _in_subprocess(sock_path: str, body: str, heartbeat_secs: float = 0) -> subprocess.CompletedProcess:
    code = (
        f"import sys; sys.path.insert(0, {str(RUNTIME_DIR)!r})\n"
        "import ipc_embed as ipc\n"
        "ipc.REPLY_TIMEOUT = 0.3\n"
        f"c = ipc.IPCClient({sock_path!r}, 'tok', heartbeat_secs={heartbeat_secs})\n" + body
    )
    return subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=20)


def test_approval_may_take_longer_than_reply_timeout(monkeypatch):
    monkeypatch.setattr(ipc, "REPLY_TIMEOUT", 0.2)

    def script(o):
        assert o.read()["type"] == "yield_request"
        time.sleep(0.6)  # the human is still reading the diff
        o.send({"type": "heartbeat_ack"})  # stray ack must be skipped
        o.send({"type": "yield_response", "approved": True, "feedback": "ok"})

    o = FakeOrchestrator(script)
    c = ipc.IPCClient(o.path, "tok", heartbeat_secs=0)
    resp = c.yield_request("coder", "file_edit", [], "why")
    assert resp == {"type": "yield_response", "approved": True, "feedback": "ok"}
    c.close()
    o.join()


def test_reply_timeout_ends_the_process():
    def script(o):
        assert o.read()["type"] == "secret_request"
        time.sleep(3)  # never answer

    o = FakeOrchestrator(script)
    started = time.monotonic()
    res = _in_subprocess(o.path, "c.get_secret('K')\nprint('unreachable')\n")
    assert res.returncode == ipc.EXIT_IPC_FAILURE, res
    assert "unreachable" not in res.stdout
    assert time.monotonic() - started < 3


def test_dropped_connection_ends_the_process():
    def script(o):
        o.conn.close()

    o = FakeOrchestrator(script)
    res = _in_subprocess(o.path, "c.yield_request('a', 'file_edit', [], 'r')\nprint('unreachable')\n")
    assert res.returncode == ipc.EXIT_IPC_FAILURE, res
    assert "unreachable" not in res.stdout


def test_heartbeats_flow_when_idle_but_never_during_a_request():
    seen = {}

    def script(o):
        for _ in range(2):  # idle: the client is "waiting on the LLM"
            assert o.read()["type"] == "heartbeat"
        msg = o.read()
        while msg["type"] == "heartbeat":
            msg = o.read()
        assert msg["type"] == "yield_request"
        seen["quiet_during_request"] = o.quiet_for(0.5)
        o.send({"type": "yield_response", "approved": False, "feedback": "no"})

    o = FakeOrchestrator(script)
    c = ipc.IPCClient(o.path, "tok", heartbeat_secs=0.05)
    time.sleep(0.3)
    assert c.yield_request("coder", "file_edit", [], "why")["approved"] is False
    c.close()
    o.join()
    assert seen["quiet_during_request"] is True


def test_orderly_close_is_not_a_failure():
    # A heartbeat racing close() must not turn a finished run into exit 70.
    def script(o):
        while o.read() is not None:  # drain beats until the client closes
            pass

    o = FakeOrchestrator(script)
    res = _in_subprocess(o.path, "import time\ntime.sleep(0.05)\nc.close()\ntime.sleep(0.2)\nprint('clean')\n",
                         heartbeat_secs=0.01)
    assert res.returncode == 0, res
    assert "clean" in res.stdout
