#!/usr/bin/env bats
# tests/ruleset_fixture.bats - demo/ruleset-fixture.sh builds what its README says, and the real verifier
# decides each of its four cases as the README says (locally: the Action's own script, the real binary, a
# local origin). The remote half, the Action in GitHub under a ruleset, is the owner's run.

setup_file() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  FIX="$BATS_FILE_TMPDIR/fixture"
  (cd "$ROOT_DIR" && ./demo/ruleset-fixture.sh "$FIX" >"$BATS_FILE_TMPDIR/fixture.log" 2>&1) || { cat "$BATS_FILE_TMPDIR/fixture.log"; return 1; }
  (cd "$ROOT_DIR" && go build -o "$BATS_FILE_TMPDIR/staircase" ./src/cmd/staircase)
}

setup() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  FIX="$BATS_FILE_TMPDIR/fixture"
  WORK="$(mktemp -d)"
  git clone -q --mirror "$FIX/test-repo" "$WORK/origin.git"
  git clone -q "$WORK/origin.git" "$WORK/ci" 2>/dev/null
  cd "$WORK/ci"
  git config user.email ci@example.com && git config user.name ci
  export STAIRCASE_BIN="$BATS_FILE_TMPDIR/staircase" KEY=.github/staircase.pub MIN_CAL=2 RUNNER_OS=Linux RUNNER_ARCH=X64 GH_TOKEN=x
  unset ACTION_REF TRUST_REF RANGE ALL ALLOWED_SIGNERS REBUILD REQUIRE_INITIATOR VERSION
}
teardown() { rm -rf "$WORK"; }

# case <head> <base>: what the Action's script does for a pull request from head into base
verify() {
  git checkout -q -B head "origin/$1"
  export BASE_REF="$2" HEAD_SHA="$(git rev-parse HEAD)"
  run bash "$ROOT_DIR/packaging/verify-action.sh"
}

@test "the fixture says what to push and what each pull request must do" {
  [ -d "$FIX/test-repo/.git" ]
  [ -z "$(git -C "$FIX/test-repo" remote)" ]          # no remote: nothing can be pushed by accident
  grep -q "pr-certified" "$FIX/README.md" && grep -q "other-key" "$FIX/README.md"
  grep -q "refs/notes/staircase-ledger" "$FIX/README.md"
  [ "$(git -C "$FIX/test-repo" notes --ref=staircase list | wc -l | tr -d ' ')" -ge 1 ]
}

@test "pr-certified into main passes, rebuilt from its ledger" {
  REBUILD=true verify pr-certified main
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [[ "$output" == *"rebuilt from"* ]]
}

@test "pr-low-assurance into main is refused at min-cal 4" {
  MIN_CAL=4 verify pr-low-assurance main
  [ "$status" -ne 0 ]
  [[ "$output" == *"below the required 4"* ]]
}

@test "pr-no-notes into main is refused: no certificate" {
  verify pr-no-notes main
  [ "$status" -ne 0 ]
  [[ "$output" == *"no change certificate"* ]]
}

@test "pr-certified into other-key is refused: the base trusts another key" {
  verify pr-certified other-key
  [ "$status" -ne 0 ]
  [[ "$output" == *"no valid signature by the trusted key"* ]]
}

@test "the workflow asserts each refusal and passes the certified range" {
  git -C "$FIX/test-repo" show main:.github/workflows/staircase-verify.yml >"$WORK/w.yml"
  [ "$(grep -c 'uses: b070nd/stAirCase@' "$WORK/w.yml")" = 4 ]
  [ "$(grep -c 'continue-on-error: true' "$WORK/w.yml")" = 3 ]
  [ "$(grep -c 'outcome }}" = failure' "$WORK/w.yml")" = 3 ]
  for job in certified-range-passes below-the-required-level-is-refused no-certificate-is-refused wrong-trusted-key-is-refused; do
    grep -q "^  $job:" "$WORK/w.yml"
  done
}
