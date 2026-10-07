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

# --- the required admission check (US-012): a check a ruleset can require, whose refusal fails it ---

admission() { git -C "$FIX/test-repo" show "$1:.github/workflows/staircase-admission.yml"; }

@test "the admission check is one stable job whose refusal fails it: no continue-on-error, no assertion of failure" {
  for base in main other-key strict; do
    admission "$base" >"$WORK/a.yml"
    [ "$(grep -c '^  [a-z-]*:$' "$WORK/a.yml")" = 1 ]
    grep -q '^  staircase-admission:$' "$WORK/a.yml"
    grep -q 'name: staircase-admission' "$WORK/a.yml"
    [ "$(grep -c 'uses: b070nd/stAirCase@' "$WORK/a.yml")" = 1 ]
    if grep -q 'continue-on-error' "$WORK/a.yml"; then false; fi
    if grep -q 'outcome' "$WORK/a.yml"; then false; fi
    grep -q '^on: pull_request' "$WORK/a.yml"
  done
}

@test "the ruleset requires that check on every base, lets nobody bypass it, and the fixture says it is not pushed" {
  command -v jq >/dev/null
  jq -e '.enforcement == "active" and (.bypass_actors | length == 0)' "$FIX/ruleset.json"
  jq -e '[.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context] == ["staircase-admission"]' "$FIX/ruleset.json"
  jq -e '.conditions.ref_name.include | sort == ["refs/heads/main","refs/heads/other-key","refs/heads/strict"]' "$FIX/ruleset.json"
  jq -e '[.rules[].type] | index("pull_request") != null and index("non_fast_forward") != null and index("deletion") != null' "$FIX/ruleset.json"
}

# decide <head> <base>: the admission check as its base branch configures it, run with the real verifier
decide() {
  admission "$2" >"$WORK/a.yml"
  export KEY="$(sed -n 's/.*key: \([^,]*\),.*/\1/p' "$WORK/a.yml" | head -1)"
  export MIN_CAL="$(sed -n 's/.*min-cal: "\([0-9]\)".*/\1/p' "$WORK/a.yml" | head -1)"
  export REBUILD="$(sed -n 's/.*rebuild: "\([a-z]*\)".*/\1/p' "$WORK/a.yml" | head -1)"
  verify "$1" "$2"
}

@test "admission: the certified range into main is eligible" {
  decide pr-certified main
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [[ "$output" == *"rebuilt from"* ]]
}

@test "admission: a change with no certificate into main fails the required check" {
  decide pr-no-notes main
  [ "$status" -ne 0 ]
  [[ "$output" == *"no change certificate"* ]]
}

@test "admission: a certificate that the base's trusted key did not sign fails the required check" {
  decide pr-certified other-key
  [ "$status" -ne 0 ]
  [[ "$output" == *"no valid signature by the trusted key"* ]]
}

@test "admission: a certificate below the base's required level fails the required check" {
  decide pr-certified strict
  [ "$status" -ne 0 ]
  [[ "$output" == *"below the required 4"* ]]
}

@test "the control: a pull request that rewrites the check's own workflow is a branch to observe, and says what it does" {
  git -C "$FIX/test-repo" show pr-edits-the-check:.github/workflows/staircase-admission.yml >"$WORK/e.yml"
  grep -q 'all: "false"' "$WORK/e.yml"                  # it weakens the check from inside the pull request
  grep -q "pr-edits-the-check" "$FIX/README.md"
  grep -qi "limit" "$FIX/README.md"
}

@test "the conformance harness is kept and the README says it is conformance, not enforcement" {
  [ "$(grep -c 'continue-on-error: true' <(git -C "$FIX/test-repo" show main:.github/workflows/staircase-verify.yml))" = 3 ]
  grep -qi "conformance" "$FIX/README.md"
  grep -q "staircase-admission" "$FIX/README.md"
}

# --- ruleset-evidence.sh reads back what GitHub did, with curl and jq (no gh): here a stand-in curl answers as GitHub's REST API documents ---

stub_api() {
  mkdir -p "$WORK/bin"
  cat >"$WORK/bin/gh" <<'GH'
#!/bin/sh
echo "gh was called" >&2; exit 99
GH
  cat >"$WORK/bin/curl" <<'CURL'
#!/bin/bash
url="${*: -1}"                                   # the URL is the last argument
echo "$*" >>"$API_LOG"
case " $* " in *" -K - "*) cat >>"$API_LOG.stdin" ;; esac   # a curl config on stdin (the token's header) is never an argument
p="${url#https://api.github.com/}"
case "$p" in
  "repos/o/t/pulls?state=all&per_page=100") echo '[{"number":1,"html_url":"https://github.com/o/t/pull/1","state":"open","head":{"ref":"pr-certified","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"base":{"ref":"main"}},
    {"number":2,"html_url":"https://github.com/o/t/pull/2","state":"open","head":{"ref":"pr-no-notes","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"base":{"ref":"main"}}]' ;;
  "repos/o/t/pulls/1") echo '{"mergeable":true,"mergeable_state":"clean"}' ;;
  "repos/o/t/pulls/2") echo '{"mergeable":false,"mergeable_state":"blocked"}' ;;
  "repos/o/t/actions/runs?head_sha=aaaa"*) echo '{"workflow_runs":[{"id":11,"path":".github/workflows/staircase-admission.yml","created_at":"2026-10-07T10:00:00Z","run_attempt":1,"html_url":"https://github.com/o/t/actions/runs/11","event":"pull_request","head_sha":"aaaa","pull_requests":[{"number":1}]}]}' ;;
  "repos/o/t/actions/runs?head_sha=bbbb"*) echo '{"workflow_runs":[{"id":22,"path":".github/workflows/staircase-admission.yml","created_at":"2026-10-07T10:05:00Z","run_attempt":2,"html_url":"https://github.com/o/t/actions/runs/22","event":"pull_request","head_sha":"bbbb","pull_requests":[{"number":2}]},
    {"id":23,"path":".github/workflows/staircase-verify.yml","created_at":"2026-10-07T10:06:00Z","run_attempt":1,"html_url":"https://github.com/o/t/actions/runs/23","event":"pull_request","head_sha":"bbbb","pull_requests":[{"number":2}]}]}' ;;
  "repos/o/t/actions/runs/11/jobs") echo '{"jobs":[{"id":111,"name":"staircase-admission","conclusion":"success","status":"completed","html_url":"https://github.com/o/t/actions/runs/11/job/111"}]}' ;;
  "repos/o/t/actions/runs/22/jobs") echo '{"jobs":[{"id":222,"name":"staircase-admission","conclusion":"failure","status":"completed","html_url":"https://github.com/o/t/actions/runs/22/job/222"}]}' ;;
  "repos/o/t/actions/jobs/111/logs" | "repos/o/t/actions/jobs/222/logs") [ -n "${LOGS:-}" ] && echo "Installed staircase v0.11.0 (build attestation verified)" || { echo "requires authentication" >&2; exit 22; } ;;
  "repos/o/t/contents/.github/workflows/staircase-admission.yml?ref=main") printf '{"content":"%s"}' "$(printf '      - uses: b070nd/stAirCase@v0.11.0\n        with: { key: k }\n' | base64 | tr -d '\n')" ;;
  "repos/b070nd/stAirCase/releases/tags/v0.11.0") echo '{"assets":[{"name":"staircase_0.11.0_linux_amd64.tar.gz","digest":"sha256:abc"},{"name":"staircase_0.11.0_darwin_arm64.tar.gz","digest":"sha256:def"}]}' ;;
  "repos/o/t/rulesets") echo '[{"id":7}]' ;;
  "repos/o/t/rulesets/7") echo '{"id":7,"name":"staircase-admission","target":"branch","enforcement":"active","conditions":{"ref_name":{"include":["refs/heads/main"]}},"rules":[{"type":"required_status_checks"}],"secret":"must-not-appear"}' ;;
  "repos/o/t/rules/branches/main") echo '[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"staircase-admission"}]}},{"type":"pull_request"}]' ;;
  *) echo "unexpected API call: $p" >&2; exit 22 ;;
esac
CURL
  chmod +x "$WORK/bin/gh" "$WORK/bin/curl"
  export API_LOG="$WORK/api.log" PATH="$WORK/bin:$PATH" POLL_SECONDS=0
}

@test "the evidence script keeps each pull request's check, run, attempt, merge state, verifier and the ruleset, with curl and no gh" {
  stub_api
  run bash "$ROOT_DIR/demo/ruleset-evidence.sh" o/t
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [[ "$output" == *"| [#1](https://github.com/o/t/pull/1) | pr-certified -> main | \`aaaaaaaaaaaa\` | success | https://github.com/o/t/actions/runs/11 (attempt 1) | clean / mergeable | v0.11.0 (workflow on main) |"* ]]
  [[ "$output" == *"| [#2](https://github.com/o/t/pull/2) | pr-no-notes -> main | \`bbbbbbbbbbbb\` | failure | https://github.com/o/t/actions/runs/22 (attempt 2) | blocked / not mergeable | v0.11.0 (workflow on main) |"* ]]
  [[ "$output" == *'"digest": "sha256:abc"'* ]]
  [[ "$output" != *"sha256:def"* ]]                    # only the archive for the runner
  [[ "$output" == *'"enforcement": "active"'* ]]
  [[ "$output" == *"not visible to this reader"* ]]    # no token: the bypass list is not shown, and it says so
  [[ "$output" == *'"context": "staircase-admission"'* ]]   # the rules in force on main
  [[ "$output" != *"must-not-appear"* ]]               # only the named fields of a ruleset are kept
  [[ "$output" != *"runs/23"* ]]                       # the conformance harness is not the required check
  [[ "$output" != *"gh was called"* ]]
}

@test "with a token the log gives the version the run installed, and the token is never an argument" {
  stub_api
  export GITHUB_TOKEN=ghp_secret_value_1234 LOGS=1
  run bash "$ROOT_DIR/demo/ruleset-evidence.sh" o/t
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [[ "$output" == *"| v0.11.0 (run log) |"* ]]
  [[ "$output" != *"ghp_secret_value_1234"* ]]
  if grep -q "ghp_secret_value_1234" "$API_LOG"; then false; fi                 # not in any command line
  grep -q "ghp_secret_value_1234" "$API_LOG.stdin"                              # it went through curl's stdin config
  if grep -qE -- "(-X|--request) " "$API_LOG"; then false; fi                    # only reads
}

@test "the evidence script refuses what is not owner/name" {
  stub_api
  run bash "$ROOT_DIR/demo/ruleset-evidence.sh" "o/t; rm -rf x"
  [ "$status" -eq 2 ]
}
