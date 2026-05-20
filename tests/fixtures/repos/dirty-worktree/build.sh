#!/usr/bin/env bash
# build.sh — build the dirty-worktree fixture repository tarball.
#
# Creates a git repository that has an uncommitted staged file.
# Used by pre-flight tests (E2E-009) to verify that staircase refuses to
# run when the working tree is dirty and --force / --auto-stash are not set.
#
# Construction: take the tiny-go fixture as a base, then add an uncommitted
# change so that `git status --porcelain` is non-empty.
#
# Usage: SOURCE_DATE_EPOCH=1700000000 bash build.sh
# Output: dirty-worktree.tar.gz in the same directory as this script.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TINY_GO_TGZ="$SCRIPT_DIR/../tiny-go/tiny-go.tar.gz"
OUT="$SCRIPT_DIR/dirty-worktree.tar.gz"

if [[ ! -f "$TINY_GO_TGZ" ]]; then
    echo "ERROR: tiny-go.tar.gz not found at $TINY_GO_TGZ" >&2
    echo "Run: bash tests/fixtures/repos/tiny-go/build.sh" >&2
    exit 1
fi

EPOCH="${SOURCE_DATE_EPOCH:-1700000000}"
COMMIT_DATE="$(date -u -r "$EPOCH" '+%Y-%m-%dT%H:%M:%S +0000' 2>/dev/null || \
               date -u -d "@$EPOCH" '+%Y-%m-%dT%H:%M:%S +0000')"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

REPO="$WORK/dirty-worktree"
mkdir -p "$REPO"

# Start from the tiny-go base.
tar xzf "$TINY_GO_TGZ" -C "$REPO"

# Re-apply git identity in case the extracted .git/config differs.
git -C "$REPO" config user.email "fixture@staircase.test"
git -C "$REPO" config user.name  "Fixture Builder"

# Add a new file that is tracked (staged) but not committed.
# This makes `git status --porcelain` non-empty, triggering the dirty-tree check.
cat > "$REPO/DIRTY.md" << 'DIRTY'
# This file is intentionally uncommitted.

It exists to make `git status` show a dirty working tree so that stAirCase
pre-flight tests can verify the dirty-tree refusal code path (E2E-009).
DIRTY

git -C "$REPO" add DIRTY.md
# Do NOT commit — the point is that it stays staged.

# Verify the repo is dirty.
STATUS="$(git -C "$REPO" status --porcelain)"
if [[ -z "$STATUS" ]]; then
    echo "ERROR: repo is clean after adding DIRTY.md" >&2
    exit 1
fi

# Archive.
tar czf "$OUT" -C "$REPO" .

echo "Built: $OUT"
echo "Dirty status: $STATUS"
echo "SHA256: $(shasum -a 256 "$OUT" | awk '{print $1}')"
