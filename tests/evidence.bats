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
  [ "$(field '.fixtures | length')" = "$(ls "$ROOT_DIR"/docs/spec/vectors/*.json "$ROOT_DIR"/docs/spec/rebuild-vectors/*.json "$ROOT_DIR"/docs/spec/audit-chain-vectors.json | wc -l | tr -d ' ')" ]
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
