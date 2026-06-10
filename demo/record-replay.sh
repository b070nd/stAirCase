#!/usr/bin/env bash
# record-replay.sh — OPTIONAL upgrade path: capture a real LLM conversation once,
# then replay it deterministically and offline forever after.
#
# The offline demo (run-demo.sh) uses a stub agent so it needs no API key. If you
# want the demo to drive a *real* model through the generated LangGraph runtime,
# run this once with a key to produce demo/replay.json, then:
#
#     staircase run <case> --replay-llm demo/replay.json
#
# replay matches recorded responses by a hash of the exact prompt, so the replay
# run makes zero API calls and is fully reproducible.
#
# Requirements: ANTHROPIC_API_KEY in the environment, plus a working Python
# runtime (run `staircase init` WITHOUT --skip-venv so the LangGraph stack is
# installed). This script is a scaffold — it documents the flow and checks
# prerequisites; wire it to your own case/topology as needed.
set -euo pipefail

: "${ANTHROPIC_API_KEY:?Set ANTHROPIC_API_KEY to record a real conversation}"
CASE_ID="${1:?Usage: record-replay.sh <case-id>}"
OUT="${2:-demo/replay.json}"

echo "Recording LLM exchanges for case #$CASE_ID → $OUT"
echo "(this performs REAL API calls and will incur cost)"
staircase run "$CASE_ID" --record-llm "$OUT"

echo
echo "✅ Recorded $OUT"
echo "Replay it offline with:"
echo "    staircase run $CASE_ID --replay-llm $OUT"
