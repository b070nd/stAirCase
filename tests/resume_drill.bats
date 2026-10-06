#!/usr/bin/env bats
# tests/resume_drill.bats - kill -9 at the boundaries of a run and of its continuation, and continue it.
#
# A run killed at a named boundary is continued (staircase resume), and the continuation is killed at its own boundaries and
# continued or recovered again. Each ends with one owned commit that holds exactly what was approved and that verifies and
# rebuilds. The binary is built with -tags barriers (see barrier_drill.bats); the agent is a stand-in Claude Code that proposes
# a.txt and b.txt in a first session and, when its prompt says it is a continuation, c.txt only.

setup_file() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  STAIRCASE_BIN="$BATS_FILE_TMPDIR/staircase"
  export ROOT_DIR STAIRCASE_BIN
  (cd "$ROOT_DIR" && go build -tags barriers -o "$STAIRCASE_BIN" ./src/cmd/staircase)
}

setup() {
  WORK="$(mktemp -d)"
  export ARGS_LOG="$WORK/claude.args" STAIRCASE_DIR="$WORK/workspace" NO_COLOR=1 HOME="$WORK/home"
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
settings=""; prompt=""
printf '%s\n' "$*" >>ARGS_LOG_PATH
while [ $# -gt 0 ]; do [ "$1" = "--settings" ] && settings="$2"; [ "$1" = "-p" ] && prompt="$2"; shift; done
cmd="$(sed -n 's/.*"command": *"\([^"]*\)".*/\1/p' "$settings" | head -1)"
hook() { printf '%s' "$1" | sh -c "$cmd" >/dev/null; }
hook '{"hook_event_name":"SessionStart","cwd":"'"$PWD"'"}'
files="a b"
case "$prompt" in *"continuing it (segment"*) files="c" ;; esac
for f in $files; do
  # a hook that fails (the run is gone) blocks the tool: an agent whose run died writes nothing more
  hook '{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"'"$PWD"'/src/'$f'.txt","content":"'$f' content\n"},"tool_use_id":"t-'$f'","cwd":"'"$PWD"'"}' || continue
  mkdir -p src && printf '%s content\n' $f >src/$f.txt
done
FAKE
  sed -i.bak "s#ARGS_LOG_PATH#$ARGS_LOG#" "$WORK/bin/claude" && rm "$WORK/bin/claude.bak"   # the agent's environment is scrubbed: the path is baked in
  chmod +x "$WORK/bin/claude"
  export PATH="$WORK/bin:$PATH"
}

teardown() {
  [ -n "${HELD_PID:-}" ] && kill -9 "$HELD_PID" 2>/dev/null || true
  rm -rf "$WORK"
}

sc() { "$STAIRCASE_BIN" "$@" 2>&1; }

# hold <barrier> <command...>: run the command held at the barrier, wait until it is there, kill it with SIGKILL.
hold() {
  local barrier="$1"; shift
  (cd "$WORK/app" && STAIRCASE_BARRIER="$barrier" STAIRCASE_BARRIER_DIR="$WORK/mark" exec "$@" >"$WORK/held.log" 2>&1 3>&-) &
  HELD_PID=$!
  local name="${barrier%@*}"
  for _ in $(seq 1 300); do [ -f "$WORK/mark/$name.reached" ] && break; sleep 0.1; done
  [ -f "$WORK/mark/$name.reached" ] || { echo "never reached $barrier"; cat "$WORK/held.log"; false; }
  [ "$(cat "$WORK/mark/$name.reached")" = "$HELD_PID" ]
  kill -9 "$HELD_PID"; wait "$HELD_PID" 2>/dev/null || true
  rm -f "$WORK/mark/$name.reached"; HELD_PID=""
}

first() { hold "$1" "$STAIRCASE_BIN" claude --yes "write two files"; }   # segment 1, killed at a boundary
resume() { (cd "$WORK/app" && "$STAIRCASE_BIN" resume 1 --fresh-context "$@" 2>&1 3>&-); }
on_branch() { git -C "$WORK/app" show "staircase/run-1:$1" 2>/dev/null; }
sound() {
  run sc verify staircase/run-1 --min-cal 2
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  run sc rebuild staircase/run-1
  [ "$status" -eq 0 ] || { echo "$output"; false; }
}
one_commit() { [ "$(git -C "$WORK/app" rev-list --count main..staircase/run-1)" = 1 ]; }
resumed_events() { sc inspect log 1 | grep -c "run_resumed" || true; }

@test "a run killed after its first approval is continued: one commit with the earlier approval and the new work" {
  first consumed@1
  cd "$WORK/app"
  run resume
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(on_branch src/a.txt)" = "a content" ]
  [ -z "$(on_branch src/b.txt)" ]                         # b was never proposed by the first segment
  [ "$(on_branch src/c.txt)" = "c content" ]
  one_commit
  [ "$(resumed_events)" = 1 ]
  sound
}

@test "a continuation killed before it changed anything is continued again" {
  first consumed@1
  cd "$WORK/app"
  hold resume-checked "$STAIRCASE_BIN" resume 1 --fresh-context
  [ "$(resumed_events)" = 0 ]                              # it had written nothing to the chain
  run sc inspect log 1
  [[ "$output" == *"status=RUNNING"* || "$output" == *"status=KILLED"* ]]
  run resume
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(on_branch src/c.txt)" = "c content" ]
  one_commit
  sound
}

@test "a continuation killed once the run is reopened is continued again, as a third segment" {
  first consumed@1
  cd "$WORK/app"
  hold resume-started "$STAIRCASE_BIN" resume 1 --fresh-context
  [ "$(resumed_events)" = 1 ]
  run resume
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(resumed_events)" = 2 ]
  run sc inspect log 1 --full
  [[ "$output" == *'"segment":3'* ]]
  [ "$(on_branch src/a.txt)" = "a content" ] && [ "$(on_branch src/c.txt)" = "c content" ]
  one_commit
  sound
}

@test "a continuation killed after its own approval is recovered: both segments' approvals, one commit" {
  first consumed@1
  cd "$WORK/app"
  hold consumed@1 "$STAIRCASE_BIN" resume 1 --fresh-context       # the continuation's first approval (c) is consumed, then it dies
  run sc recover 1
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(on_branch src/a.txt)" = "a content" ] && [ "$(on_branch src/c.txt)" = "c content" ]
  one_commit
  sound
}

@test "a continuation killed after the branch moved is not continued again: it is recovered, keeping its commit" {
  first consumed@1
  cd "$WORK/app"
  hold git-cas "$STAIRCASE_BIN" resume 1 --fresh-context
  commit="$(git rev-parse staircase/run-1)"
  run resume
  [ "$status" -ne 0 ]
  [[ "$output" == *"already made its commit"* ]]
  [ "$(git rev-parse staircase/run-1)" = "$commit" ]
  run sc recover 1
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(git rev-parse staircase/run-1)" = "$commit" ]
  one_commit
  sound
}

@test "a run that is alive cannot be continued twice; once it is killed it can" {
  first consumed@1
  cd "$WORK/app"
  (STAIRCASE_BARRIER=resume-started STAIRCASE_BARRIER_DIR="$WORK/mark" exec "$STAIRCASE_BIN" resume 1 --fresh-context >"$WORK/held.log" 2>&1 3>&-) &
  HELD_PID=$!
  for _ in $(seq 1 300); do [ -f "$WORK/mark/resume-started.reached" ] && break; sleep 0.1; done
  [ -f "$WORK/mark/resume-started.reached" ]
  run sc resume 1 --fresh-context
  [ "$status" -ne 0 ]
  [[ "$output" == *"still running"* ]]
  kill -9 "$HELD_PID"; wait "$HELD_PID" 2>/dev/null || true; HELD_PID=""
  run resume
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  one_commit
  sound
}

@test "work the agent did after its last approval is not adopted: refused, then discarded on request" {
  first consumed@1
  cd "$WORK/app"
  wt="$(ls -d "$STAIRCASE_DIR"/worktrees/run-1)"
  mkdir -p "$wt/src" && echo "unapproved" >"$wt/src/stray.txt"
  run resume
  [ "$status" -ne 0 ]
  [[ "$output" == *"stray.txt"* ]]
  [[ "$output" == *"--discard-unapproved"* ]]
  [ "$(resumed_events)" = 0 ]
  [ -f "$wt/src/stray.txt" ]                               # nothing was touched
  run resume --discard-unapproved
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ -z "$(on_branch src/stray.txt)" ]
  [ "$(on_branch src/c.txt)" = "c content" ]
  one_commit
  sound
}

@test "an agent harness run is continued in its own session, and says so" {
  first consumed@1
  cd "$WORK/app"
  id="$(sed -n 's/.*--session-id \([0-9a-f-]*\).*/\1/p' "$ARGS_LOG" | head -1)"
  [ -n "$id" ]                                              # the first segment was started under an id staircase chose
  run sc resume 1
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(grep -c -- "--resume $id" "$ARGS_LOG")" = 1 ]       # and the continuation resumed that session
  [ "$(grep -c -- "--session-id" "$ARGS_LOG")" = 1 ]
  sc inspect log 1 --full | grep -q "native_resume"
  [ "$(on_branch src/c.txt)" = "c content" ]
  one_commit
  sound
}

@test "--fresh-context starts a new session instead, and the chain says so" {
  first consumed@1
  cd "$WORK/app"
  run resume
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(grep -c -- "--session-id" "$ARGS_LOG")" = 2 ]
  [ "$(grep -c -- "--resume" "$ARGS_LOG")" = 0 ]
  sc inspect log 1 --full | grep -q "fresh_grounded"
}
