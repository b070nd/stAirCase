#!/usr/bin/env bats
# tests/verify_action.bats - packaging/verify-action.sh, the stAirCase verify Action
#
# The script normally installs a release; STAIRCASE_BIN replaces that with a
# stand-in that prints what it is asked to check, so the trust rules can be
# tested without a network: the key and the allowed signers come from the base
# branch, not from the pull request, and every commit is checked by default.

setup() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  WORK="$(mktemp -d)"
  cat >"$WORK/fake-staircase" <<'FAKE'
#!/bin/sh
# prints the arguments, and the content of the files it was told to trust
echo "args: $*"
while [ $# -gt 0 ]; do
  case "$1" in
    --key) echo "key=$(cat "$2")"; shift ;;
    --allowed-signers) echo "signers=$(cat "$2")"; shift ;;
  esac
  shift
done
FAKE
  chmod +x "$WORK/fake-staircase"

  git init -q --bare "$WORK/origin.git"
  git clone -q "$WORK/origin.git" "$WORK/repo" 2>/dev/null
  cd "$WORK/repo"
  git config user.email t@example.com
  git config user.name t
  git checkout -q -b main
  mkdir -p .github
  echo "GOOD-KEY" >.github/staircase.pub
  echo "alice GOOD-SIGNER" >.github/allowed_signers
  git add -A && git commit -q -m base
  git push -q origin main
  # the pull request replaces the key and the signers with its own
  git checkout -q -b pr
  echo "EVIL-KEY" >.github/staircase.pub
  echo "mallory EVIL-SIGNER" >.github/allowed_signers
  git add -A && git commit -q -m "agent change"
  export STAIRCASE_BIN="$WORK/fake-staircase" KEY=.github/staircase.pub BASE_REF=main HEAD_SHA="$(git rev-parse HEAD)"
  unset ALL RANGE REBUILD ALLOWED_SIGNERS TRUST_REF
}

teardown() {
  rm -rf "$WORK"
}

action() {
  run bash "$ROOT_DIR/packaging/verify-action.sh"
}

@test "the key is the base branch's, not the pull request's" {
  action
  [ "$status" -eq 0 ]
  [[ "$output" == *"key=GOOD-KEY"* ]]
  [[ "$output" != *"EVIL"* ]]
}

@test "the allowed signers come from the base branch too" {
  ALLOWED_SIGNERS=.github/allowed_signers action
  [ "$status" -eq 0 ]
  [[ "$output" == *"signers=alice GOOD-SIGNER"* ]]
  [[ "$output" != *"EVIL"* ]]
}

@test "every commit is checked unless all is false" {
  action
  [[ "$output" == *"--all"* ]]
  ALL=false action
  [[ "$output" != *"--all"* ]]
}

@test "rebuild is opt in" {
  action
  [[ "$output" != *"--rebuild"* ]]
  REBUILD=true action
  [[ "$output" == *"--rebuild"* ]]
}

@test "require-initiator is opt in" {
  action
  [[ "$output" != *"--require-initiator"* ]]
  REQUIRE_INITIATOR=true action
  [[ "$output" == *"--require-initiator"* ]]
}

@test "a key that is only in the pull request is refused" {
  git -C "$WORK/repo" checkout -q pr
  echo "NEW-KEY" >"$WORK/repo/.github/new.pub"
  git -C "$WORK/repo" add -A && git -C "$WORK/repo" commit -q -m "add a key"
  export HEAD_SHA="$(git -C "$WORK/repo" rev-parse HEAD)"
  KEY=.github/new.pub action
  [ "$status" -ne 0 ]
  [[ "$output" == *"is not on origin/main"* ]]
}

@test "trust-ref names another trusted ref" {
  git -C "$WORK/repo" tag trusted-v1 main
  TRUST_REF=trusted-v1 BASE_REF= RANGE="main..pr" action
  [ "$status" -eq 0 ]
  [[ "$output" == *"key=GOOD-KEY"* ]]
}
