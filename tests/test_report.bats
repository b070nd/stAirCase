#!/usr/bin/env bats
# tests/test_report.bats - packaging/test-report.sh turns a failing test run into a
# workflow annotation, which the public check-runs API serves without a sign-in.

setup() { ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"; }

@test "a passing command is passed through with no annotation" {
  run "$ROOT_DIR/packaging/test-report.sh" sh -c 'echo "ok  pkg/a"'
  [ "$status" -eq 0 ]
  [[ "$output" == *"ok  pkg/a"* ]]
  [[ "$output" != *"::error"* ]]
}

@test "a failing command keeps its exit status and names what failed" {
  run "$ROOT_DIR/packaging/test-report.sh" sh -c '
    echo "=== RUN   TestSomething"
    echo "--- FAIL: TestSomething (0.01s)"
    echo "    thing_test.go:42: expected 100% got 3"
    echo "FAIL	pkg/a	1.2s"
    exit 7'
  [ "$status" -eq 7 ]
  [[ "$output" == *"::error title=Tests failed::"* ]]
  [[ "$output" == *"--- FAIL: TestSomething"* ]]
  [[ "$output" == *"thing_test.go:42"* ]]
  [[ "$output" == *"100%25"* ]]       # % is escaped for the annotation
  [[ "$output" == *"%0A"* ]]          # lines are joined with an escaped newline
}

@test "a failure with no test line says so rather than staying silent" {
  run "$ROOT_DIR/packaging/test-report.sh" sh -c 'echo "go: downloading something"; exit 3'
  [ "$status" -eq 3 ]
  [[ "$output" == *"::error title=Tests failed::"* ]]
  [[ "$output" == *"exit status 3"* ]]
}
