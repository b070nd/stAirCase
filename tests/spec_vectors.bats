#!/usr/bin/env bats
# tests/spec_vectors.bats - the independent conformance scripts never pass vacuously.

setup() {
  SPEC="$(cd "$BATS_TEST_DIRNAME/../docs/spec" && pwd)"
  WORK="$(mktemp -d)"
}
teardown() { rm -rf "$WORK"; }

# copy <script> <inventory>: the script, its shared report code and its inventory, in a scratch directory
copy() { cp "$SPEC/$1" "$SPEC/vectorreport.py" "$WORK/" && cp -R "$SPEC/$2" "$WORK/"; }

@test "the shipped inventories are decided, counted and reported as JSON" {
  for s in rebuild_vectors audit_chain_vectors; do
    run python3 "$SPEC/$s.py" --json "$WORK/$s.json"
    [ "$status" -eq 0 ]
    [[ "$output" == *"vector(s) decided, 0 differ"* ]]
    [ "$(python3 -c "import json,sys; d=json.load(open('$WORK/$s.json')); print(d['ok'], d['count'] >= d['minimum'] > 0)")" = "True True" ]
  done
}

@test "a rebuild inventory with no vectors fails" {
  copy rebuild_vectors.py rebuild-vectors
  rm "$WORK"/rebuild-vectors/*.json
  run python3 "$WORK/rebuild_vectors.py"
  [ "$status" -ne 0 ]
  [[ "$output" == *"no vectors were found"* ]]
}

@test "a rebuild inventory that is missing altogether fails" {
  cp "$SPEC/rebuild_vectors.py" "$SPEC/vectorreport.py" "$WORK/"
  run python3 "$WORK/rebuild_vectors.py"
  [ "$status" -ne 0 ]
}

@test "a rebuild inventory with vectors removed fails below the recorded floor" {
  copy rebuild_vectors.py rebuild-vectors
  rm "$WORK"/rebuild-vectors/0[1-9]-*.json
  run python3 "$WORK/rebuild_vectors.py"
  [ "$status" -ne 0 ]
  [[ "$output" == *"fewer than"* ]]
}

@test "an audit chain inventory with no vectors fails" {
  cp "$SPEC/audit_chain_vectors.py" "$SPEC/vectorreport.py" "$WORK/"
  echo '{}' >"$WORK/audit-chain-vectors.json"
  run python3 "$WORK/audit_chain_vectors.py"
  [ "$status" -ne 0 ]
  [[ "$output" == *"no vectors were found"* ]]
}

@test "a vector decided differently fails and is named in the JSON" {
  copy rebuild_vectors.py rebuild-vectors
  sed -i.bak 's/"tree": "[0-9a-f]*"/"tree": "0000000000000000000000000000000000000000"/' "$WORK/rebuild-vectors/01-create-edit-delete.json"
  run python3 "$WORK/rebuild_vectors.py" --json "$WORK/out.json"
  [ "$status" -ne 0 ]
  [ "$(python3 -c "import json; d=json.load(open('$WORK/out.json')); print(d['failed'])")" = "['01-create-edit-delete']" ]
}
