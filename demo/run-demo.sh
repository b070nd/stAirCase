#!/usr/bin/env bash
# run-demo.sh — fully offline, no-API-key walkthrough of the stAirCase
# governance layer: HITL approval, approval-content binding, signed audit chain.
#
# It uses real `staircase` commands end to end. The only stand-in is the agent
# itself (demo/agent_stub.py), which speaks the real IPC protocol so the demo
# runs deterministically without an LLM. See demo/record-replay.sh to upgrade
# this to a real-model run.
#
# Usage:  ./demo/run-demo.sh            (interactive — you approve in a browser)
#         ./demo/run-demo.sh --auto     (auto-approves via curl; used by CI)
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
DEMO_DIR="$REPO_ROOT/demo"
APPROVAL_TOKEN="demo-approval-token"
TARGET_FILE="GREETING.md"

# ── tooling checks ────────────────────────────────────────────────────────────
command -v python3 >/dev/null || { echo "✗ python3 required"; exit 1; }
command -v git     >/dev/null || { echo "✗ git required"; exit 1; }
command -v curl    >/dev/null || { echo "✗ curl required"; exit 1; }

# Pick a free TCP port so concurrent/leftover runs never collide.
APPROVAL_PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"

say()  { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
note() { printf '  \033[2m%s\033[0m\n' "$*"; }

# ── build the binary ──────────────────────────────────────────────────────────
say "Building staircase"
STAIRCASE_BIN="$REPO_ROOT/staircase-demo-bin"
( cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$STAIRCASE_BIN" ./src/cmd/staircase )
staircase() { "$STAIRCASE_BIN" "$@"; }

# ── disposable workspace + target repo ────────────────────────────────────────
WORK="$(mktemp -d "${TMPDIR:-/tmp}/staircase-demo.XXXXXX")"
export STAIRCASE_DIR="$WORK/.staircase"
TARGET_REPO="$WORK/app"
cleanup() {
  if [ -n "${RUN_PID:-}" ]; then
    kill "$RUN_PID" 2>/dev/null || true
    wait "$RUN_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK" "$STAIRCASE_BIN"
}
trap cleanup EXIT

say "Creating a disposable target git repo"
mkdir -p "$TARGET_REPO"
git -C "$TARGET_REPO" init -q -b main
git -C "$TARGET_REPO" config user.email demo@staircase.local
git -C "$TARGET_REPO" config user.name "stAirCase Demo"
git -C "$TARGET_REPO" commit -q --allow-empty -m "initial commit"
note "$TARGET_REPO (branch: main)"

# ── workspace setup (all real commands) ───────────────────────────────────────
say "Initializing the stAirCase workspace"
mkdir -p "$STAIRCASE_DIR"
# --skip-venv: the offline stub agent needs only the Python stdlib, so we skip
# the heavy LangGraph bootstrap and provide a minimal venv below.
staircase init --skip-venv

say "Registering vendor, project, topology, case"
staircase vendor add demo
staircase project add demo app --source "$TARGET_REPO"
staircase topology register 1 supervisor >/dev/null
staircase topology agent add 1 coder "You write code." >/dev/null
staircase case new 1 >/dev/null
staircase story add 1 "Create a greeting file" >/dev/null
CASE_ID=1
note "case #$CASE_ID ready"

# ── minimal venv + the offline agent in place of a compiled graph ─────────────
say "Preparing the runtime (offline stub agent — no API key needed)"
python3 -m venv --without-pip "$STAIRCASE_DIR/venv" >/dev/null 2>&1 || python3 -m venv "$STAIRCASE_DIR/venv"
mkdir -p "$STAIRCASE_DIR/tmp"
cp "$DEMO_DIR/agent_stub.py" "$STAIRCASE_DIR/tmp/graph_exec_case${CASE_ID}.py"
note "stub agent installed as the case's runtime script"

export DEMO_PROJECT_PATH="$TARGET_REPO"
export DEMO_TARGET_FILE="$TARGET_FILE"
[ "$TAMPER" = "1" ] && export DEMO_TAMPER=1

# ── run with the inbound approval API enabled ─────────────────────────────────
if [ "$TAMPER" = "1" ]; then
  say "TAMPER MODE: the agent will write DIFFERENT bytes than it gets approved"
  note "Expected outcome: the run FAILS and nothing is committed."
fi

say "Starting the run with the HTTP approval server"
RUN_LOG="$WORK/run.log"
staircase run "$CASE_ID" \
  --approval-port "$APPROVAL_PORT" \
  --approval-token "$APPROVAL_TOKEN" \
  --force --skip-gates >"$RUN_LOG" 2>&1 &
RUN_PID=$!

API="http://127.0.0.1:$APPROVAL_PORT/v1/yields"
AUTH=(-H "Authorization: Bearer $APPROVAL_TOKEN")

# Wait for the pending yield to appear.
say "Waiting for the agent to request approval"
YIELD_ID=""
for _ in $(seq 1 100); do
  RESP="$(curl -s "${AUTH[@]}" "$API" 2>/dev/null || true)"
  YIELD_ID="$(printf '%s' "$RESP" | python3 "$DEMO_DIR/_first_yield_id.py" 2>/dev/null || true)"
  [ -n "$YIELD_ID" ] && break
  kill -0 "$RUN_PID" 2>/dev/null || { echo "✗ run exited early:"; cat "$RUN_LOG"; exit 1; }
  sleep 0.2
done
[ -n "$YIELD_ID" ] || { echo "✗ no approval request appeared:"; cat "$RUN_LOG"; exit 1; }

# Show the operator exactly what is being approved.
say "Pending approval"
curl -s "${AUTH[@]}" "$API/$YIELD_ID" | python3 "$DEMO_DIR/_show_yield.py"

# ── approve ───────────────────────────────────────────────────────────────────
if [ "$AUTO" = "1" ]; then
  say "Auto-approving (CI mode)"
  curl -s -X POST "${AUTH[@]}" -d '{"feedback":"approved by CI"}' "$API/$YIELD_ID/approve" >/dev/null
else
  say "Approve the change"
  echo "  Open in your browser (send the header) or just press Enter to approve here:"
  echo "    curl -X POST ${API}/${YIELD_ID}/approve -H 'Authorization: Bearer ${APPROVAL_TOKEN}'"
  read -r -p "  Press Enter to approve, or type 'r' to reject: " ANS
  if [ "$ANS" = "r" ]; then
    curl -s -X POST "${AUTH[@]}" -d '{"feedback":"rejected in demo"}' "$API/$YIELD_ID/reject" >/dev/null
    say "Rejected — waiting for the run to finish"
  else
    curl -s -X POST "${AUTH[@]}" -d '{"feedback":"approved in demo"}' "$API/$YIELD_ID/approve" >/dev/null
  fi
fi

# ── wait for completion ───────────────────────────────────────────────────────
wait "$RUN_PID" || true
RUN_PID=""

# ── show the result ───────────────────────────────────────────────────────────
if [ "$TAMPER" = "1" ]; then
  say "Run finished — verifying the tamper was blocked"
  if staircase inspect runs | grep -qiE '\bFAILED\b'; then
    note "✓ Run FAILED as expected — the post-approval edit was rejected."
  else
    echo "✗ expected the tampered run to FAIL"; cat "$RUN_LOG"; exit 1
  fi
  if git -C "$TARGET_REPO" log --oneline 2>/dev/null | grep -q "staircase: run"; then
    echo "✗ tampered content must NOT be committed"; exit 1
  fi
  note "✓ Nothing committed. Look for the approval_content_mismatch event below."
else
  say "Run finished — here is what the agent actually committed"
  if git -C "$TARGET_REPO" rev-parse --verify -q "staircase/run-1" >/dev/null 2>&1; then
    git -C "$TARGET_REPO" log "staircase/run-1" --stat --oneline -1 || true
  elif git -C "$TARGET_REPO" log --oneline -1 2>/dev/null | grep -q staircase; then
    git -C "$TARGET_REPO" log --stat --oneline -1
  fi
  if [ -f "$TARGET_REPO/$TARGET_FILE" ]; then
    note "Created file contents:"
    sed 's/^/    /' "$TARGET_REPO/$TARGET_FILE"
  fi
fi

say "The tamper-evident audit chain for this run"
staircase inspect runs || true
if staircase audit export 1 >/dev/null 2>&1; then
  CP="$STAIRCASE_DIR/audit/run-1.checkpoint.json"
  staircase audit verify "$CP"
  if [ "$TAMPER" = "1" ]; then
    note "The audit chain recorded the rejection:"
    python3 "$DEMO_DIR/_show_mismatch.py" "$CP" 2>/dev/null || true
  fi
else
  note "(audit export/verify requires a successful run)"
fi

say "Done."
note "Every decision above — the approval, who made it, and the committed"
note "bytes — is recorded in a signed, hash-chained audit log. Nothing the"
note "agent wrote reached the repo without a human in the loop."
