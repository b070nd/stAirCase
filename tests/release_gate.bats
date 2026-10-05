#!/usr/bin/env bats
# tests/release_gate.bats - packaging/release-gate.sh with a stand-in gh

setup() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  WORK="$(mktemp -d)"
  git init -q --bare "$WORK/origin.git"
  git clone -q "$WORK/origin.git" "$WORK/repo" 2>/dev/null
  cd "$WORK/repo"
  git config user.email t@example.com
  git config user.name t
  git checkout -q -b master
  git commit -q --allow-empty -m one
  git push -q origin master
  SHA="$(git rev-parse HEAD)"
  mkdir "$WORK/bin"
  cat >"$WORK/bin/gh" <<'FAKE'
#!/bin/sh
# answers the workflow-runs query with $FAKE_RUNS (a JSON array of runs), applying the
# --jq filter like the real gh does; a run that gives no event, branch, repository or
# commit is a push to master of o/r at $GITHUB_SHA. The query is kept in $GH_QUERY_LOG.
filter=""
echo "$@" >>"$GH_QUERY_LOG"
while [ $# -gt 0 ]; do [ "$1" = "--jq" ] && filter="$2"; shift; done
printf '%s' "$FAKE_RUNS" |
  jq -c --arg sha "$GITHUB_SHA" '[.[] | {event: "push", head_branch: "master", head_repository: {full_name: "o/r"}, head_sha: $sha} + .]' |
  jq -c '{workflow_runs: .}' | jq -c "$filter"
FAKE
  chmod +x "$WORK/bin/gh"
  export GH_QUERY_LOG="$WORK/query.log"
  export PATH="$WORK/bin:$PATH" GITHUB_REPOSITORY=o/r GITHUB_SHA="$SHA" WAIT_SECONDS=2 POLL_SECONDS=1
}

teardown() { rm -rf "$WORK"; }

gate() { run bash "$ROOT_DIR/packaging/release-gate.sh"; }

@test "a commit whose CI succeeded is released" {
  FAKE_RUNS='[{"status":"completed","conclusion":"success"}]' gate
  [ "$status" -eq 0 ]
}

@test "a commit whose CI failed is refused" {
  FAKE_RUNS='[{"status":"completed","conclusion":"failure"}]' gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"did not succeed"* ]]
}

@test "a commit with no CI run is refused after the wait" {
  FAKE_RUNS='[]' gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"no successful CI run"* ]]
}

@test "a CI run still going is waited for, then refused if it never finishes" {
  FAKE_RUNS='[{"status":"in_progress","conclusion":null}]' gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"waiting for CI"* ]]
}

@test "a tag on a commit that is not on the default branch is refused" {
  git -C "$WORK/repo" checkout -q -b side
  git -C "$WORK/repo" commit -q --allow-empty -m "only on a side branch"
  export GITHUB_SHA="$(git -C "$WORK/repo" rev-parse HEAD)"
  FAKE_RUNS='[{"status":"completed","conclusion":"success"}]' gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"is not on master"* ]]
}

# What counts as "CI passed": the default branch's own push run, for this commit.

@test "the query asks for push runs of the default branch for the tagged commit" {
  FAKE_RUNS='[{"status":"completed","conclusion":"success"}]' gate
  [ "$status" -eq 0 ]
  grep -q "head_sha=$SHA" "$GH_QUERY_LOG"
  grep -q "event=push" "$GH_QUERY_LOG"
  grep -q "branch=master" "$GH_QUERY_LOG"
}

@test "a pull request run that succeeded is not the default branch's CI" {
  FAKE_RUNS='[{"event":"pull_request","status":"completed","conclusion":"success"}]' gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"no successful CI run"* ]]
}

@test "a success on another branch is not the default branch's CI" {
  FAKE_RUNS='[{"head_branch":"side","status":"completed","conclusion":"success"}]' gate
  [ "$status" -ne 0 ]
}

@test "a success from a fork is not this repository's CI" {
  FAKE_RUNS='[{"head_repository":{"full_name":"fork/r"},"status":"completed","conclusion":"success"}]' gate
  [ "$status" -ne 0 ]
}

@test "a success for another commit is not this commit's CI" {
  FAKE_RUNS='[{"head_sha":"0000000000000000000000000000000000000000","status":"completed","conclusion":"success"}]' gate
  [ "$status" -ne 0 ]
}

@test "a failed run of the commit still refuses it when a pull request run of it succeeded" {
  FAKE_RUNS='[{"status":"completed","conclusion":"failure"},{"event":"pull_request","status":"completed","conclusion":"success"}]' gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"did not succeed"* ]]
}

@test "a default branch name that is not a branch name is refused before any query" {
  DEFAULT_BRANCH='x" or true or "' FAKE_RUNS='[{"status":"completed","conclusion":"success"}]' gate
  [ "$status" -ne 0 ]
  [[ "$output" == *"not a branch name"* ]]
}
