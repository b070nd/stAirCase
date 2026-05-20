#!/bin/sh
# example_check.sh — stAirCase plugin gate example (shell)
#
# Input  (stdin):  {"case_id": 42, "ws_dir": "/path/to/ws"}
# Output (stdout): {"status": "PASS", "message": "..."}
#
# Allowed status values: PASS, WARN, FAIL, SKIP

# Read stdin (JSON input from the gate runner)
input=$(cat)

# Example check: always pass.
# Replace this logic with your custom validation.
printf '{"status":"PASS","message":"example shell gate always passes"}\n'
