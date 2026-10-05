#!/usr/bin/env bats
# tests/verify_release.bats - packaging/verify-release.sh with stand-in curl, cosign and gh.
# What is real: the checksum comparison, the control flow and the record. What is not: the signature checks.

setup() {
  ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"
  WORK="$(mktemp -d)"
  mkdir "$WORK/bin" "$WORK/site"
  for p in darwin_arm64 darwin_amd64 linux_amd64 linux_arm64; do
    printf 'archive of %s\n' "$p" >"$WORK/site/staircase_1.2.3_$p.tar.gz"
  done
  (cd "$WORK/site" && shasum -a 256 staircase_1.2.3_*.tar.gz >checksums.txt)
  echo '{"bundle":1}' >"$WORK/site/checksums.txt.sigstore.json"
  cat >"$WORK/bin/curl" <<'FAKE'
#!/bin/sh
# curl -fsSL [-o file] url: serve files of $SITE by name; the attestations endpoint answers with a bundle
out=""; url=""
while [ $# -gt 0 ]; do case "$1" in -o) out="$2"; shift ;; -*) ;; *) url="$1" ;; esac; shift; done
case "$url" in
  */attestations/sha256:*) [ -n "${NO_ATTESTATION:-}" ] && body='{"attestations":[]}' || body='{"attestations":[{"bundle":{"mediaType":"x"}}]}'
    if [ -n "$out" ]; then printf '%s' "$body" >"$out"; else printf '%s' "$body"; fi ;;
  *) f="$SITE/$(basename "$url")"; [ -f "$f" ] || exit 22
     if [ -n "$out" ]; then cp "$f" "$out"; else cat "$f"; fi ;;
esac
FAKE
  cat >"$WORK/bin/cosign" <<'FAKE'
#!/bin/sh
[ "$1" = version ] && { echo "GitVersion:    v9.9.9"; exit 0; }
exit "${COSIGN_RC:-0}"
FAKE
  cat >"$WORK/bin/gh" <<'FAKE'
#!/bin/sh
[ "$1" = --version ] && { echo "gh version 9.9 (test)"; exit 0; }
# gh attestation verify <file> --bundle b -R repo --signer-workflow W --source-ref REF
[ -n "${GH_PASS_ALL:-}" ] && exit 0
w=""; r=""
while [ $# -gt 0 ]; do case "$1" in --signer-workflow) w="$2"; shift ;; --source-ref) r="$2"; shift ;; esac; shift; done
[ "$w" = "o/r/.github/workflows/release.yml" ] && [ "$r" = "refs/tags/v1.2.3" ]
FAKE
  chmod +x "$WORK/bin/"*
  export PATH="$WORK/bin:$PATH" SITE="$WORK/site" REPO=o/r RELEASE_URL=https://example.test/dl API_URL=https://example.test/api
}
teardown() { rm -rf "$WORK"; }

verify() { run "$ROOT_DIR/packaging/verify-release.sh" v1.2.3 "$WORK/out"; }
field() { jq -r "$1" "$WORK/out/release-verification.json"; }

@test "a release whose checks pass and whose controls fail is verified, and the record says what ran" {
  verify
  [ "$status" -eq 0 ] || { echo "$output"; false; }
  [ "$(field .overall)" = verified ]
  [ "$(field '.checks | length')" = 9 ]
  [ "$(field .tag)" = v1.2.3 ]
  [ "$(field .tools.gh)" = "gh version 9.9 (test)" ]
  [ "$(field .tools.cosign)" = "v9.9.9" ]
  [ "$(field .source_ref)" = refs/tags/v1.2.3 ]
  [[ "$(field .archive_sha256)" =~ ^[0-9a-f]{64}$ ]]
  ! grep -q "$WORK" "$WORK/out/release-verification.json"
}

@test "an archive that is not the one in checksums.txt is not verified" {
  for f in "$SITE"/staircase_1.2.3_*.tar.gz; do echo tampered >"$f"; done
  verify
  [ "$status" -ne 0 ]
  [ "$(field .overall)" = "not verified" ]
  [ "$(field '.checks[] | select(.name | startswith("the archive")) | select(.name | contains("SHA")) | .ok')" = false ]
}

@test "a checksums signature that does not verify is not verified" {
  COSIGN_RC=1 verify
  [ "$status" -ne 0 ]
  [ "$(field '.checks[] | select(.name | startswith("checksums.txt is signed")) | .ok')" = false ]
}

@test "a verifier that passes a control proves nothing: the release is not verified" {
  GH_PASS_ALL=1 verify
  [ "$status" -ne 0 ]
  [ "$(field .overall)" = "not verified" ]
  [ "$(field '[.checks[] | select(.name | startswith("control")) | .ok] | any')" = false ]
}

@test "a release with no attestation is not verified" {
  NO_ATTESTATION=1 verify
  [ "$status" -ne 0 ]
  [ "$(field '.checks[] | select(.name | startswith("the archive'"'"'s attestation")) | .ok')" = false ]
}

@test "an asset that cannot be downloaded is a failed check, not a crash" {
  rm "$SITE/checksums.txt.sigstore.json"
  verify
  [ "$status" -ne 0 ]
  [ "$(field '.checks[] | select(.name | startswith("download the checksums signature")) | .ok')" = false ]
}
