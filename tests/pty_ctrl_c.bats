#!/usr/bin/env bats
# tests/pty_ctrl_c.bats - the terminal approval dialog in a real pseudo-terminal.
#
# A person decides each proposal in a full-screen dialog; this drives that dialog
# the way a person does, with keystrokes on a pty: approve one change, press
# Ctrl-C at the next, and check the run stopped, recorded its outcome and kept
# exactly the approved change for `staircase recover`. The agent is a stand-in
# shell script that speaks Claude Code's hook protocol.

setup_file() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  STAIRCASE_BIN="$BATS_FILE_TMPDIR/staircase"
  export ROOT_DIR STAIRCASE_BIN
  (cd "$ROOT_DIR" && go build -o "$STAIRCASE_BIN" ./src/cmd/staircase)
}

setup() {
  if ! command -v python3 >/dev/null; then
    # a gate that skips what it cannot run is not a gate: CI and the release checks require it
    [ -z "${STAIRCASE_REQUIRE_DEPS:-}" ] || { echo "python3 is required (STAIRCASE_REQUIRE_DEPS) and was not found"; false; }
    skip "python3 is needed to drive a pty"
  fi
  WORK="$(mktemp -d)"
  export STAIRCASE_DIR="$WORK/workspace" NO_COLOR=1 HOME="$WORK/home"
  mkdir -p "$WORK/bin" "$WORK/app" "$HOME"
  git -C "$WORK/app" init -q -b main
  git -C "$WORK/app" config user.email pty@example.com
  git -C "$WORK/app" config user.name pty
  echo "# app" >"$WORK/app/README.md"
  git -C "$WORK/app" add -A && git -C "$WORK/app" commit -q -m init
  "$STAIRCASE_BIN" init --skip-venv >/dev/null 2>&1
  echo '{"rules":[]}' >"$STAIRCASE_DIR/policy.json" # no rules: every change goes to the person
  cat >"$WORK/bin/claude" <<'FAKE'
#!/bin/bash
# a stand-in Claude Code: two Write calls through the session's own hook, then it hangs
settings=""
while [ $# -gt 0 ]; do [ "$1" = "--settings" ] && settings="$2"; shift; done
cmd="$(sed -n 's/.*"command": *"\([^"]*\)".*/\1/p' "$settings" | head -1)"
hook() { printf '%s' "$1" | sh -c "$cmd" >/dev/null; }
hook '{"hook_event_name":"SessionStart","cwd":"'"$PWD"'"}'
for f in a b; do
  hook '{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"'"$PWD"'/src/'$f'.txt","content":"'$f' content\n"},"tool_use_id":"t-'$f'","cwd":"'"$PWD"'"}'
done
exec sleep 600
FAKE
  chmod +x "$WORK/bin/claude"
  export PATH="$WORK/bin:$PATH"
  # the keystrokes of a person, on a pty: expect <text on screen>, then send <keys>
  cat >"$WORK/drive.py" <<'PY'
import os, pty, re, select, struct, sys, termios, fcntl, time
steps = sys.argv[1:]            # pairs: text to wait for, keys to send ("\x03" style escapes)
pid, fd = pty.fork()
if pid == 0:
    os.chdir(os.environ["APP"])
    os.execv(os.environ["BIN"], [os.environ["BIN"], "claude", "--yes", "do the work"])
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
ansi = re.compile(rb"\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[()][0-9A-B]|\x1b[=>]")
seen = b""
def wait_for(text, timeout):
    global seen
    end = time.time() + timeout
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.2)
        if r:
            try:
                chunk = os.read(fd, 65536)
            except OSError:
                return False
            if not chunk:
                return False
            seen += chunk
        if text.encode() in ansi.sub(b"", seen):
            return True
    return False
for i in range(0, len(steps), 2):
    if not wait_for(steps[i], 20):
        print("TIMEOUT waiting for %r; screen: %r" % (steps[i], ansi.sub(b"", seen)[-600:]))
        os.kill(pid, 9); sys.exit(3)
    seen = b""
    time.sleep(0.3)
    os.write(fd, steps[i + 1].encode().decode("unicode_escape").encode("latin-1"))
# after the last keys: how does the process end?
end = time.time() + 20
status = None
while time.time() < end:
    wait_for("\0never\0", 0.3)
    p, st = os.waitpid(pid, os.WNOHANG)
    if p:
        status = os.waitstatus_to_exitcode(st)
        break
if status is None:
    os.kill(pid, 9); print("the process did not end after the last keys"); sys.exit(4)
print("exit status %d" % status)
print(ansi.sub(b"", seen).decode("utf-8", "replace")[-400:])
PY
}

teardown() { pkill -9 -f "$WORK/bin" 2>/dev/null || true; rm -rf "$WORK"; }

sc() { "$STAIRCASE_BIN" "$@" 2>&1; }

@test "approve one change in the dialog, Ctrl-C at the next: the run stops, the approval is kept and recoverable" {
  run env APP="$WORK/app" BIN="$STAIRCASE_BIN" python3 "$WORK/drive.py" \
    "HITL Yield Request" "y" \
    "HITL Yield Request" '\x03'
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" == *"exit status"* ]]
  [[ "$output" != *"exit status 0"* ]] # the run did not succeed: it was stopped

  run sc inspect log 1
  [[ "$output" == *"status=KILLED"* ]]
  [ "$(wc -l <"$STAIRCASE_DIR/journal/run-1.approved.jsonl" | tr -d ' ')" = 1 ] # the change approved with y, not the second

  cd "$WORK/app"
  run sc recover 1
  [ "$status" -eq 0 ]
  [ "$(git show staircase/run-1:src/a.txt)" = "a content" ]
  run git show staircase/run-1:src/b.txt
  [ "$status" -ne 0 ] # the one interrupted at is not in it
}

@test "Ctrl-C at the first dialog: nothing was approved, nothing to recover" {
  run env APP="$WORK/app" BIN="$STAIRCASE_BIN" python3 "$WORK/drive.py" "HITL Yield Request" '\x03'
  echo "$output"
  [ "$status" -eq 0 ]
  [[ "$output" != *"exit status 0"* ]]
  run sc inspect log 1
  [[ "$output" == *"status=KILLED"* ]]
  [ ! -s "$STAIRCASE_DIR/journal/run-1.approved.jsonl" ]
  cd "$WORK/app"
  run sc recover 1
  [ "$status" -ne 0 ]
}
