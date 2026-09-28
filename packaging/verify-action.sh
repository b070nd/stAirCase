#!/usr/bin/env bash
# verify-action.sh - the "stAirCase verify" GitHub Action (action.yml).
#
# Installs a staircase release after checking its build attestation, fetches
# the change certificates (git notes) and checks every commit in the range
# that names an agent (Assisted-by:), or every commit with ALL=true.
#
# Inputs come as environment variables, never pasted into the script:
#   KEY      path of the trusted public signing key in the repository (required)
#   MIN_CAL  lowest change assurance level accepted
#   ALL      "true": every commit needs a certificate
#   RANGE    commit range; default for a pull request: origin/<base>..<head>
#   VERSION  release to install (vX.Y.Z); anything else means the latest
#   BASE_REF, HEAD_SHA, RUNNER_OS, RUNNER_ARCH, GH_TOKEN: from the workflow
set -euo pipefail

: "${KEY:?the key input is required: the path of your public signing key in the repository}"
[ -f "$KEY" ] || { echo "::error::no public key at $KEY"; exit 1; }

case "${RUNNER_OS:-}/${RUNNER_ARCH:-}" in
  Linux/X64) plat=linux_amd64 ;;
  Linux/ARM64) plat=linux_arm64 ;;
  macOS/X64) plat=darwin_amd64 ;;
  macOS/ARM64) plat=darwin_arm64 ;;
  *) echo "::error::unsupported runner ${RUNNER_OS:-?}/${RUNNER_ARCH:-?}"; exit 1 ;;
esac

tag="${VERSION:-}"
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+ ]]; then
  tag="$(gh release view -R b070nd/stAirCase --json tagName -q .tagName)"
fi
archive="staircase_${tag#v}_${plat}.tar.gz"
dir="$(mktemp -d)"
gh release download "$tag" -R b070nd/stAirCase -p "$archive" -D "$dir"
gh attestation verify "$dir/$archive" -R b070nd/stAirCase >/dev/null
tar -xzf "$dir/$archive" -C "$dir" staircase
echo "Installed staircase $tag (build attestation verified)"

git fetch -q origin 'refs/notes/staircase:refs/notes/staircase' ||
  echo "::warning::the remote has no change certificates (refs/notes/staircase); push them with: git push origin refs/notes/staircase"

range="${RANGE:-}"
if [ -z "$range" ]; then
  [ -n "${BASE_REF:-}" ] || { echo "::error::outside a pull request, set the range input (for example main..HEAD)"; exit 1; }
  git fetch -q origin "$BASE_REF"
  range="origin/$BASE_REF..${HEAD_SHA:-HEAD}"
fi
args=("$range" --key "$KEY" --min-cal "${MIN_CAL:-2}")
[ "${ALL:-false}" = "true" ] && args+=(--all)
"$dir/staircase" verify "${args[@]}"
