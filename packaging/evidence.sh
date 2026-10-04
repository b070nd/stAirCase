#!/usr/bin/env bash
# evidence.sh [outdir] - the canonical release checks, with a machine-readable record.
#
# Runs, in order: the gate (make check), a count of every Go test (run, passed,
# failed, skipped), the kill drills, the independent conformance scripts, the packaged
# verifier on a fresh mirror of the repository (verify and rebuild), and the
# vulnerability scan. Each step is recorded with its exit status, its counts and a
# digest of its log. A check that cannot run is recorded as "skipped" with the reason
# and does not make the result "passed": overall is "passed" only when no step failed
# and none was skipped. The opt-in live-agent runs need credentials that only their
# owner has: they are recorded as "not_run" with the reason, and are not counted as passed.
#
# Writes <outdir>/evidence.json (default: evidence/) and the step logs beside it. The
# JSON names the stAirCase commit (and whether the tree was clean), the tool versions,
# the OS and architecture, and a digest of every conformance fixture. It holds no
# paths of this machine, environment values or log text, so it can be published.
# The exit status is 0 only when overall is "passed".
set -uo pipefail
cd "$(dirname "$0")/.."
out="${1:-evidence}"
mkdir -p "$out"
export STAIRCASE_REQUIRE_DEPS=1
want() { [ -z "${EVIDENCE_STEPS:-}" ] || [[ " $EVIDENCE_STEPS " == *" $1 "* ]]; }
steps="$out/steps.jsonl"
: >"$steps"

record() { # record <name> <status> <reason> <counts-json> <log>
  local logsha=""
  [ -f "$5" ] && logsha="$(shasum -a 256 "$5" | cut -d' ' -f1)"
  jq -cn --arg name "$1" --arg status "$2" --arg reason "$3" --argjson counts "${4:-null}" --arg log "$(basename "$5")" --arg sha "$logsha" \
    '{name: $name, status: $status, reason: $reason, counts: $counts, log: $log, log_sha256: $sha}' >>"$steps"
  printf '%-8s %s %s\n' "$2" "$1" "$3"
}

# step <name> <command...>: pass or fail by exit status
step() {
  local name="$1" log="$out/$(tr ' /' '__' <<<"$1").log"; shift
  if "$@" >"$log" 2>&1; then record "$name" passed "" null "$log"; else record "$name" failed "exit status $?" null "$log"; fi
}

# bats_step <name> <files...>: pass by exit status, with the number of tests it ran
bats_step() {
  local name="$1" log="$out/$(tr ' /' '__' <<<"$1").log"; shift
  command -v bats >/dev/null || { record "$name" failed "bats is not installed" null /dev/null; return; }
  bats --tap "$@" >"$log" 2>&1; local rc=$?
  local ran ok bad skipped
  ran="$(grep -cE '^(not )?ok ' "$log")"; bad="$(grep -c '^not ok ' "$log")"; skipped="$(grep -cE '^ok .* # skip' "$log")"; ok=$((ran - bad - skipped))
  local counts; counts="$(jq -cn --argjson r "$ran" --argjson p "$ok" --argjson f "$bad" --argjson s "$skipped" '{tests: $r, passed: $p, failed: $f, skipped: $s}')"
  if [ "$ran" -eq 0 ]; then record "$name" failed "no tests ran: an empty inventory proves nothing" "$counts" "$log"
  elif [ "$rc" -ne 0 ] || [ "$bad" -ne 0 ]; then record "$name" failed "$bad failed" "$counts" "$log"
  elif [ "$skipped" -ne 0 ]; then record "$name" skipped "$skipped test(s) skipped" "$counts" "$log"
  else record "$name" passed "" "$counts" "$log"; fi
}

want make-check && step "make check" make check

# every Go test, counted
want go-tests && {
golog="$out/go_tests.jsonl"
go test ./... -count=1 -timeout=600s -json >"$golog" 2>"$out/go_tests.stderr"; gorc=$?
# TestDrillRotateHelper is the child process of a drill, which skips unless the drill starts it: not a check that was skipped
helpers='["TestDrillRotateHelper"]'
counts="$(jq -sc --argjson helpers "$helpers" '[.[] | select(.Test != null and (.Test as $t | $helpers | index($t) | not) and (.Action == "pass" or .Action == "fail" or .Action == "skip"))]
  | {tests: length, passed: map(select(.Action == "pass")) | length, failed: map(select(.Action == "fail")) | length, skipped: map(select(.Action == "skip")) | length}' "$golog")"
if [ "$(jq .tests <<<"$counts")" -eq 0 ]; then record "go tests" failed "no tests ran" "$counts" "$golog"
elif [ "$gorc" -ne 0 ]; then record "go tests" failed "$(jq .failed <<<"$counts") failed" "$counts" "$golog"
elif [ "$(jq .skipped <<<"$counts")" -ne 0 ]; then
  record "go tests" skipped "$(jq .skipped <<<"$counts") test(s) skipped on this platform: $(jq -sr --argjson helpers "$helpers" '[.[] | select(.Action == "skip" and .Test != null and (.Test as $t | $helpers | index($t) | not)) | .Test] | unique | join(", ")' "$golog" | cut -c1-300)" "$counts" "$golog"
else record "go tests" passed "" "$counts" "$golog"; fi
}

want drills && bats_step "kill drills (random moments, named boundaries)" tests/kill_drill.bats tests/barrier_drill.bats

# the independent conformance scripts: stdlib only, except the certificate one
venv="$out/.spec-venv"
if python3 -c "import cryptography" >/dev/null 2>&1; then
  py=python3
elif python3 -m venv "$venv" >/dev/null 2>&1 && "$venv/bin/pip" install -q cryptography==50.0.1 >/dev/null 2>&1; then
  py="$venv/bin/python"
else
  py=""
fi
want conformance && for s in verify_vectors rebuild_vectors audit_chain_vectors; do
  log="$out/$s.log"
  if [ "$s" = verify_vectors ] && [ -z "$py" ]; then record "conformance: $s" failed "the cryptography package could not be installed" null /dev/null; continue; fi
  interp=python3; [ "$s" = verify_vectors ] && interp="$py"
  if "$interp" "docs/spec/$s.py" --json "$out/$s.json" >"$log" 2>&1; then
    record "conformance: $s" passed "" "$(jq -c '{vectors: .count, failed: (.failed | length), minimum: .minimum}' "$out/$s.json")" "$log"
  else
    record "conformance: $s" failed "see the log" "$(jq -c '{vectors: .count, failed: (.failed | length), minimum: .minimum}' "$out/$s.json" 2>/dev/null || echo null)" "$log"
  fi
done
rm -rf "$venv"

want verifier && bats_step "packaged verifier on a fresh mirror (verify and rebuild)" tests/verify_action_release.bats

want vuln && { if command -v govulncheck >/dev/null; then step "vulnerability scan" govulncheck ./...; else record "vulnerability scan" failed "govulncheck is not installed" null /dev/null; fi; }

record "live agent runs (opt-in: make smoke-claude, smoke-codex)" not_run "need the owner's agent credentials and cost money; run them by hand and record the result" null /dev/null

# the record
commit="$(git rev-parse HEAD)"
dirty=false; [ -n "$(git status --porcelain)" ] && dirty=true
fixtures="$(for f in docs/spec/vectors/*.json docs/spec/rebuild-vectors/*.json docs/spec/audit-chain-vectors.json; do
  jq -cn --arg path "$f" --arg sha "$(shasum -a 256 "$f" | cut -d' ' -f1)" '{path: $path, sha256: $sha}'; done | jq -sc .)"
ver() { "$@" 2>&1 | head -1; }
partial=false; [ -n "${EVIDENCE_STEPS:-}" ] && partial=true
jq -n --arg commit "$commit" --argjson partial "$partial" --argjson dirty "$dirty" --arg os "$(uname -s)" --arg arch "$(uname -m)" \
  --arg go "$(go version)" --arg git "$(ver git --version)" --arg bats "$(ver bats --version)" --arg python "$(ver python3 --version)" \
  --arg node "$(ver node --version)" --arg lint "$(ver golangci-lint --version)" --arg vuln "$(ver govulncheck -version)" \
  --arg ssh "$(ssh -V 2>&1 | head -1)" --argjson fixtures "$fixtures" --slurpfile steps "$steps" '
  {schema: 1, commit: $commit, tree_clean: ($dirty | not),
   platform: {os: $os, arch: $arch},
   tools: {go: $go, git: $git, bats: $bats, python: $python, node: $node, golangci_lint: $lint, govulncheck: $vuln, ssh: $ssh},
   fixtures: $fixtures,
   steps: $steps,
   summary: {passed: ($steps | map(select(.status == "passed")) | length),
             failed: ($steps | map(select(.status == "failed")) | length),
             skipped: ($steps | map(select(.status == "skipped")) | length),
             not_run: ($steps | map(select(.status == "not_run")) | length)}}
  | .partial = $partial
  | .overall = (if .partial then "partial" elif .summary.failed == 0 and .summary.skipped == 0 then "passed" else "not passed" end)' >"$out/evidence.json"
rm -f "$steps"
echo
jq -r '"commit \(.commit[0:12]) (clean tree: \(.tree_clean)) on \(.platform.os)/\(.platform.arch): \(.summary.passed) passed, \(.summary.failed) failed, \(.summary.skipped) skipped, \(.summary.not_run) not run: \(.overall)"' "$out/evidence.json"
[ "$(jq -r .overall "$out/evidence.json")" = passed ]
