#!/usr/bin/env bash
# generate.sh <tag> <outdir> - make the historical fixture of a release: build that tag's staircase in a
# temporary worktree, run one governed session with a stand-in Claude Code (two approved file writes), and keep
#   repo.bundle      the repository with the run branch and its git notes (certificate, ledger when the release had one)
#   signing.pub      the workspace's public signing key
#   checkpoint.json  `staircase audit export 1`
#   workspace.db     the workspace database (only when asked: KEEP_DB=1)
# tests/historical.bats checks that today's staircase still verifies, rebuilds and reads each of them. The fixtures in
# this directory were made with this script on 2026-10-06; they are kept, not regenerated, so the check stays about
# the old releases and not about whatever this script would produce today.
set -euo pipefail
TAG="${1:?usage: generate.sh <tag> <outdir>}"; OUT="${2:?}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
W="$(mktemp -d)"; trap 'git -C "$ROOT" worktree remove --force "$W/src" 2>/dev/null; rm -rf "$W"' EXIT
git -C "$ROOT" worktree add -q --detach "$W/src" "$TAG"
(cd "$W/src" && GOFLAGS=-mod=mod go build -o "$W/staircase" ./src/cmd/staircase)
BIN="$W/staircase"
export STAIRCASE_DIR="$W/ws" NO_COLOR=1 HOME="$W/home"
mkdir -p "$W/bin" "$W/app" "$HOME" "$OUT"
OUT="$(cd "$OUT" && pwd)" # absolute: the steps below run in other directories
git -C "$W/app" init -q -b main
git -C "$W/app" config user.email fixture@example.com; git -C "$W/app" config user.name fixture
printf '# app\n' >"$W/app/README.md"; git -C "$W/app" add -A
GIT_AUTHOR_DATE="2026-01-01T00:00:00Z" GIT_COMMITTER_DATE="2026-01-01T00:00:00Z" git -C "$W/app" commit -q -m init
"$BIN" init --skip-venv >/dev/null 2>&1 || "$BIN" init >/dev/null 2>&1
echo '{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}' >"$STAIRCASE_DIR/policy.json"
cat >"$W/bin/claude" <<'FAKE'
#!/bin/bash
settings=""
while [ $# -gt 0 ]; do [ "$1" = "--settings" ] && settings="$2"; shift; done
cmd="$(sed -n 's/.*"command": *"\([^"]*\)".*/\1/p' "$settings" | head -1)"
hook() { printf '%s' "$1" | sh -c "$cmd" >/dev/null; }
hook '{"hook_event_name":"SessionStart","cwd":"'"$PWD"'"}'
for f in a b; do
  call='{"tool_name":"Write","tool_input":{"file_path":"'"$PWD"'/src/'$f'.txt","content":"'$f' content\n"},"tool_use_id":"t-'$f'","cwd":"'"$PWD"'"}'
  hook "{\"hook_event_name\":\"PreToolUse\",${call:1}"
  mkdir -p src && printf '%s content\n' $f >src/$f.txt
  hook "{\"hook_event_name\":\"PostToolUse\",${call:1}"
done
printf '{"is_error":false,"result":"done"}'
FAKE
chmod +x "$W/bin/claude"; export PATH="$W/bin:$PATH"
yes_flag=(--yes); "$BIN" claude --help 2>&1 | grep -q -- '--yes' || yes_flag=()   # v0.3.0 asks on stdin instead
(cd "$W/app" && printf 'y\n' | "$BIN" claude ${yes_flag[@]+"${yes_flag[@]}"} "write two files") >"$W/session.log" 2>&1 || { tail -20 "$W/session.log"; exit 1; }
git -C "$W/app" bundle create "$OUT/repo.bundle" --all >/dev/null 2>&1
cp "$STAIRCASE_DIR/.signing.pub" "$OUT/signing.pub"
"$BIN" audit export 1 >/dev/null && cp "$STAIRCASE_DIR/audit/run-1.checkpoint.json" "$OUT/checkpoint.json"
[ -z "${KEEP_DB:-}" ] || cp "$STAIRCASE_DIR/workspace.db" "$OUT/workspace.db"
echo "fixture of $TAG in $OUT"
