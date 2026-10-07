#!/usr/bin/env bats
# tests/ci_evidence_check.bats - demo/ci-evidence-check.sh reads the retained CI artifacts of a release candidate (the
# Linux evidence.json and the release gate's result) from files the owner downloaded, with no gh and no network, and
# refuses anything short of "this exact commit, built and tested on Linux, by the default branch's push run".

SHA=fc36ae357452b271b4dc3588aedb3b76e798e5eb

setup() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  WORK="$(mktemp -d)"; cd "$WORK"
  mkdir -p bin
  printf '#!/bin/sh\necho "gh was called" >&2; exit 99\n' >bin/gh && chmod +x bin/gh   # the check must not need it
  printf '#!/bin/sh\necho "the network was used" >&2; exit 99\n' >bin/curl && chmod +x bin/curl
  export PATH="$WORK/bin:$PATH"
  ev "$SHA" Linux true false passed 0 0
  gate "$SHA" success push master b070nd/stAirCase 1
}
teardown() { rm -rf "$WORK"; }

# ev <commit> <os> <tree_clean> <partial> <overall> <failed> <skipped>
ev() {
  jq -n --arg c "$1" --arg os "$2" --argjson clean "$3" --argjson partial "$4" --arg overall "$5" --argjson f "$6" --argjson s "$7" \
    '{schema: 1, commit: $c, tree_clean: $clean, platform: {os: $os, arch: "x86_64"}, partial: $partial, overall: $overall,
      steps: [{name: "go tests", status: "passed", reason: "", counts: {tests: 900, passed: 890, failed: 0, skipped: 10}},
              {name: "live agent runs (opt-in)", status: "not_run", reason: "need credentials", counts: null}],
      summary: {passed: 12, failed: $f, skipped: $s, not_run: 1}}' >evidence.json
}
# gate <sha> <conclusion> <event> <branch> <repository> <attempt>
gate() {
  jq -n --arg sha "$1" --arg c "$2" --arg e "$3" --arg b "$4" --arg r "$5" --argjson a "$6" \
    '{gate: "ci", passed: ($c == "success"), run: {status: "completed", conclusion: $c, id: 37468108219, url: "https://github.com/b070nd/stAirCase/actions/runs/37468108219",
      attempt: $a, event: $e, branch: $b, repository: $r, sha: $sha}}' >release-gate.json
}
check() { run bash "$ROOT_DIR/demo/ci-evidence-check.sh" "$SHA" evidence.json release-gate.json "$@"; }

@test "the exact commit, built on Linux, passed, by the default branch's push run: accepted, and the record names the run" {
  check
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [[ "$output" == *"37468108219"* ]] && [[ "$output" == *"attempt 1"* ]]
  [[ "$output" == *"https://github.com/b070nd/stAirCase/actions/runs/37468108219"* ]]
  [[ "$output" == *"$SHA"* ]]
  [[ "$output" == *"live agent runs (opt-in): not_run"* ]]        # what was not run is stated, not hidden
  [[ "$output" != *"gh was called"* ]] && [[ "$output" != *"the network was used"* ]]
}

@test "the artifacts as GitHub's download gives them (zip files) are read" {
  command -v zip >/dev/null
  mkdir z && cp evidence.json z/ && (cd z && zip -q ../evidence.zip evidence.json && zip -q ../extra.zip evidence.json)
  mv release-gate.json z/ && (cd z && zip -q ../gate.zip release-gate.json)
  run bash "$ROOT_DIR/demo/ci-evidence-check.sh" "$SHA" evidence.zip gate.zip
  [ "$status" -eq 0 ] || { echo "$output"; false; }
}

@test "refused: evidence of another commit" {
  ev aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa Linux true false passed 0 0
  check; [ "$status" -ne 0 ]; [[ "$output" == *"another commit"* ]]
}
@test "refused: evidence that is partial" {
  ev "$SHA" Linux true true partial 0 0
  check; [ "$status" -ne 0 ]; [[ "$output" == *"partial"* ]]
  ev "$SHA" Linux true true passed 0 0                               # a record that contradicts itself is not accepted either
  check; [ "$status" -ne 0 ]; [[ "$output" == *"partial"* ]]
}
@test "refused: evidence that did not pass, or has a failure or a skip" {
  ev "$SHA" Linux true false "not passed" 0 0; check; [ "$status" -ne 0 ]
  ev "$SHA" Linux true false passed 1 0; check; [ "$status" -ne 0 ]; [[ "$output" == *"failed"* ]]
  ev "$SHA" Linux true false passed 0 2; check; [ "$status" -ne 0 ]; [[ "$output" == *"skipped"* ]]
}
@test "refused: evidence from another platform, or from a dirty tree" {
  ev "$SHA" Darwin true false passed 0 0; check; [ "$status" -ne 0 ]; [[ "$output" == *"Linux"* ]]
  ev "$SHA" Linux false false passed 0 0; check; [ "$status" -ne 0 ]; [[ "$output" == *"clean"* ]]
}
@test "refused: a gate run that did not succeed, was not a push, was not the default branch, or is of another repository or commit" {
  gate "$SHA" failure push master b070nd/stAirCase 1; check; [ "$status" -ne 0 ]
  gate "$SHA" success pull_request master b070nd/stAirCase 1; check; [ "$status" -ne 0 ]; [[ "$output" == *"push"* ]]
  gate "$SHA" success push feature/x b070nd/stAirCase 1; check; [ "$status" -ne 0 ]; [[ "$output" == *"master"* ]]
  gate "$SHA" success push master someone/else 1; check; [ "$status" -ne 0 ]; [[ "$output" == *"repository"* ]]
  gate aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa success push master b070nd/stAirCase 1; check; [ "$status" -ne 0 ]; [[ "$output" == *"another commit"* ]]
}
@test "refused: the arguments are not a commit and two files" {
  run bash "$ROOT_DIR/demo/ci-evidence-check.sh" main evidence.json release-gate.json
  [ "$status" -eq 2 ]
  run bash "$ROOT_DIR/demo/ci-evidence-check.sh" "$SHA" evidence.json
  [ "$status" -eq 2 ]
  run bash "$ROOT_DIR/demo/ci-evidence-check.sh" "$SHA" missing.json release-gate.json
  [ "$status" -ne 0 ]
}
