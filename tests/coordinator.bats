#!/usr/bin/env bats
# tests/coordinator.bats - the local coordinator (staircase serve) and the ownership of runs and sessions.
#
# A run owns itself for its whole life (a lock on journal/run-N.owner.lock) and a session owns its registration; the
# operating system drops both when the process ends, a kill -9 included. This kills sessions with SIGKILL (the binary is
# built with -tags barriers, which holds a process at a named point) and checks what the one coordinator then does.
# The agent is a stand-in that speaks Claude Code's hook protocol, so no login is needed.

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
  [ -n "${SERVE_PID:-}" ] && kill "$SERVE_PID" 2>/dev/null || true
  rm -rf "$WORK"
}

sc() { "$STAIRCASE_BIN" "$@" 2>&1; }


free_port() { python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'; }

@test "there is one coordinator for a workspace: a second serve is refused and names the first" {
  "$STAIRCASE_BIN" serve --port 0 --token one >"$WORK/serve1.log" 2>&1 3>&- &
  SERVE_PID=$!
  for _ in $(seq 1 100); do grep -q "Review every session" "$WORK/serve1.log" && break; sleep 0.1; done
  grep -q "Review every session" "$WORK/serve1.log"
  first="$(sed -n 's#.*\(http://127.0.0.1:[0-9]*\).*#\1#p' "$WORK/serve1.log" | head -1)"
  run sc serve --port 0 --token two
  [ "$status" -ne 0 ]
  [[ "$output" == *"already serving this workspace at $first"* ]]
  kill "$SERVE_PID"; wait "$SERVE_PID" 2>/dev/null || true; SERVE_PID=""
  # the first has ended: another can serve
  "$STAIRCASE_BIN" serve --port 0 --token three >"$WORK/serve3.log" 2>&1 3>&- &
  SERVE_PID=$!
  for _ in $(seq 1 100); do grep -q "Review every session" "$WORK/serve3.log" && break; sleep 0.1; done
  grep -q "Review every session" "$WORK/serve3.log"
}

@test "after a session is killed: its registration is removed, its run is reported, the checkout is untouched" {
  port="$(free_port)"
  (cd "$WORK/app" && STAIRCASE_BARRIER=consumed@2 STAIRCASE_BARRIER_DIR="$WORK/mark" exec "$STAIRCASE_BIN" claude --yes --approval-port "$port" --approval-token t "write two files" >"$WORK/held.log" 2>&1 3>&-) &
  HELD_PID=$!
  for _ in $(seq 1 300); do [ -f "$WORK/mark/consumed.reached" ] && break; sleep 0.1; done
  [ -f "$WORK/mark/consumed.reached" ] || { cat "$WORK/held.log"; false; }
  [ "$(ls "$STAIRCASE_DIR"/sessions/*.json | wc -l | tr -d ' ')" = 1 ]      # the live session is registered
  before="$(git -C "$WORK/app" status --porcelain=v1; git -C "$WORK/app" rev-parse HEAD main)"

  kill -9 "$HELD_PID"; wait "$HELD_PID" 2>/dev/null || true; HELD_PID=""
  [ "$(ls "$STAIRCASE_DIR"/sessions/*.json | wc -l | tr -d ' ')" = 1 ]      # a kill left its registration behind

  "$STAIRCASE_BIN" serve --port 0 --token k >"$WORK/serve.log" 2>&1 3>&- &
  SERVE_PID=$!
  for _ in $(seq 1 100); do grep -q "Review every session" "$WORK/serve.log" && break; sleep 0.1; done
  grep -q "Removed the registration" "$WORK/serve.log"
  grep -q "Run #1 was killed before it finished" "$WORK/serve.log"
  grep -q "staircase recover 1" "$WORK/serve.log"
  ! grep -q -- "--force" "$WORK/serve.log"
  [ "$(ls "$STAIRCASE_DIR"/sessions/*.json 2>/dev/null | grep -v hub.json | wc -l | tr -d ' ')" = 0 ]
  # nothing outside the workspace changed: the developer's checkout is as it was
  [ "$(git -C "$WORK/app" status --porcelain=v1; git -C "$WORK/app" rev-parse HEAD main)" = "$before" ]
  kill "$SERVE_PID"; wait "$SERVE_PID" 2>/dev/null || true; SERVE_PID=""
}
