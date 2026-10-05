#!/usr/bin/env bats
# tests/barrier_drill.bats - kill -9 at each boundary of a run, from a fresh process's point of view.
#
# kill_drill.bats kills at random moments. This kills at named ones: the staircase
# binary is built with -tags barriers, which holds the process at a point and writes a
# marker file when it gets there. The drill waits for the marker (a point that is never
# reached fails the test, it is not skipped), kills the process with SIGKILL, and then
# a fresh process reopens what was left: the approvals, the audit chain, the branch,
# the evidence and the run's record. SIGKILL ends the process without cleanup; it does
# not model a power loss, which also loses what the operating system had not yet written.
#
# The agent is a stand-in that speaks Claude Code's hook protocol, asks for two file
# writes, writes them once approved and exits, so no login is needed.

setup_file() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  STAIRCASE_BIN="$BATS_FILE_TMPDIR/staircase"
  export ROOT_DIR STAIRCASE_BIN
  (cd "$ROOT_DIR" && go build -tags barriers -o "$STAIRCASE_BIN" ./src/cmd/staircase)
}

setup() {
  WORK="$(mktemp -d)"
  export STAIRCASE_DIR="$WORK/workspace" NO_COLOR=1 HOME="$WORK/home"
  mkdir -p "$WORK/bin" "$WORK/app" "$WORK/mark" "$HOME"
  git -C "$WORK/app" init -q -b main
  git -C "$WORK/app" config user.email drill@example.com
  git -C "$WORK/app" config user.name drill
  echo "# app" >"$WORK/app/README.md"
  git -C "$WORK/app" add -A && git -C "$WORK/app" commit -q -m init
  "$STAIRCASE_BIN" init --skip-venv >/dev/null 2>&1
  echo '{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}' >"$STAIRCASE_DIR/policy.json"
  cat >"$WORK/bin/claude" <<'FAKE'
#!/bin/bash
settings=""
while [ $# -gt 0 ]; do [ "$1" = "--settings" ] && settings="$2"; shift; done
cmd="$(sed -n 's/.*"command": *"\([^"]*\)".*/\1/p' "$settings" | head -1)"
hook() { printf '%s' "$1" | sh -c "$cmd" >/dev/null; }
hook '{"hook_event_name":"SessionStart","cwd":"'"$PWD"'"}'
for f in a b; do
  hook '{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"'"$PWD"'/src/'$f'.txt","content":"'$f' content\n"},"tool_use_id":"t-'$f'","cwd":"'"$PWD"'"}'
  mkdir -p src && printf '%s content\n' $f >src/$f.txt
done
FAKE
  chmod +x "$WORK/bin/claude"
  export PATH="$WORK/bin:$PATH"
}

teardown() {
  [ -n "${HELD_PID:-}" ] && kill -9 "$HELD_PID" 2>/dev/null || true
  rm -rf "$WORK"
}

sc() { "$STAIRCASE_BIN" "$@" 2>&1; }

# hold <barrier> <command...>: run the command held at the barrier, wait until it is
# there, and kill it with SIGKILL. A barrier that is not reached in 30 s fails the test.
hold() {
  local barrier="$1"; shift
  (cd "$WORK/app" && STAIRCASE_BARRIER="$barrier" STAIRCASE_BARRIER_DIR="$WORK/mark" exec "$@" >"$WORK/held.log" 2>&1 3>&-) &
  HELD_PID=$!
  local name="${barrier%@*}"
  for _ in $(seq 1 300); do [ -f "$WORK/mark/$name.reached" ] && break; sleep 0.1; done
  [ -f "$WORK/mark/$name.reached" ] || { echo "never reached $barrier"; cat "$WORK/held.log"; false; }
  [ "$(cat "$WORK/mark/$name.reached")" = "$HELD_PID" ]  # it is the process that is killed, not another
  kill -9 "$HELD_PID"
  wait "$HELD_PID" 2>/dev/null || true
  rm -f "$WORK/mark/$name.reached"
  HELD_PID=""
}

session() { hold "$1" "$STAIRCASE_BIN" claude --yes "write two files"; }
decisions() { sc inspect log 1 | grep -c "yield_decided" || true; }
on_branch() { git -C "$WORK/app" show "staircase/run-1:$1" 2>/dev/null; }
note() { git -C "$WORK/app" notes --ref="$1" show "$(git -C "$WORK/app" rev-parse staircase/run-1)" 2>/dev/null; }

# the approved changes verify and rebuild like any other
sound() {
  run sc verify staircase/run-1 --min-cal 2
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  run sc rebuild staircase/run-1
  [ "$status" -eq 0 ] || { echo "$output"; false; }
}

@test "killed once an approval is in the journal, before it is on the chain: nothing is recovered" {
  session journal-synced
  [ "$(wc -l <"$STAIRCASE_DIR/journal/run-1.approved.jsonl" | tr -d ' ')" = 1 ]
  [ "$(decisions)" = 0 ]
  cd "$WORK/app"
  run sc recover 1 --force
  [ "$status" -ne 0 ]
  [[ "$output" == *"nothing approved"* ]]
  # what only the journal knows was never decided on the chain, so it is never committed
  [ "$(git rev-parse main)" = "$(git rev-parse staircase/run-1)" ]
}

@test "killed once the first decision is on the chain: recovery commits it and nothing else" {
  session audit-committed
  [ "$(decisions)" = 1 ]
  cd "$WORK/app"
  run sc recover 1 --force
  [ "$status" -eq 0 ]
  [ "$(on_branch src/a.txt)" = "a content" ]
  [ -z "$(on_branch src/b.txt)" ]
  sound
}

@test "killed with the second approval consumed, the agent not yet answered: both are recovered" {
  session consumed@2
  [ "$(decisions)" = 2 ]
  cd "$WORK/app"
  run sc recover 1 --force
  [ "$status" -eq 0 ]
  [ "$(on_branch src/a.txt)" = "a content" ]
  [ "$(on_branch src/b.txt)" = "b content" ]
  sound
}

@test "killed with the commit named on the chain and the branch not yet moved: recovery makes its own commit" {
  session commit-prepared
  cd "$WORK/app"
  [ "$(git rev-parse main)" = "$(git rev-parse staircase/run-1)" ]   # no branch holds it yet
  [ "$(sc inspect log 1 | grep -c commit_prepared)" = 1 ]
  named="$(sc inspect log 1 --full | sed -n 's/.*"commit":"\([0-9a-f]\{40\}\)".*/\1/p' | head -1)"
  [ -n "$named" ]
  run sc recover 1 --force
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(on_branch src/a.txt)" = "a content" ] && [ "$(on_branch src/b.txt)" = "b content" ]
  [ "$(git rev-parse staircase/run-1)" != "$named" ]   # the branch holds recovery's commit, not a dangling one
  [ "$(git rev-list --count main..staircase/run-1)" = 1 ]
  sound
}

@test "killed right after the branch moved to the run's commit: recovery keeps that commit and finishes its evidence" {
  session git-cas
  cd "$WORK/app"
  commit="$(git rev-parse staircase/run-1)"
  [ "$commit" != "$(git rev-parse main)" ]
  [ -z "$(note staircase)" ]                 # no certificate yet
  run sc inspect log 1
  [[ "$output" == *"status=RUNNING"* ]]
  run sc recover 1 --force
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(git rev-parse staircase/run-1)" = "$commit" ]   # no second commit
  [ -n "$(note staircase)" ] && [ -n "$(note staircase-ledger)" ]
  run sc inspect log 1
  [[ "$output" == *"status=KILLED"* ]]
  sound
}

@test "killed with the evidence published, before the run's record: recovery completes the record and keeps the evidence" {
  session evidence-published
  cd "$WORK/app"
  commit="$(git rev-parse staircase/run-1)"
  certificate="$(cat "$STAIRCASE_DIR/audit/run-1.certificate.json")"
  [ -n "$(note staircase)" ]
  run sc inspect log 1
  [[ "$output" == *"status=RUNNING"* ]]
  run sc recover 1 --force
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(git rev-parse staircase/run-1)" = "$commit" ]
  [ "$(cat "$STAIRCASE_DIR/audit/run-1.certificate.json")" = "$certificate" ]   # the run's own certificate stays
  run sc inspect log 1
  [[ "$output" == *"commit=$commit"* ]]
  sound
}

@test "killed after the run's record is complete: nothing is left to recover, and it verifies" {
  session db-completed
  cd "$WORK/app"
  run sc inspect log 1
  [[ "$output" == *"status=SUCCESS"* ]]
  run sc recover 1 --force
  [ "$status" -ne 0 ]
  [[ "$output" == *"nothing to recover"* ]]
  sound
}

# the same dead session, and recover itself killed at its own boundaries
@test "recover killed after it recorded its operation: a second recover delivers one commit" {
  session consumed@2
  cd "$WORK/app"
  hold recover-op-recorded "$STAIRCASE_BIN" recover 1 --force
  [ "$(git rev-parse main)" = "$(git rev-parse staircase/run-1)" ]   # it had not committed
  run sc recover 1 --force
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(on_branch src/b.txt)" = "b content" ]
  [ "$(git rev-list --count main..staircase/run-1)" = 1 ]
  sound
}

@test "recover killed after its commit, before its evidence: a second recover repairs the same commit" {
  session consumed@2
  cd "$WORK/app"
  hold recover-committed "$STAIRCASE_BIN" recover 1 --force
  commit="$(git rev-parse staircase/run-1)"
  [ "$commit" != "$(git rev-parse main)" ]
  [ -z "$(note staircase)" ]
  run sc recover 1 --force
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(git rev-parse staircase/run-1)" = "$commit" ]
  [ "$(git rev-list --count main..staircase/run-1)" = 1 ]
  [ -n "$(note staircase)" ] && [ -n "$(note staircase-ledger)" ]
  sound
}
