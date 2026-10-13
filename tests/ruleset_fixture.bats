#!/usr/bin/env bats
# tests/ruleset_fixture.bats - demo/ruleset-fixture.sh builds what its README says, and the real verifier
# decides each of its five cases as the README says (locally: the Action's own script, the real binary, a
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

# verify <head> <base>: what the Action's script does for a pull request from head into base
verify() {
  git checkout -q -B head "origin/$1"
  export BASE_REF="$2" HEAD_SHA="$(git rev-parse HEAD)"
  run bash "$ROOT_DIR/packaging/verify-action.sh"
}

admission() { git -C "$FIX/test-repo" show main:.github/workflows/staircase-admission.yml; }

# decide <head> <base>: the admission check as the DEFAULT branch's workflow configures it for that base, run with the real verifier
decide() {
  admission >"$WORK/a.yml"
  export KEY="$(sed -n 's/^ *key: //p' "$WORK/a.yml" | head -1)"
  export REBUILD="$(sed -n 's/^ *rebuild: "\([a-z]*\)".*/\1/p' "$WORK/a.yml" | head -1)"
  local expr; expr="$(sed -n 's/^ *min-cal: "\(.*\)"$/\1/p' "$WORK/a.yml" | head -1)"
  if [[ "$expr" =~ github\.base_ref\ ==\ \'([a-z-]+)\'\ \&\&\ \'([0-9])\'\ \|\|\ \'([0-9])\' ]]; then
    if [ "$2" = "${BASH_REMATCH[1]}" ]; then export MIN_CAL="${BASH_REMATCH[2]}"; else export MIN_CAL="${BASH_REMATCH[3]}"; fi
  else
    export MIN_CAL="$expr"
  fi
  verify "$1" "$2"
}

@test "the fixture says what to push and what each pull request must do" {
  [ -d "$FIX/test-repo/.git" ]
  [ -z "$(git -C "$FIX/test-repo" remote)" ]          # no remote: nothing can be pushed by accident
  for b in pr-certified pr-sealed pr-below-strict pr-no-notes pr-edits-the-check other-key strict; do grep -q "$b" "$FIX/README.md"; done
  grep -q "refs/notes/staircase-ledger" "$FIX/README.md"
  [ "$(git -C "$FIX/test-repo" notes --ref=staircase list | wc -l | tr -d ' ')" -ge 3 ]   # a certificate for each certified commit
}

@test "each pull request has a head commit of its own: checks attach to a commit, so a shared head would share its results" {
  heads="$(for b in pr-certified pr-sealed pr-below-strict pr-no-notes pr-edits-the-check; do git -C "$FIX/test-repo" rev-parse "$b"; done)"
  [ "$(printf '%s\n' "$heads" | sort -u | wc -l | tr -d ' ')" = 5 ]
}

@test "a pull request branches from what it must merge with: no conflict with main or with the bases it targets" {
  for pair in pr-certified:main pr-sealed:main pr-no-notes:main pr-edits-the-check:main pr-sealed:other-key pr-below-strict:strict; do
    h="${pair%%:*}"; b="${pair##*:}"
    git -C "$FIX/test-repo" merge-tree --write-tree "$b" "$h" >/dev/null || { echo "$h into $b conflicts"; false; }
  done
}

@test "pr-certified into main passes, rebuilt from its ledger" {
  REBUILD=true verify pr-certified main
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [[ "$output" == *"rebuilt from"* ]]
}

@test "a sealed change into main is a valid CAL 2 change: it passes at min-cal 2 and is refused at min-cal 3" {
  MIN_CAL=2 verify pr-below-strict main
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  MIN_CAL=3 verify pr-below-strict main
  [ "$status" -ne 0 ]
  [[ "$output" == *"below the required 3"* ]]
}

@test "pr-no-notes into main is refused: no certificate" {
  verify pr-no-notes main
  [ "$status" -ne 0 ]
  [[ "$output" == *"no change certificate"* ]]
}

@test "the harness and the README agree: the same cases, on heads and bases that are opened" {
  git -C "$FIX/test-repo" show main:.github/workflows/staircase-verify.yml >"$WORK/w.yml"
  [ "$(grep -c 'uses: b070nd/stAirCase@' "$WORK/w.yml")" = 4 ]
  [ "$(grep -c 'continue-on-error: true' "$WORK/w.yml")" = 3 ]
  [ "$(grep -c 'outcome }}" = failure' "$WORK/w.yml")" = 3 ]
  for job in certified-range-passes below-the-required-level-is-refused no-certificate-is-refused wrong-trusted-key-is-refused; do
    grep -q "^  $job:" "$WORK/w.yml"
  done
  # a job whose head and base are never opened as a pull request tests nothing: each (head, base) it names is in the README's table
  grep -q "pr-below-strict | strict" "$FIX/README.md"
  for h in $(grep -o "github.head_ref == '[a-z-]*'" "$WORK/w.yml" | sed "s/.*== '//; s/'//" | sort -u); do
    git -C "$FIX/test-repo" rev-parse --verify -q "$h" >/dev/null
    grep -q "| $h |" "$FIX/README.md"
  done
  grep -q "head_ref == 'pr-below-strict' && github.base_ref == 'strict'" "$WORK/w.yml"
  grep -q "head_ref == 'pr-sealed' && github.base_ref == 'other-key'" "$WORK/w.yml"
  grep -q "head_ref == 'pr-certified' && github.base_ref == 'main'" "$WORK/w.yml"
}

# --- the required admission check (US-012): a check a ruleset can require, whose refusal fails it ---

@test "the admission check is one stable job whose refusal fails it, and it lives on the default branch only" {
  admission >"$WORK/a.yml"
  [ "$(grep -c '^  [a-z-]*:$' "$WORK/a.yml")" = 1 ]
  grep -q '^  staircase-admission:$' "$WORK/a.yml"
  grep -q 'name: staircase-admission-\${{ github.base_ref }}' "$WORK/a.yml"      # the check's name carries the base it was run for
  [ "$(grep -c 'uses: b070nd/stAirCase@' "$WORK/a.yml")" = 1 ]
  if grep -q 'continue-on-error' "$WORK/a.yml"; then false; fi
  if grep -q 'outcome' "$WORK/a.yml"; then false; fi
  grep -q '^on: pull_request_target$' "$WORK/a.yml"
  grep -q 'ref: "\${{ github.event.pull_request.head.sha }}"' "$WORK/a.yml"   # it checks out the pull request's head to verify it
  grep -q '^  attestations: read$' "$WORK/a.yml"                          # the Action checks the verifier's build attestation
  grep -q '^  contents: read$' "$WORK/a.yml"
  if grep -qE 'contents: write|pull-requests: write|secrets\.' "$WORK/a.yml"; then false; fi   # nothing a pull request could use
  grep -q "github.base_ref == 'strict'" "$WORK/a.yml"                    # one copy chooses the level from the base
  # a copy on another branch is never the one that runs (GitHub runs the default branch's), so the fixture does not diverge them
  for b in other-key strict; do
    [ "$(git -C "$FIX/test-repo" show $b:.github/workflows/staircase-admission.yml)" = "$(admission)" ]
  done
}

@test "one ruleset per base, each requiring that base's own check, nobody may bypass, nobody may force-push or delete" {
  command -v jq >/dev/null
  for base in main other-key strict; do
    f="$FIX/ruleset-$base.json"
    jq -e --arg b "$base" '.enforcement == "active" and (.bypass_actors | length == 0) and .name == ("staircase-admission-" + $b)' "$f"
    jq -e --arg b "$base" '.conditions.ref_name.include == ["refs/heads/" + $b]' "$f"
    jq -e --arg b "$base" '[.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context] == ["staircase-admission-" + $b]' "$f"
    jq -e '[.rules[].type] | index("pull_request") != null and index("non_fast_forward") != null and index("deletion") != null' "$f"
  done
}

@test "a passing run for one base can never satisfy another base's required check (GitHub attaches checks to a commit, and a commit can head pull requests into several bases)" {
  # the names GitHub would see: the job's name with github.base_ref filled in, against what each base's ruleset requires
  admission >"$WORK/a.yml"
  grep -q 'name: staircase-admission-\${{ github.base_ref }}' "$WORK/a.yml"
  names=""
  for base in main other-key strict; do
    names+="$(jq -r '.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context' "$FIX/ruleset-$base.json")"$'\n'
  done
  [ "$(printf '%s' "$names" | sort -u | wc -l | tr -d ' ')" = 3 ]                                    # three different names
  if printf '%s' "$names" | grep -qx 'staircase-admission'; then false; fi                           # and none is the bare, shared one
  # the case that was observed: pr-sealed heads a pull request into main (valid there) and one into other-key (refused there)
  grep -q "| pr-sealed | main |" "$FIX/README.md"
  grep -q "| pr-sealed | other-key |" "$FIX/README.md"
  grep -qi "same commit" "$FIX/README.md"
}

@test "admission: the certified commit into main is eligible" {
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
  decide pr-sealed other-key
  [ "$status" -ne 0 ]
  [[ "$output" == *"no valid signature by the trusted key"* ]]
}

@test "admission: a certificate below the level strict requires fails the required check, and the same commit passes into main" {
  decide pr-below-strict strict
  [ "$status" -ne 0 ]
  [[ "$output" == *"below the required 4"* ]]
  decide pr-below-strict main
  [ "$status" -eq 0 ] || { echo "$output"; false; }
}

@test "the control: a pull request that rewrites the check's own workflow is turned down by the default branch's copy of it" {
  git -C "$FIX/test-repo" show pr-edits-the-check:.github/workflows/staircase-admission.yml >"$WORK/e.yml"
  grep -q 'all: "false"' "$WORK/e.yml"                  # it weakens the check from inside the pull request
  # what GitHub runs for pull_request_target is the default branch's workflow: its settings decide, and the uncertified commit fails
  decide pr-edits-the-check main
  [ "$status" -ne 0 ]
  [[ "$output" == *"no change certificate"* ]]
  # whereas the pull request's own copy of the workflow would have let it through (why the trigger matters)
  KEY=.github/staircase.pub MIN_CAL=1 ALL=false REBUILD=true verify pr-edits-the-check main
  [ "$status" -eq 0 ]
  [[ "$output" == *"0 commit(s) checked"* ]]
}

@test "the repository carries its own README, in .github/, that says what it is and where the record is" {
  git -C "$FIX/test-repo" show main:.github/README.md >"$WORK/r.md"
  grep -qi "ruleset" "$WORK/r.md"
  grep -q "pr-sealed" "$WORK/r.md"
  grep -q "release-evidence" "$WORK/r.md"
}

@test "the README says what pull_request_target runs, and that the harness is conformance, not enforcement" {
  grep -q "pull_request_target" "$FIX/README.md"
  grep -qi "default branch" "$FIX/README.md"
  if grep -qi "workflow of the base branch\|base branch's copy" "$FIX/README.md"; then false; fi
  grep -qi "conformance" "$FIX/README.md"
  grep -q "staircase-admission" "$FIX/README.md"
}

@test "every workflow of the fixture can read the verifier's build attestation" {
  for f in staircase-admission.yml staircase-verify.yml; do
    git -C "$FIX/test-repo" show main:.github/workflows/$f | grep -q '^  attestations: read$'
  done
}

@test "the Action the workflows use and the verifier they install are the ones asked for" {
  out="$BATS_FILE_TMPDIR/pinned"
  (cd "$ROOT_DIR" && FIXTURE_ACTION_REF=0123456789abcdef0123456789abcdef01234567 FIXTURE_VERIFIER_VERSION=v1.2.3 ./demo/ruleset-fixture.sh "$out" v1.0.0 >/dev/null 2>&1)
  for f in staircase-admission.yml staircase-verify.yml; do
    git -C "$out/test-repo" show main:.github/workflows/$f | grep -q 'uses: b070nd/stAirCase@0123456789abcdef0123456789abcdef01234567'
    git -C "$out/test-repo" show main:.github/workflows/$f | grep -q 'version: v1.2.3'
  done
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
    {"number":2,"html_url":"https://github.com/o/t/pull/2","state":"open","head":{"ref":"pr-no-notes","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"base":{"ref":"main"}},
    {"number":3,"html_url":"https://github.com/o/t/pull/3","state":"open","head":{"ref":"pr-sealed","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"other-key"}},
    {"number":4,"html_url":"https://github.com/o/t/pull/4","state":"open","head":{"ref":"pr-sealed","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}]' ;;
  "repos/o/t/pulls/3") echo '{"mergeable":true,"mergeable_state":"blocked"}' ;;
  "repos/o/t/pulls/4") echo '{"mergeable":true,"mergeable_state":"clean"}' ;;
  "repos/o/t/actions/runs?head_sha=cccc"*) echo '{"workflow_runs":[{"id":31,"path":".github/workflows/staircase-admission.yml","created_at":"2026-10-07T11:00:00Z","run_attempt":1,"html_url":"https://github.com/o/t/actions/runs/31","event":"pull_request_target","head_sha":"cccc","pull_requests":[{"number":3},{"number":4}]},
    {"id":32,"path":".github/workflows/staircase-admission.yml","created_at":"2026-10-07T11:00:20Z","run_attempt":1,"html_url":"https://github.com/o/t/actions/runs/32","event":"pull_request_target","head_sha":"cccc","pull_requests":[{"number":3},{"number":4}]}]}' ;;
  "repos/o/t/actions/runs/31/jobs") echo '{"jobs":[{"id":331,"name":"staircase-admission-main","conclusion":"success","status":"completed","html_url":"https://github.com/o/t/actions/runs/31/job/331"}]}' ;;
  "repos/o/t/actions/runs/32/jobs") echo '{"jobs":[{"id":332,"name":"staircase-admission-other-key","conclusion":"failure","status":"completed","html_url":"https://github.com/o/t/actions/runs/32/job/332"}]}' ;;
  "repos/o/t/pulls/1") echo '{"mergeable":true,"mergeable_state":"clean"}' ;;
  "repos/o/t/pulls/2") echo '{"mergeable":false,"mergeable_state":"blocked"}' ;;
  "repos/o/t/actions/runs?head_sha=aaaa"*) echo '{"workflow_runs":[{"id":11,"path":".github/workflows/staircase-admission.yml","created_at":"2026-10-07T10:00:00Z","run_attempt":1,"html_url":"https://github.com/o/t/actions/runs/11","event":"pull_request","head_sha":"aaaa","pull_requests":[{"number":1}]}]}' ;;
  "repos/o/t/actions/runs?head_sha=bbbb"*) echo '{"workflow_runs":[{"id":22,"path":".github/workflows/staircase-admission.yml","created_at":"2026-10-07T10:05:00Z","run_attempt":2,"html_url":"https://github.com/o/t/actions/runs/22","event":"pull_request","head_sha":"bbbb","pull_requests":[{"number":2}]},
    {"id":23,"path":".github/workflows/staircase-verify.yml","created_at":"2026-10-07T10:06:00Z","run_attempt":1,"html_url":"https://github.com/o/t/actions/runs/23","event":"pull_request","head_sha":"bbbb","pull_requests":[{"number":2}]}]}' ;;
  "repos/o/t/actions/runs/11/jobs") echo '{"jobs":[{"id":111,"name":"staircase-admission-main","conclusion":"success","status":"completed","html_url":"https://github.com/o/t/actions/runs/11/job/111"}]}' ;;
  "repos/o/t/actions/runs/22/jobs") echo '{"jobs":[{"id":222,"name":"staircase-admission-main","conclusion":"failure","status":"completed","html_url":"https://github.com/o/t/actions/runs/22/job/222"}]}' ;;
  "repos/o/t/actions/jobs/111/logs" | "repos/o/t/actions/jobs/222/logs" | "repos/o/t/actions/jobs/331/logs" | "repos/o/t/actions/jobs/332/logs") [ -n "${LOGS:-}" ] && echo "Installed staircase v0.11.0 (build attestation verified)" || { echo "requires authentication" >&2; exit 22; } ;;
  "repos/o/t") echo '{"default_branch":"trunk"}' ;;
  "repos/o/t/contents/.github/workflows/staircase-admission.yml?ref=trunk") printf '{"content":"%s"}' "$(printf '      - uses: b070nd/stAirCase@0123456789abcdef0123456789abcdef01234567\n        with:\n          version: v0.11.0\n          key: k\n' | base64 | tr -d '\n')" ;;
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
  [[ "$output" == *"| [#1](https://github.com/o/t/pull/1) | open | pr-certified -> main | \`aaaaaaaaaaaa\` | success | https://github.com/o/t/actions/runs/11 (attempt 1) | clean / mergeable | v0.11.0 (workflow on trunk) |"* ]]
  [[ "$output" == *"| [#2](https://github.com/o/t/pull/2) | open | pr-no-notes -> main | \`bbbbbbbbbbbb\` | failure | https://github.com/o/t/actions/runs/22 (attempt 2) | blocked / not mergeable | v0.11.0 (workflow on trunk) |"* ]]
  # two pull requests share one head commit and each of its two runs lists both: each is given the run of ITS base's check
  [[ "$output" == *"| [#3](https://github.com/o/t/pull/3) | open | pr-sealed -> other-key | \`cccccccccccc\` | failure | https://github.com/o/t/actions/runs/32 (attempt 1) |"* ]]
  [[ "$output" == *"| [#4](https://github.com/o/t/pull/4) | open | pr-sealed -> main | \`cccccccccccc\` | success | https://github.com/o/t/actions/runs/31 (attempt 1) |"* ]]
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

# --- the fixture can be built under one identity (a public test repository must not carry made-up authors) ---

@test "built with FIXTURE_GIT_NAME and FIXTURE_GIT_EMAIL, every commit and the certificate's requester carry that identity and nothing else" {
  OUT="$WORK/own"
  FIXTURE_GIT_NAME="Test Owner" FIXTURE_GIT_EMAIL="owner@example.org" \
    run bash "$ROOT_DIR/demo/ruleset-fixture.sh" "$OUT" v1.0.0
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  ids="$(git -C "$OUT/test-repo" log --all --format='%an <%ae>%n%cn <%ce>' | sort -u)"
  [ "$ids" = "Test Owner <owner@example.org>" ] || { echo "identities: $ids"; false; }
  if grep -r -l -E "demo@staircase.local|fixture@example.com" "$OUT/test-repo/.git/refs" "$OUT/test-repo/.github" 2>/dev/null | grep -q .; then false; fi
  # the certificate says who requested the change: the checkout's git email, which is the same identity
  cd "$OUT/test-repo" && git fetch -q . 2>/dev/null || true
  git clone -q "$OUT/test-repo" "$WORK/own-ci" 2>/dev/null
  cd "$WORK/own-ci" && git fetch -q origin 'refs/notes/*:refs/notes/*' && git checkout -q -B head origin/pr-certified
  git show origin/main:.github/staircase.pub >"$WORK/own-key.pub"          # the key comes from the base branch, as the Action reads it
  run "$BATS_FILE_TMPDIR/staircase" verify head --key "$WORK/own-key.pub" --min-cal 2
  [[ "$output" == *"owner@example.org"* ]] || { echo "$output"; false; }
  [[ "$output" != *"demo@staircase.local"* ]]
}
