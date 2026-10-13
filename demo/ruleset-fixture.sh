#!/usr/bin/env bash
# ruleset-fixture.sh <outdir> [version] - build a test repository for running the real stAirCase verify
# Action in GitHub under a ruleset, with the pull requests to open and what each must do.
#
# It runs the offline demo (a real run: a certified commit and its git notes), seals a second change with the same
# workspace key (twice), then makes a repository with the trusted key and the workflows, and branches for five cases:
#
#   pr-certified        -> main        a certified commit (CAL 3), rebuilt from its ledger: the check PASSES
#   pr-no-notes         -> main        a commit with no certificate: REFUSED
#   pr-sealed           -> main        certified (CAL 2) by main's key, which main accepts: PASSES
#   pr-wrong-signer     -> other-key   certified (CAL 2) by the workspace key, but the base trusts another key: REFUSED
#   pr-below-strict     -> strict      certified (CAL 2) by the trusted key, but strict requires CAL 4: REFUSED
#   pr-edits-the-check  -> main        an uncertified change that also turns the check down: REFUSED (the PR's edit is not used)
#
# Why a head commit of its own for each: GitHub attaches check runs to a COMMIT, not to a pull request, so pull requests that share a
# head commit share their results, and a required check that must pass would be polluted by the ones that must fail.
#
# Two workflows, two different things:
#   staircase-admission.yml  the REQUIRED CHECK ("staircase-admission"): the Action alone, so its refusal fails the check and a
#                            ruleset that requires it blocks the merge. It runs on pull_request_target, which GitHub runs from the
#                            DEFAULT branch whatever the pull request's base: so there is one copy, on main, and it chooses
#                            min-cal from the base branch (4 for strict, else 2). The key is read from the base branch.
#   staircase-verify.yml     the conformance harness: runs each case with continue-on-error and asserts the outcome. It tests
#                            the Action; it blocks nothing, and its green is not evidence (a crash is also a failure).
# Nothing here pushes anything: the README it writes lists the commands for you to run in your own test repository.
#
# Environment: FIXTURE_GIT_NAME / FIXTURE_GIT_EMAIL (the identity of every commit), FIXTURE_ACTION_REF (what `uses:` names,
# default the version: a commit SHA while a fix is not released), FIXTURE_VERIFIER_VERSION (the release the Action installs,
# default the version).
# Usage: ./demo/ruleset-fixture.sh /tmp/staircase-ruleset-fixture [version]   (default version: v1.0.1)
set -euo pipefail
OUT="${1:?usage: ruleset-fixture.sh <outdir> [version]}"
VERSION="${2:-v1.0.1}"
ACTION_REF="${FIXTURE_ACTION_REF:-$VERSION}"
VERIFIER="${FIXTURE_VERIFIER_VERSION:-$VERSION}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ ! -e "$OUT" ] || { echo "$OUT exists: choose a new directory" >&2; exit 1; }

# the identity of every commit the fixture holds: a public test repository should carry the owner's, not a made-up one
FIXTURE_NAME="${FIXTURE_GIT_NAME:-ruleset fixture}"; FIXTURE_EMAIL="${FIXTURE_GIT_EMAIL:-fixture@example.com}"
demo_out="$(cd "$REPO_ROOT" && DEMO_GIT_NAME="$FIXTURE_NAME" DEMO_GIT_EMAIL="$FIXTURE_EMAIL" DEMO_KEEP=1 ./demo/run-demo.sh --auto 2>&1)"
KEPT="$(printf '%s\n' "$demo_out" | sed -n 's/^DEMO_KEPT=//p' | tail -1)"
[ -n "$KEPT" ] && [ -d "$KEPT/app" ] || { echo "the demo kept nothing" >&2; exit 1; }
trap 'rm -rf "$KEPT"' EXIT

# Three more certified changes, made with the SAME workspace key: each staged in the demo's checkout and sealed (CAL 2, with its own
# certificate and ledger). They are the heads of the pull request that main accepts at CAL 2 and of the two that must be refused for a reason of trust or level,
# one each, so that no two pull requests share a commit (a check attaches to a commit, and shared heads share and mix their results).
app="$KEPT/app"
base_demo="$(git -C "$app" rev-parse staircase/run-1^)"
printf '{"rules":[{"action_types":["file_edit"],"effect":"approve"}]}\n' >"$KEPT/.staircase/policy.json"
seal_change() {   # <branch> <file>
  git -C "$app" checkout -q -b "$1" "$base_demo"
  printf 'A change accepted after it was made (staircase seal): CAL 2.\n' >"$app/$2"
  git -C "$app" add "$2"
  (cd "$app" && STAIRCASE_DIR="$KEPT/.staircase" NO_COLOR=1 "$KEPT/staircase" seal -m "a change accepted after it was made ($2)" --by "an editor agent" --allow "$2" --yes >"$KEPT/seal-$1.log" 2>&1) \
    || { cat "$KEPT/seal-$1.log" >&2; echo "seal failed" >&2; exit 1; }
}
seal_change fixture-sealed SEALED.md
seal_change fixture-wrong-signer WRONG-SIGNER.md
seal_change fixture-below-strict BELOW-STRICT.md

mkdir -p "$OUT"
repo="$OUT/test-repo"
git clone -q "$app" "$repo" 2>/dev/null
cd "$repo"
git config user.email "$FIXTURE_EMAIL"
git config user.name "$FIXTURE_NAME"
git fetch -q origin '+refs/notes/*:refs/notes/*'
certified="$(git rev-parse origin/staircase/run-1)"
sealed="$(git rev-parse origin/fixture-sealed)"
wrong="$(git rev-parse origin/fixture-wrong-signer)"
below="$(git rev-parse origin/fixture-below-strict)"
base="$(git rev-parse "$certified^")"          # the demo's base commit: the certified commit's parent
for c in "$sealed" "$wrong" "$below"; do
  [ "$(git rev-parse "$c^")" = "$base" ] || { echo "a sealed commit is not on the demo's base" >&2; exit 1; }
done
git checkout -q -B main "$base"

mkdir -p .github/workflows
cp "$KEPT/.staircase/.signing.pub" .github/staircase.pub
cat >.github/workflows/staircase-verify.yml <<YML
name: stAirCase verify (ruleset fixture, conformance harness)
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
      - uses: b070nd/stAirCase@$ACTION_REF
        with: { version: $VERIFIER, key: .github/staircase.pub, rebuild: "true", min-cal: "2" }
  below-the-required-level-is-refused:
    if: github.head_ref == 'pr-below-strict' && github.base_ref == 'strict'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - id: verify
        continue-on-error: true
        uses: b070nd/stAirCase@$ACTION_REF
        with: { version: $VERIFIER, key: .github/staircase.pub, min-cal: "4" }
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
        uses: b070nd/stAirCase@$ACTION_REF
        with: { version: $VERIFIER, key: .github/staircase.pub }
      - name: The Action must have refused
        run: test "\${{ steps.verify.outcome }}" = failure
  wrong-trusted-key-is-refused:
    if: github.head_ref == 'pr-wrong-signer' && github.base_ref == 'other-key'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - id: verify
        continue-on-error: true
        uses: b070nd/stAirCase@$ACTION_REF
        with: { version: $VERIFIER, key: .github/staircase.pub }
      - name: The Action must have refused
        run: test "\${{ steps.verify.outcome }}" = failure
YML
cat >.github/workflows/staircase-admission.yml <<YML
name: staircase admission
# pull_request_target runs the workflow of the DEFAULT branch whatever the pull request's base, so a pull request cannot edit the
# check it must pass, and this one copy decides for every base: min-cal is chosen from the base branch's name. The job's name carries the base
# too, because GitHub attaches a check to a COMMIT: a run for one base must not satisfy the required check of another. The pull request is
# checked out only so that the verifier can read its commits (nothing of it is executed); the key is read from the base branch.
on: pull_request_target
permissions:
  contents: read
  attestations: read
jobs:
  staircase-admission:
    name: staircase-admission-\${{ github.base_ref }}
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { ref: "\${{ github.event.pull_request.head.sha }}", fetch-depth: 0 }
      - uses: b070nd/stAirCase@$ACTION_REF
        with:
          version: $VERIFIER
          key: .github/staircase.pub
          rebuild: "true"
          min-cal: "\${{ github.base_ref == 'strict' && '4' || '2' }}"
YML
cat >.github/README.md <<'RM'
# staircase-ruleset

A test repository for the [stAirCase](https://github.com/b070nd/stAirCase) verify Action under a GitHub ruleset. It is built by
`demo/ruleset-fixture.sh` in that repository; it is not a project, and nothing here should be copied as an example of an application.

**The rule it tests:** a pull request into `main`, `other-key` or `strict` can be merged only if the Action says its commits are covered by
a change certificate signed by the key that base trusts, at the level that base requires. Each base has its own ruleset and its own required
check (`staircase-admission-main`, `staircase-admission-other-key`, `staircase-admission-strict`).

| Pull request (head into base) | The check | Why |
|---|---|---|
| `pr-certified` into `main` | passes, merge allowed | a real certified commit (CAL 3), rebuilt from its ledger |
| `pr-sealed` into `main` | passes, merge allowed | a change certified after the fact (CAL 2), which `main` accepts |
| `pr-wrong-signer` into `other-key` | **fails, blocked** | certified, but this base trusts another key |
| `pr-below-strict` into `strict` | **fails, blocked** | certified at CAL 2, and `strict` requires CAL 4 |
| `pr-no-notes` into `main` | **fails, blocked** | a commit with no certificate |
| `pr-edits-the-check` into `main` | **fails, blocked** | it also edits the check's own workflow; the copy on `main` runs, so the edit is not used |

Every pull request has a head commit of its own, and the required check is named for its base. GitHub attaches a check to a commit, not to a pull
request: an earlier build of this repository shared one commit between a pull request into `main` and one into `other-key`, and with one check
name the pass for `main` unlocked the merge into `other-key`, which must have been refused. Open no pull request from `other-key` or `strict`: they
are bases, not heads.

The results, the run logs they come from and what GitHub answered to a merge of a blocked pull request are recorded in
[docs/release-evidence](https://github.com/b070nd/stAirCase/tree/master/docs/release-evidence) of the stAirCase repository.
RM
git add -A && git commit -q -m "the trusted key, the admission check and the conformance harness"

# the two certified commits are the demo's own (a certificate is a note on the commit's hash), on the base main grew from
git branch -f pr-certified "$certified"
git branch -f pr-sealed "$sealed"
git branch -f pr-wrong-signer "$wrong"
git branch -f pr-below-strict "$below"
# a commit with no certificate
git checkout -q -b pr-no-notes "$base"
echo "an agent-assisted change nobody certified" >uncertified.txt
git add uncertified.txt && git commit -q -m "an uncertified change"
# a base that trusts another key
git checkout -q -b other-key main
head -c 32 /dev/urandom >.github/staircase.pub
git add -A && git commit -q -m "trust another key"
# a base that requires CAL 4 (the admission workflow on main reads the branch's name)
git checkout -q -b strict main
echo "Pull requests into this branch need CAL 4: the admission check on main chooses min-cal from the base branch's name." >STRICT.md
git add -A && git commit -q -m "this branch requires CAL 4"
# a pull request that weakens the check from inside: its own copy of the workflow is not the one that runs
git checkout -q -b pr-edits-the-check main
sed -i.bak 's/^          min-cal: .*/          min-cal: "1"\n          all: "false"/' .github/workflows/staircase-admission.yml
rm -f .github/workflows/staircase-admission.yml.bak
echo "an agent-assisted change nobody certified, with the check turned down" >uncertified-edit.txt
git add -A && git commit -q -m "an uncertified change that turns the check down"
git checkout -q main
git remote remove origin   # it pointed at the demo's scratch folder; you add your own

for base in main other-key strict; do
cat >"$OUT/ruleset-$base.json" <<JSON
{
  "name": "staircase-admission-$base",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/heads/$base"], "exclude": [] } },
  "bypass_actors": [],
  "rules": [
    { "type": "pull_request", "parameters": { "required_approving_review_count": 0, "dismiss_stale_reviews_on_push": false,
        "require_code_owner_review": false, "require_last_push_approval": false, "required_review_thread_resolution": false } },
    { "type": "required_status_checks", "parameters": { "strict_required_status_checks_policy": false,
        "required_status_checks": [ { "context": "staircase-admission-$base" } ] } },
    { "type": "non_fast_forward" },
    { "type": "deletion" }
  ]
}
JSON
done

cat >"$OUT/README.md" <<MD
# stAirCase verify Action: ruleset test fixture

Built by \`demo/ruleset-fixture.sh\` (the Action at \`$ACTION_REF\`, installing the verifier \`$VERIFIER\`). Nothing in it was pushed.

It holds two different things. **Enforcement:** one required check per base (\`staircase-admission-main\`, \`-other-key\`, \`-strict\`) and a ruleset for each that requires it,
so that a pull request the verifier refuses cannot be merged. **Conformance:** the \`staircase-verify\` harness, which runs each case with
continue-on-error and asserts the Action failed; it tests the Action and blocks nothing, and **its green is not evidence** (a crash is
also a failure). Read the admission check.

## 1. Push it to YOUR test repository

\`\`\`bash
cd test-repo
git remote add origin <the URL of your test repository>
git push origin main other-key strict pr-certified pr-sealed pr-wrong-signer pr-below-strict pr-no-notes pr-edits-the-check
git push origin refs/notes/staircase refs/notes/staircase-ledger   # the certificates and ledgers
\`\`\`

Open the pull requests below and let the checks run once, so that GitHub knows the checks' names; then import the three files
\`ruleset-main.json\`, \`ruleset-other-key.json\` and \`ruleset-strict.json\` (Settings, Rules, Rulesets, New ruleset, Import a ruleset). Each
requires a pull request and its own base's check, allows no bypass, and forbids deleting or force-pushing that branch. **Why one each:** GitHub
attaches a check to a commit, so a single shared check name would let a pass for one base unlock the same commit for another. The ruleset targets branches: the notes refs are
not covered by it, and the certificates are trusted by their signature, not by where they are.

## 2. Open these pull requests, and read what each does

| Head | Base | The base's required check | Merge | Why |
|---|---|---|---|---|
| pr-certified | main | passes | **eligible** | a real certified commit (CAL 3), rebuilt from its ledger, key trusted from main |
| pr-sealed | main | passes | **eligible** | a change certified after the fact (CAL 2), which main accepts, signed by main's key |
| pr-no-notes | main | **fails** | **blocked** | a commit with no certificate |
| pr-wrong-signer | other-key | **fails** | **blocked** | wrong signer: the base trusts another key, so the certificate is not trusted |
| pr-below-strict | strict | **fails** | **blocked** | insufficient CAL: a sealed change is CAL 2, strict requires 4 |
| pr-edits-the-check | main | **fails** | **blocked** | the pull request edits the check's own workflow (min-cal 1, all false); that edit is not used |

Each scenario has its own head commit, and that matters: GitHub attaches a check to a commit whatever the base, so two pull requests from one
head would share their checks, and a red one for another base would show a green one as blocked. An earlier build of this fixture shared
one head between a pull request into main and one into other-key, and its single check name let the pass for main unlock the merge into other-key
(a refused change merged). Now the check is named for its base as well, so a stray second pull request from one head cannot do that either.
Never open a pull request from other-key or strict: they are bases, not heads, and GitHub's "Compare & pull request" banner offers them after a push.

**What \`pull_request_target\` runs.** GitHub runs the workflow of the repository's **default branch** (main) for every pull request,
whatever its base, and its \`GITHUB_SHA\` and \`GITHUB_REF\` are the default branch's. So a copy of the workflow on another branch never
runs (an earlier version of this fixture had copies on strict and other-key, and a pull request into strict was checked at CAL 2),
and a pull request that edits the workflow is simply not using its edit. The one copy on main chooses \`min-cal\` from the base
branch's name. The setup is only safe because nothing of the pull request is executed: keep it that way. Protect \`.github/\` too
(a ruleset or CODEOWNERS rule that requires review) so main's workflow cannot be changed unreviewed.

## 3. Record

\`\`\`bash
./demo/ruleset-evidence.sh <owner>/<test-repo> > evidence.md      # from the stAirCase checkout: curl and jq, no gh
\`\`\`

It reads GitHub's REST API with curl. A public test repository needs no login; for a private one, and for the run logs and the ruleset's
bypass list, put a read-only token in \`GITHUB_TOKEN\` (it goes to curl on stdin and is never printed). For each pull request it writes the
URL, the head SHA, the state of the required check and its run URL and attempt, GitHub's mergeability (blocked or clean), the verifier
version the run installed (from the run log with a token, else from the workflow file on the default branch) and the release asset's
digest; then the rules in force on each base and the rulesets as GitHub holds them. Read it before you keep it: it holds no token.
The certified commit is $certified; the sealed ones are $sealed and $below.
MD
echo "fixture written to $OUT (test-repo/ and README.md)"
