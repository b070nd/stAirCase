#!/usr/bin/env bats
# tests/verify_action_release.bats - the packaged verifier, through the Action's script
#
# The repository is the one the offline demo makes: a run's certified commit and its git
# notes. The verifier is the real binary, packed as a release archive and handed over by
# a stand-in `gh` (the acquisition path is tested in verify_action_acquire.bats). The
# trusted key comes from the base branch, as in a pull request.

setup_file() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  OUT="$(cd "$ROOT_DIR" && DEMO_KEEP=1 ./demo/run-demo.sh --auto 2>&1)"
  KEPT="$(printf '%s\n' "$OUT" | sed -n 's/^DEMO_KEPT=//p' | tail -1)"
  [ -n "$KEPT" ] && [ -d "$KEPT/app" ] || { echo "the demo kept nothing: $OUT" >&2; return 1; }
  echo "$KEPT" >"$BATS_FILE_TMPDIR/kept"
  mkdir "$BATS_FILE_TMPDIR/pack"
  cp "$KEPT/staircase" "$BATS_FILE_TMPDIR/pack/staircase"
  tar -czf "$BATS_FILE_TMPDIR/real.tar.gz" -C "$BATS_FILE_TMPDIR/pack" staircase
}

teardown_file() { rm -rf "$(cat "$BATS_FILE_TMPDIR/kept" 2>/dev/null)"; }

setup() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  KEPT="$(cat "$BATS_FILE_TMPDIR/kept")"
  WORK="$(mktemp -d)"
  mkdir "$WORK/bin"
  cat >"$WORK/bin/gh" <<'FAKE'
#!/bin/sh
case "$1 $2" in
  "release download")
    while [ $# -gt 0 ]; do case "$1" in -p) archive="$2"; shift ;; -D) dir="$2"; shift ;; esac; shift; done
    cp "$REAL_ARCHIVE" "$dir/$archive" ;;
  "attestation verify") exit 0 ;;
  *) exit 99 ;;
esac
FAKE
  chmod +x "$WORK/bin/gh"
  export PATH="$WORK/bin:$PATH" REAL_ARCHIVE="$BATS_FILE_TMPDIR/real.tar.gz"

  # what the CI machine sees: the origin holds the run branch and its notes, and the
  # base branch holds the trusted key (the demo workspace's public signing key)
  git clone -q --mirror "$KEPT/app" "$WORK/origin.git"
  git clone -q "$WORK/origin.git" "$WORK/ci" 2>/dev/null
  cd "$WORK/ci"
  git config user.email ci@example.com
  git config user.name ci
  git checkout -q main
  cp "$KEPT/.staircase/.signing.pub" trusted.pub
  git add trusted.pub && git commit -q -m "the trusted key"
  git push -q origin main
  git checkout -q -b pr origin/staircase/run-1
  export KEY=trusted.pub BASE_REF=main HEAD_SHA="$(git rev-parse HEAD)" RUNNER_OS=Linux RUNNER_ARCH=X64 GH_TOKEN=x VERSION=v0.0.0 MIN_CAL=2
  unset STAIRCASE_BIN ACTION_REF TRUST_REF RANGE ALL ALLOWED_SIGNERS REBUILD REQUIRE_INITIATOR
}

teardown() { rm -rf "$WORK"; }

action() { run bash "$ROOT_DIR/packaging/verify-action.sh"; }

@test "a certified range passes" {
  action
  [ "$status" -eq 0 ]
  [[ "$output" == *"valid change certificate"* ]]
  [[ "$output" == *"commit(s) checked"* ]]
}

@test "a certified range passes with the commit rebuilt from its ledger" {
  REBUILD=true action
  [ "$status" -eq 0 ]
  [[ "$output" == *"rebuilt from"* ]]
}

@test "a range whose certificates are missing fails" {
  git -C "$WORK/origin.git" update-ref -d refs/notes/staircase
  action
  [ "$status" -ne 0 ]
  [[ "$output" == *"no change certificate"* ]]
}

@test "a certificate signed by another key fails" {
  git checkout -q main
  head -c 32 /dev/urandom >trusted.pub          # the base branch now trusts a different key
  git add trusted.pub && git commit -q -m "another key" && git push -q origin main
  git checkout -q pr
  action
  [ "$status" -ne 0 ]
  [[ "$output" == *"no valid signature by the trusted key"* ]]
}

@test "a minimum level the commit does not reach fails" {
  MIN_CAL=4 action
  [ "$status" -ne 0 ]
  [[ "$output" == *"below the required 4"* ]]
}
