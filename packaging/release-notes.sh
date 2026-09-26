#!/bin/sh
# release-notes.sh <version> — print the CHANGELOG.md section of a version,
# used as the GitHub release notes. Fails when the section is missing or empty.
set -eu
version="${1:?usage: release-notes.sh <version, e.g. 0.2.0>}"
notes="$(awk -v v="$version" '
  index($0, "## [" v "]") == 1 { on = 1; next }
  on && /^## \[/ { exit }
  on { print }
' "$(dirname "$0")/../CHANGELOG.md")"
[ -n "$(printf '%s' "$notes" | tr -d '[:space:]')" ] || { echo "CHANGELOG.md has no section for $version" >&2; exit 1; }
printf '%s\n' "$notes"
