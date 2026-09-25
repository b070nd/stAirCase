# shellcheck shell=bash
# lib.sh — helpers shared by run-demo.sh and smoke.sh (sourced, not run).

say()  { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }
note() { printf '  \033[2m%s\033[0m\n' "$*"; }
ok()   { printf '  ✓ %s\n' "$*"; }
# die prints why, and the tail of $RUN_LOG when a run log is set.
die()  { echo "✗ $*"; if [ -f "${RUN_LOG:-}" ]; then tail -20 "$RUN_LOG"; fi; exit 1; }

require_tools() {
  for tool in "$@"; do command -v "$tool" >/dev/null || die "$tool required"; done
}

# build_binaries <dir>: builds staircase and the demo's stand-in model into
# <dir> from this checkout, and defines staircase() to run the built binary.
build_binaries() {
  local repo_root; repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  ( cd "$repo_root" && CGO_ENABLED=0 go build -o "$1/staircase" ./src/cmd/staircase \
                    && CGO_ENABLED=0 go build -o "$1/demotool" ./demo/demotool )
  STAIRCASE_BIN="$1/staircase"
  # shellcheck disable=SC2034 # used by the scripts that source this file
  DEMOTOOL="$1/demotool"
}
staircase() { "$STAIRCASE_BIN" "$@"; }
