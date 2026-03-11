# stAirCase Demo Workspace

Interactive walkthrough of the full stAirCase workflow. Run every command yourself — the workspace is self-contained and disposable.

**Time:** ~10 min · **Requirements:** `bash` 4+, `jq`, `git`

## Setup

```sh
# From the repo root
export PATH="$(pwd):$PATH"
cd examples/demo-workspace
```

## 1 — Initialize

```sh
staircase init
# ✔  initialized workspace 'demo-workspace'
```

Two files appear: `.staircase/config.json` and `.staircase/manifest.json`. The manifest starts empty:

```sh
cat .staircase/manifest.json
# {"version":"1.0","vendors":{}}
```

Running `init` again is safe — it's idempotent.

## 2 — Add vendors and projects

```sh
# Add two projects (vendor 'acme' is auto-created)
staircase project add acme/webshop
# ✔  auto-created vendor 'acme'
# ✔  added project 'acme/webshop'

staircase project add acme/api
# ✔  added project 'acme/api'
```

Each project gets its own `.staircase/` directory for agent context:

```sh
ls acme/webshop/.staircase/
# active/   config.json   tasks/
```

## 3 — Add components

Components are the subprojects the agent works across within a project. Add several at once:

```sh
staircase component add acme/webshop storefront checkout-api admin-panel
# ✔  added component 'storefront' to acme/webshop
# ✔  added component 'checkout-api' to acme/webshop
# ✔  added component 'admin-panel' to acme/webshop
```

## 4 — Create and switch cases

A case is a unit of work (sprint, bug, feature). Each gets its own context snapshot.

```sh
# Open three cases for webshop
staircase case new acme/webshop SPRINT-1
staircase case new acme/webshop SPRINT-2
staircase case new acme/webshop BUG-042

# See them all (* = active)
staircase case list acme/webshop
#   SPRINT-1                2026-03-11 10:01:23
#   SPRINT-2                2026-03-11 10:01:24
# * BUG-042                 2026-03-11 10:01:25
```

Switch back to SPRINT-1 — the current context is saved first, then SPRINT-1 is loaded:

```sh
staircase case switch acme/webshop SPRINT-1
# ✔  switched to 'SPRINT-1' for acme/webshop

# The active context now has SPRINT-1
cat acme/webshop/.staircase/active/context.json | jq '.caseId'
# "SPRINT-1"

# BUG-042's context was saved automatically
cat acme/webshop/.staircase/tasks/BUG-042/context.json | jq '.caseId'
# "BUG-042"
```

The switch is atomic: save current context → load new context → update manifest. Each step uses `mktemp` + `mv`, so a crash mid-switch never leaves corrupted state.

The context JSON includes the case ID, vendor, project, component list, and fields your agent extends:

```sh
cat acme/webshop/.staircase/active/context.json | jq .
# {
#   "caseId": "SPRINT-1",
#   "vendor": "acme",
#   "project": "webshop",
#   "components": ["storefront", "checkout-api", "admin-panel"],
#   "created": "2026-03-11T10:01:23Z",
#   "stories": [],
#   "files": [],
#   "gitDiff": ""
# }
```

## 5 — Source linking

Your source code likely lives elsewhere. Link it so the agent runs in your real codebase while staircase metadata stays in the workspace:

```sh
# Link an external source directory (must exist)
staircase project link acme/webshop ~/src/webshop
# ✔  linked acme/webshop → /Users/you/src/webshop

staircase project link acme/api ~/src/api
# ✔  linked acme/api → /Users/you/src/api
```

The path is resolved to its canonical absolute form and stored in both the manifest and the project's `config.json`. After linking:

- `staircase run` `cd`s into `~/src/webshop` instead of the workspace stub
- The runner receives the absolute context path — your source repo stays untouched
- `project info` shows the source path
- `ls` and `status` flag the project as linked

Unlink when you're done or the path changes:

```sh
staircase project unlink acme/api
# ✔  unlinked acme/api

staircase project info acme/api
#   source:       (not linked)
```

## 6 — Workspace inspection

```sh
# Tree view — quick visual check
staircase ls
# demo-workspace  (/path/to/demo-workspace)
# └── acme
#     ├── api
#     │   ├── case:       (none)
#     │   └── components: 0
#     └── webshop
#         ├── case:       SPRINT-1
#         └── components: 3 [→ linked]

# Table view — good for scripting
staircase status
# VENDOR        PROJECT               ACTIVE CASE       COMPS   SRC  MODIFIED
# ------------  --------------------  ----------------  ------  ---  -------------------
# acme          api                   (none)            0       -    -
# acme          webshop               SPRINT-1          3       ✓    2026-03-11 10:01:23

# Machine-readable manifest — pipe to jq, feed to CI
staircase status --json | jq '.vendors.acme.projects | keys'
# ["api", "webshop"]
```

Show detailed project information:

```sh
staircase project info acme/webshop
#   acme/webshop
#
#   source:       /Users/you/src/webshop
#   active case:  SPRINT-1
#   runner:       ralph-tui
#   components:
#     - storefront
#     - checkout-api
#     - admin-panel
```

## 7 — Doctor and self-healing

```sh
staircase doctor
# ✔  workspace is healthy
```

Break something on purpose to see the repair:

```sh
# Remove the tmp directory
rm -rf .staircase/tmp

staircase doctor
# ⚠  tmp/ missing
# ✖  1 issue(s) remaining

# Auto-repair
staircase doctor --fix
# ⚠  tmp/ missing
# ✔  repaired tmp/
# ✔  all issues repaired

staircase doctor
# ✔  workspace is healthy
```

Doctor checks for: missing/invalid config and manifest, missing vendor and project `.staircase/` directories, active cases without `context.json`, missing component directories, stale source paths (linked but directory gone), and unwritable tmp directory.

The stale-source check is intentionally not auto-fixed — the directory may be on a temporarily unmounted volume. Resolve with `project unlink` or by remounting:

```sh
# If a linked source goes missing:
staircase doctor
# ⚠  acme/webshop: source path missing: /Users/you/src/webshop
# ✖  1 issue(s) remaining

# Fix by unlinking or remounting
staircase project unlink acme/webshop
```

## 8 — Export

```sh
# JSON to stdout — includes manifest entry, active context, and all saved cases
staircase export acme/webshop | jq '.agent.tasks | keys'
# ["BUG-042", "SPRINT-1", "SPRINT-2"]

# Tar archive — good for backups or sharing
staircase export acme/webshop --format tar
# ✔  exported to staircase-export-acme-webshop-2026-03-11.tar.gz
```

## 9 — Git hooks

```sh
git -C acme/webshop init -q
staircase hooks install acme/webshop
# ✔  created 01-format.sh
# ✔  created 99-post-run.sh
# ✔  pre-commit hook for acme/webshop
# ✔  post-merge hook for acme/webshop
```

Two hooks are installed in every `.git` repo found under the project and its components:

- **pre-commit** runs `hooks.d/01-format.sh` — edit this to call `prettier`, `gofmt`, `black`, etc.
- **post-merge** runs `staircase doctor --fix` silently to keep the workspace healthy after pulls.

Running `hooks install` again is safe — it detects existing blocks and skips them.

## 10 — Dry-run mode

Every command supports `--dry-run`. Nothing touches disk:

```sh
staircase --dry-run project add acme/new-service
# →  [DRY RUN] add project 'acme/new-service'

staircase --dry-run case new acme/webshop SPRINT-3
# →  [DRY RUN] create case 'SPRINT-3' for acme/webshop

staircase --dry-run project link acme/webshop ~/src/webshop
# →  [DRY RUN] link acme/webshop → /Users/you/src/webshop

staircase --dry-run component add acme/webshop payments
# →  [DRY RUN] add component 'payments'
```

Set `DRY_RUN=1` in your environment for the same effect across an entire shell session.

## 11 — Run an agent

```sh
# Launch the default runner (ralph-tui) — skip if not installed
staircase run acme/webshop

# Override the runner for this invocation
staircase run acme/webshop --runner claude-code

# Inject agent config merged into context before launch
staircase run acme/webshop --config '{"model":"claude-sonnet","maxTokens":8192}'

# Preview what would run without executing
staircase --dry-run run acme/webshop
# →  [DRY RUN] cd /Users/you/src/webshop && ralph-tui --prd /path/to/.staircase/active/context.json
```

When a source is linked, `run` `cd`s into the real codebase and passes the absolute context path to the runner. When unlinked, it `cd`s into the workspace stub instead.

Runner resolution order (highest wins): `--runner` flag → `STAIRCASE_RUNNER` env → project config → workspace config → `ralph-tui`.

## Cleanup

```sh
# Tear down everything — it's just files
cd ../..
rm -rf examples/demo-workspace/.staircase examples/demo-workspace/acme examples/demo-workspace/hooks.d
```

Or delete the whole `demo-workspace` directory and start fresh.

## What to try next

- Add a second vendor (`clientx/landing`) and create cases there — switching one project's case never affects the other
- Break the manifest (`echo "bad" > .staircase/manifest.json`) and watch `doctor --fix` recover it
- Set `NO_COLOR=1` and pipe output to see CI-safe formatting
- Set `STAIRCASE_DEBUG=1` to trace every internal command
- Run `staircase config runner claude-code` to change the default runner for the workspace
