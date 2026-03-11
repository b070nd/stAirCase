#!/usr/bin/env bats
# tests/integration.bats — staircase 1.1.0 integration tests
#
# Run:    bats tests/integration.bats
# Quiet:  bats --tap tests/integration.bats
# Filter: bats tests/integration.bats --filter "link"

STAIRCASE="${BATS_TEST_DIRNAME}/../staircase"

setup() {
  WORK_DIR="$(mktemp -d)"
  cd "$WORK_DIR" || return 1
  export NO_COLOR=1
}

teardown() {
  rm -rf "$WORK_DIR"
}

# ── Helpers ────────────────────────────────────────────────────────────────────

# Run staircase with all args; merge stderr into stdout so bats captures it.
sc() { "$STAIRCASE" "$@" 2>&1; }

# Init workspace + add a project + open a case (all output suppressed).
scaffold() {
  local vp="${1:-acme/webshop}" cs="${2:-SPRINT-1}"
  sc init > /dev/null
  sc project add "$vp" > /dev/null
  sc case new "$vp" "$cs" > /dev/null
}

# ═══════════════════════════════════════════════════════════════════════════════
#  INIT
# ═══════════════════════════════════════════════════════════════════════════════

@test "init: exits 0 on empty directory" {
  run sc init
  [ "$status" -eq 0 ]
}

@test "init: creates .staircase/config.json, manifest.json, tmp/" {
  sc init > /dev/null
  [ -f ".staircase/config.json" ]
  [ -f ".staircase/manifest.json" ]
  [ -d ".staircase/tmp" ]
}

@test "init: manifest has correct initial schema" {
  sc init > /dev/null
  run jq -r '.version' ".staircase/manifest.json"
  [ "$output" = "1.0" ]
  run jq -r '.vendors' ".staircase/manifest.json"
  [ "$output" = "{}" ]
}

@test "init: config.json sets runner and hooks_dir" {
  sc init > /dev/null
  run jq -r '.runner' ".staircase/config.json"
  [ "$output" = "ralph-tui" ]
  run jq -r '.hooks_dir' ".staircase/config.json"
  [ "$output" = "hooks.d" ]
}

@test "init: idempotent — second run exits 0 and preserves manifest" {
  sc init > /dev/null
  sc project add acme/web > /dev/null
  run sc init
  [ "$status" -eq 0 ]
  run jq -r '.vendors.acme' ".staircase/manifest.json"
  [ "$output" != "null" ]
}

@test "init: output goes to stderr, stdout is clean" {
  run bash -c "'$STAIRCASE' init 2>/dev/null"
  [ -z "$output" ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  CONFIG
# ═══════════════════════════════════════════════════════════════════════════════

@test "config --list: prints KEY VALUE SOURCE table" {
  sc init > /dev/null
  run sc config --list
  [ "$status" -eq 0 ]
  [[ "$output" == *"KEY"* ]]
  [[ "$output" == *"runner"* ]]
  [[ "$output" == *"hooks_dir"* ]]
}

@test "config: set and get roundtrip" {
  sc init > /dev/null
  sc config runner claude-code > /dev/null
  run sc config runner
  [ "$output" = "claude-code" ]
}

@test "config: outside workspace exits 1" {
  run sc config --list
  [ "$status" -eq 1 ]
  [[ "$output" == *"no workspace"* ]]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  VENDOR
# ═══════════════════════════════════════════════════════════════════════════════

@test "vendor add: creates vendor dir and manifest entry with registered_at" {
  sc init > /dev/null
  run sc vendor add acme
  [ "$status" -eq 0 ]
  [ -d "acme" ]
  run jq -r '.vendors.acme.registered_at' ".staircase/manifest.json"
  [[ "$output" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T ]]
}

@test "vendor add: duplicate exits 1" {
  sc init > /dev/null
  sc vendor add acme > /dev/null
  run sc vendor add acme
  [ "$status" -eq 1 ]
  [[ "$output" == *"already exists"* ]]
}

@test "vendor add: missing name exits 1" {
  sc init > /dev/null
  run sc vendor add
  [ "$status" -eq 1 ]
}

@test "vendor remove: removes empty vendor" {
  sc init > /dev/null
  sc vendor add acme > /dev/null
  run sc vendor remove acme
  [ "$status" -eq 0 ]
  run jq -r '.vendors.acme // "null"' ".staircase/manifest.json"
  [ "$output" = "null" ]
}

@test "vendor remove: refuses vendor with projects" {
  sc init > /dev/null
  sc project add acme/web > /dev/null
  run sc vendor remove acme
  [ "$status" -eq 1 ]
  [[ "$output" == *"projects"* ]]
}

@test "vendor list: shows vendors with project counts" {
  sc init > /dev/null
  sc project add acme/web > /dev/null
  sc project add acme/api > /dev/null
  run sc vendor list
  [ "$status" -eq 0 ]
  [[ "$output" == *"acme"* ]]
  [[ "$output" == *"2"* ]]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  PROJECT
# ═══════════════════════════════════════════════════════════════════════════════

@test "project add: creates vendor/project/.staircase/ structure" {
  sc init > /dev/null
  run sc project add acme/webshop
  [ "$status" -eq 0 ]
  [ -d "acme/webshop/.staircase/active" ]
  [ -d "acme/webshop/.staircase/tasks" ]
  [ -f "acme/webshop/.staircase/config.json" ]
}

@test "project add: auto-creates vendor when absent" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run jq -r '.vendors.acme.registered_at' ".staircase/manifest.json"
  [[ "$output" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T ]]
}

@test "project add: registers in manifest with empty components and null activeCase" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run jq -r '.vendors.acme.projects.webshop.activeCase' ".staircase/manifest.json"
  [ "$output" = "null" ]
  run jq '.vendors.acme.projects.webshop.components' ".staircase/manifest.json"
  [ "$output" = "[]" ]
}

@test "project add: duplicate exits 1" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run sc project add acme/webshop
  [ "$status" -eq 1 ]
  [[ "$output" == *"already exists"* ]]
}

@test "project add: missing v/p arg exits 1" {
  sc init > /dev/null
  run sc project add
  [ "$status" -eq 1 ]
}

@test "project add: non-slash name exits 1" {
  sc init > /dev/null
  run sc project add webshop
  [ "$status" -eq 1 ]
}

@test "project remove: removes from manifest, leaves directory" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run sc project remove acme/webshop
  [ "$status" -eq 0 ]
  run jq -r '.vendors.acme.projects.webshop // "null"' ".staircase/manifest.json"
  [ "$output" = "null" ]
  [ -d "acme/webshop" ]
}

@test "project list: shows all projects across vendors" {
  sc init > /dev/null
  sc project add acme/web > /dev/null
  sc project add acme/api > /dev/null
  sc project add clientx/app > /dev/null
  run sc project list
  [ "$status" -eq 0 ]
  [[ "$output" == *"acme/web"* ]]
  [[ "$output" == *"acme/api"* ]]
  [[ "$output" == *"clientx/app"* ]]
}

@test "project list: filter by vendor" {
  sc init > /dev/null
  sc project add acme/web > /dev/null
  sc project add clientx/app > /dev/null
  run sc project list acme
  [[ "$output" == *"acme/web"* ]]
  [[ "$output" != *"clientx"* ]]
}

@test "project info: shows active case, runner, source, and components" {
  scaffold acme/webshop SPRINT-1
  run sc project info acme/webshop
  [ "$status" -eq 0 ]
  [[ "$output" == *"SPRINT-1"* ]]
  [[ "$output" == *"ralph-tui"* ]]
  [[ "$output" == *"(not linked)"* ]]
}

@test "project info: unknown project exits 1" {
  sc init > /dev/null
  run sc project info acme/ghost
  [ "$status" -eq 1 ]
  [[ "$output" == *"not registered"* ]]
}

@test "project info: missing v/p exits 1" {
  sc init > /dev/null
  run sc project info
  [ "$status" -eq 1 ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  PROJECT LINK / UNLINK
# ═══════════════════════════════════════════════════════════════════════════════

@test "project link: stores canonical path in manifest" {
  scaffold acme/webshop
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  run jq -r '.vendors.acme.projects.webshop.source' ".staircase/manifest.json"
  [ "$output" = "$src" ]
  rm -rf "$src"
}

@test "project link: stores canonical path in project config.json" {
  scaffold acme/webshop
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  run jq -r '.source' "acme/webshop/.staircase/config.json"
  [ "$output" = "$src" ]
  rm -rf "$src"
}

@test "project link: resolves relative path to absolute" {
  scaffold acme/webshop
  mkdir -p "my-source"
  sc project link acme/webshop my-source > /dev/null
  run jq -r '.vendors.acme.projects.webshop.source' ".staircase/manifest.json"
  [[ "$output" = /* ]]
}

@test "project link: exits 1 for non-existent path" {
  scaffold acme/webshop
  run sc project link acme/webshop /nonexistent/path/xyz999
  [ "$status" -eq 1 ]
  [[ "$output" == *"does not exist"* ]]
}

@test "project link: exits 1 for unknown project" {
  sc init > /dev/null
  run sc project link acme/ghost /tmp
  [ "$status" -eq 1 ]
  [[ "$output" == *"not registered"* ]]
}

@test "project link: missing path arg exits 1" {
  scaffold acme/webshop
  run sc project link acme/webshop
  [ "$status" -eq 1 ]
}

@test "project link --dry-run: does not modify manifest" {
  scaffold acme/webshop
  local src; src="$(mktemp -d)"
  local before; before="$(cat .staircase/manifest.json)"
  run sc --dry-run project link acme/webshop "$src"
  [ "$status" -eq 0 ]
  [[ "$output" == *"[DRY RUN]"* ]]
  [ "$(cat .staircase/manifest.json)" = "$before" ]
  rm -rf "$src"
}

@test "project unlink: removes source from manifest and project config.json" {
  scaffold acme/webshop
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  sc project unlink acme/webshop > /dev/null
  run jq -r '.vendors.acme.projects.webshop.source // "null"' ".staircase/manifest.json"
  [ "$output" = "null" ]
  run jq -r '.source // "null"' "acme/webshop/.staircase/config.json"
  [ "$output" = "null" ]
  rm -rf "$src"
}

@test "project unlink: idempotent when no source is set" {
  scaffold acme/webshop
  run sc project unlink acme/webshop
  [ "$status" -eq 0 ]
}

@test "project link: project info shows source path when linked" {
  scaffold acme/webshop
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  run sc project info acme/webshop
  [[ "$output" == *"$src"* ]]
  [[ "$output" != *"(not linked)"* ]]
  rm -rf "$src"
}

@test "project unlink: project info shows (not linked) after unlinking" {
  scaffold acme/webshop
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  sc project unlink acme/webshop > /dev/null
  run sc project info acme/webshop
  [[ "$output" == *"(not linked)"* ]]
  rm -rf "$src"
}

# ═══════════════════════════════════════════════════════════════════════════════
#  COMPONENT
# ═══════════════════════════════════════════════════════════════════════════════

@test "component add: creates directory and updates manifest" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run sc component add acme/webshop storefront
  [ "$status" -eq 0 ]
  [ -d "acme/webshop/storefront" ]
  run jq -r '.vendors.acme.projects.webshop.components[0]' ".staircase/manifest.json"
  [ "$output" = "storefront" ]
}

@test "component add: multi-component in one call" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc component add acme/webshop storefront api admin > /dev/null
  run jq '.vendors.acme.projects.webshop.components | length' ".staircase/manifest.json"
  [ "$output" = "3" ]
  [ -d "acme/webshop/storefront" ]
  [ -d "acme/webshop/api" ]
  [ -d "acme/webshop/admin" ]
}

@test "component add: idempotent — duplicate not added twice" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc component add acme/webshop storefront > /dev/null
  sc component add acme/webshop storefront > /dev/null
  run jq '.vendors.acme.projects.webshop.components | length' ".staircase/manifest.json"
  [ "$output" = "1" ]
}

@test "component add: also updates project config.json" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc component add acme/webshop storefront > /dev/null
  run jq -r '.components[0]' "acme/webshop/.staircase/config.json"
  [ "$output" = "storefront" ]
}

@test "component remove: removes from manifest and config.json" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc component add acme/webshop storefront api > /dev/null
  sc component remove acme/webshop storefront > /dev/null
  run jq '.vendors.acme.projects.webshop.components | length' ".staircase/manifest.json"
  [ "$output" = "1" ]
  run jq -r '.vendors.acme.projects.webshop.components[0]' ".staircase/manifest.json"
  [ "$output" = "api" ]
}

@test "component list: marks missing directories with !" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc component add acme/webshop storefront > /dev/null
  rm -rf "acme/webshop/storefront"
  run sc component list acme/webshop
  [[ "$output" == *"!"* ]]
  [[ "$output" == *"storefront"* ]]
}

@test "component add: missing v/p exits 1" {
  sc init > /dev/null
  run sc component add
  [ "$status" -eq 1 ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  CASE
# ═══════════════════════════════════════════════════════════════════════════════

@test "case new: creates task dir and active/context.json" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run sc case new acme/webshop SPRINT-1
  [ "$status" -eq 0 ]
  [ -d "acme/webshop/.staircase/tasks/SPRINT-1" ]
  [ -f "acme/webshop/.staircase/active/context.json" ]
}

@test "case new: context.json has correct schema" {
  scaffold
  run jq -r '.caseId' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "SPRINT-1" ]
  run jq -r '.vendor' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "acme" ]
  run jq -r '.project' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "webshop" ]
  run jq '.stories' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "[]" ]
  run jq '.files' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "[]" ]
  run jq -r '.gitDiff' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "" ]
  run jq -r '.created' "acme/webshop/.staircase/active/context.json"
  [[ "$output" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]
}

@test "case new: context.json includes component list" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc component add acme/webshop storefront api > /dev/null
  sc case new acme/webshop SPRINT-1 > /dev/null
  run jq '.components | length' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "2" ]
}

@test "case new: sets activeCase in manifest" {
  scaffold
  run jq -r '.vendors.acme.projects.webshop.activeCase' ".staircase/manifest.json"
  [ "$output" = "SPRINT-1" ]
}

@test "case new: second case sets new activeCase" {
  scaffold
  sc case new acme/webshop BUG-042 > /dev/null
  run jq -r '.vendors.acme.projects.webshop.activeCase' ".staircase/manifest.json"
  [ "$output" = "BUG-042" ]
}

@test "case new: special characters in case ID produce valid JSON" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc case new acme/webshop 'feat/hello"world' > /dev/null
  run jq -r '.caseId' "acme/webshop/.staircase/active/context.json"
  [ "$output" = 'feat/hello"world' ]
}

@test "case new: missing case-id exits 1" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run sc case new acme/webshop
  [ "$status" -eq 1 ]
}

@test "case switch: exits 0 between two cases" {
  scaffold
  sc case new acme/webshop BUG-042 > /dev/null
  run sc case switch acme/webshop SPRINT-1
  [ "$status" -eq 0 ]
  [[ "$output" == *"switched"* ]]
}

@test "case switch: updates activeCase in manifest" {
  scaffold
  sc case new acme/webshop BUG-042 > /dev/null
  sc case switch acme/webshop SPRINT-1 > /dev/null
  run jq -r '.vendors.acme.projects.webshop.activeCase' ".staircase/manifest.json"
  [ "$output" = "SPRINT-1" ]
}

@test "case switch: saves current context to previous case directory" {
  scaffold
  sc case new acme/webshop BUG-042 > /dev/null
  sc case switch acme/webshop SPRINT-1 > /dev/null
  [ -f "acme/webshop/.staircase/tasks/BUG-042/context.json" ]
  run jq -r '.caseId' "acme/webshop/.staircase/tasks/BUG-042/context.json"
  [ "$output" = "BUG-042" ]
}

@test "case switch: restores saved context with custom data" {
  scaffold
  # Pre-populate SPRINT-1's saved context with custom fields
  printf '{"caseId":"SPRINT-1","vendor":"acme","project":"webshop","components":[],"created":"2026-01-01T00:00:00Z","stories":["story-A"],"files":["src/main.ts"],"gitDiff":"diff --git a"}\n' \
    > "acme/webshop/.staircase/tasks/SPRINT-1/context.json"
  sc case new acme/webshop BUG-042 > /dev/null
  sc case switch acme/webshop SPRINT-1 > /dev/null
  run jq -r '.stories[0]' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "story-A" ]
  run jq -r '.files[0]' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "src/main.ts" ]
}

@test "case switch: to unknown case exits 1 with hint" {
  scaffold
  run sc case switch acme/webshop GHOST-999
  [ "$status" -eq 1 ]
  [[ "$output" == *"not found"* ]]
  [[ "$output" == *"case new"* ]]
}

@test "case list: shows all cases with active marker" {
  scaffold
  sc case new acme/webshop BUG-042 > /dev/null
  sc case new acme/webshop FEAT-007 > /dev/null
  run sc case list acme/webshop
  [ "$status" -eq 0 ]
  [[ "$output" == *"SPRINT-1"* ]]
  [[ "$output" == *"BUG-042"* ]]
  [[ "$output" == *"FEAT-007"* ]]
  [[ "$output" == *"*"* ]]
}

@test "case list: active case has asterisk, others do not" {
  scaffold
  sc case new acme/webshop BUG-042 > /dev/null
  run sc case list acme/webshop
  local active_line inactive_line
  active_line="$(echo "$output" | grep 'BUG-042')"
  [[ "$active_line" == *"*"* ]]
  inactive_line="$(echo "$output" | grep 'SPRINT-1')"
  [[ "$inactive_line" != *"*"* ]]
}

@test "case info: outputs active context as JSON to stdout" {
  scaffold
  run bash -c "'$STAIRCASE' case info acme/webshop 2>/dev/null | jq -r '.caseId'"
  [ "$status" -eq 0 ]
  [ "$output" = "SPRINT-1" ]
}

@test "case info: specific case by name" {
  scaffold
  sc case new acme/webshop BUG-042 > /dev/null
  run bash -c "'$STAIRCASE' case info acme/webshop SPRINT-1 2>/dev/null | jq -r '.caseId'"
  [ "$output" = "SPRINT-1" ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  RUN
# ═══════════════════════════════════════════════════════════════════════════════

@test "run --dry-run: prints cd target and runner command" {
  scaffold
  run sc --dry-run run acme/webshop
  [ "$status" -eq 0 ]
  [[ "$output" == *"[DRY RUN]"* ]]
  [[ "$output" == *"ralph-tui"* ]]
}

@test "run --dry-run: does not modify context.json" {
  scaffold
  local before; before="$(cat acme/webshop/.staircase/active/context.json)"
  sc --dry-run run acme/webshop > /dev/null
  [ "$(cat acme/webshop/.staircase/active/context.json)" = "$before" ]
}

@test "run: missing active context exits 1 with hint" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run sc run acme/webshop
  [ "$status" -eq 1 ]
  [[ "$output" == *"case new"* ]]
}

@test "run: unknown project exits 1" {
  sc init > /dev/null
  run sc run acme/ghost
  [ "$status" -eq 1 ]
}

@test "run --dry-run: with linked source shows source path" {
  scaffold
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  run sc --dry-run run acme/webshop
  [ "$status" -eq 0 ]
  [[ "$output" == *"$src"* ]]
  rm -rf "$src"
}

@test "run --dry-run: STAIRCASE_RUNNER env overrides default" {
  scaffold
  run bash -c "STAIRCASE_RUNNER=my-runner '$STAIRCASE' --dry-run run acme/webshop 2>&1"
  [ "$status" -eq 0 ]
  [[ "$output" == *"my-runner"* ]]
}

@test "run: missing v/p exits 1" {
  sc init > /dev/null
  run sc run
  [ "$status" -eq 1 ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  LS
# ═══════════════════════════════════════════════════════════════════════════════

@test "ls: no workspace prints info and exits 0" {
  run sc ls
  [ "$status" -eq 0 ]
  [[ "$output" == *"no workspace"* ]]
}

@test "ls: empty workspace exits 0" {
  sc init > /dev/null
  run sc ls
  [ "$status" -eq 0 ]
}

@test "ls: shows vendors and projects with tree characters" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc project add acme/api > /dev/null
  run sc ls
  [ "$status" -eq 0 ]
  [[ "$output" == *"acme"* ]]
  [[ "$output" == *"webshop"* ]]
  [[ "$output" == *"api"* ]]
  [[ "$output" == *"├──"* ]]
}

@test "ls: shows active case" {
  scaffold
  run sc ls
  [[ "$output" == *"SPRINT-1"* ]]
}

@test "ls: shows (none) for project without active case" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run sc ls
  [[ "$output" == *"(none)"* ]]
}

@test "ls: shows component count" {
  scaffold
  sc component add acme/webshop storefront api > /dev/null
  run sc ls
  [[ "$output" == *"2"* ]]
}

@test "ls: shows linked indicator for linked projects" {
  scaffold
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  run sc ls
  [[ "$output" == *"linked"* ]]
  rm -rf "$src"
}

@test "ls: does not show linked indicator for unlinked projects" {
  scaffold
  run sc ls
  [[ "$output" != *"linked"* ]]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  STATUS
# ═══════════════════════════════════════════════════════════════════════════════

@test "status: shows header with VENDOR PROJECT ACTIVE CASE COMPS SRC MODIFIED" {
  sc init > /dev/null
  run sc status
  [ "$status" -eq 0 ]
  [[ "$output" == *"VENDOR"* ]]
  [[ "$output" == *"PROJECT"* ]]
  [[ "$output" == *"ACTIVE CASE"* ]]
  [[ "$output" == *"SRC"* ]]
}

@test "status: shows vendor, project, and active case" {
  scaffold
  run sc status
  [[ "$output" == *"acme"* ]]
  [[ "$output" == *"webshop"* ]]
  [[ "$output" == *"SPRINT-1"* ]]
}

@test "status: shows (none) for project without active case" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run sc status
  [[ "$output" == *"(none)"* ]]
}

@test "status: SRC column shows ✓ for linked project" {
  scaffold
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  run sc status
  [[ "$output" == *"✓"* ]]
  rm -rf "$src"
}

@test "status: SRC column shows - for unlinked project" {
  scaffold
  run sc status
  [[ "$output" != *"✓"* ]]
}

@test "status --json: outputs valid JSON manifest" {
  sc init > /dev/null
  run bash -c "'$STAIRCASE' status --json 2>/dev/null | jq empty"
  [ "$status" -eq 0 ]
}

@test "status --json: contains vendor and project data" {
  scaffold
  run bash -c "'$STAIRCASE' status --json 2>/dev/null | jq -r '.vendors.acme.projects.webshop.activeCase'"
  [ "$output" = "SPRINT-1" ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  DOCTOR
# ═══════════════════════════════════════════════════════════════════════════════

@test "doctor: healthy workspace exits 0" {
  scaffold
  run sc doctor
  [ "$status" -eq 0 ]
  [[ "$output" == *"healthy"* ]]
}

@test "doctor: no workspace exits 0 with info" {
  run sc doctor
  [ "$status" -eq 0 ]
  [[ "$output" == *"no workspace"* ]]
}

@test "doctor: missing tmp/ exits 1" {
  sc init > /dev/null
  rm -rf ".staircase/tmp"
  run sc doctor
  [ "$status" -eq 1 ]
  [[ "$output" == *"tmp"* ]]
}

@test "doctor --fix: repairs missing tmp/" {
  sc init > /dev/null
  rm -rf ".staircase/tmp"
  run sc doctor --fix
  [ "$status" -eq 0 ]
  [ -d ".staircase/tmp" ]
}

@test "doctor: invalid manifest JSON exits 1" {
  sc init > /dev/null
  printf 'not-json' > ".staircase/manifest.json"
  run sc doctor
  [ "$status" -eq 1 ]
  [[ "$output" == *"invalid JSON"* ]]
}

@test "doctor --fix: repairs invalid manifest" {
  sc init > /dev/null
  printf 'not-json' > ".staircase/manifest.json"
  run sc doctor --fix
  [ "$status" -eq 0 ]
  run jq empty ".staircase/manifest.json"
  [ "$status" -eq 0 ]
}

@test "doctor: missing project .staircase/ dir exits 1" {
  scaffold
  rm -rf "acme/webshop/.staircase"
  run sc doctor
  [ "$status" -eq 1 ]
}

@test "doctor --fix: repairs missing project .staircase/ dir" {
  scaffold
  rm -rf "acme/webshop/.staircase"
  run sc doctor --fix
  [ "$status" -eq 0 ]
  [ -d "acme/webshop/.staircase/active" ]
}

@test "doctor: activeCase without context.json exits 1" {
  scaffold
  rm -f "acme/webshop/.staircase/active/context.json"
  run sc doctor
  [ "$status" -eq 1 ]
  [[ "$output" == *"context.json"* ]]
}

@test "doctor --fix: repairs missing active context.json" {
  scaffold
  rm -f "acme/webshop/.staircase/active/context.json"
  run sc doctor --fix
  [ "$status" -eq 0 ]
  [ -f "acme/webshop/.staircase/active/context.json" ]
}

@test "doctor: missing component directory exits 1" {
  scaffold
  sc component add acme/webshop storefront > /dev/null
  rm -rf "acme/webshop/storefront"
  run sc doctor
  [ "$status" -eq 1 ]
  [[ "$output" == *"storefront"* ]]
}

@test "doctor: stale source path exits 1" {
  scaffold
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  rm -rf "$src"
  run sc doctor
  [ "$status" -eq 1 ]
  [[ "$output" == *"source path missing"* ]]
}

@test "doctor --fix: does NOT auto-fix stale source path (path may be unmounted)" {
  scaffold
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  rm -rf "$src"
  sc doctor --fix > /dev/null || true
  # Link must still be in manifest after --fix
  run jq -r '.vendors.acme.projects.webshop.source' ".staircase/manifest.json"
  [ "$output" = "$src" ]
}

@test "doctor --fix: repairs multiple issues in one pass" {
  sc init > /dev/null
  sc project add acme/alpha > /dev/null
  sc project add acme/beta > /dev/null
  sc case new acme/beta BUG-001 > /dev/null
  rm -rf ".staircase/tmp"
  rm -f "acme/beta/.staircase/active/context.json"
  run sc doctor --fix
  [ "$status" -eq 0 ]
  [[ "$output" == *"repaired"* ]]
  [ -d ".staircase/tmp" ]
  [ -f "acme/beta/.staircase/active/context.json" ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  EXPORT
# ═══════════════════════════════════════════════════════════════════════════════

@test "export: json produces valid JSON" {
  scaffold
  run bash -c "'$STAIRCASE' export acme/webshop 2>/dev/null | jq empty"
  [ "$status" -eq 0 ]
}

@test "export: json has vendor and project fields" {
  scaffold
  run bash -c "'$STAIRCASE' export acme/webshop 2>/dev/null | jq -r '.vendor'"
  [ "$output" = "acme" ]
  run bash -c "'$STAIRCASE' export acme/webshop 2>/dev/null | jq -r '.project'"
  [ "$output" = "webshop" ]
}

@test "export: json has exported_at ISO timestamp" {
  scaffold
  run bash -c "'$STAIRCASE' export acme/webshop 2>/dev/null | jq -r '.exported_at'"
  [[ "$output" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T ]]
}

@test "export: json includes active context" {
  scaffold
  run bash -c "'$STAIRCASE' export acme/webshop 2>/dev/null | jq -r '.agent.active.caseId'"
  [ "$output" = "SPRINT-1" ]
}

@test "export: json includes saved case contexts" {
  scaffold
  sc case new acme/webshop BUG-042 > /dev/null
  sc case switch acme/webshop SPRINT-1 > /dev/null
  run bash -c "'$STAIRCASE' export acme/webshop 2>/dev/null | jq -r '.agent.tasks | keys[]'"
  [ "$status" -eq 0 ]
  [[ "$output" == *"SPRINT-1"* ]]
  [[ "$output" == *"BUG-042"* ]]
}

@test "export --format tar: creates named tarball" {
  scaffold
  sc export acme/webshop --format tar > /dev/null 2>&1
  local ds; ds="$(date -u +%Y-%m-%d)"
  [ -f "staircase-export-acme-webshop-${ds}.tar.gz" ]
}

@test "export --format tar: tarball contains .staircase directory" {
  scaffold
  sc export acme/webshop --format tar > /dev/null 2>&1
  local ds; ds="$(date -u +%Y-%m-%d)"
  run tar tzf "staircase-export-acme-webshop-${ds}.tar.gz"
  [[ "$output" == *"acme/webshop/.staircase/"* ]]
}

@test "export: unknown project exits 1" {
  sc init > /dev/null
  run sc export acme/ghost
  [ "$status" -eq 1 ]
  [[ "$output" == *"not registered"* ]]
}

@test "export: unknown format exits 1" {
  scaffold
  run sc export acme/webshop --format xml
  [ "$status" -eq 1 ]
}

@test "export --dry-run: does not create tarball" {
  scaffold
  run sc --dry-run export acme/webshop --format tar
  [ "$status" -eq 0 ]
  [[ "$output" == *"[DRY RUN]"* ]]
  local ds; ds="$(date -u +%Y-%m-%d)"
  [ ! -f "staircase-export-acme-webshop-${ds}.tar.gz" ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  HOOKS
# ═══════════════════════════════════════════════════════════════════════════════

@test "hooks install: creates hooks.d stubs and installs git hooks" {
  scaffold
  git -C "acme/webshop" init -q
  run sc hooks install acme/webshop
  [ "$status" -eq 0 ]
  [ -d "hooks.d" ]
  [ -f "hooks.d/01-format.sh" ]
  [ -f "hooks.d/99-post-run.sh" ]
  [ -f "acme/webshop/.git/hooks/pre-commit" ]
  [ -f "acme/webshop/.git/hooks/post-merge" ]
}

@test "hooks install: stubs and hooks are executable" {
  scaffold
  git -C "acme/webshop" init -q
  sc hooks install acme/webshop > /dev/null
  [ -x "hooks.d/01-format.sh" ]
  [ -x "hooks.d/99-post-run.sh" ]
  [ -x "acme/webshop/.git/hooks/pre-commit" ]
  [ -x "acme/webshop/.git/hooks/post-merge" ]
}

@test "hooks install: idempotent — guard block appears exactly once" {
  scaffold
  git -C "acme/webshop" init -q
  sc hooks install acme/webshop > /dev/null
  sc hooks install acme/webshop > /dev/null
  sc hooks install acme/webshop > /dev/null
  run grep -c '>>> stAirCase hooks <<<' "acme/webshop/.git/hooks/pre-commit"
  [ "$output" -eq 1 ]
  run grep -c '>>> stAirCase hooks <<<' "acme/webshop/.git/hooks/post-merge"
  [ "$output" -eq 1 ]
}

@test "hooks install: preserves existing hook content" {
  scaffold
  git -C "acme/webshop" init -q
  mkdir -p "acme/webshop/.git/hooks"
  printf '#!/usr/bin/env bash\necho "my-custom-hook"\n' > "acme/webshop/.git/hooks/pre-commit"
  chmod +x "acme/webshop/.git/hooks/pre-commit"
  sc hooks install acme/webshop > /dev/null
  run grep "my-custom-hook" "acme/webshop/.git/hooks/pre-commit"
  [ "$status" -eq 0 ]
}

@test "hooks install: silently skips project with no .git" {
  scaffold
  run sc hooks install acme/webshop
  [ "$status" -eq 0 ]
  [ ! -d "hooks.d" ] || [ ! -f "acme/webshop/.git/hooks/pre-commit" ]
}

@test "hooks install: installs into component repos" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc component add acme/webshop storefront > /dev/null
  sc case new acme/webshop SPRINT-1 > /dev/null
  git -C "acme/webshop/storefront" init -q
  sc hooks install acme/webshop > /dev/null
  [ -f "acme/webshop/storefront/.git/hooks/pre-commit" ]
}

@test "hooks install: missing v/p exits 1" {
  sc init > /dev/null
  run sc hooks install
  [ "$status" -eq 1 ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  DRY-RUN (global flag)
# ═══════════════════════════════════════════════════════════════════════════════

@test "dry-run: init does not create files" {
  run sc --dry-run init
  [ "$status" -eq 0 ]
  [[ "$output" == *"[DRY RUN]"* ]]
  [ ! -d ".staircase" ]
}

@test "dry-run: project add does not create directories" {
  sc init > /dev/null
  run sc --dry-run project add acme/webshop
  [ "$status" -eq 0 ]
  [ ! -d "acme/webshop" ]
}

@test "dry-run: case new does not create task directory" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  run sc --dry-run case new acme/webshop SPRINT-99
  [ "$status" -eq 0 ]
  [ ! -d "acme/webshop/.staircase/tasks/SPRINT-99" ]
}

@test "dry-run: component add does not create dir or modify manifest" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  local before; before="$(cat .staircase/manifest.json)"
  run sc --dry-run component add acme/webshop storefront
  [ "$status" -eq 0 ]
  [ ! -d "acme/webshop/storefront" ]
  [ "$(cat .staircase/manifest.json)" = "$before" ]
}

@test "dry-run: DRY_RUN env var works same as --dry-run flag" {
  run bash -c "DRY_RUN=1 '$STAIRCASE' init 2>&1"
  [ "$status" -eq 0 ]
  [[ "$output" == *"[DRY RUN]"* ]]
  [ ! -d ".staircase" ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  ENVIRONMENT & FLAGS
# ═══════════════════════════════════════════════════════════════════════════════

@test "--version: prints 1.1.0" {
  run "$STAIRCASE" --version
  [ "$status" -eq 0 ]
  [ "$output" = "staircase 1.1.0" ]
}

@test "--help: prints usage with project link and unlink" {
  run sc --help
  [ "$status" -eq 0 ]
  [[ "$output" == *"Usage:"* ]]
  [[ "$output" == *"project link"* ]]
  [[ "$output" == *"project unlink"* ]]
}

@test "NO_COLOR: suppresses ANSI escape codes" {
  run bash -c "NO_COLOR=1 '$STAIRCASE' init 2>&1"
  [ "$status" -eq 0 ]
  [[ "$output" != *$'\033['* ]]
}

@test "STAIRCASE_RUNNER env: overrides runner in dry-run" {
  scaffold
  run bash -c "STAIRCASE_RUNNER=my-runner '$STAIRCASE' --dry-run run acme/webshop 2>&1"
  [[ "$output" == *"my-runner"* ]]
}

@test "unknown top-level command: exits 1" {
  run sc gibberish
  [ "$status" -eq 1 ]
}

@test "project unknown subcommand: exits 1" {
  sc init > /dev/null
  run sc project gibberish
  [ "$status" -eq 1 ]
}

@test "case unknown subcommand: exits 1" {
  sc init > /dev/null
  run sc case gibberish
  [ "$status" -eq 1 ]
}

# ═══════════════════════════════════════════════════════════════════════════════
#  EDGE CASES & INTEGRATION
# ═══════════════════════════════════════════════════════════════════════════════

@test "atomicity: rapid case creates leave manifest valid JSON" {
  scaffold
  for i in $(seq 1 10); do
    sc case new acme/webshop "RAPID-$i" > /dev/null
  done
  run jq empty ".staircase/manifest.json"
  [ "$status" -eq 0 ]
  run jq -r '.vendors.acme.projects.webshop.activeCase' ".staircase/manifest.json"
  [ "$output" = "RAPID-10" ]
}

@test "multi-vendor: cases are independent across vendors" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc project add clientx/app > /dev/null
  sc case new acme/webshop SPRINT-1 > /dev/null
  sc case new clientx/app PHASE-1 > /dev/null
  run jq -r '.vendors.acme.projects.webshop.activeCase' ".staircase/manifest.json"
  [ "$output" = "SPRINT-1" ]
  run jq -r '.vendors.clientx.projects.app.activeCase' ".staircase/manifest.json"
  [ "$output" = "PHASE-1" ]
  # Switching one does not affect the other
  sc case new acme/webshop BUG-001 > /dev/null
  run jq -r '.vendors.clientx.projects.app.activeCase' ".staircase/manifest.json"
  [ "$output" = "PHASE-1" ]
}

@test "full lifecycle: init → project → components → cases → link → export → doctor" {
  sc init > /dev/null
  sc project add acme/webshop > /dev/null
  sc component add acme/webshop storefront api > /dev/null
  sc case new acme/webshop SPRINT-1 > /dev/null
  sc case new acme/webshop BUG-042 > /dev/null
  sc case switch acme/webshop SPRINT-1 > /dev/null

  # Active case is correct
  run jq -r '.vendors.acme.projects.webshop.activeCase' ".staircase/manifest.json"
  [ "$output" = "SPRINT-1" ]

  # Context carries component list
  run jq '.components | length' "acme/webshop/.staircase/active/context.json"
  [ "$output" = "2" ]

  # Link a source directory
  local src; src="$(mktemp -d)"
  sc project link acme/webshop "$src" > /dev/null
  run jq -r '.vendors.acme.projects.webshop.source' ".staircase/manifest.json"
  [ "$output" = "$src" ]

  # Export is valid JSON with correct project
  run bash -c "'$STAIRCASE' export acme/webshop 2>/dev/null | jq -r '.project'"
  [ "$output" = "webshop" ]

  # Doctor is happy
  run sc doctor
  [ "$status" -eq 0 ]
  [[ "$output" == *"healthy"* ]]

  rm -rf "$src"
}
