#!/usr/bin/env bash
# release-gate.sh - refuse to release a commit the CI gate has not passed.
#
# The release workflow runs on a tag push, which nothing ties to a green build.
# This checks that the tagged commit is on the default branch and that the CI
# workflow (ci.yml: make check, vectors, the vulnerability scan) succeeded for
# exactly that commit, waiting for a run that is still going.
#
# Environment: GITHUB_REPOSITORY, GITHUB_SHA, GH_TOKEN (from the workflow);
#   DEFAULT_BRANCH (master), WAIT_SECONDS (1800), POLL_SECONDS (30)
set -euo pipefail

: "${GITHUB_REPOSITORY:?}" "${GITHUB_SHA:?}"
branch="${DEFAULT_BRANCH:-master}"

git fetch -q origin "$branch"
if ! git merge-base --is-ancestor "$GITHUB_SHA" "origin/$branch"; then
  echo "::error::$GITHUB_SHA is not on $branch: release only what was merged and built there"
  exit 1
fi

deadline=$((SECONDS + ${WAIT_SECONDS:-1800}))
while :; do
  runs="$(gh api "repos/$GITHUB_REPOSITORY/actions/workflows/ci.yml/runs?head_sha=$GITHUB_SHA&per_page=20" \
    --jq '.workflow_runs | map({status, conclusion})')"
  if [ "$(jq '[.[] | select(.conclusion == "success")] | length' <<<"$runs")" -gt 0 ]; then
    echo "CI passed for $GITHUB_SHA"
    exit 0
  fi
  if [ "$(jq '[.[] | select(.status != "completed")] | length' <<<"$runs")" -eq 0 ] &&
     [ "$(jq 'length' <<<"$runs")" -gt 0 ]; then
    echo "::error::CI ran for $GITHUB_SHA and did not succeed: $(jq -c . <<<"$runs")"
    exit 1
  fi
  if [ "$SECONDS" -ge "$deadline" ]; then
    echo "::error::no successful CI run for $GITHUB_SHA within the wait: $(jq -c . <<<"$runs")"
    exit 1
  fi
  echo "waiting for CI on $GITHUB_SHA: $(jq -c . <<<"$runs")"
  sleep "${POLL_SECONDS:-30}"
done
