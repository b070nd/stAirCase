#!/usr/bin/env bats
# tests/historical.bats - today's staircase against what older releases made.
#
# docs/spec/historical/<tag> holds a real governed run made by that release (docs/spec/historical/generate.sh):
# the repository with its run branch and git notes, the public key, the audit checkpoint and, for two releases, the
# workspace database. The six contracts promise that what verified before verifies now; this is that promise checked
# against the releases, not against fixtures made by today's code.

setup_file() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  STAIRCASE_BIN="$BATS_FILE_TMPDIR/staircase"
  export ROOT_DIR STAIRCASE_BIN
  (cd "$ROOT_DIR" && go build -o "$STAIRCASE_BIN" ./src/cmd/staircase)
}

setup() {
  H="$ROOT_DIR/docs/spec/historical"
  WORK="$(mktemp -d)"
  export HOME="$WORK/home" NO_COLOR=1
  mkdir -p "$HOME"
}
teardown() { rm -rf "$WORK"; }

TAGS="v0.3.0 v0.4.0 v0.5.0 v0.6.0 v0.7.1 v0.8.0 v0.10.0"

clone() { # clone <tag>: a fresh clone that has only the bundle: the run branch and the notes
  cd "$WORK" && rm -rf "$WORK/c"
  git clone -q "$H/$1/repo.bundle" "$WORK/c" 2>/dev/null
  cd "$WORK/c"
  git fetch -q origin '+refs/heads/staircase/*:refs/remotes/origin/staircase/*' '+refs/notes/*:refs/notes/*'
}

@test "the inventory is not empty: every release the contracts promise to read has its fixture" {
  [ "$(ls -d "$H"/v0.*/ | wc -l | tr -d ' ')" -ge 7 ]
  for t in $TAGS; do
    [ -s "$H/$t/repo.bundle" ] && [ -s "$H/$t/signing.pub" ] && [ -s "$H/$t/checkpoint.json" ]
  done
}

@test "a certificate made by each release verifies, with only the public key, in a fresh clone" {
  for t in $TAGS; do
    clone "$t"
    run env STAIRCASE_DIR="$WORK/none" "$STAIRCASE_BIN" verify origin/staircase/run-1 --key "$H/$t/signing.pub" --min-cal 2
    [ "$status" -eq 0 ] || { echo "$t: $output"; false; }
    [[ "$output" == *"valid change certificate"* ]] || { echo "$t: $output"; false; }
  done
}

@test "a commit of a release that kept a ledger rebuilds, tree for tree" {
  for t in v0.6.0 v0.7.1 v0.8.0 v0.10.0; do
    clone "$t"
    run env STAIRCASE_DIR="$WORK/none" "$STAIRCASE_BIN" verify origin/staircase/run-1 --key "$H/$t/signing.pub" --min-cal 2 --rebuild
    [ "$status" -eq 0 ] || { echo "$t: $output"; false; }
    [[ "$output" == *"is identical"* ]] || { echo "$t: $output"; false; }
  done
}

@test "an audit checkpoint of each release verifies, signature and chain" {
  for t in $TAGS; do
    mkdir -p "$WORK/ws-$t" && cp "$H/$t/signing.pub" "$WORK/ws-$t/.signing.pub"
    run env STAIRCASE_DIR="$WORK/ws-$t" "$STAIRCASE_BIN" audit verify "$H/$t/checkpoint.json"
    [ "$status" -eq 0 ] || { echo "$t: $output"; false; }
    [[ "$output" == *"OK"* ]]
  done
}

@test "a workspace of an old release opens, and its audit chain (version 1 and later) still verifies" {
  for t in v0.3.0 v0.5.0 v0.8.0; do
    rm -rf "$WORK/db"; mkdir "$WORK/db"; cp "$H/$t/workspace.db" "$WORK/db/"
    run env STAIRCASE_DIR="$WORK/db" "$STAIRCASE_BIN" inspect log 1
    [ "$status" -eq 0 ] || { echo "$t: $output"; false; }
    [[ "$output" == *"Hash chain intact"* ]] || { echo "$t: $output"; false; }
  done
}

@test "controls: a wrong key, a level not reached and a changed checkpoint are each refused" {
  t=v0.8.0
  # another key
  clone "$t"
  head -c 32 /dev/urandom >"$WORK/other.pub"
  run env STAIRCASE_DIR="$WORK/none" "$STAIRCASE_BIN" verify origin/staircase/run-1 --key "$WORK/other.pub" --min-cal 2
  [ "$status" -ne 0 ]
  # a level the commit does not reach
  run env STAIRCASE_DIR="$WORK/none" "$STAIRCASE_BIN" verify origin/staircase/run-1 --key "$H/$t/signing.pub" --min-cal 4
  [ "$status" -ne 0 ]
  # a checkpoint with one byte of a payload changed
  mkdir -p "$WORK/ws" && cp "$H/$t/signing.pub" "$WORK/ws/.signing.pub"
  sed 's/base_sha\\":\\"1/base_sha\\":\\"2/' "$H/$t/checkpoint.json" >"$WORK/changed.json"
  if cmp -s "$WORK/changed.json" "$H/$t/checkpoint.json"; then echo "the control changed nothing"; false; fi
  run env STAIRCASE_DIR="$WORK/ws" "$STAIRCASE_BIN" audit verify "$WORK/changed.json"
  [ "$status" -ne 0 ]
}
