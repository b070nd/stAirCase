#!/usr/bin/env bats
# tests/evidence.bats - packaging/evidence.sh records what ran, honestly.
# The expensive steps are stood in for by stub programs; the recording is real.

setup() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  WORK="$(mktemp -d)"
  mkdir "$WORK/bin"
  export PATH="$WORK/bin:$PATH"
}
teardown() { rm -rf "$WORK"; }

stub() { printf '#!/bin/sh\n%s\n' "$2" >"$WORK/bin/$1"; chmod +x "$WORK/bin/$1"; }
evidence() { EVIDENCE_STEPS="$1" run "$ROOT_DIR/packaging/evidence.sh" "$WORK/out"; }
field() { jq -r "$1" "$WORK/out/evidence.json"; }

@test "the record names the commit, platform, tools and every conformance fixture" {
  stub govulncheck 'echo "no vulnerabilities"'
  evidence vuln
  [ "$(field .commit)" = "$(git -C "$ROOT_DIR" rev-parse HEAD)" ]
  [ "$(field .platform.os)" = "$(uname -s)" ]
  [ "$(field .tools.go | cut -c1-10)" = "go version" ]
  [ "$(field '.fixtures | length')" = "$(ls "$ROOT_DIR"/docs/spec/vectors/*.json "$ROOT_DIR"/docs/spec/rebuild-vectors/*.json "$ROOT_DIR"/docs/spec/audit-chain-vectors.json "$ROOT_DIR"/docs/spec/historical/*/repo.bundle "$ROOT_DIR"/docs/spec/historical/*/checkpoint.json | wc -l | tr -d ' ')" ]
  [ "$(field '.fixtures | map(select(.sha256 | test("^[0-9a-f]{64}$"))) | length')" = "$(field '.fixtures | length')" ]
}

@test "a step that fails is recorded as failed and the run does not pass" {
  stub govulncheck 'echo "found one"; exit 3'
  evidence vuln
  [ "$status" -ne 0 ]
  [ "$(field '.steps[] | select(.name == "vulnerability scan") | .status')" = failed ]
  [ "$(field .overall)" != passed ]
}

@test "a check that cannot run is a failure, never a pass" {
  # a PATH with the system tools but no govulncheck
  mkdir "$WORK/lean"
  for t in jq shasum git go uname sh bash head cut tr grep cat date mkdir rm basename dirname; do ln -s "$(command -v $t)" "$WORK/lean/$t"; done
  PATH="$WORK/lean" EVIDENCE_STEPS=vuln run "$(command -v bash)" "$ROOT_DIR/packaging/evidence.sh" "$WORK/out"
  [ "$status" -ne 0 ]
  [ "$(field '.steps[] | select(.name == "vulnerability scan") | .status')" = failed ]
  [[ "$(field '.steps[] | select(.name == "vulnerability scan") | .reason')" == *"not installed"* ]]
}

@test "a run limited to some steps says partial, and the live-agent runs are not counted as passed" {
  stub govulncheck 'exit 0'
  evidence vuln
  [ "$status" -ne 0 ]
  [ "$(field .overall)" = partial ]
  [ "$(field '.steps[] | select(.name | startswith("live agent")) | .status')" = not_run ]
  [ "$(field .summary.not_run)" = 1 ]
}

@test "the record holds no path of this machine" {
  stub govulncheck 'exit 0'
  evidence vuln
  ! grep -q "$WORK" "$WORK/out/evidence.json"
  ! grep -q "$HOME" "$WORK/out/evidence.json"
}

# a bats run, as the evidence script reads it: the TAP the stand-in prints decides
fake_bats() { stub bats "printf '%s\n' $1; exit ${2:-0}"; }

@test "a required test that was skipped is recorded as skipped and the run is not passed" {
  fake_bats "'1..2' 'ok 1 one' 'ok 2 two # skip needs a login'"
  evidence drills
  [ "$status" -ne 0 ]
  [ "$(field '.steps[] | select(.name | startswith("kill drills")) | .status')" = skipped ]
  [ "$(field '.steps[] | select(.name | startswith("kill drills")) | .counts | [.tests, .passed, .skipped] | join(",")')" = "2,1,1" ]
  [ "$(field .overall)" != passed ]
}

@test "a run that executed no tests is a failure, not an empty pass" {
  fake_bats "'1..0'"
  evidence drills
  [ "$status" -ne 0 ]
  [ "$(field '.steps[] | select(.name | startswith("kill drills")) | .status')" = failed ]
  [[ "$(field '.steps[] | select(.name | startswith("kill drills")) | .reason')" == *"no tests ran"* ]]
}

@test "a failing test fails the step and the record is still written, with the commit" {
  fake_bats "'1..2' 'ok 1 one' 'not ok 2 two'" 1
  evidence drills
  [ "$status" -ne 0 ]
  [ "$(field '.steps[] | select(.name | startswith("kill drills")) | .status')" = failed ]
  [ "$(field .commit)" = "$(git -C "$ROOT_DIR" rev-parse HEAD)" ]
  [ "$(field .overall)" != passed ]
}

@test "a complete run with nothing failed or skipped says passed, with the live-agent runs not run" {
  # every step stood in for: green stubs, so the one thing left to hold the result back is the live-agent runs
  fake_bats "'1..3' 'ok 1 a' 'ok 2 b' 'ok 3 c'"
  stub govulncheck 'exit 0'
  stub make 'exit 0'
  stub go 'case "$1" in version) echo "go version stub";; test) echo "{\"Action\":\"pass\",\"Test\":\"TestX\"}";; *) exit 0;; esac'
  EVIDENCE_STEPS="" run "$ROOT_DIR/packaging/evidence.sh" "$WORK/out"
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(field .overall)" = passed ]
  [ "$(field .partial)" = false ]
  [ "$(field .summary.failed)" = 0 ]
  [ "$(field .summary.skipped)" = 0 ]
  [ "$(field '.steps[] | select(.name | startswith("live agent")) | .status')" = not_run ]
}

# go test -json lines for a stand-in `go`: a sandbox engine's subtest that skipped, and an ordinary one
sandbox_skips() {
  stub make 'exit 0'
  stub go 'case "$1" in version) echo "go version stub";; test)
    echo "{\"Action\":\"pass\",\"Test\":\"TestA\"}"
    echo "{\"Action\":\"skip\",\"Test\":\"TestShellSandbox/bwrap\"}"
    '"$1"'
    ;; *) exit 0;; esac'
}

@test "a sandbox engine this machine does not have, and nobody requires, is an optional skip: listed, counted, not hidden" {
  sandbox_skips 'true'
  evidence go-tests
  [ "$(field '.steps[] | select(.name == "go tests") | .status')" = passed ]
  [ "$(field '.steps[] | select(.name == "go tests") | .counts.optional_skipped')" = 1 ]
  [[ "$(field '.steps[] | select(.name == "go tests") | .reason')" == *"TestShellSandbox/bwrap"* ]]
}

@test "a skipped engine that is required is a skip that counts against the run" {
  sandbox_skips 'true'
  STAIRCASE_REQUIRE_SANDBOX=bwrap evidence go-tests
  [ "$(field '.steps[] | select(.name == "go tests") | .status')" = skipped ]
  [ "$(field '.steps[] | select(.name == "go tests") | .counts.skipped')" = 1 ]
}

@test "an ordinary skipped test counts against the run" {
  sandbox_skips 'echo "{\"Action\":\"skip\",\"Test\":\"TestNeedsSomething\"}"'
  evidence go-tests
  [ "$(field '.steps[] | select(.name == "go tests") | .status')" = skipped ]
  [[ "$(field '.steps[] | select(.name == "go tests") | .reason')" == *"TestNeedsSomething"* ]]
}

@test "a skip that says it is a documented limit is optional, listed, and an unmarked skip of the same test is not" {
  stub make 'exit 0'
  stub go 'case "$1" in version) echo "go version stub";; test)
    echo "{\"Action\":\"pass\",\"Test\":\"TestA\"}"
    printf "%s\n" "{\"Action\":\"output\",\"Test\":\"TestKernel\",\"Output\":\"x_test.go:9: documented limit: needs a newer kernel\"}"
    echo "{\"Action\":\"skip\",\"Test\":\"TestKernel\"}"
    ;; *) exit 0;; esac'
  evidence go-tests
  [ "$(field '.steps[] | select(.name == "go tests") | .status')" = passed ]
  [[ "$(field '.steps[] | select(.name == "go tests") | .reason')" == *"TestKernel"* ]]
  stub go 'case "$1" in version) echo "go version stub";; test)
    printf "%s\n" "{\"Action\":\"output\",\"Test\":\"TestKernel\",\"Output\":\"x_test.go:9: needs a newer kernel\"}"
    echo "{\"Action\":\"skip\",\"Test\":\"TestKernel\"}"
    ;; *) exit 0;; esac'
  evidence go-tests
  [ "$(field '.steps[] | select(.name == "go tests") | .status')" != passed ]
}
