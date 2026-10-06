#!/usr/bin/env bash
# ruleset-evidence.sh <owner>/<repo> - read back, with YOUR authenticated gh, what GitHub did with the fixture's pull requests
# (demo/ruleset-fixture.sh) and keep it: for each pull request its URL, head and base, the required check "staircase-admission"
# (conclusion, run URL, attempt), GitHub's merge state, the verifier version the run installed and the release asset's digest;
# then the ruleset as GitHub holds it. Reads only (gh pr list, gh api GET); prints no token. Needs gh (logged in) and jq.
#
# Usage: ./demo/ruleset-evidence.sh owner/test-repo > evidence.md
set -euo pipefail
REPO="${1:?usage: ruleset-evidence.sh <owner>/<repo>}"
[[ "$REPO" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "the repository must be owner/name" >&2; exit 2; }
command -v gh >/dev/null && command -v jq >/dev/null || { echo "gh and jq are needed" >&2; exit 2; }
CHECK=staircase-admission
ACTION_REPO=b070nd/stAirCase

echo "# Admission evidence: $REPO"
echo
echo "Read at $(date -u +%Y-%m-%dT%H:%M:%SZ) with \`ruleset-evidence.sh\`; the required check is \`$CHECK\`. Nothing here was edited."
echo
echo "| PR | head -> base | head SHA | check | run (attempt) | merge state | verifier |"
echo "|---|---|---|---|---|---|---|"
details=""
prs="$(gh pr list -R "$REPO" --state all --limit 100 --json number,url,headRefName,baseRefName,headRefOid,state,mergeable,mergeStateStatus)"
while IFS= read -r pr; do
  num="$(jq -r .number <<<"$pr")"; sha="$(jq -r .headRefOid <<<"$pr")"
  runs="$(gh api "repos/$REPO/actions/runs?head_sha=$sha&per_page=100")"
  run="$(jq -c --argjson n "$num" --arg c "$CHECK" '[.workflow_runs[] | select(any(.pull_requests[]?; .number == $n)) | select(.path | endswith("staircase-admission.yml"))] | sort_by(.created_at) | last // {}' <<<"$runs")"
  rid="$(jq -r '.id // empty' <<<"$run")"
  conclusion="no run"; jid=""; version="-"; digest=""
  if [ -n "$rid" ]; then
    job="$(gh api "repos/$REPO/actions/runs/$rid/jobs" | jq -c --arg c "$CHECK" '[.jobs[] | select(.name == $c)] | last // {}')"
    conclusion="$(jq -r '.conclusion // .status // "unknown"' <<<"$job")"; jid="$(jq -r '.id // empty' <<<"$job")"
    if [ -n "$jid" ]; then
      log="$(gh api "repos/$REPO/actions/jobs/$jid/logs" 2>/dev/null || true)"
      version="$(grep -o 'Installed staircase v[0-9][0-9A-Za-z.+-]*' <<<"$log" | head -1 | sed 's/Installed staircase //' || true)"
      [ -n "$version" ] || version="not in the log"
      if [ "$version" != "not in the log" ]; then
        digest="$(gh api "repos/$ACTION_REPO/releases/tags/$version" 2>/dev/null | jq -c '[.assets[] | select(.name | test("linux_amd64.tar.gz$")) | {name, digest}]' || true)"
      fi
    fi
  fi
  printf '| [#%s](%s) | %s -> %s | `%s` | %s | %s (attempt %s) | %s / %s | %s |\n' "$num" "$(jq -r .url <<<"$pr")" \
    "$(jq -r .headRefName <<<"$pr")" "$(jq -r .baseRefName <<<"$pr")" "${sha:0:12}" "$conclusion" \
    "$(jq -r '.html_url // "-"' <<<"$run")" "$(jq -r '.run_attempt // "-"' <<<"$run")" \
    "$(jq -r .mergeStateStatus <<<"$pr")" "$(jq -r .mergeable <<<"$pr")" "$version"
  details+="$(jq -n --argjson pr "$pr" --argjson run "$run" --arg conclusion "$conclusion" --arg version "$version" --arg digest "${digest:-null}" \
    '{pr: $pr.number, state: $pr.state, head: $pr.headRefOid, check: $conclusion, run: {id: $run.id, url: $run.html_url, attempt: $run.run_attempt,
      event: $run.event, path: $run.path, head_sha: $run.head_sha}, verifier: $version, release_asset: ($digest | fromjson? // null)}')"$'\n'
done < <(jq -c 'sort_by(.number) | .[]' <<<"$prs")

echo
echo "## Per pull request"
echo
echo '```json'
printf '%s' "$details" | jq -s .
echo '```'
echo
echo "## Rulesets"
echo
echo '```json'
ids="$(gh api "repos/$REPO/rulesets" | jq -r '.[].id')"
for id in $ids; do
  gh api "repos/$REPO/rulesets/$id" | jq '{id, name, target, enforcement, conditions, bypass_actors, rules}'
done | jq -s .
echo '```'
