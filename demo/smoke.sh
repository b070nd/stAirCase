#!/usr/bin/env bash
# smoke.sh - opt-in acceptance runs against real agents (they cost money and
# need credentials; `make check` never runs them).
#
#   ./demo/smoke.sh model    a real model through the LLM gateway records a small
#                            run; a second run replays the recording with the
#                            gateway unreachable and must commit the same bytes.
#                            Needs AI_GATEWAY_API_KEY (model: SMOKE_MODEL,
#                            default openai/gpt-6-astra; SMOKE_GATEWAY_URL
#                            overrides the gateway).
#   ./demo/smoke.sh claude   the same task as a `staircase claude` session; the
#                            first approval is held 40 s (longer than Claude
#                            Code's default hook timeout). Needs a logged-in `claude`.
#   ./demo/smoke.sh codex    the same task as a `staircase codex` session with
#                            SMOKE_CODEX_MODEL (default gpt-6-luna). Needs a
#                            logged-in codex (on PATH or in the ChatGPT app).
#   ./demo/smoke.sh codex-stop
#                            the Gate 3 run against Codex: the first approval is held
#                            70 s (longer than a hook's default timeout) and then
#                            rejected, the agent's retry is approved, and a failing
#                            --check must block its first attempt to stop (Stop hook)
#                            until it is fixed. Afterwards the commit is verified and
#                            rebuilt in a fresh clone with only the public key. Same
#                            credentials as codex; set SMOKE_CODEX_MODEL=nonexistent
#                            for a free run that stops at the model call.
#
# Each skips (exit 0) when its credentials are missing. Approvals are given
# through the approval API; the key is passed on stdin and never printed.
set -euo pipefail

MODE="${1:?usage: smoke.sh model|claude|codex|codex-stop}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SMOKE_MODEL="${SMOKE_MODEL:-openai/gpt-6-astra}"

case "$MODE" in
  model)  [ -n "${AI_GATEWAY_API_KEY:-}" ] || { echo "⏭  smoke model skipped: AI_GATEWAY_API_KEY is not set"; exit 0; } ;;
  claude) AUTH="$(claude auth status 2>/dev/null || true)"   # captured: grep -q in a pipe would SIGPIPE it
          [[ "$AUTH" =~ \"loggedIn\":\ *true ]] \
            || { echo "⏭  smoke claude skipped: the claude CLI is not installed or not logged in"; exit 0; } ;;
  codex|codex-stop)
          CODEX="$(command -v codex || echo /Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex)"
          "$CODEX" login status >/dev/null 2>&1 \
            || { echo "⏭  smoke codex skipped: codex is not installed or not logged in"; exit 0; } ;;
  *) echo "usage: smoke.sh model|claude|codex|codex-stop"; exit 2 ;;
esac

# shellcheck source=demo/lib.sh
. "$REPO_ROOT/demo/lib.sh"
require_tools go git curl

WORK="$(mktemp -d "${TMPDIR:-/tmp}/staircase-smoke.XXXXXX")"
export STAIRCASE_DIR="$WORK/.staircase"
# SMOKE_KEEP=1 keeps the scratch directory (a paid run's evidence) for a look afterwards.
trap 'kill ${RUN_PID:-} 2>/dev/null || true; if [ -n "${SMOKE_KEEP:-}" ]; then echo "kept: $WORK"; else rm -rf "$WORK"; fi' EXIT
build_binaries "$WORK"

REPO="$WORK/app"
mkdir -p "$REPO"
git -C "$REPO" init -q -b main
printf '# app\n' > "$REPO/README.md"
git -C "$REPO" add -A
git -C "$REPO" -c user.email=smoke@staircase.local -c user.name=smoke commit -q -m init

BP="$WORK/blueprint"
mkdir -p "$BP"
cat > "$BP/blueprint.yaml" <<EOF
name: smoke
supervisor: supervisor
agents:
  - {name: supervisor, model: $SMOKE_MODEL, prompt: "You coordinate the work. Hand the task to the coder, then end."}
  - {name: coder, model: $SMOKE_MODEL, prompt: "You write code. Use create_file for new files. Change nothing else."}
edges:
  - {from: supervisor, to: coder}
  - {from: coder, to: supervisor}
cases:
  - slug: greet
    prd: Create GREETING.md containing one short, friendly line of greeting. Do not change any other file.
    stories:
      - text: Create GREETING.md with a greeting
        scope: {allow: [GREETING.md], max_files: 1}
limits: {max_scope_violations: 2, max_run_secs: 900}
EOF

mkdir -p "$STAIRCASE_DIR"
staircase init >/dev/null
[ "$MODE" = model ] && printf '%s' "$AI_GATEWAY_API_KEY" | staircase secret set LLM_GATEWAY_API_KEY >/dev/null
# SMOKE_GATEWAY_URL points the recording at another OpenAI-compatible gateway
# (the script's own check runs it against demo/demotool).
[ -n "${SMOKE_GATEWAY_URL:-}" ] && printf '%s' "$SMOKE_GATEWAY_URL" | staircase secret set LLM_GATEWAY_URL >/dev/null
staircase vendor add smoke >/dev/null
staircase project add smoke app --source "$REPO" >/dev/null
staircase blueprint import "$BP" >/dev/null
staircase project bind 1 "$(staircase blueprint list | awk 'NR==3 {print $1}')" >/dev/null
staircase compile 1 >/dev/null

TASK="Create GREETING.md containing one short, friendly line of greeting. Do not change any other file."

# run <run-id> <hold-first-approval-seconds> <staircase command and flags...>:
# approvals come through the approval API, the first one held.
run() {
  local id="$1" hold="$2"; shift 2
  local port; port="$("$DEMOTOOL" freeport)"
  (cd "$REPO" && staircase "$@" --approval-port "$port" --approval-token t) >"$WORK/run$id.log" 2>&1 &
  RUN_PID=$!
  local api="http://127.0.0.1:$port/v1/yields" n=0 y
  while kill -0 "$RUN_PID" 2>/dev/null; do
    y="$(curl -s -H 'Authorization: Bearer t' "$api" 2>/dev/null | "$DEMOTOOL" first-yield || true)"
    if [ -n "$y" ]; then
      curl -s -H 'Authorization: Bearer t' "$api/$y" | "$DEMOTOOL" show-yield
      if [ "$n" = 0 ] && [ "$hold" -gt 0 ]; then echo "  holding the first approval ${hold}s"; sleep "$hold"; fi
      if [ "$n" = 0 ] && [ -n "${REJECT_FIRST:-}" ]; then
        echo "  rejecting the first proposal"
        curl -s -X POST -H 'Authorization: Bearer t' -d '{"feedback":"not like this, try again"}' "$api/$y/reject" >/dev/null
      else
        curl -s -X POST -H 'Authorization: Bearer t' -d '{"feedback":"smoke"}' "$api/$y/approve" >/dev/null
      fi
      n=$((n + 1))
    fi
    sleep 0.2
  done
  wait "$RUN_PID" || { tail -20 "$WORK/run$id.log"; die "run #$id failed"; }
  RUN_PID=""
  local want="${WANT_FILE:-GREETING.md}"
  git -C "$REPO" cat-file -e "staircase/run-$id:$want" 2>/dev/null || die "run #$id did not commit $want"
  echo "  ✓ run #$id SUCCESS after $n decision(s); $want:"
  git -C "$REPO" show "staircase/run-$id:$want" | sed 's/^/    │ /'
  staircase audit export "$id" >/dev/null && staircase audit verify "$STAIRCASE_DIR/audit/run-$id.checkpoint.json"
  fresh_verify "$id"
}

# fresh_verify <run-id>: a clone that has never seen the workspace checks the
# commit with only the public signing key: certificate, level, and a rebuild from
# the ledger note. This is what a reviewer or a CI job has.
fresh_verify() {
  local id="$1" fresh="$WORK/fresh-$1"
  git clone -q "$REPO" "$fresh"
  git -C "$fresh" fetch -q origin '+refs/heads/staircase/*:refs/remotes/origin/staircase/*' '+refs/notes/*:refs/notes/*'
  (cd "$fresh" && STAIRCASE_DIR="$WORK/empty-ws" HOME="$WORK/empty-home" \
     "$STAIRCASE_BIN" verify "origin/staircase/run-$id" --key "$STAIRCASE_DIR/.signing.pub" --min-cal 2 --rebuild) \
    || die "a fresh clone could not verify run #$id with only the public key"
}

case "$MODE" in
  model)
    say "Recording a real run ($SMOKE_MODEL)"
    run 1 0 run 1 --record-llm "$WORK/llm.jsonl"
    ! grep -qF "$AI_GATEWAY_API_KEY" "$WORK/llm.jsonl" || die "the recording contains the gateway key"
    echo "  ✓ recording holds $(wc -l < "$WORK/llm.jsonl" | tr -d ' ') exchanges and no key"
    say "Replaying it offline (gateway URL made unreachable)"
    printf 'http://127.0.0.1:9/v1' | staircase secret set LLM_GATEWAY_URL >/dev/null
    run 2 0 run 1 --replay-llm "$WORK/llm.jsonl"
    [ "$(git -C "$REPO" rev-parse staircase/run-1:GREETING.md)" = "$(git -C "$REPO" rev-parse staircase/run-2:GREETING.md)" ] \
      || die "the replay committed different bytes"
    echo "  ✓ the replay committed the same bytes, offline" ;;
  claude)
    say "Claude Code, every tool call governed by hooks"
    run 1 40 claude --yes --allow GREETING.md "$TASK" ;;
  codex-stop)
    STOP_CHECK='test -f NOTES.txt && grep -q STAIRCASE-CHECK NOTES.txt || { echo "NOTES.txt must mention the word STAIRCASE-CHECK"; exit 1; }'
    say "Codex: a held and rejected approval, then a Stop refused until the check passes"
    REJECT_FIRST=1 WANT_FILE=NOTES.txt run 1 70 codex --yes --model "${SMOKE_CODEX_MODEL:-gpt-6-luna}" --allow NOTES.txt \
      --check "$STOP_CHECK" "Create NOTES.txt containing one short line describing this project. Do not change any other file."
    staircase inspect log 1 | grep -E "done_checked|yield_decided" | sed 's/^/    /'
    [ "$(staircase inspect log 1 | grep -c done_checked)" -ge 2 ] || die "the Stop hook did not run the check twice: it never blocked the first attempt to stop"
    echo "  ✓ the first attempt to stop was blocked by the failing check, the second allowed" ;;
  codex)
    say "Codex, edits decided first, command changes reviewed after"
    run 1 0 codex --yes --model "${SMOKE_CODEX_MODEL:-gpt-6-luna}" --allow GREETING.md "$TASK" ;;
esac
echo "✅ smoke $MODE passed"
