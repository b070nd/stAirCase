#!/usr/bin/env bash
# ci-evidence-check.sh <commit> <evidence> <release-gate> - read the retained CI artifacts of a release candidate, from files.
#
# The CI run of the default branch keeps `evidence-linux-<sha>` (evidence.json: what was built and tested on Linux) and the
# release run keeps `release-gate` (the CI run the release rests on). Download both from the run pages in a browser (the
# Artifacts list at the bottom of each run) and give them here, as the zip files or the JSON inside. No gh, no network, no
# login: this only reads the two files, and refuses anything short of "this exact commit, built and tested on Linux with
# nothing failed, skipped or partial, by a successful push run of the default branch of this repository".
#
# Usage: ./demo/ci-evidence-check.sh <full commit sha> evidence-linux-<sha>.zip release-gate.zip
# Environment: REPO (b070nd/stAirCase), BRANCH (master).
set -uo pipefail
sha="${1:-}"; evfile="${2:-}"; gatefile="${3:-}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] && [ -n "$evfile" ] && [ -n "$gatefile" ] || { echo "usage: ci-evidence-check.sh <full 40-character commit sha> <evidence.json|zip> <release-gate.json|zip>" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is needed" >&2; exit 2; }
repo="${REPO:-b070nd/stAirCase}"; branch="${BRANCH:-master}"

# json <file> <name>: the JSON of an artifact, from a zip (the member called <name>, else the only .json) or as it is
json() {
  [ -f "$1" ] || { echo "$1: no such file" >&2; return 1; }
  case "$(head -c 2 "$1")" in
    PK)
      command -v unzip >/dev/null || { echo "unzip is needed for $1" >&2; return 1; }
      if unzip -Z1 "$1" | grep -qx "$2"; then unzip -p "$1" "$2"
      else
        only="$(unzip -Z1 "$1" | grep '\.json$')"
        [ "$(wc -l <<<"$only" | tr -d ' ')" = 1 ] && [ -n "$only" ] || { echo "$1: no $2 in it" >&2; return 1; }
        unzip -p "$1" "$only"
      fi ;;
    *) cat "$1" ;;
  esac
}
ev="$(json "$evfile" evidence.json)" && jq -e . >/dev/null <<<"$ev" || { echo "$evfile is not a readable evidence.json" >&2; exit 1; }
gate="$(json "$gatefile" release-gate.json)" && jq -e . >/dev/null <<<"$gate" || { echo "$gatefile is not a readable release gate result" >&2; exit 1; }

bad=0
refuse() { echo "REFUSED: $*"; bad=1; }
[ "$(jq -r .commit <<<"$ev")" = "$sha" ] || refuse "the evidence is of another commit ($(jq -r .commit <<<"$ev"))"
[ "$(jq -r .platform.os <<<"$ev")" = Linux ] || refuse "the evidence was not made on Linux ($(jq -r .platform.os <<<"$ev"))"
[ "$(jq -r .tree_clean <<<"$ev")" = true ] || refuse "the evidence was made on a tree that was not clean"
[ "$(jq -r .partial <<<"$ev")" = false ] || refuse "the evidence is partial: not every step was run"
[ "$(jq -r .overall <<<"$ev")" = passed ] || refuse "the evidence's overall result is '$(jq -r .overall <<<"$ev")', not passed"
[ "$(jq -r .summary.failed <<<"$ev")" = 0 ] || refuse "$(jq -r .summary.failed <<<"$ev") step(s) failed"
[ "$(jq -r .summary.skipped <<<"$ev")" = 0 ] || refuse "$(jq -r .summary.skipped <<<"$ev") step(s) skipped"

[ "$(jq -r .gate <<<"$gate")" = ci ] && [ "$(jq -r .passed <<<"$gate")" = true ] || refuse "the release gate did not record a passed CI run"
[ "$(jq -r .run.conclusion <<<"$gate")" = success ] || refuse "the gate's run concluded '$(jq -r .run.conclusion <<<"$gate")'"
[ "$(jq -r .run.event <<<"$gate")" = push ] || refuse "the gate's run was a '$(jq -r .run.event <<<"$gate")' run, not a push"
[ "$(jq -r .run.branch <<<"$gate")" = "$branch" ] || refuse "the gate's run was of '$(jq -r .run.branch <<<"$gate")', not $branch"
[ "$(jq -r .run.repository <<<"$gate")" = "$repo" ] || refuse "the gate's run is of the repository '$(jq -r .run.repository <<<"$gate")', not $repo"
[ "$(jq -r .run.sha <<<"$gate")" = "$sha" ] || refuse "the gate's run is of another commit ($(jq -r .run.sha <<<"$gate"))"

echo "commit $sha"
jq -r '"evidence: \(.platform.os)/\(.platform.arch), clean tree \(.tree_clean), overall \(.overall), partial \(.partial); \(.summary.passed) passed, \(.summary.failed) failed, \(.summary.skipped) skipped, \(.summary.not_run) not run"' <<<"$ev"
jq -r '.steps[] | "  \(.name): \(.status)" + (if .reason != "" then " (\(.reason))" else "" end)' <<<"$ev"
jq -r '"release gate: CI run \(.run.id), attempt \(.run.attempt), \(.run.event) on \(.run.branch) of \(.run.repository), \(.run.conclusion)\n  \(.run.url)"' <<<"$gate"
[ "$bad" = 0 ] && echo "ACCEPTED" || exit 1
