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
# Two workflows, two different things:
#   staircase-admission.yml  the REQUIRED CHECK ("staircase-admission"): the Action alone, so its refusal fails the check and a
#                            ruleset that requires it blocks the merge. One copy per base: main (CAL 2), other-key (another
#                            trusted key), strict (CAL 4). ruleset.json is the ruleset to import.
#   staircase-verify.yml     the conformance harness: runs each case with continue-on-error and asserts the outcome, so every
#                            check is GREEN when the Action did the right thing. It tests the Action; it blocks nothing.
# Nothing here pushes anything: the README it writes lists the commands for you to run in your
# own test repository (with whatever ruleset you want to exercise).
#
# Usage: ./demo/ruleset-fixture.sh /tmp/staircase-ruleset-fixture [version]   (default version: v0.11.0)
set -euo pipefail
OUT="${1:?usage: ruleset-fixture.sh <outdir> [version]}"
VERSION="${2:-v0.11.0}"
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
  attestations: read
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
admission() { # <min-cal>
  cat >.github/workflows/staircase-admission.yml <<YML
name: staircase admission
# pull_request_target runs the workflow of the BASE branch, so a pull request cannot edit the check it must pass. Nothing of the pull
# request is executed: it is checked out only so that the verifier can read its commits, and the key is read from the base.
on: pull_request_target
permissions:
  contents: read
  attestations: read
jobs:
  staircase-admission:
    name: staircase-admission
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { ref: "\${{ github.event.pull_request.head.sha }}", fetch-depth: 0 }
      - uses: b070nd/stAirCase@$VERSION
        with: { key: .github/staircase.pub, rebuild: "true", min-cal: "$1" }
YML
}
admission 2
git add -A && git commit -q -m "the trusted key, the admission check and the conformance harness"

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
# a base that requires CAL 4
git checkout -q -b strict main
admission 4
git add -A && git commit -q -m "require CAL 4"
# a pull request that weakens the check from inside (the check's own workflow is in the pull request)
git checkout -q -b pr-edits-the-check "$base"
mkdir -p .github/workflows
git show main:.github/workflows/staircase-admission.yml | sed 's/min-cal: "2" }/min-cal: "1", all: "false" }/' >.github/workflows/staircase-admission.yml
echo "an agent-assisted change nobody certified, with the check turned down" >uncertified-edit.txt
git add -A && git commit -q -m "an uncertified change that turns the check down"
git checkout -q main
git remote remove origin   # it pointed at the demo's scratch folder; you add your own

cat >"$OUT/ruleset.json" <<'JSON'
{
  "name": "staircase-admission",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/heads/main", "refs/heads/other-key", "refs/heads/strict"], "exclude": [] } },
  "bypass_actors": [],
  "rules": [
    { "type": "pull_request", "parameters": { "required_approving_review_count": 0, "dismiss_stale_reviews_on_push": false,
        "require_code_owner_review": false, "require_last_push_approval": false, "required_review_thread_resolution": false } },
    { "type": "required_status_checks", "parameters": { "strict_required_status_checks_policy": false,
        "required_status_checks": [ { "context": "staircase-admission" } ] } },
    { "type": "non_fast_forward" },
    { "type": "deletion" }
  ]
}
JSON

cat >"$OUT/README.md" <<MD
# stAirCase verify Action: ruleset test fixture

Built by \`demo/ruleset-fixture.sh\` (verifier version: $VERSION). Nothing in it was pushed, and nothing here has been run in GitHub.

It holds two different things. **Enforcement:** the required check \`staircase-admission\` and a ruleset that requires it, so that a
pull request the verifier refuses cannot be merged. **Conformance:** the older \`staircase-verify\` harness, which runs the Action
with continue-on-error and asserts it refused; its checks are green when the Action behaved, so it tests the Action and blocks nothing.

## 1. Push it to YOUR test repository

\`\`\`bash
cd test-repo
git remote add origin <the URL of your test repository>
git push origin main other-key strict pr-certified pr-no-notes pr-edits-the-check pr-low-assurance
git push origin refs/notes/staircase refs/notes/staircase-ledger   # the certificates and ledgers
\`\`\`

Let the first run of the workflow finish on a branch push or a pull request so that GitHub knows the check's name, then import
\`ruleset.json\` (Settings, Rules, Rulesets, New ruleset, Import a ruleset). It requires a pull request and the check
\`staircase-admission\` on main, other-key and strict, allows no bypass, and forbids deleting or force-pushing them. The ruleset
targets branches: the notes refs are not covered by it, and the certificates are trusted by their signature, not by where they are.

## 2. Open these pull requests, and read what each does

| Head | Base | Required check \`staircase-admission\` | Merge | Why |
|---|---|---|---|---|
| pr-certified | main | passes | **eligible** | a real certified commit, rebuilt from its ledger, key trusted from main |
| pr-no-notes | main | **fails** | **blocked** | a commit with no certificate |
| pr-certified | other-key | **fails** | **blocked** | wrong signer: the base trusts another key, so the certificate is not trusted |
| pr-certified | strict | **fails** | **blocked** | insufficient CAL: strict requires CAL 4 |
| pr-edits-the-check | main | **fails** (observe) | **blocked** (observe) | the pull request edits staircase-admission.yml itself (min-cal 1, all false); the base's copy of the workflow should run instead |

The last row is a control. The admission check runs on \`pull_request_target\`, which uses the workflow of the **base** branch, so the
pull request's own edit of staircase-admission.yml (min-cal 1, all false) should be ignored and the check should fail. With a plain
\`pull_request\` trigger the pull request's own copy runs and it would pass (checked locally: the verifier then reports "0 commit(s) checked").
Record what GitHub does. The setup is only safe because nothing of the pull request is executed: keep it that way, and do not add steps that
run its files. Still protect \`.github/\` (a ruleset or CODEOWNERS rule that requires review) so the base's workflow cannot be changed unreviewed.

The conformance harness also runs on these pull requests (its jobs are named certified-range-passes,
below-the-required-level-is-refused, no-certificate-is-refused, wrong-trusted-key-is-refused). Its green is conformance of the Action,
not admission. pr-low-assurance (the same commit at min-cal 4 into main) is its case for the CAL refusal.

## 3. Record

\`\`\`bash
./demo/ruleset-evidence.sh <owner>/<test-repo> > evidence.md      # from the stAirCase checkout: curl and jq, no gh
\`\`\`

It reads GitHub's REST API with curl. A public test repository needs no login; for a private one, and for the run logs and the ruleset's
bypass list, put a read-only token in \`GITHUB_TOKEN\` (it goes to curl on stdin and is never printed). For each pull request it writes the
URL, the head SHA, the state of the required check and its run URL and attempt, GitHub's mergeability (blocked or clean), the verifier
version the run installed (from the run log with a token, else from the workflow file on the base) and the release asset's digest; then the
rules in force on each base and the ruleset as GitHub holds it. Read it before you keep it: it holds no token, only what GitHub reports.
The certified commit is $certified.
MD
echo "fixture written to $OUT (test-repo/ and README.md)"
