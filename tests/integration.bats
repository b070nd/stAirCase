#!/usr/bin/env bats
# tests/integration.bats - current CLI smoke tests
#
# Run: bats tests/integration.bats

setup_file() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  STAIRCASE_BIN="$BATS_FILE_TMPDIR/staircase"
  export ROOT_DIR STAIRCASE_BIN

  (cd "$ROOT_DIR" && go build -o "$STAIRCASE_BIN" ./src/cmd/staircase)
}

setup() {
  WORK_DIR="$(mktemp -d)"
  export STAIRCASE_DIR="$WORK_DIR/workspace"
  export NO_COLOR=1
}

teardown() {
  rm -rf "$WORK_DIR"
}

sc() {
  "$STAIRCASE_BIN" "$@" 2>&1
}

@test "version: prints current development version" {
  run sc version
  [ "$status" -eq 0 ]
  [[ "$output" == stAirCase\ v* ]]
}

@test "help: lists current top-level commands" {
  run sc help
  [ "$status" -eq 0 ]
  [[ "$output" == *"audit"* ]]
  [[ "$output" == *"compile"* ]]
  [[ "$output" == *"secret"* ]]
  [[ "$output" == *"topology"* ]]
}

@test "unknown command: exits non-zero" {
  run sc nope
  [ "$status" -ne 0 ]
  [[ "$output" == *"unknown command"* ]]
}

@test "init --help: exposes offline wheel option" {
  run sc init --help
  [ "$status" -eq 0 ]
  [[ "$output" == *"--offline-wheels"* ]]
}

@test "gate --help: documents case execution" {
  run sc gate --help
  [ "$status" -eq 0 ]
  [[ "$output" == *"case"* ]]
}
