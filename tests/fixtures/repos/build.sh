#!/usr/bin/env bash
# build.sh — dispatcher for fixture repository builders.
#
# Usage: bash build.sh <fixture-name>
#        SOURCE_DATE_EPOCH=1700000000 bash build.sh all
#
# Each fixture is built by tests/fixtures/repos/<name>/build.sh and produces
# tests/fixtures/repos/<name>.tar.gz.  Tarballs are checked into git so CI
# never needs to rebuild them.  Run this script (and commit the result) when
# fixture source changes.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-1700000000}"

usage() {
    echo "Usage: $0 <fixture-name|all>"
    echo ""
    echo "Available fixtures:"
    for d in "$SCRIPT_DIR"/*/; do
        name="$(basename "$d")"
        if [[ -f "$d/build.sh" ]]; then
            echo "  $name"
        fi
    done
    exit 1
}

build_one() {
    local name="$1"
    local build_script="$SCRIPT_DIR/$name/build.sh"
    if [[ ! -f "$build_script" ]]; then
        echo "ERROR: no build.sh for fixture '$name'" >&2
        exit 1
    fi
    echo "==> Building fixture: $name"
    bash "$build_script"
}

case "${1:-}" in
    "")
        usage
        ;;
    all)
        for d in "$SCRIPT_DIR"/*/; do
            name="$(basename "$d")"
            if [[ -f "$d/build.sh" ]]; then
                build_one "$name"
            fi
        done
        ;;
    *)
        build_one "$1"
        ;;
esac
