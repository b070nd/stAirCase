#!/usr/bin/env bash
# run-demo.sh - fully offline, no-API-key walkthrough of stAirCase, end to end:
# a blueprint imported from its own repository and bound to a project, a run on
# the real Go agent runtime with every change approved by a human, exactly the
# approved bytes committed on the run's branch, the developer's checkout left
# untouched, stories accepted, and a verified, signed audit chain.
#
# The only stand-in is the model: demo/demotool serves an OpenAI-compatible
# gateway that plays the two agents deterministically. See
# demo/record-replay.sh for a real-model run.
#
# Usage:  ./demo/run-demo.sh            (interactive - you approve each proposal)
#         ./demo/run-demo.sh --auto     (auto-approves via curl; used by CI)
#         ./demo/run-demo.sh --tamper   (an approved shell command changes a
#                                        file after its approval → reviewed, put back)
#         ./demo/run-demo.sh --drift    (the agent wanders outside its stories'
#                                        scope → run HALTED)
set -euo pipefail

AUTO=0
MODE=""
for arg in "$@"; do
  case "$arg" in
    --auto)   AUTO=1 ;;
    --tamper) MODE=tamper ;;
    --drift)  MODE=drift ;;
    *) echo "unknown option $arg"; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APPROVAL_TOKEN="demo-approval-token"

# shellcheck source=demo/lib.sh
. "$REPO_ROOT/demo/lib.sh"
require_tools go git curl

# ── disposable workspace ──────────────────────────────────────────────────────
WORK="$(mktemp -d "${TMPDIR:-/tmp}/staircase-demo.XXXXXX")"
export STAIRCASE_DIR="$WORK/.staircase"
TARGET_REPO="$WORK/app"
BLUEPRINT_REPO="$WORK/blueprints/hello"
cleanup() {
  for pid in ${RUN_PID:-} ${GATEWAY_PID:-}; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  rm -rf "$WORK"
}
trap cleanup EXIT

say "Building staircase and the demo's stand-in model"
build_binaries "$WORK"
APPROVAL_PORT="$("$DEMOTOOL" freeport)"

# ── the product repository (with uncommitted work the run must not touch) ─────
say "Creating the product repository"
mkdir -p "$TARGET_REPO"
git -C "$TARGET_REPO" init -q -b main
git -C "$TARGET_REPO" config user.email demo@staircase.local
git -C "$TARGET_REPO" config user.name "stAirCase Demo"
printf '# app\n\nStatus: draft\n' > "$TARGET_REPO/README.md"
printf 'Old notes nobody reads.\n' > "$TARGET_REPO/OLD_NOTES.md"
git -C "$TARGET_REPO" add -A
git -C "$TARGET_REPO" commit -q -m "initial commit"
printf 'work in progress\n' > "$TARGET_REPO/WIP.md"   # the developer's uncommitted work
note "$TARGET_REPO (main, plus an uncommitted WIP.md)"
# fingerprint: HEAD, branch, status, index and every file's content
checkout_fingerprint() {
  { git -C "$TARGET_REPO" rev-parse HEAD; git -C "$TARGET_REPO" symbolic-ref HEAD
    git -C "$TARGET_REPO" status --porcelain=v2; git -C "$TARGET_REPO" ls-files -s
    ( cd "$TARGET_REPO" && find . -path ./.git -prune -o -type f -print | LC_ALL=C sort | xargs cat ); } | git hash-object --stdin
}
BEFORE="$(checkout_fingerprint)"

# ── the blueprint, in its own repository ──────────────────────────────────────
say "The blueprint lives in its own repository, not in the product's"
mkdir -p "$BLUEPRINT_REPO"
cp -R "$REPO_ROOT/examples/blueprints/hello/." "$BLUEPRINT_REPO/"
git -C "$BLUEPRINT_REPO" init -q -b main
git -C "$BLUEPRINT_REPO" add -A
git -C "$BLUEPRINT_REPO" -c user.email=demo@staircase.local -c user.name=demo commit -q -m "hello blueprint"
note "$BLUEPRINT_REPO"

say "Starting the stand-in model (an OpenAI-compatible gateway, no API key)"
GATEWAY_URL_FILE="$WORK/gateway.url"
"$DEMOTOOL" serve "$GATEWAY_URL_FILE" ${MODE:+--$MODE} &
GATEWAY_PID=$!
for _ in $(seq 1 50); do [ -s "$GATEWAY_URL_FILE" ] && break; sleep 0.1; done
[ -s "$GATEWAY_URL_FILE" ] || die "stand-in model did not start"

say "Initializing the workspace, importing and binding the blueprint"
mkdir -p "$STAIRCASE_DIR"
staircase init >/dev/null
printf '%s' "demo-key" | staircase secret set LLM_GATEWAY_API_KEY >/dev/null
staircase secret set LLM_GATEWAY_URL < "$GATEWAY_URL_FILE" >/dev/null
staircase vendor add demo >/dev/null
staircase project add demo app --source "$TARGET_REPO" >/dev/null
staircase blueprint import "$BLUEPRINT_REPO"
HASH="$(staircase blueprint list | awk 'NR==3 {print $1}')"
staircase project bind 1 "$HASH"
CASE_ID=1
staircase compile "$CASE_ID" >/dev/null
staircase gate "$CASE_ID" 2>&1 | grep -E "plan_pinned|plan_compiled" || true

# ── run with the inbound approval API ─────────────────────────────────────────
RUN_FLAGS=(--approval-port "$APPROVAL_PORT" --approval-token "$APPROVAL_TOKEN")
case "$MODE" in
  tamper) say "TAMPER MODE: after its files are approved, the agent asks to run a shell"
          note "command that changes one of them. You approve the command, then reject what it"
          note "changed. Expected: the file is put back; only the bytes you approved are committed."
          RUN_FLAGS+=(--allow-shell-exec) ;;
  drift)  say "DRIFT MODE: after its stories' work, the agent keeps proposing files outside"
          note "their scope. Expected: each goes to you marked DRIFT; the 3rd halts the run." ;;
esac

say "Starting the run"
RUN_LOG="$WORK/run.log"
staircase run "$CASE_ID" "${RUN_FLAGS[@]}" >"$RUN_LOG" 2>&1 &
RUN_PID=$!
API="http://127.0.0.1:$APPROVAL_PORT/v1/yields"
AUTH=(-H "Authorization: Bearer $APPROVAL_TOKEN")

DECIDED=0
for _ in $(seq 1 1200); do
  kill -0 "$RUN_PID" 2>/dev/null || break
  YIELD_ID="$(curl -s "${AUTH[@]}" "$API" 2>/dev/null | "$DEMOTOOL" first-yield || true)"
  if [ -z "$YIELD_ID" ]; then sleep 0.1; continue; fi
  say "Pending approval - exactly what would be applied"
  YIELD="$(curl -s "${AUTH[@]}" "$API/$YIELD_ID")"
  printf '%s' "$YIELD" | "$DEMOTOOL" show-yield
  DECISION=approve
  if [ "$MODE" = "tamper" ] && [[ "$YIELD" == *'"review_after":true'* ]]; then
    note "rejecting: the approved command changed a file you already approved"
    DECISION=reject
  elif [ "$AUTO" = "1" ]; then
    note "auto-approving (CI mode)"
  else
    read -r -p "  Press Enter to approve, or type 'r' to reject: " ANS
    [ "$ANS" = "r" ] && DECISION=reject
  fi
  curl -s -X POST "${AUTH[@]}" -d "{\"feedback\":\"${DECISION}d in demo\"}" "$API/$YIELD_ID/$DECISION" >/dev/null
  DECIDED=$((DECIDED + 1))
done
[ "$DECIDED" -gt 0 ] || die "no approval request appeared"

RUN_EXIT=0
wait "$RUN_PID" || RUN_EXIT=$?
RUN_PID=""
grep -a "Drift:" "$RUN_LOG" || true

# ── check the result ──────────────────────────────────────────────────────────
STATUS="$(staircase inspect runs | awk '$1 == "1" {print $3}')"
case "$MODE" in
  tamper)
    say "Checking the tamper was reverted"
    [ "$STATUS" = "SUCCESS" ] && [ "$RUN_EXIT" -eq 0 ] || die "the run must still succeed (got $STATUS, exit $RUN_EXIT)"
    GOT="$(git -C "$TARGET_REPO" show staircase/run-1:GREETING.md)" || die "GREETING.md is not on the run branch"
    [[ "$GOT" != *injected* ]] || die "the rejected change must NOT be committed"
    ok "the command's change was put back; the branch holds only the approved bytes" ;;
  drift)
    say "Checking the drift was halted"
    [ "$STATUS" = "KILLED" ] || die "expected the drifting run to be halted (got $STATUS)"
    [ "$RUN_EXIT" -ne 0 ] || die "a halted run must exit non-zero"
    ok "run halted (KILLED) and exited $RUN_EXIT"
    if staircase run "$CASE_ID" >"$WORK/rerun.log" 2>&1; then die "a halted case must not run again unacknowledged"; fi
    grep -q -- "--ack-drift" "$WORK/rerun.log" || die "the refusal must ask for --ack-drift"
    ok "the case does not run again until 'staircase run $CASE_ID --ack-drift'" ;;
  *)
    say "Checking the delivery"
    [ "$RUN_EXIT" -eq 0 ] || die "a successful run must exit 0 (got $RUN_EXIT)"
    CHANGES="$(git -C "$TARGET_REPO" diff --name-status main staircase/run-1 | LC_ALL=C sort | tr '\t' ' ' | paste -sd, -)"
    [ "$CHANGES" = "A GREETING.md,D OLD_NOTES.md,M README.md" ] || die "run branch changes: $CHANGES"
    ok "the run branch holds exactly the approved changes: $CHANGES"
    git -C "$TARGET_REPO" show staircase/run-1:GREETING.md | sed 's/^/    │ /'
    say "Accepting the stories (a human verified the work)"
    for STORY in 1 2; do staircase story accept "$STORY" >/dev/null; done
    CASE_STATUS="$(staircase case status "$CASE_ID")"
    [[ "$CASE_STATUS" == *"Status:   COMPLETED"* ]] || { echo "$CASE_STATUS"; die "case should be COMPLETED"; }
    ok "case #$CASE_ID COMPLETED" ;;
esac

say "Checking the developer's checkout"
[ "$(checkout_fingerprint)" = "$BEFORE" ] || die "the developer's checkout changed"
ok "HEAD, branch, index, status and every file are exactly as before"
[ "$(git -C "$TARGET_REPO" worktree list | wc -l | tr -d ' ')" = "1" ] || [ "$MODE" != "" ] || die "a worktree was left behind"
BRANCHES="$(git -C "$TARGET_REPO" branch --format='%(refname:short)' | paste -sd, -)"
[ "$BRANCHES" = "main,staircase/run-1" ] || die "unexpected branches: $BRANCHES"
ok "the product repository gained only the run branch ($BRANCHES) and its certificate note; no harness files"
if [ -z "$MODE" ]; then
  say "Checking the change certificate of the run's commit"
  (cd "$TARGET_REPO" && staircase verify staircase/run-1 --min-cal 3) | sed 's/^/    /'
  git -C "$TARGET_REPO" log -1 --format=%B staircase/run-1 | grep -q '^Assisted-by: ' || die "no Assisted-by trailer"
  ok "signed, about exactly this commit, CAL 3; the commit names the agents that helped"
fi

say "The tamper-evident audit chain"
staircase inspect runs || true
if staircase audit export 1 >/dev/null 2>&1; then
  CP="$STAIRCASE_DIR/audit/run-1.checkpoint.json"
  staircase audit verify "$CP"
elif [ -z "$MODE" ]; then
  die "audit export failed for a successful run"
else
  note "(audit export needs a finished run)"
fi

say "Done."
note "Every decision above - the approvals, who made them, the committed bytes,"
note "the blueprint and plan the run executed - is on a signed, hash-chained"
note "audit log. Nothing the agents wrote reached the repo without a human."
