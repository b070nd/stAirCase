"""
Python-side conformance test for the IPC protocol schema (audit check 3.3.3).

Validates every corpus file in tests/conformance/corpus/ against
proto/ipc.v1.schema.json using the jsonschema library.  Files marked with
``"expect_error": true`` must fail validation; all other files must pass.

Run: cd <repo_root>/runner && python3 -m pytest tests/test_conformance.py -v
"""
import json
from pathlib import Path
from typing import Any

import jsonschema
import pytest

# Paths relative to this file: runner/tests/ → repo root is two levels up.
_REPO_ROOT = Path(__file__).parents[2]
_SCHEMA_PATH = _REPO_ROOT / "proto" / "ipc.v1.schema.json"
_CORPUS_DIR = _REPO_ROOT / "tests" / "conformance" / "corpus"


def _corpus_files() -> list[Path]:
    return sorted(_CORPUS_DIR.glob("*.json"))


# ---------------------------------------------------------------------------
# Parametrised test — one sub-test per corpus file
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("corpus_file", _corpus_files(), ids=lambda p: p.name)
def test_corpus_file(corpus_file: Path) -> None:
    """Each corpus file is either a valid or deliberately malformed message.

    Valid files must pass schema validation for their declared ``type``.
    Malformed files (``"expect_error": true``) must fail validation.
    """
    full_schema: dict[str, Any] = json.loads(_SCHEMA_PATH.read_text())
    doc: Any = json.loads(corpus_file.read_text())

    if not isinstance(doc, dict):
        pytest.fail(f"{corpus_file.name}: top-level document must be a JSON object")

    expect_error: bool = doc.get("expect_error") is True
    kind: str = doc.get("type", "")  # type: ignore[assignment]

    if not isinstance(kind, str) or kind == "":
        # type field missing or not a string — always a schema violation.
        if expect_error:
            return  # expected: kind-level rejection
        pytest.fail(f"{corpus_file.name}: valid corpus file has no string 'type' field")

    # Look up the per-kind $def from the top-level schema.
    kind_schema = full_schema.get("$defs", {}).get(kind)
    if kind_schema is None:
        if expect_error:
            return  # unknown kind counts as an expected error
        pytest.fail(f"{corpus_file.name}: kind {kind!r} has no $defs entry in schema")

    # Validate doc against the kind-specific sub-schema.
    # We embed the full $defs so that any internal $refs resolve correctly.
    wrapped: dict[str, Any] = {
        **kind_schema,
        "$defs": full_schema.get("$defs", {}),
    }

    validator_cls = jsonschema.Draft202012Validator
    errors = list(validator_cls(wrapped).iter_errors(doc))

    if expect_error:
        assert errors, (
            f"{corpus_file.name}: expected schema validation errors but got none"
        )
    else:
        assert not errors, (
            f"{corpus_file.name}: unexpected validation errors: "
            + "; ".join(e.message for e in errors)
        )
