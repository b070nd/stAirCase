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
#   TRUST_REF  ref to read KEY and ALLOWED_SIGNERS from (default origin/<base> in a pull request; required outside one)
#   REBUILD  "true": also rebuild each commit from its ledger note
#   REQUIRE_INITIATOR  "true": CAL 4 needs a signed, trusted initiator
#   STAIRCASE_BIN  use this binary instead of installing a release (for testing)
#   MIN_CAL  lowest change assurance level accepted
#   ALL      "false": check only commits that name an agent (default: every commit)
#   RANGE    commit range; default for a pull request: origin/<base>..<head>
#   VERSION  the version input: a release tag (vX.Y.Z), else an error; empty uses ACTION_REF
#   ACTION_REF  github.action_ref: the action's own release tag if it is one, else the latest release is used with a warning
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

  # Which release: the version input if given, else the action's own release tag.
  # A version that is not a release tag is an error, never "the latest": the verifier
  # that runs must be the one that was asked for.
  release_tag='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
  if [ -n "${VERSION:-}" ]; then
    [[ "$VERSION" =~ $release_tag ]] || { echo "::error::the version input must be a release tag such as v0.7.1, not '$VERSION'"; exit 1; }
    tag="$VERSION"
  elif [[ "${ACTION_REF:-}" =~ $release_tag ]]; then
    tag="$ACTION_REF"
  else
    echo "::warning::${ACTION_REF:-the ref the action is used at} is not a release tag, so the latest release is installed; use the action at a release tag (uses: b070nd/stAirCase@v0.7.1) or set the version input to pin the verifier"
    tag="$(gh release view -R b070nd/stAirCase --json tagName -q .tagName)"
    [[ "$tag" =~ $release_tag ]] || { echo "::error::the latest release is '$tag', not a release tag"; exit 1; }
  fi
  archive="staircase_${tag#v}_${plat}.tar.gz"
  dir="$(mktemp -d)"
  gh release download "$tag" -R b070nd/stAirCase -p "$archive" -D "$dir"
  # The build attestation must come from this repository's release workflow, for this tag.
  gh attestation verify "$dir/$archive" -R b070nd/stAirCase \
    --signer-workflow b070nd/stAirCase/.github/workflows/release.yml --source-ref "refs/tags/$tag" >/dev/null
  tar -xzf "$dir/$archive" -C "$dir" staircase
  [ -f "$dir/staircase" ] && [ -x "$dir/staircase" ] || { echo "::error::the release archive holds no staircase program"; exit 1; }
  echo "Installed staircase $tag (build attestation verified)"
fi

git fetch -q origin 'refs/notes/staircase:refs/notes/staircase' ||
  echo "::warning::the remote has no change certificates (refs/notes/staircase); push them with: git push origin refs/notes/staircase"
if [ "${REBUILD:-false}" = "true" ]; then
  git fetch -q origin 'refs/notes/staircase-ledger:refs/notes/staircase-ledger' ||
    echo "::warning::the remote has no ledgers (refs/notes/staircase-ledger); push them with: git push origin refs/notes/staircase-ledger"
fi

# The protected base is fetched whatever range is checked, and is what is trusted:
# a custom range must not change where the key and the signers come from.
if [ -n "${BASE_REF:-}" ]; then
  git fetch -q origin "$BASE_REF"
  git rev-parse --verify -q "origin/$BASE_REF" >/dev/null || { echo "::error::cannot resolve the pull request's base, origin/$BASE_REF"; exit 1; }
fi
range="${RANGE:-}"
if [ -z "$range" ]; then
  [ -n "${BASE_REF:-}" ] || { echo "::error::outside a pull request, set the range input (for example main..HEAD)"; exit 1; }
  range="origin/$BASE_REF..${HEAD_SHA:-HEAD}"
fi
# The trusted files come from the base branch (or TRUST_REF), not the pull request's checkout.
trust="${TRUST_REF:-}"
if [ -z "$trust" ] && [ -n "${BASE_REF:-}" ]; then
  trust="origin/$BASE_REF"
fi
[ -n "$trust" ] || { echo "::error::outside a pull request the trusted ref must be named: set the trust-ref input to a protected branch or tag that holds the key (and the allowed signers)"; exit 1; }
trusted() { # trusted <path>: print a file's content at the trusted ref
  if ! git show "$trust:$1" 2>/dev/null; then
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
