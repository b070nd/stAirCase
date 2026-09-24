#!/usr/bin/env bash
# run-demo.sh — fully offline, no-API-key walkthrough of the stAirCase
# governance layer: HITL approval, approval-content binding, signed audit chain.
#
# It uses real `staircase` commands end to end, including the Go agent runtime.
# The only stand-in is the model: demo/demotool serves an OpenAI-compatible
# gateway that plays the two agents deterministically, so the demo runs
# without an LLM. See demo/record-replay.sh for a real-model run.
#
# Usage:  ./demo/run-demo.sh            (interactive — you approve each proposal)
#         ./demo/run-demo.sh --auto     (auto-approves via curl; used by CI)
#         ./demo/run-demo.sh --tamper   (an approved shell command changes the
#                                        file after its approval → run FAILS)
set -euo pipefail

AUTO=0
TAMPER=0
for arg in "$@"; do
  case "$arg" in
    --auto)   AUTO=1 ;;
    --tamper) TAMPER=1 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPROVAL_TOKEN="demo-approval-token"
TARGET_FILE="GREETING.md"

# ── tooling checks ────────────────────────────────────────────────────────────
command -v go   >/dev/null || { echo "✗ go required"; exit 1; }
command -v git  >/dev/null || { echo "✗ git required"; exit 1; }
command -v curl >/dev/null || { echo "✗ curl required"; exit 1; }

say()  { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
note() { printf '  \033[2m%s\033[0m\n' "$*"; }

# ── disposable workspace + target repo ────────────────────────────────────────
WORK="$(mktemp -d "${TMPDIR:-/tmp}/staircase-demo.XXXXXX")"
export STAIRCASE_DIR="$WORK/.staircase"
TARGET_REPO="$WORK/app"
cleanup() {
  for pid in ${RUN_PID:-} ${GATEWAY_PID:-}; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$WORK"
}
trap cleanup EXIT

# ── build the binaries ────────────────────────────────────────────────────────
say "Building staircase and the demo's stand-in model"
STAIRCASE_BIN="$WORK/staircase"
DEMOTOOL="$WORK/demotool"
( cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$STAIRCASE_BIN" ./src/cmd/staircase \
                  && CGO_ENABLED=0 go build -o "$DEMOTOOL" ./demo/demotool )
staircase() { "$STAIRCASE_BIN" "$@"; }
APPROVAL_PORT="$("$DEMOTOOL" freeport)"

say "Creating a disposable target git repo"
mkdir -p "$TARGET_REPO"
git -C "$TARGET_REPO" init -q -b main
git -C "$TARGET_REPO" config user.email demo@staircase.local
git -C "$TARGET_REPO" config user.name "stAirCase Demo"
git -C "$TARGET_REPO" commit -q --allow-empty -m "initial commit"
note "$TARGET_REPO (branch: main)"

# ── the stand-in model ────────────────────────────────────────────────────────
say "Starting the stand-in model (an OpenAI-compatible gateway, no API key)"
GATEWAY_URL_FILE="$WORK/gateway.url"
if [ "$TAMPER" = "1" ]; then "$DEMOTOOL" serve "$GATEWAY_URL_FILE" --tamper & else "$DEMOTOOL" serve "$GATEWAY_URL_FILE" & fi
GATEWAY_PID=$!
for _ in $(seq 1 50); do [ -s "$GATEWAY_URL_FILE" ] && break; sleep 0.1; done
[ -s "$GATEWAY_URL_FILE" ] || { echo "✗ stand-in model did not start"; exit 1; }
note "$(cat "$GATEWAY_URL_FILE")"

# ── workspace setup (all real commands) ───────────────────────────────────────
say "Initializing the stAirCase workspace"
mkdir -p "$STAIRCASE_DIR"
staircase init --skip-venv
printf '%s' "demo-key" | staircase secret set LLM_GATEWAY_API_KEY >/dev/null
cat "$GATEWAY_URL_FILE" | staircase secret set LLM_GATEWAY_URL >/dev/null
note "gateway key and URL stored as encrypted workspace secrets"

say "Registering vendor, project, topology, case"
staircase vendor add demo
staircase project add demo app --source "$TARGET_REPO"
staircase topology register 1 supervisor >/dev/null
staircase topology agent add 1 supervisor "You coordinate the work." --model demo/scripted >/dev/null
staircase topology agent add 1 coder "You write code." --model demo/scripted >/dev/null
staircase topology edge add 1 supervisor coder >/dev/null
staircase topology edge add 1 coder supervisor >/dev/null
staircase case new 1 >/dev/null
staircase story add 1 "Create a greeting file" >/dev/null
CASE_ID=1
staircase compile "$CASE_ID" >/dev/null
note "case #$CASE_ID compiled to a plan (supervisor ⇄ coder)"

# ── run with the inbound approval API enabled ─────────────────────────────────
RUN_FLAGS=(--approval-port "$APPROVAL_PORT" --approval-token "$APPROVAL_TOKEN" --skip-gates)
if [ "$TAMPER" = "1" ]; then
  say "TAMPER MODE: after its file is approved, the agent asks to run a shell"
  note "command that changes the file. Expected: the run FAILS, nothing is committed."
  RUN_FLAGS+=(--allow-shell-exec)
fi

say "Starting the run with the HTTP approval server"
RUN_LOG="$WORK/run.log"
staircase run "$CASE_ID" "${RUN_FLAGS[@]}" >"$RUN_LOG" 2>&1 &
RUN_PID=$!

API="http://127.0.0.1:$APPROVAL_PORT/v1/yields"
AUTH=(-H "Authorization: Bearer $APPROVAL_TOKEN")

# ── approve every proposal until the run ends ─────────────────────────────────
say "Waiting for the agents' proposals"
DECIDED=0
for _ in $(seq 1 600); do
  kill -0 "$RUN_PID" 2>/dev/null || break
  YIELD_ID="$(curl -s "${AUTH[@]}" "$API" 2>/dev/null | "$DEMOTOOL" first-yield || true)"
  if [ -z "$YIELD_ID" ]; then sleep 0.1; continue; fi

  say "Pending approval — exactly what would be applied"
  curl -s "${AUTH[@]}" "$API/$YIELD_ID" | "$DEMOTOOL" show-yield
  if [ "$AUTO" = "1" ]; then
    note "auto-approving (CI mode)"
    curl -s -X POST "${AUTH[@]}" -d '{"feedback":"approved by CI"}' "$API/$YIELD_ID/approve" >/dev/null
  else
    read -r -p "  Press Enter to approve, or type 'r' to reject: " ANS
    if [ "$ANS" = "r" ]; then
      curl -s -X POST "${AUTH[@]}" -d '{"feedback":"rejected in demo"}' "$API/$YIELD_ID/reject" >/dev/null
    else
      curl -s -X POST "${AUTH[@]}" -d '{"feedback":"approved in demo"}' "$API/$YIELD_ID/approve" >/dev/null
    fi
  fi
  DECIDED=$((DECIDED + 1))
done
[ "$DECIDED" -gt 0 ] || { echo "✗ no approval request appeared:"; cat "$RUN_LOG"; exit 1; }

# ── wait for completion (capture the exit code — CI relies on it) ─────────────
RUN_EXIT=0
wait "$RUN_PID" || RUN_EXIT=$?
RUN_PID=""

# ── show the result ───────────────────────────────────────────────────────────
if [ "$TAMPER" = "1" ]; then
  say "Run finished — verifying the tamper was blocked"
  if staircase inspect runs | grep -qiE '\bFAILED\b'; then
    note "✓ Run FAILED as expected — the post-approval change was refused."
  else
    echo "✗ expected the tampered run to FAIL"; cat "$RUN_LOG"; exit 1
  fi
  if [ "$RUN_EXIT" -eq 0 ]; then
    echo "✗ a failed run must exit non-zero so CI can detect it (got 0)"; exit 1
  fi
  note "✓ Process exited non-zero ($RUN_EXIT) — CI/automation detects the failure."
  # Runs commit only on their own branch (never your checkout), so look there.
  if git -C "$TARGET_REPO" log --oneline staircase/run-1 2>/dev/null | grep -q "staircase: run"; then
    echo "✗ tampered content must NOT be committed"; exit 1
  fi
  note "✓ Nothing committed. The refusal is on the audit chain below."
else
  if [ "$RUN_EXIT" -ne 0 ]; then
    echo "✗ a successful run must exit 0 (got $RUN_EXIT)"; cat "$RUN_LOG"; exit 1
  fi
  say "Run finished (exit 0) — here is what the agent committed"
  git -C "$TARGET_REPO" log "staircase/run-1" --stat --oneline -1
  if ! git -C "$TARGET_REPO" cat-file -e "staircase/run-1:$TARGET_FILE" 2>/dev/null; then
    echo "✗ the approved file is not on the run branch"; cat "$RUN_LOG"; exit 1
  fi
  note "Created file contents (on the run branch; your checkout was not touched):"
  git -C "$TARGET_REPO" show "staircase/run-1:$TARGET_FILE" | sed 's/^/    /'
fi

say "The tamper-evident audit chain for this run"
staircase inspect runs || true
if staircase audit export 1 >/dev/null 2>&1; then
  CP="$STAIRCASE_DIR/audit/run-1.checkpoint.json"
  staircase audit verify "$CP"
  if [ "$TAMPER" = "1" ]; then
    note "Why finalize refused to commit:"
    "$DEMOTOOL" show-mismatch "$CP" || true
  fi
else
  note "(audit export/verify requires a successful run)"
fi

say "Done."
note "Every decision above — the approval, who made it, and the committed"
note "bytes — is recorded in a signed, hash-chained audit log. Nothing the"
note "agents wrote reached the repo without a human in the loop."
