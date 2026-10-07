#!/usr/bin/env bats
# tests/release_notes.bats - packaging/release-notes.sh cuts exactly one release's section out of CHANGELOG.md.

setup() { ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"; }

@test "a release's notes hold its own section and stop at the next heading of any kind" {
  run sh "$ROOT_DIR/packaging/release-notes.sh" 0.2.0
  [ "$status" -eq 0 ]
  [[ "$output" != *"Before the Go rewrite"* ]]      # the heading that opens the Bash line is not part of 0.2.0
  [[ "$output" != *"earlier Bash implementation"* ]]
  [[ "$output" != *"Initial release"* ]]            # nor is anything of the Bash line's own sections
}

@test "1.0.0 is the Go release: the Bash line's 1.0.0 section does not leak into it" {
  run sh "$ROOT_DIR/packaging/release-notes.sh" 1.0.0
  [ "$status" -eq 0 ]
  [[ "$output" == *"compatibility promise"* ]]
  [[ "$output" != *"Initial release"* ]]
  [[ "$output" != *"Zero Python dependencies"* ]]
}

@test "the Bash line's sections are named so that they cannot be mistaken for a release" {
  run sh "$ROOT_DIR/packaging/release-notes.sh" "bash 1.0.0"
  [ "$status" -eq 0 ]
  [[ "$output" == *"Initial release"* ]]
  [ "$(grep -c '^## \[1.0.0\]' "$ROOT_DIR/CHANGELOG.md")" = 1 ]
}

@test "a version with no section fails" {
  run sh "$ROOT_DIR/packaging/release-notes.sh" 9.9.9
  [ "$status" -ne 0 ]
}
