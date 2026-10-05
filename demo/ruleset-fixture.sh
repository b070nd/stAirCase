#!/usr/bin/env bash
# ruleset-fixture.sh <outdir> - build a test repository for running the real stAirCase verify
# Action in GitHub, with the pull requests to open and what each must do.
#
# It runs the offline demo (a real run: a certified commit and its git notes), then makes a
# repository with the trusted key and a workflow, and branches for four cases:
#
#   pr-certified        -> main        the certified commit, rebuilt from its ledger: the check PASSES
#   pr-low-assurance    -> main        the same commit with min-cal 4: it must be REFUSED
#   pr-no-notes         -> main        a commit with no certificate: it must be REFUSED
#   pr-certified        -> other-key   a base that trusts another key: it must be REFUSED
#
# The workflow runs each case's Action step with continue-on-error and then asserts the outcome,
# so every case's check is GREEN when the Action did the right thing and RED when it did not.
# Nothing here pushes anything: the README it writes lists the commands for you to run in your
# own test repository (with whatever ruleset you want to exercise).
#
# Usage: ./demo/ruleset-fixture.sh /tmp/staircase-ruleset-fixture [version]   (default version: v0.9.0)
set -euo pipefail
OUT="${1:?usage: ruleset-fixture.sh <outdir> [version]}"
VERSION="${2:-v0.9.0}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ ! -e "$OUT" ] || { echo "$OUT exists: choose a new directory" >&2; exit 1; }

demo_out="$(cd "$REPO_ROOT" && DEMO_KEEP=1 ./demo/run-demo.sh --auto 2>&1)"
KEPT="$(printf '%s\n' "$demo_out" | sed -n 's/^DEMO_KEPT=//p' | tail -1)"
[ -n "$KEPT" ] && [ -d "$KEPT/app" ] || { echo "the demo kept nothing" >&2; exit 1; }
trap 'rm -rf "$KEPT"' EXIT

mkdir -p "$OUT"
repo="$OUT/test-repo"
git clone -q "$KEPT/app" "$repo" 2>/dev/null
cd "$repo"
git config user.email fixture@example.com
git config user.name "ruleset fixture"
git fetch -q origin 'refs/notes/*:refs/notes/*'
certified="$(git rev-parse origin/staircase/run-1)"
base="$(git rev-parse "$certified^")"          # the demo's base commit: the certified commit's parent
git checkout -q -B main "$base"

mkdir -p .github/workflows
cp "$KEPT/.staircase/.signing.pub" .github/staircase.pub
cat >.github/workflows/staircase-verify.yml <<YML
name: stAirCase verify (ruleset fixture)
on: pull_request
permissions:
  contents: read
jobs:
  certified-range-passes:
    if: github.head_ref == 'pr-certified' && github.base_ref == 'main'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: b070nd/stAirCase@$VERSION
        with: { key: .github/staircase.pub, rebuild: "true", min-cal: "2" }
  below-the-required-level-is-refused:
    if: github.head_ref == 'pr-low-assurance'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - id: verify
        continue-on-error: true
        uses: b070nd/stAirCase@$VERSION
        with: { key: .github/staircase.pub, min-cal: "4" }
      - name: The Action must have refused
        run: test "\${{ steps.verify.outcome }}" = failure
  no-certificate-is-refused:
    if: github.head_ref == 'pr-no-notes'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - id: verify
        continue-on-error: true
        uses: b070nd/stAirCase@$VERSION
        with: { key: .github/staircase.pub }
      - name: The Action must have refused
        run: test "\${{ steps.verify.outcome }}" = failure
  wrong-trusted-key-is-refused:
    if: github.head_ref == 'pr-certified' && github.base_ref == 'other-key'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - id: verify
        continue-on-error: true
        uses: b070nd/stAirCase@$VERSION
        with: { key: .github/staircase.pub }
      - name: The Action must have refused
        run: test "\${{ steps.verify.outcome }}" = failure
YML
git add -A && git commit -q -m "the trusted key and the verify workflow"

# the certified commit is the demo's own (its certificate is a note on its hash), on the base main grew from
git branch -f pr-certified "$certified"
git branch -f pr-low-assurance "$certified"
# a commit with no certificate
git checkout -q -b pr-no-notes "$base"
echo "an agent-assisted change nobody certified" >uncertified.txt
git add uncertified.txt && git commit -q -m "an uncertified change"
# a base that trusts another key
git checkout -q -b other-key main
head -c 32 /dev/urandom >.github/staircase.pub
git add -A && git commit -q -m "trust another key"
git checkout -q main
git remote remove origin   # it pointed at the demo's scratch folder; you add your own

cat >"$OUT/README.md" <<MD
# stAirCase verify Action: ruleset test fixture

Built by \`demo/ruleset-fixture.sh\` (verifier version: $VERSION). Nothing in it was pushed.

## 1. Push it to YOUR test repository

\`\`\`bash
cd test-repo
git remote add origin <the URL of your test repository>
git push origin main other-key pr-certified pr-low-assurance pr-no-notes
git push origin refs/notes/staircase refs/notes/staircase-ledger   # the certificates and ledgers
\`\`\`

Turn on whatever ruleset you want to exercise (for example: require the "certified-range-passes"
check on main, and protect the notes refs).

## 2. Open these pull requests

| Head | Base | The check must be | Why |
|---|---|---|---|
| pr-certified | main | **green: certified-range-passes** | a real certified commit, rebuilt from its ledger, trusted key from main |
| pr-low-assurance | main | green: below-the-required-level-is-refused | the same commit with min-cal 4: the Action must refuse (step outcome failure) |
| pr-no-notes | main | green: no-certificate-is-refused | a commit with no certificate: the Action must refuse |
| pr-certified | other-key | green: wrong-trusted-key-is-refused | the base trusts another key, so the certificate is not trusted: the Action must refuse |

The three refusal cases run the Action with continue-on-error and then assert that it failed, so a GREEN
check means "the Action refused as it should" and a RED check means it did not.

## 3. Record

For each pull request: the repository and PR URL, the run URL and attempt, the head and base SHAs, the
Action ref and verifier version (the run log prints "Installed staircase vX (build attestation verified)"),
and the check's conclusion. The certified commit is $certified.
MD
echo "fixture written to $OUT (test-repo/ and README.md)"
