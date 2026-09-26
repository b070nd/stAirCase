#!/usr/bin/env python3
"""example_check.py — stAirCase plugin gate example (Python).

Input  (stdin):  {"case_id": 42, "ws_dir": "/path/to/ws"}
Output (stdout): {"status": "PASS", "message": "..."}

Allowed status values: PASS, WARN, FAIL, SKIP

Note: the subprocess runs with an empty environment (no PATH, no secrets).
Use absolute paths for any external tools.
"""
import json
import sys

def main() -> None:
    data = json.loads(sys.stdin.readline())
    case_id: int = data.get("case_id", 0)
    ws_dir: str = data.get("ws_dir", "")

    # Example check: always pass.
    # Replace this logic with your custom validation.
    result = {
        "status": "PASS",
        "message": f"example Python gate always passes (case_id={case_id}, ws_dir={ws_dir!r})",
    }
    sys.stdout.write(json.dumps(result) + "\n")
    sys.stdout.flush()

if __name__ == "__main__":
    main()
