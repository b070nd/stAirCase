"""recording.py — LLM recording and deterministic replay for stAirCase tests.

Three objects:
  RecordingSession — shared exchange accumulator (one per run); owns the output
                     file.  Create one and pass it to every RecordingClient so
                     that exchanges from multiple models all land in one file.
  RecordingClient  — wraps a real LangChain LLM (already ``bind_tools``-ed);
                     appends every exchange to a RecordingSession.
  ReplayClient     — loads a previously recorded JSON file; replays responses
                     by matching a hash of the incoming messages.

Usage (controlled by the bootstrap message):
  boot["record_llm"] = "/path/to/new-recording.json"   → RecordingSession + RecordingClient
  boot["replay_llm"] = "/path/to/existing.json"         → ReplayClient (no real API calls)

Recording format (compatible with docs/testing.md §2.3):
  {
    "id":        "<basename of file without .json>",
    "model":     "<first model seen, or 'multi' for mixed runs>",
    "exchanges": [
      {
        "prompt_hash":    "sha256:<hex>",
        "prompt_preview": "<first 120 chars of serialised messages>",
        "model":          "<model string for this specific exchange>",
        "response":       <dict — LangChain AIMessage serialised via model_dump()>
      }
    ]
  }
"""
from __future__ import annotations

import hashlib
import json
import os
from typing import Any


def _hash_messages(messages: list[Any]) -> str:
    """Return a stable sha256 hex digest of the messages list."""
    # Convert each message to its dict representation for stable serialisation.
    serialised = json.dumps(
        [m.model_dump() if hasattr(m, "model_dump") else str(m) for m in messages],
        sort_keys=True,
        default=str,
    )
    return "sha256:" + hashlib.sha256(serialised.encode()).hexdigest()


class RecordingSession:
    """Shared exchange accumulator for a single run (may span multiple models).

    Create one per run and pass it to every :class:`RecordingClient` so that
    all exchanges end up in a single JSON file.  Call :meth:`close` (or use
    as a context manager) when the run finishes.
    """

    def __init__(self, output_path: str) -> None:
        self._output_path = output_path
        self._exchanges: list[dict[str, Any]] = []
        self._first_model: str = ""

    def record(self, model: str, messages: list[Any], response: Any) -> None:
        """Append one LLM exchange to the session."""
        if not self._first_model:
            self._first_model = model
        prompt_hash = _hash_messages(messages)
        serialised = json.dumps(
            [m.model_dump() if hasattr(m, "model_dump") else str(m) for m in messages],
            sort_keys=True,
            default=str,
        )
        self._exchanges.append(
            {
                "prompt_hash": prompt_hash,
                "prompt_preview": serialised[:120],
                "model": model,
                "response": (
                    response.model_dump() if hasattr(response, "model_dump") else str(response)
                ),
            }
        )

    def close(self) -> None:
        """Write the recording JSON file."""
        basename = os.path.basename(self._output_path)
        record_id = basename.removesuffix(".json") if basename.endswith(".json") else basename
        data = {
            "id": record_id,
            "model": self._first_model,
            "exchanges": self._exchanges,
        }
        os.makedirs(os.path.dirname(os.path.abspath(self._output_path)), exist_ok=True)
        with open(self._output_path, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, default=str)

    def __enter__(self) -> "RecordingSession":
        return self

    def __exit__(self, *_: Any) -> None:
        self.close()


class RecordingClient:
    """Wraps a real LangChain LLM, recording every exchange to a shared session.

    The wrapped *real_llm* should already have ``bind_tools`` applied before
    being handed to this class.  All attribute access (e.g. ``bind_tools``,
    ``model_name``) is forwarded to the wrapped LLM.
    """

    def __init__(self, real_llm: Any, model: str, session: RecordingSession) -> None:
        self._llm = real_llm
        self._model = model
        self._session = session

    def invoke(self, messages: list[Any], **kwargs: Any) -> Any:
        response = self._llm.invoke(messages, **kwargs)
        self._session.record(self._model, messages, response)
        return response

    # Forward everything else to the wrapped LLM so bind_tools etc. work.
    def __getattr__(self, name: str) -> Any:
        return getattr(self._llm, name)


class ReplayClient:
    """Replays a previously recorded LLM conversation.

    Raises ``ValueError`` loudly when the incoming prompt has no matching
    recording — this is intentional: it means the test's behaviour has
    drifted from what was recorded, which is a signal worth acting on.
    """

    def __init__(self, recording_path: str) -> None:
        with open(recording_path, encoding="utf-8") as f:
            data = json.load(f)
        # Build a hash → response map for O(1) lookup.
        self._index: dict[str, Any] = {
            ex["prompt_hash"]: ex["response"] for ex in data.get("exchanges", [])
        }
        self._recording_path = recording_path

    def invoke(self, messages: list[Any], **_kwargs: Any) -> Any:
        prompt_hash = _hash_messages(messages)
        if prompt_hash not in self._index:
            # Format a helpful error: show what hash was expected vs. available.
            available = list(self._index.keys())
            raise ValueError(
                f"ReplayClient: no recorded response for prompt hash {prompt_hash!r}.\n"
                f"Recording file: {self._recording_path}\n"
                f"Available hashes ({len(available)}): "
                + (", ".join(available[:5]) + (" …" if len(available) > 5 else ""))
            )

        stored = self._index[prompt_hash]
        # Reconstruct an AIMessage-compatible object from the stored dict.
        return _ReplayMessage(stored)

    # LangGraph/LangChain calls bind_tools and similar on LLMs.  For replay we
    # simply return self — the replay client ignores tool schemas.
    def bind_tools(self, *_args: Any, **_kwargs: Any) -> "ReplayClient":
        return self

    def __getattr__(self, name: str) -> Any:
        # Silently absorb any other attribute access (e.g. model_name lookups).
        return None


class _ReplayMessage:
    """Lightweight adapter that exposes the stored response dict as a message.

    LangGraph reads ``.content`` and ``.tool_calls`` from messages; this
    adapter provides both from the recorded dict without requiring a full
    LangChain import.
    """

    def __init__(self, data: dict[str, Any]) -> None:
        self._data = data

    @property
    def content(self) -> Any:
        return self._data.get("content", "")

    @property
    def tool_calls(self) -> list[Any]:
        return self._data.get("tool_calls", [])

    def model_dump(self) -> dict[str, Any]:
        return self._data

    def __repr__(self) -> str:  # pragma: no cover
        return f"_ReplayMessage(content={self.content!r})"
