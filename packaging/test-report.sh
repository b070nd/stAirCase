#!/usr/bin/env bash
# test-report.sh <command...> - run a test command, pass its output and exit status
# through, and when it fails add a GitHub Actions error annotation that names the failing
# tests. A job log needs a sign-in to read; an annotation does not (the check-runs API
# serves it), so a failure on a CI runner can be diagnosed from outside.
set -uo pipefail
log="$(mktemp)"
"$@" 2>&1 | tee "$log"
rc="${PIPESTATUS[0]}"
if [ "$rc" -ne 0 ]; then
  lines="$(grep -E '^(--- FAIL|FAIL|panic:)|_test\.go:[0-9]+|Error:|Messages:|expected|actual' "$log" | head -60)"
  [ -n "$lines" ] || lines="no failing test line in the output (exit status $rc); last lines:
$(tail -n 5 "$log")"
  msg="$(printf '%s' "$lines" | sed -e 's/%/%25/g' -e 's/\r/%0D/g' | awk '{printf "%s%%0A", $0}')"
  echo "::error title=Tests failed::${msg}"
fi
rm -f "$log"
exit "$rc"
