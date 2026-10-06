#!/usr/bin/env bats
# tests/kill_drill.bats - a real SIGKILL of a running staircase, then recovery.
#
# A governed session approves two changes, its process is killed with kill -9
# (no cleanup runs), and `staircase recover` must commit exactly the approved
# changes from what the dead process left on disk. The agent is a stand-in shell
# script that speaks Claude Code's hook protocol, so this runs without a login.

setup_file() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  STAIRCASE_BIN="$BATS_FILE_TMPDIR/staircase"
  export ROOT_DIR STAIRCASE_BIN
  (cd "$ROOT_DIR" && go build -o "$STAIRCASE_BIN" ./src/cmd/staircase)
}

setup() {
  WORK="$(mktemp -d)"
  export STAIRCASE_DIR="$WORK/workspace" NO_COLOR=1 HOME="$WORK/home"
  mkdir -p "$WORK/bin" "$WORK/app" "$HOME"
  git -C "$WORK/app" init -q -b main
  git -C "$WORK/app" config user.email drill@example.com
  git -C "$WORK/app" config user.name drill
  echo "# app" >"$WORK/app/README.md"
  git -C "$WORK/app" add -A && git -C "$WORK/app" commit -q -m init
  "$STAIRCASE_BIN" init --skip-venv >/dev/null 2>&1
  # rules approve file edits, so no person is needed; the drill is about the kill
  echo '{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}' >"$STAIRCASE_DIR/policy.json"
  cat >"$WORK/bin/claude" <<'FAKE'
#!/bin/bash
# a stand-in Claude Code: runs the session's own hook for two Write calls, then hangs
settings=""
while [ $# -gt 0 ]; do [ "$1" = "--settings" ] && settings="$2"; shift; done
cmd="$(sed -n 's/.*"command": *"\([^"]*\)".*/\1/p' "$settings" | head -1)"
hook() { printf '%s' "$1" | sh -c "$cmd" >/dev/null; }
hook '{"hook_event_name":"SessionStart","cwd":"'"$PWD"'"}'
for f in a b; do
  hook '{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"'"$PWD"'/src/'$f'.txt","content":"'$f' content\n"},"tool_use_id":"t-'$f'","cwd":"'"$PWD"'"}'
done
echo $$ >__AGENT_PID__ # the session passes its agent a filtered environment, so the path is baked in
exec sleep 600
FAKE
  sed -i.bak "s#__AGENT_PID__#$WORK/agent.pid#" "$WORK/bin/claude" && rm "$WORK/bin/claude.bak"
  chmod +x "$WORK/bin/claude"
  export PATH="$WORK/bin:$PATH"
}

teardown() {
  [ -f "$WORK/agent.pid" ] && kill -9 "$(cat "$WORK/agent.pid")" 2>/dev/null || true
  [ -n "${RUN_PID:-}" ] && kill -9 "$RUN_PID" 2>/dev/null || true
  rm -rf "$WORK"
}

sc() { "$STAIRCASE_BIN" "$@" 2>&1; }

@test "kill -9 in the middle of a session, then recover what it had approved" {
  (cd "$WORK/app" && exec "$STAIRCASE_BIN" claude --yes "write two files" >"$WORK/session.log" 2>&1 3>&-) & # 3>&-: bats waits for anything that holds its descriptor 3
  RUN_PID=$!
  # wait until the stand-in has had both changes approved: it then hangs
  for _ in $(seq 1 300); do [ -f "$WORK/agent.pid" ] && break; sleep 0.1; done
  [ -f "$WORK/agent.pid" ] || { cat "$WORK/session.log"; false; }
  [ "$(wc -l <"$STAIRCASE_DIR/journal/run-1.approved.jsonl" | tr -d ' ')" = 2 ]

  kill -9 "$RUN_PID"
  wait "$RUN_PID" 2>/dev/null || true
  kill -9 "$(cat "$WORK/agent.pid")" 2>/dev/null || true

  # what the dead process left: a run that still says RUNNING and no commit
  run sc inspect log 1
  [[ "$output" == *"status=RUNNING"* ]]
  [ "$(git -C "$WORK/app" rev-parse main)" = "$(git -C "$WORK/app" rev-parse staircase/run-1)" ]

  cd "$WORK/app"
  # a run of an older version has no owner lock to say its process is gone: it is asked of the person
  mv "$STAIRCASE_DIR/journal/run-1.owner.lock" "$WORK/owner.lock.aside"
  run sc recover 1
  [ "$status" -ne 0 ]
  [[ "$output" == *"may still be running"* ]]
  mv "$WORK/owner.lock.aside" "$STAIRCASE_DIR/journal/run-1.owner.lock"

  # this version's run: the dead process's lock is free, so no --force is needed
  run sc recover 1
  [ "$status" -eq 0 ]
  [[ "$output" == *"2 approved proposal(s)"* ]]
  [ "$(git show staircase/run-1:src/a.txt)" = "a content" ]
  [ "$(git show staircase/run-1:src/b.txt)" = "b content" ]

  # the recovered commit is evidence like any other: it verifies, says the run did not finish, and rebuilds
  run sc verify staircase/run-1 --min-cal 2
  [ "$status" -eq 0 ]
  run sc verify staircase/run-1 --min-cal 3
  [ "$status" -ne 0 ]
  run sc rebuild staircase/run-1
  [ "$status" -eq 0 ]
  run sc inspect log 1
  [[ "$output" == *"run_recovered"* ]]
  [[ "$output" == *"status=KILLED"* ]]
}

# What a crash of `recover` itself leaves must not be a dead end: it makes a commit,
# then writes the ledger, the certificate and the run's record. This kills it at
# random moments, from the same dead session, and a second `recover` must finish
# the job every time.
@test "kill -9 of recover itself, at random moments, never leaves a dead end" {
  (cd "$WORK/app" && exec "$STAIRCASE_BIN" claude --yes "write two files" >"$WORK/session.log" 2>&1 3>&-) &
  RUN_PID=$!
  for _ in $(seq 1 300); do [ -f "$WORK/agent.pid" ] && break; sleep 0.1; done
  [ -f "$WORK/agent.pid" ] || { cat "$WORK/session.log"; false; }
  kill -9 "$RUN_PID"; wait "$RUN_PID" 2>/dev/null || true
  kill -9 "$(cat "$WORK/agent.pid")" 2>/dev/null || true
  mkdir "$WORK/snap" && cp -a "$STAIRCASE_DIR" "$WORK/snap/workspace" && cp -a "$WORK/app" "$WORK/snap/app"

  RANDOM="${DRILL_SEED:-7}"   # a failure can be replayed with the same seed
  killed=0
  for i in $(seq 1 "${DRILL_ITERATIONS:-60}"); do
    rm -rf "$STAIRCASE_DIR" "$WORK/app"
    cp -a "$WORK/snap/workspace" "$STAIRCASE_DIR" && cp -a "$WORK/snap/app" "$WORK/app"
    cd "$WORK/app"
    "$STAIRCASE_BIN" recover 1 --force >/dev/null 2>&1 3>&- &
    pid=$!
    sleep "0.0$((RANDOM % 9))$((RANDOM % 10))"
    if kill -9 "$pid" 2>/dev/null; then killed=$((killed + 1)); fi
    wait "$pid" 2>/dev/null || true

    # whatever was left, another recover (or none, if the first finished) must end with everything in place
    second="$("$STAIRCASE_BIN" recover 1 --force 2>&1 3>&-)" || true
    run sc inspect log 1
    [[ "$output" == *"run_recovered"* ]] || { echo "iteration $i: no run_recovered after a second recover: $second"; false; }
    a="$(git show staircase/run-1:src/a.txt 2>&1)"; b="$(git show staircase/run-1:src/b.txt 2>&1)"
    { [ "$a" = "a content" ] && [ "$b" = "b content" ]; } || { echo "iteration $i: the files are wrong: a=[$a] b=[$b]; second recover said: $second"; false; }
    run sc verify staircase/run-1 --min-cal 2
    [ "$status" -eq 0 ] || { echo "iteration $i: verify: $output"; false; }
    run sc rebuild staircase/run-1
    [ "$status" -eq 0 ] || { echo "iteration $i: rebuild: $output"; false; }
  done
  echo "recover was killed in $killed of ${DRILL_ITERATIONS:-60} runs" >&3
}
