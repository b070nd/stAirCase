"""test_recording.py — unit tests for staircase_runner.recording.

Covers:
  - RecordingSession writes a well-formed JSON file.
  - RecordingClient appends exchanges to the session and forwards invoke().
  - RecordingClient delegates unknown attributes to the wrapped LLM.
  - ReplayClient replays responses by prompt hash.
  - ReplayClient raises ValueError with a helpful message on a cache miss.
  - ReplayClient.bind_tools() returns self (no-op).
  - Multi-model recording: two RecordingClients share one session.
  - Context-manager usage of RecordingSession (__enter__/__exit__).
"""
from __future__ import annotations

import json
import os
import tempfile

import pytest

from staircase_runner.recording import (
    RecordingClient,
    RecordingSession,
    ReplayClient,
    _ReplayMessage,
    _hash_messages,
)


# ── Fakes ─────────────────────────────────────────────────────────────────────


class _FakeMsg:
    """Minimal LangChain-message stand-in."""

    def __init__(self, content: str = "hello") -> None:
        self.content = content

    def model_dump(self) -> dict:
        return {"type": "human", "content": self.content}


class _FakeResponse:
    content = "assistant reply"
    tool_calls: list = []

    def model_dump(self) -> dict:
        return {"type": "ai", "content": self.content, "tool_calls": []}


class _FakeLLM:
    call_count = 0
    model_name = "fake-llm"

    def invoke(self, messages, **kwargs):
        self.call_count += 1
        return _FakeResponse()

    def bind_tools(self, tools):
        return self


@pytest.fixture()
def tmp_path_json(tmp_path):
    return str(tmp_path / "rec.json")


# ── RecordingSession ──────────────────────────────────────────────────────────


def test_session_writes_json(tmp_path_json):
    session = RecordingSession(tmp_path_json)
    msgs = [_FakeMsg("first prompt")]
    session.record("model-a", msgs, _FakeResponse())
    session.close()

    data = json.loads(open(tmp_path_json).read())
    assert data["id"] == "rec"
    assert data["model"] == "model-a"
    assert len(data["exchanges"]) == 1
    ex = data["exchanges"][0]
    assert ex["prompt_hash"].startswith("sha256:")
    assert ex["model"] == "model-a"
    assert ex["response"]["content"] == "assistant reply"


def test_session_context_manager(tmp_path_json):
    msgs = [_FakeMsg()]
    with RecordingSession(tmp_path_json) as session:
        session.record("m", msgs, _FakeResponse())
    data = json.loads(open(tmp_path_json).read())
    assert len(data["exchanges"]) == 1


def test_session_multiple_exchanges(tmp_path_json):
    session = RecordingSession(tmp_path_json)
    for i in range(5):
        session.record("m", [_FakeMsg(f"msg-{i}")], _FakeResponse())
    session.close()
    data = json.loads(open(tmp_path_json).read())
    assert len(data["exchanges"]) == 5


def test_session_creates_parent_dirs(tmp_path):
    nested = str(tmp_path / "a" / "b" / "c" / "rec.json")
    session = RecordingSession(nested)
    session.record("m", [_FakeMsg()], _FakeResponse())
    session.close()
    assert os.path.isfile(nested)


# ── RecordingClient ───────────────────────────────────────────────────────────


def test_recording_client_invokes_real_llm(tmp_path_json):
    session = RecordingSession(tmp_path_json)
    llm = _FakeLLM()
    client = RecordingClient(llm, "fake", session)
    resp = client.invoke([_FakeMsg()])
    assert resp.content == "assistant reply"
    assert llm.call_count == 1


def test_recording_client_records_exchange(tmp_path_json):
    session = RecordingSession(tmp_path_json)
    client = RecordingClient(_FakeLLM(), "fake", session)
    client.invoke([_FakeMsg("unique-content")])
    assert len(session._exchanges) == 1
    ex = session._exchanges[0]
    assert ex["model"] == "fake"
    assert "unique-content" in ex["prompt_preview"]


def test_recording_client_delegates_attributes(tmp_path_json):
    session = RecordingSession(tmp_path_json)
    llm = _FakeLLM()
    client = RecordingClient(llm, "fake", session)
    # model_name is on the wrapped LLM, not RecordingClient directly
    assert client.model_name == "fake-llm"


def test_multi_model_shared_session(tmp_path_json):
    """Two RecordingClients using different models write to one file."""
    session = RecordingSession(tmp_path_json)
    client_a = RecordingClient(_FakeLLM(), "model-a", session)
    client_b = RecordingClient(_FakeLLM(), "model-b", session)

    client_a.invoke([_FakeMsg("from a")])
    client_b.invoke([_FakeMsg("from b")])
    session.close()

    data = json.loads(open(tmp_path_json).read())
    assert len(data["exchanges"]) == 2
    models = {ex["model"] for ex in data["exchanges"]}
    assert models == {"model-a", "model-b"}
    # top-level model is the first model seen
    assert data["model"] == "model-a"


# ── ReplayClient ──────────────────────────────────────────────────────────────


def _make_recording(path: str, exchanges: list[tuple[list, dict]]) -> None:
    """Write a minimal recording JSON file for replay tests."""
    records = []
    for msgs, resp_dict in exchanges:
        records.append(
            {
                "prompt_hash": _hash_messages(msgs),
                "prompt_preview": "preview",
                "model": "test-model",
                "response": resp_dict,
            }
        )
    with open(path, "w") as f:
        json.dump({"id": "test", "model": "test-model", "exchanges": records}, f)


def test_replay_client_returns_correct_response(tmp_path_json):
    msgs = [_FakeMsg("question")]
    resp_dict = {"type": "ai", "content": "recorded answer", "tool_calls": []}
    _make_recording(tmp_path_json, [(msgs, resp_dict)])

    replay = ReplayClient(tmp_path_json)
    msg = replay.invoke(msgs)
    assert isinstance(msg, _ReplayMessage)
    assert msg.content == "recorded answer"
    assert msg.tool_calls == []


def test_replay_client_miss_raises_valueerror(tmp_path_json):
    msgs_recorded = [_FakeMsg("recorded")]
    _make_recording(tmp_path_json, [(msgs_recorded, {"content": "x", "tool_calls": []})])

    replay = ReplayClient(tmp_path_json)
    with pytest.raises(ValueError, match="no recorded response"):
        replay.invoke([_FakeMsg("different prompt")])


def test_replay_client_miss_message_contains_available_hashes(tmp_path_json):
    msgs = [_FakeMsg("q")]
    _make_recording(tmp_path_json, [(msgs, {"content": "a", "tool_calls": []})])

    replay = ReplayClient(tmp_path_json)
    with pytest.raises(ValueError) as exc_info:
        replay.invoke([_FakeMsg("other")])
    # The error message should mention available hashes
    assert "sha256:" in str(exc_info.value)


def test_replay_client_bind_tools_returns_self(tmp_path_json):
    _make_recording(tmp_path_json, [])
    replay = ReplayClient(tmp_path_json)
    assert replay.bind_tools(["tool1", "tool2"]) is replay


def test_replay_client_model_dump_roundtrip(tmp_path_json):
    msgs = [_FakeMsg()]
    resp_dict = {"type": "ai", "content": "hi", "tool_calls": [{"name": "read_file"}]}
    _make_recording(tmp_path_json, [(msgs, resp_dict)])

    replay = ReplayClient(tmp_path_json)
    msg = replay.invoke(msgs)
    assert msg.model_dump() == resp_dict
    assert msg.tool_calls == [{"name": "read_file"}]


# ── _hash_messages stability ──────────────────────────────────────────────────


def test_hash_messages_is_stable():
    msgs = [_FakeMsg("hello")]
    h1 = _hash_messages(msgs)
    h2 = _hash_messages(msgs)
    assert h1 == h2
    assert h1.startswith("sha256:")


def test_hash_messages_differs_on_content():
    h1 = _hash_messages([_FakeMsg("hello")])
    h2 = _hash_messages([_FakeMsg("world")])
    assert h1 != h2
