#!/usr/bin/env bash
# record-replay.sh - record a real-model run once, then replay it offline.
#
# Recording talks to the real model and costs money. Store the provider key as
# a workspace secret first, e.g. for the LLM gateway (provider/model names):
#     printf %s "$AI_GATEWAY_API_KEY" | staircase secret set LLM_GATEWAY_API_KEY
# or ANTHROPIC_API_KEY / OPENAI_API_KEY / GOOGLE_API_KEY / XAI_API_KEY.
#
# A replay answers every model call from the recording, offline and for free.
# It matches each request exactly, so a change to the plan, the prompts or the
# files the agents read makes it fail loudly instead of answering wrongly.
# Approvals are not recorded: you (or your approval server) decide again.
set -euo pipefail

CASE_ID="${1:?Usage: record-replay.sh <case-id> [recording]}"
OUT="${2:-llm-case${CASE_ID}.jsonl}"

echo "Recording model exchanges for case #$CASE_ID → $OUT (real API calls)"
staircase run "$CASE_ID" --record-llm "$OUT"

echo
echo "✅ Recorded $OUT - replay it offline with:"
echo "    staircase run $CASE_ID --replay-llm $OUT"
