#!/usr/bin/env bats
# tests/homebrew_formula.bats - packaging/homebrew-formula.sh writes a formula that Homebrew's own audit accepts: the version comes from the
# URL (an explicit `version` line is redundant and `brew audit --strict` rejects it), every platform has its archive and checksum, and a
# missing checksum is an error, never an empty one.

setup() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  WORK="$(mktemp -d)"
  for p in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
    printf '%s  staircase_1.0.0_%s.tar.gz\n' "$(printf '%s' "$p" | shasum -a 256 | cut -d' ' -f1)" "$p"
  done >"$WORK/checksums.txt"
  printf '%064d  staircase_1.0.0_windows_amd64.zip\n' 0 >>"$WORK/checksums.txt"
}
teardown() { rm -rf "$WORK"; }

@test "the formula takes its version from the URL: no explicit version line" {
  run bash "$ROOT_DIR/packaging/homebrew-formula.sh" 1.0.0 "$WORK/checksums.txt" "$WORK/staircase.rb"
  [ "$status" -eq 0 ]
  if grep -qE '^\s*version "' "$WORK/staircase.rb"; then false; fi
  grep -q '^  version_scheme 1' "$WORK/staircase.rb"        # still orders the Go releases after the Bash line's 1.x
}

@test "every platform has its archive URL and its own checksum, and nothing points outside the release" {
  bash "$ROOT_DIR/packaging/homebrew-formula.sh" 1.0.0 "$WORK/checksums.txt" "$WORK/staircase.rb"
  for p in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
    grep -q "url \"https://github.com/b070nd/stAirCase/releases/download/v1.0.0/staircase_1.0.0_${p}.tar.gz\"" "$WORK/staircase.rb"
    grep -A1 "staircase_1.0.0_${p}.tar.gz" "$WORK/staircase.rb" | grep -q "sha256 \"$(printf '%s' "$p" | shasum -a 256 | cut -d' ' -f1)\""
  done
  [ "$(grep -c 'url "' "$WORK/staircase.rb")" = 4 ]
  if grep 'url "' "$WORK/staircase.rb" | grep -v 'https://github.com/b070nd/stAirCase/releases/download/v1.0.0/' ; then false; fi
}

@test "a missing checksum is an error" {
  grep -v darwin_amd64 "$WORK/checksums.txt" >"$WORK/short.txt"
  run bash "$ROOT_DIR/packaging/homebrew-formula.sh" 1.0.0 "$WORK/short.txt" "$WORK/staircase.rb"
  [ "$status" -ne 0 ]
  [[ "$output" == *"no checksum for darwin_amd64"* ]]
}

@test "the formula is valid Ruby" {
  command -v ruby >/dev/null || skip "ruby is not installed"
  bash "$ROOT_DIR/packaging/homebrew-formula.sh" 1.0.0 "$WORK/checksums.txt" "$WORK/staircase.rb"
  run ruby -c "$WORK/staircase.rb"
  [ "$status" -eq 0 ]
}
