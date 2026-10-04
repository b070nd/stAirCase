#!/usr/bin/env bash
# release-gate.sh - refuse to release a commit the CI gate has not passed.
#
# The release workflow runs on a tag push, which nothing ties to a green build.
# This checks that the tagged commit is on the default branch and that the CI
# workflow (ci.yml: make check, vectors, the vulnerability scan) succeeded for
# exactly that commit, waiting for a run that is still going.
#
# Only a run of this repository's own push to the default branch counts: a pull
# request run, a run of another branch or a run from a fork can carry the same
# commit but is not what merging to the default branch tested. The query names the
# event and the branch, and the answer is filtered again here rather than trusted.
#
# Environment: GITHUB_REPOSITORY, GITHUB_SHA, GH_TOKEN (from the workflow);
#   DEFAULT_BRANCH (master), WAIT_SECONDS (1800), POLL_SECONDS (30)
set -euo pipefail

: "${GITHUB_REPOSITORY:?}" "${GITHUB_SHA:?}"
branch="${DEFAULT_BRANCH:-master}"
[[ "$branch" =~ ^[A-Za-z0-9._/-]+$ ]] || { echo "::error::DEFAULT_BRANCH is not a branch name: $branch"; exit 1; }

git fetch -q origin "$branch"
if ! git merge-base --is-ancestor "$GITHUB_SHA" "origin/$branch"; then
  echo "::error::$GITHUB_SHA is not on $branch: release only what was merged and built there"
  exit 1
fi

deadline=$((SECONDS + ${WAIT_SECONDS:-1800}))
while :; do
  runs="$(gh api "repos/$GITHUB_REPOSITORY/actions/workflows/ci.yml/runs?head_sha=$GITHUB_SHA&event=push&branch=$branch&per_page=20" \
    --jq ".workflow_runs | map(select(.event == \"push\" and .head_branch == \"$branch\" and .head_repository.full_name == \"$GITHUB_REPOSITORY\" and .head_sha == \"$GITHUB_SHA\")) | map({status, conclusion})")"
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
