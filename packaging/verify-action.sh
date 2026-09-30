#!/usr/bin/env bash
# verify-action.sh - the "stAirCase verify" GitHub Action (action.yml).
#
# Installs a staircase release after checking its build attestation, fetches
# the change certificates (git notes) and checks every commit in the range.
# By default every commit needs a certificate; ALL=false checks only those
# that name an agent (Assisted-by:), which a commit can dodge by omitting it.
#
# The trusted key and the allowed signers are read from a trusted ref (the pull
# request's base branch), never from the pull request's own files: a pull
# request that replaces them would otherwise vouch for itself.
#
# Inputs come as environment variables, never pasted into the script:
#   KEY      path of the trusted public signing key in the repository (required)
#   ALLOWED_SIGNERS  path of the allowed_signers file (optional)
#   TRUST_REF  ref to read KEY and ALLOWED_SIGNERS from (default origin/<base>)
#   REBUILD  "true": also rebuild each commit from its ledger note
#   REQUIRE_INITIATOR  "true": CAL 4 needs a signed, trusted initiator
#   STAIRCASE_BIN  use this binary instead of installing a release (for testing)
#   MIN_CAL  lowest change assurance level accepted
#   ALL      "false": check only commits that name an agent (default: every commit)
#   RANGE    commit range; default for a pull request: origin/<base>..<head>
#   VERSION  release to install (vX.Y.Z); anything else means the latest
#   BASE_REF, HEAD_SHA, RUNNER_OS, RUNNER_ARCH, GH_TOKEN: from the workflow
set -euo pipefail

: "${KEY:?the key input is required: the path of your public signing key in the repository}"

if [ -n "${STAIRCASE_BIN:-}" ]; then
  dir="$(mktemp -d)"
  cp "$STAIRCASE_BIN" "$dir/staircase"
else
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
fi

git fetch -q origin 'refs/notes/staircase:refs/notes/staircase' ||
  echo "::warning::the remote has no change certificates (refs/notes/staircase); push them with: git push origin refs/notes/staircase"
if [ "${REBUILD:-false}" = "true" ]; then
  git fetch -q origin 'refs/notes/staircase-ledger:refs/notes/staircase-ledger' ||
    echo "::warning::the remote has no ledgers (refs/notes/staircase-ledger); push them with: git push origin refs/notes/staircase-ledger"
fi

range="${RANGE:-}"
if [ -z "$range" ]; then
  [ -n "${BASE_REF:-}" ] || { echo "::error::outside a pull request, set the range input (for example main..HEAD)"; exit 1; }
  git fetch -q origin "$BASE_REF"
  range="origin/$BASE_REF..${HEAD_SHA:-HEAD}"
fi
# The trusted files come from the base branch (or TRUST_REF), not the pull request's checkout.
trust="${TRUST_REF:-}"
if [ -z "$trust" ] && [ -n "${BASE_REF:-}" ]; then
  trust="origin/$BASE_REF"
fi
trusted() { # trusted <path>: print a file's content at the trusted ref, or the working tree without one
  if [ -z "$trust" ]; then
    echo "::notice::no trusted ref (not a pull request and no trust-ref): reading $1 from the checkout" >&2
    cat -- "$1"
  elif ! git show "$trust:$1" 2>/dev/null; then
    echo "::error::$1 is not on $trust. The trusted key and signers are read from there, not from the pull request: merge them into the protected branch first" >&2
    return 1
  fi
}
keyfile="$dir/trusted.pub"
trusted "$KEY" > "$keyfile"
args=("$range" --key "$keyfile" --min-cal "${MIN_CAL:-2}")
if [ -n "${ALLOWED_SIGNERS:-}" ]; then
  signers="$dir/allowed_signers"
  trusted "$ALLOWED_SIGNERS" > "$signers"
  args+=(--allowed-signers "$signers")
fi
[ "${ALL:-true}" = "false" ] || args+=(--all)
[ "${REBUILD:-false}" = "true" ] && args+=(--rebuild)
[ "${REQUIRE_INITIATOR:-false}" = "true" ] && args+=(--require-initiator)
"$dir/staircase" verify "${args[@]}"
