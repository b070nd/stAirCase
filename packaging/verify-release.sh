#!/usr/bin/env bash
# verify-release.sh <tag> [outdir] - check a published release without signing in to GitHub.
#
# For the archive of this platform it checks, and records in <outdir>/release-verification.json:
#   1. its SHA-256 is the one in the release's checksums.txt;
#   2. checksums.txt carries a Sigstore signature made by this repository's release workflow
#      for this tag (cosign verify-blob, keyless identity and issuer);
#   3. the archive's build attestation (fetched from the public attestations endpoint, so
#      `gh` needs no login) verifies as made by that workflow for that tag, with the same
#      flags the verify Action uses (--signer-workflow, --source-ref);
#   4. two controls that must FAIL: the same attestation checked against another tag, and
#      against another repository's workflow. A verifier that passes a control proves nothing.
# Needs curl, jq, shasum, cosign and gh (no authentication). The record names the archive digest,
# the tool versions and each check's exit status; it holds no path of this machine.
# Environment (for tests): RELEASE_URL, API_URL, REPO (b070nd/stAirCase).
set -uo pipefail
tag="${1:?usage: verify-release.sh <tag, e.g. v0.8.0> [outdir]}"
out="${2:-release-verification}"
repo="${REPO:-b070nd/stAirCase}"
release_url="${RELEASE_URL:-https://github.com/$repo/releases/download/$tag}"
api_url="${API_URL:-https://api.github.com/repos/$repo}"
workflow="$repo/.github/workflows/release.yml"
mkdir -p "$out"

case "$(uname -s)/$(uname -m)" in
  Darwin/arm64) plat=darwin_arm64 ;;
  Darwin/x86_64) plat=darwin_amd64 ;;
  Linux/x86_64) plat=linux_amd64 ;;
  Linux/aarch64 | Linux/arm64) plat=linux_arm64 ;;
  *) echo "unsupported platform $(uname -s)/$(uname -m)" >&2; exit 2 ;;
esac
archive="staircase_${tag#v}_${plat}.tar.gz"

checks='[]'
note() { # note <name> <expect pass|fail> <exit status>
  local ok=false
  { [ "$2" = pass ] && [ "$3" -eq 0 ]; } || { [ "$2" = fail ] && [ "$3" -ne 0 ]; } && ok=true
  checks="$(jq -c --arg n "$1" --arg e "$2" --argjson s "$3" --argjson ok "$ok" '. + [{name: $n, expect: $e, exit_status: $s, ok: $ok}]' <<<"$checks")"
  printf '%-4s %s (expected to %s, exit status %s)\n' "$($ok && echo ok || echo FAIL)" "$1" "$2" "$3"
}
fetch() { curl -fsSL -o "$out/$1" "$2"; }

fetch checksums.txt "$release_url/checksums.txt"; note "download checksums.txt" pass $?
fetch checksums.txt.sigstore.json "$release_url/checksums.txt.sigstore.json"; note "download the checksums signature bundle" pass $?
fetch "$archive" "$release_url/$archive"; note "download $archive" pass $?

digest="$(shasum -a 256 "$out/$archive" 2>/dev/null | cut -d' ' -f1)"
want="$(awk -v f="$archive" '$2 == f {print $1}' "$out/checksums.txt" 2>/dev/null)"
[ -n "$digest" ] && [ "$digest" = "$want" ]; note "the archive's SHA-256 is the one in checksums.txt" pass $?

cosign verify-blob "$out/checksums.txt" --bundle "$out/checksums.txt.sigstore.json" \
  --certificate-identity "https://github.com/$workflow@refs/tags/$tag" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com >"$out/cosign.log" 2>&1
note "checksums.txt is signed by the release workflow for $tag" pass $?

curl -fsSL "$api_url/attestations/sha256:$digest" | jq -c '.attestations[0].bundle' >"$out/attestation.sigstore.json" 2>/dev/null
[ -s "$out/attestation.sigstore.json" ] && [ "$(cat "$out/attestation.sigstore.json")" != null ]; note "the archive's attestation was found" pass $?

attest() { # attest <signer workflow> <source ref>
  GH_TOKEN=unused gh attestation verify "$out/$archive" --bundle "$out/attestation.sigstore.json" -R "$repo" \
    --signer-workflow "$1" --source-ref "$2" >>"$out/gh.log" 2>&1
}
: >"$out/gh.log"
attest "$workflow" "refs/tags/$tag"; note "the attestation is the release workflow's, for $tag" pass $?
attest "$workflow" "refs/tags/v0.0.0-not-this-one"; note "control: the same attestation against another tag" fail $?
attest "someone-else/stAirCase/.github/workflows/release.yml" "refs/tags/$tag"; note "control: the same attestation against another repository's workflow" fail $?

ver() { "$@" 2>&1 | head -1; }
jq -n --arg tag "$tag" --arg repo "$repo" --arg archive "$archive" --arg digest "$digest" --arg workflow "$workflow" \
  --arg gh "$(ver gh --version)" --arg cosign "$(cosign version 2>&1 | sed -n 's/^GitVersion: *//p' | head -1)" \
  --argjson checks "$checks" '
  {schema: 1, repository: $repo, tag: $tag, archive: $archive, archive_sha256: $digest, signer_workflow: $workflow,
   source_ref: ("refs/tags/" + $tag), tools: {gh: $gh, cosign: $cosign}, checks: $checks,
   overall: (if ($checks | map(.ok) | all) then "verified" else "not verified" end)}' >"$out/release-verification.json"
rm -f "$out/gh.log.tmp"
echo
jq -r '"\(.tag) \(.archive): \(.overall)"' "$out/release-verification.json"
[ "$(jq -r .overall "$out/release-verification.json")" = verified ]
