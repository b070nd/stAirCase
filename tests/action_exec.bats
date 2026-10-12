#!/usr/bin/env bats
# tests/action_exec.bats - the verify Action starts on a real runner. GitHub runs the step's `run:` line from action.yml with
# GITHUB_ACTION_PATH pointing at a checkout of the Action's repository, so what is stored in git decides: a script without its
# executable bit answers "Permission denied" (exit 126) before it verifies anything. The other tests start the script with `bash script.sh`,
# which hides exactly that, so this one runs the line the way the runner does.

setup() { ROOT_DIR="$(cd "$BATS_TEST_DIRNAME/.." && pwd)"; }

@test "action.yml's run line starts the script as the runner does: it reaches its own input check, not 'Permission denied'" {
  line="$(python3 - "$ROOT_DIR/action.yml" <<'PY'
import sys,re
s=open(sys.argv[1]).read()
m=re.search(r"^\s+run: (.+)$", s, re.M)
v=m.group(1).strip()
if v[0] in "'\"" and v[-1]==v[0]: v=v[1:-1]
print(v)
PY
)"
  [ -n "$line" ]
  # the runner: bash --noprofile --norc -e -o pipefail, GITHUB_ACTION_PATH set, the inputs in the environment (KEY empty here)
  run env -i PATH="$PATH" HOME="$HOME" GITHUB_ACTION_PATH="$ROOT_DIR" KEY="" bash --noprofile --norc -e -o pipefail -c "$line"
  [ "$status" -ne 126 ] || { echo "the script could not be executed: $output"; false; }
  [[ "$output" != *"Permission denied"* ]]
  [[ "$output" == *"the key input is required"* ]] || { echo "$output"; false; }
}

@test "every script in packaging/ is stored executable, as the others are" {
  bad="$(git -C "$ROOT_DIR" ls-files -s -- 'packaging/*.sh' | awk '$1 != "100755" {print $4" ("$1")"}')"
  [ -z "$bad" ] || { echo "not executable in git: $bad"; false; }
}
