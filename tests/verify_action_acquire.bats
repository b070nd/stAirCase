#!/usr/bin/env bats
# tests/verify_action_acquire.bats - how packaging/verify-action.sh gets the verifier
#
# `gh` is a stand-in that logs what it is asked and serves a fixture archive, so the
# acquisition path (platform, version, download, attestation, unpacking) and the
# trust rules around it run offline. A binary that was not verified must never run:
# the fixture archive holds a program that leaves a marker file when it does.

setup() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  WORK="$(mktemp -d)"
  mkdir "$WORK/bin" "$WORK/fixture"
  export PATH="$WORK/bin:$PATH"
  export GH_LOG="$WORK/gh.log" RAN="$WORK/ran"
  : >"$GH_LOG"

  # the program the archive holds
  cat >"$WORK/fixture/staircase" <<FAKE
#!/bin/sh
echo ran >"$RAN"
echo "staircase-from-the-archive \$*"
FAKE
  chmod +x "$WORK/fixture/staircase"
  tar -czf "$WORK/good.tar.gz" -C "$WORK/fixture" staircase
  : >"$WORK/fixture/other"
  tar -czf "$WORK/no-member.tar.gz" -C "$WORK/fixture" other

  cat >"$WORK/bin/gh" <<'FAKE'
#!/bin/sh
echo "gh $*" >>"$GH_LOG"
case "$1 $2" in
  "release view") [ -z "$FAKE_VIEW_FAIL" ] || exit 1; echo "${FAKE_LATEST:-v9.9.9}" ;;
  "release download")
    [ -z "$FAKE_DOWNLOAD_FAIL" ] || { echo "download failed" >&2; exit 1; }
    while [ $# -gt 0 ]; do case "$1" in -p) archive="$2"; shift ;; -D) dir="$2"; shift ;; esac; shift; done
    cp "${FAKE_ARCHIVE:-$GOOD}" "$dir/$archive" ;;
  "attestation verify") exit "${FAKE_ATTEST_RC:-0}" ;;
  *) echo "unexpected gh call: $*" >&2; exit 99 ;;
esac
FAKE
  chmod +x "$WORK/bin/gh"
  export GOOD="$WORK/good.tar.gz"

  git init -q --bare "$WORK/origin.git"
  git clone -q "$WORK/origin.git" "$WORK/repo" 2>/dev/null
  cd "$WORK/repo"
  git config user.email t@example.com
  git config user.name t
  git checkout -q -b main
  echo "GOOD-KEY" >staircase.pub
  git add -A && git commit -q -m base
  git push -q origin main
  git checkout -q -b pr
  git commit -q --allow-empty -m "agent change"
  export KEY=staircase.pub BASE_REF=main HEAD_SHA="$(git rev-parse HEAD)" RUNNER_OS=Linux RUNNER_ARCH=X64 GH_TOKEN=x
  unset STAIRCASE_BIN VERSION ACTION_REF TRUST_REF RANGE ALL ALLOWED_SIGNERS REBUILD REQUIRE_INITIATOR
}

teardown() { rm -rf "$WORK"; }

action() { run bash "$ROOT_DIR/packaging/verify-action.sh"; }
asked() { grep -c -- "$1" "$GH_LOG" || true; }

@test "each supported runner gets its own archive" {
  for pair in "Linux X64 linux_amd64" "Linux ARM64 linux_arm64" "macOS X64 darwin_amd64" "macOS ARM64 darwin_arm64"; do
    set -- $pair
    : >"$GH_LOG"
    RUNNER_OS=$1 RUNNER_ARCH=$2 VERSION=v1.2.3 action
    [ "$status" -eq 0 ]
    grep -q -- "-p staircase_1.2.3_$3.tar.gz" "$GH_LOG"
  done
}

@test "an unsupported runner is refused before anything is downloaded" {
  RUNNER_OS=Windows RUNNER_ARCH=X64 VERSION=v1.2.3 action
  [ "$status" -ne 0 ]
  [[ "$output" == *"unsupported runner"* ]]
  [ ! -s "$GH_LOG" ]
}

@test "the attestation is checked against the release workflow, source and tag" {
  VERSION=v1.2.3 action
  [ "$status" -eq 0 ]
  line="$(grep "attestation verify" "$GH_LOG")"
  [[ "$line" == *"-R b070nd/stAirCase"* ]]
  [[ "$line" == *"--signer-workflow b070nd/stAirCase/.github/workflows/release.yml"* ]]
  [[ "$line" == *"--source-ref refs/tags/v1.2.3"* ]]
}

@test "a failed download stops before anything runs" {
  FAKE_DOWNLOAD_FAIL=1 VERSION=v1.2.3 action
  [ "$status" -ne 0 ]
  [ "$(asked 'attestation verify')" -eq 0 ]
  [ ! -e "$RAN" ]
}

@test "a rejected attestation stops before the binary runs" {
  FAKE_ATTEST_RC=1 VERSION=v1.2.3 action
  [ "$status" -ne 0 ]
  [ ! -e "$RAN" ]
}

@test "an archive without the program stops before anything runs" {
  FAKE_ARCHIVE="$WORK/no-member.tar.gz" VERSION=v1.2.3 action
  [ "$status" -ne 0 ]
  [ ! -e "$RAN" ]
}

@test "control: a verified archive runs the verifier it holds" {
  VERSION=v1.2.3 action
  [ "$status" -eq 0 ]
  [[ "$output" == *"staircase-from-the-archive"* ]]
  [ -e "$RAN" ]
}

@test "an explicit version that is not a release tag is rejected, not turned into the latest" {
  for bad in v1.2 1.2.3 latest main "v1.2.3; echo pwned" "v1.2.3 " v1 ""; do
    : >"$GH_LOG"
    if [ -n "$bad" ]; then
      VERSION="$bad" ACTION_REF=v9.9.9 action
      [ "$status" -ne 0 ]
      [[ "$output" == *"release tag"* ]]
      [ ! -s "$GH_LOG" ]
    fi
  done
}

@test "without a version the action's own release tag is used" {
  ACTION_REF=v1.2.3 action
  [ "$status" -eq 0 ]
  grep -q -- "-p staircase_1.2.3_linux_amd64.tar.gz" "$GH_LOG"
  [ "$(asked 'release view')" -eq 0 ]
}

@test "an explicit version beats the action's own tag" {
  VERSION=v2.0.0 ACTION_REF=v1.2.3 action
  [ "$status" -eq 0 ]
  grep -q -- "-p staircase_2.0.0_linux_amd64.tar.gz" "$GH_LOG"
}

@test "a floating ref installs the latest release, and says so" {
  ACTION_REF=main FAKE_LATEST=v3.4.5 action
  [ "$status" -eq 0 ]
  grep -q -- "-p staircase_3.4.5_linux_amd64.tar.gz" "$GH_LOG"
  [[ "$output" == *"::warning::"*"main is not a release tag"* ]]
}

@test "a commit SHA with no version input selects the latest release, with a warning" {
  ACTION_REF=0123456789abcdef0123456789abcdef01234567 FAKE_LATEST=v3.4.5 action
  [ "$status" -eq 0 ]
  grep -q -- "-p staircase_3.4.5_linux_amd64.tar.gz" "$GH_LOG"
  [[ "$output" == *"::warning::"*"is not a release tag"* ]]
}

@test "a commit SHA with a version input pins the verifier to that version, without a warning" {
  ACTION_REF=0123456789abcdef0123456789abcdef01234567 VERSION=v1.2.3 FAKE_LATEST=v9.9.9 action
  [ "$status" -eq 0 ]
  grep -q -- "-p staircase_1.2.3_linux_amd64.tar.gz" "$GH_LOG"
  [[ "$output" != *"::warning::"*"not a release tag"* ]]
}

@test "a latest release that is not a release tag is refused" {
  ACTION_REF=main FAKE_LATEST=nightly action
  [ "$status" -ne 0 ]
  [ ! -e "$RAN" ]
}

@test "outside a pull request the trusted ref must be named" {
  BASE_REF= RANGE=main..pr VERSION=v1.2.3 action
  [ "$status" -ne 0 ]
  [[ "$output" == *"trust-ref"* ]]
  [ ! -e "$RAN" ]
  git -C "$WORK/repo" tag trusted main
  BASE_REF= RANGE=main..pr TRUST_REF=trusted VERSION=v1.2.3 action
  [ "$status" -eq 0 ]
  [[ "$output" == *"staircase-from-the-archive"* ]]
}

@test "a custom range still trusts the pull request's base, fetched on its own" {
  git -C "$WORK/repo" update-ref -d refs/remotes/origin/main   # not there locally: it must be fetched
  RANGE="HEAD~1..HEAD" VERSION=v1.2.3 action
  [ "$status" -eq 0 ]
  [[ "$output" == *"HEAD~1..HEAD"* ]]
}
