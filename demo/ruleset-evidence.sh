#!/usr/bin/env bash
# ruleset-evidence.sh <owner>/<repo> - read back what GitHub did with the fixture's pull requests (demo/ruleset-fixture.sh) and
# keep it: for each pull request its URL, head SHA, the required check "staircase-admission" (conclusion, run URL, attempt),
# GitHub's mergeability, the verifier version the run installed and the release asset's digest; then the rules in force on each base
# branch and the ruleset as GitHub holds it. Reads only, with curl and jq (no gh): GET requests to the GitHub REST API.
#
# A public test repository needs no login. A private one, the run logs and a ruleset's bypass list need a token that can read it:
# put it in GITHUB_TOKEN (read-only scopes are enough: Actions, Pull requests and Administration read for a fine-grained token).
# The token is handed to curl on its standard input, never as an argument, and is never printed. Without a token the verifier
# version comes from the workflow file on the base branch, and the report says so.
#
# Usage: ./demo/ruleset-evidence.sh owner/test-repo > evidence.md
# Environment: GITHUB_TOKEN (optional), API_URL (default https://api.github.com), POLL_SECONDS (wait for GitHub to compute mergeability)
set -euo pipefail
REPO="${1:?usage: ruleset-evidence.sh <owner>/<repo>}"
[[ "$REPO" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "the repository must be owner/name" >&2; exit 2; }
command -v curl >/dev/null && command -v jq >/dev/null || { echo "curl and jq are needed" >&2; exit 2; }
API="${API_URL:-https://api.github.com}"
CHECK=staircase-admission
ACTION_REPO=b070nd/stAirCase

api() { # api <path>: GET, JSON (or the text of a log); the token travels as a curl config on stdin
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    printf 'header = "Authorization: Bearer %s"\n' "$GITHUB_TOKEN" |
      curl -fsSL -K - -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' "$API/$1"
  else
    curl -fsSL -H 'Accept: application/vnd.github+json' -H 'X-GitHub-Api-Version: 2022-11-28' "$API/$1"
  fi
}

echo "# Admission evidence: $REPO"
echo
echo "Read at $(date -u +%Y-%m-%dT%H:%M:%SZ) with \`ruleset-evidence.sh\` ($([ -n "${GITHUB_TOKEN:-}" ] && echo "with a token" || echo "without a token")); the required check is \`$CHECK\`. Nothing here was edited."
echo
echo "| PR | head -> base | head SHA | check | run (attempt) | mergeability | verifier |"
echo "|---|---|---|---|---|---|---|"
details=""; bases=""
prs="$(api "repos/$REPO/pulls?state=all&per_page=100")"
while IFS= read -r pr; do
  num="$(jq -r .number <<<"$pr")"; sha="$(jq -r .head.sha <<<"$pr")"; base="$(jq -r .base.ref <<<"$pr")"
  bases+="$base"$'\n'
  merge='{}'
  for _ in 1 2 3 4 5; do   # GitHub computes mergeability when asked: null means not yet
    merge="$(api "repos/$REPO/pulls/$num")"
    [ "$(jq -r '.mergeable // "null"' <<<"$merge")" != null ] && break
    sleep "${POLL_SECONDS:-2}"
  done
  runs="$(api "repos/$REPO/actions/runs?head_sha=$sha&per_page=100")"
  run="$(jq -c --argjson n "$num" '[.workflow_runs[] | select(any(.pull_requests[]?; .number == $n)) | select(.path | endswith("staircase-admission.yml"))] | sort_by(.created_at) | last // {}' <<<"$runs")"
  rid="$(jq -r '.id // empty' <<<"$run")"
  conclusion="no run"; version="-"; digest="null"
  if [ -n "$rid" ]; then
    job="$(api "repos/$REPO/actions/runs/$rid/jobs" | jq -c --arg c "$CHECK" '[.jobs[] | select(.name == $c)] | last // {}')"
    conclusion="$(jq -r '.conclusion // .status // "unknown"' <<<"$job")"; jid="$(jq -r '.id // empty' <<<"$job")"
    if [ -n "$jid" ] && log="$(api "repos/$REPO/actions/jobs/$jid/logs" 2>/dev/null)" && v="$(grep -o 'Installed staircase v[0-9][0-9A-Za-z.+-]*' <<<"$log" | head -1 | sed 's/Installed staircase //')" && [ -n "$v" ]; then
      version="$v (run log)"
    elif v="$(api "repos/$REPO/contents/.github/workflows/staircase-admission.yml?ref=$base" 2>/dev/null | jq -r '.content // empty' | base64 -d 2>/dev/null | grep -o "uses: $ACTION_REPO@[^ ]*" | head -1 | sed 's/.*@//')" && [ -n "$v" ]; then
      version="$v (workflow on $base)"
    else
      version="unknown"
    fi
    tag="${version%% *}"
    if [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+ ]]; then
      digest="$(api "repos/$ACTION_REPO/releases/tags/$tag" 2>/dev/null | jq -c '[.assets[] | select(.name | test("linux_amd64.tar.gz$")) | {name, digest}]' || echo null)"
    fi
  fi
  printf '| [#%s](%s) | %s -> %s | `%s` | %s | %s (attempt %s) | %s / %s | %s |\n' "$num" "$(jq -r .html_url <<<"$pr")" \
    "$(jq -r .head.ref <<<"$pr")" "$base" "${sha:0:12}" "$conclusion" \
    "$(jq -r '.html_url // "-"' <<<"$run")" "$(jq -r '.run_attempt // "-"' <<<"$run")" \
    "$(jq -r '.mergeable_state // "unknown"' <<<"$merge")" "$(jq -r 'if .mergeable == true then "mergeable" elif .mergeable == false then "not mergeable" else "not computed" end' <<<"$merge")" "$version"
  details+="$(jq -n --argjson pr "$pr" --argjson run "$run" --argjson merge "$merge" --arg conclusion "$conclusion" --arg version "$version" --argjson digest "${digest:-null}" \
    '{pr: $pr.number, state: $pr.state, head: $pr.head.sha, base: $pr.base.ref, check: $conclusion, mergeable: $merge.mergeable, mergeable_state: $merge.mergeable_state,
      run: {id: $run.id, url: $run.html_url, attempt: $run.run_attempt, event: $run.event, path: $run.path, head_sha: $run.head_sha}, verifier: $version, release_asset: $digest}')"$'\n'
done < <(jq -c 'sort_by(.number) | .[]' <<<"$prs")

echo
echo "## Per pull request"
echo
echo '```json'
printf '%s' "$details" | jq -s .
echo '```'
echo
echo "## Rules in force on each base branch"
echo
echo '```json'
for b in $(printf '%s' "$bases" | sort -u); do
  jq -n --arg b "$b" --argjson r "$(api "repos/$REPO/rules/branches/$b" 2>/dev/null || echo null)" '{branch: $b, rules: $r}'
done | jq -s .
echo '```'
echo
echo "## Rulesets"
echo
echo '```json'
ids="$(api "repos/$REPO/rulesets" 2>/dev/null | jq -r '.[].id' || true)"
for id in $ids; do
  api "repos/$REPO/rulesets/$id" | jq '{id, name, target, enforcement, conditions, bypass_actors: (.bypass_actors // "not visible to this reader (needs a token that can administer the repository)"), rules}'
done | jq -s .
echo '```'
