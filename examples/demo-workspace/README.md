# stAirCase Demo Workspace

Interactive walkthrough of the full stAirCase v1.2 workflow. Run every command yourself — the workspace is self-contained and disposable.

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

Three directories and files appear:

```sh
ls .staircase/
# config.json  manifest.json  prd/  tmp/
```

The `prd/` directory is new in v1.2 — all case files live here, flat, namespaced by vendor and project.

```sh
cat .staircase/manifest.json
# {"version":"1.2","vendors":{}}
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

Project directories are plain stub dirs — no `.staircase/` metadata inside them. In v1.2, all context lives in the workspace's `.staircase/prd/`.

```sh
ls acme/webshop/
# (empty)
```

You can also create a project and link it to an existing source directory in one step:

```sh
# staircase project add acme/webshop ~/src/webshop
# This creates acme/webshop as a symlink → ~/src/webshop
```

## 3 — Add components

Components are the subprojects the agent works across within a project.

```sh
staircase component add acme/webshop storefront checkout-api admin-panel
# ✔  added component 'storefront' to acme/webshop
# ✔  added component 'checkout-api' to acme/webshop
# ✔  added component 'admin-panel' to acme/webshop
```

## 4 — Create cases

A case is a unit of work (sprint, bug, feature). Each case is a PRD JSON file stored flat in `.staircase/prd/`. Multiple cases per project coexist permanently — there is no "active" concept.

```sh
# Open three cases for webshop
staircase case new acme/webshop SPRINT-1
staircase case new acme/webshop SPRINT-2
staircase case new acme/webshop BUG-042
```

All three PRD files now exist:

```sh
ls .staircase/prd/
# acme.webshop.SPRINT-1.json
# acme.webshop.SPRINT-2.json
# acme.webshop.BUG-042.json
```

You can also create vendor-scope cases (spanning all projects):

```sh
staircase case new acme SYNC-001
# ✔  created case 'SYNC-001' (acme.SYNC-001)
ls .staircase/prd/
# acme.SYNC-001.json  acme.webshop.BUG-042.json  ...
```

List all cases (last created is marked with `*`):

```sh
staircase case list acme/webshop
#   acme.webshop.SPRINT-1            project  2026-03-12 10:01:23
#   acme.webshop.SPRINT-2            project  2026-03-12 10:01:24
# * acme.webshop.BUG-042             project  2026-03-12 10:01:25
```

Inspect a case:

```sh
cat .staircase/prd/acme.webshop.SPRINT-1.json | jq .
# {
#   "id": "acme.webshop.SPRINT-1",
#   "vendor": "acme",
#   "project": "webshop",
#   "scope": "project",
#   "components": ["storefront", "checkout-api", "admin-panel"],
#   "created": "2026-03-12T10:01:23Z",
#   "modified": "2026-03-12T10:01:23Z",
#   "stories": [],
#   "files": [],
#   "gitDiff": ""
# }
```

The agent reads this as its PRD. Extend `stories`, `files`, and `gitDiff` as your workflow needs.

Delete a case when you're done:

```sh
staircase case delete acme/webshop BUG-042
# ✔  deleted case 'acme.webshop.BUG-042'
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

The workspace project directory becomes a symlink:

```sh
ls -la acme/
# lrwxr-xr-x  acme/webshop -> /Users/you/src/webshop
# lrwxr-xr-x  acme/api     -> /Users/you/src/api
```

After linking:
- `staircase run` `cd`s into the real codebase and passes the absolute PRD path to the runner
- Your source repos stay clean — no `.staircase/` metadata bleeds in
- `project info` shows the linked path
- `ls` shows `→ target` next to the project
- `status` flags the project as linked

Unlink when you're done or the path changes:

```sh
staircase project unlink acme/api
# ✔  unlinked acme/api

staircase project info acme/api
#   source:       (not linked)
```

After unlinking, the symlink is replaced with an empty stub directory.

## 6 — Workspace inspection

```sh
# Tree view — quick visual check
staircase ls
# demo-workspace  (/path/to/demo-workspace)
# └── acme  (1 vendor case)
#     ├── api
#     │   ├── last case:  (none)
#     │   ├── cases:      0
#     │   └── components: 0
#     └── webshop
#         ├── last case:  SPRINT-2
#         ├── cases:      2
#         └── components: 3 [→ /Users/you/src/webshop]

# Table view — good for scripting
staircase status
# VENDOR        PROJECT               LAST CASE         CASES  SRC  MODIFIED
# ------------  --------------------  ----------------  -----  ---  -------------------
# acme          api                   (none)            0      -    -
# acme          webshop               SPRINT-2          2      ✓    2026-03-12 10:01:24

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
#   last case:    SPRINT-2
#   runner:       ralph-tui
#   cases:        2
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
# Remove the tmp directory and prd directory
rm -rf .staircase/tmp .staircase/prd

staircase doctor
# ⚠  tmp/ missing
# ⚠  prd/ missing
# ✖  2 issue(s) remaining

# Auto-repair
staircase doctor --fix
# ⚠  tmp/ missing
# ✔  repaired tmp/
# ⚠  prd/ missing
# ✔  repaired prd/
# ✔  all issues repaired

staircase doctor
# ✔  workspace is healthy
```

Doctor v1.2 also warns if any per-project `.staircase/` directories are found (a sign of a v1.1 workspace not yet migrated):

```sh
mkdir -p acme/api/.staircase
staircase doctor
# ⚠  acme/api: per-project .staircase/ found — run: staircase migrate
```

## 8 — Migrating from v1.1

If you have a v1.1 workspace, migrate it in-place:

```sh
staircase migrate
# →  migrating workspace to v1.2...
# ✔  migrated case acme.webshop.SPRINT-1
# ✔  removed acme/webshop/.staircase/
# ✔  migration complete — workspace is now v1.2
```

The migrate command:
1. Creates `.staircase/prd/`
2. Converts all `v/p/.staircase/tasks/*/context.json` and `active/context.json` to flat PRD files
3. Moves runner from per-project `config.json` to the manifest
4. Converts `activeCase` → `lastCase` in the manifest
5. If a source path is set: removes the stub dir and creates a symlink
6. Removes per-project `.staircase/` directories
7. Bumps manifest and config versions to `"1.2"`

## 9 — Export

```sh
# JSON to stdout — includes manifest entry and all case PRDs
staircase export acme/webshop | jq '.cases | keys'
# ["SPRINT-1", "SPRINT-2"]

staircase export acme/webshop | jq '.cases["SPRINT-1"].components'
# ["storefront", "checkout-api", "admin-panel"]

# Tar archive — good for backups or sharing
staircase export acme/webshop --format tar
# ✔  exported to staircase-export-acme-webshop-2026-03-12.tar.gz
```

## 10 — Git hooks

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

## 11 — Dry-run mode

Every command supports `--dry-run`. Nothing touches disk:

```sh
staircase --dry-run project add acme/new-service
# →  [DRY RUN] add project 'acme/new-service'

staircase --dry-run case new acme/webshop SPRINT-3
# →  [DRY RUN] create case 'SPRINT-3' (acme.webshop.SPRINT-3)

staircase --dry-run case delete acme/webshop SPRINT-2
# →  [DRY RUN] delete case 'acme.webshop.SPRINT-2'

staircase --dry-run project link acme/webshop ~/src/webshop
# →  [DRY RUN] link acme/webshop → /Users/you/src/webshop

staircase --dry-run component add acme/webshop payments
# →  [DRY RUN] add component 'payments'
```

Set `DRY_RUN=1` in your environment for the same effect across an entire shell session.

## 12 — Run an agent

```sh
# Launch the default runner (ralph-tui) — skip if not installed
staircase run acme/webshop SPRINT-1

# Override the runner for this invocation
staircase run acme/webshop SPRINT-1 --runner claude-code

# Inject agent config merged into a temp copy of the PRD before launch
staircase run acme/webshop SPRINT-1 --config '{"model":"claude-sonnet","maxTokens":8192}'

# Preview what would run without executing
staircase --dry-run run acme/webshop SPRINT-1
# →  [DRY RUN] cd /Users/you/src/webshop && ralph-tui --prd /path/.staircase/prd/acme.webshop.SPRINT-1.json

# Vendor-scope run
staircase run acme SYNC-001
```

The `case-id` is required — there is no "active" case. Run any case by name at any time.

When a source is linked, `run` `cd`s into the real codebase and passes the absolute PRD path to the runner. The original PRD file is never modified by `run` (even with `--config` — that merges into a temp copy).

After a successful launch, `lastCase` in the manifest is updated to the run case-id.

Runner resolution order (highest wins): `--runner` flag → `STAIRCASE_RUNNER` env → project manifest entry → workspace config → `ralph-tui`.

## Cleanup

```sh
# Tear down everything — it's just files and symlinks
cd ../..
rm -rf examples/demo-workspace/.staircase examples/demo-workspace/acme examples/demo-workspace/hooks.d
```

Or delete the whole `demo-workspace` directory and start fresh.

## What to try next

- Add a second vendor (`clientx/landing`) and create cases there — cases are fully independent across vendors and projects
- Create a vendor-scope case (`staircase case new clientx QUARTERLY-REVIEW`) for cross-project work
- Break the manifest (`echo "bad" > .staircase/manifest.json`) and watch `doctor --fix` recover it
- Set `NO_COLOR=1` and pipe output to see CI-safe formatting
- Set `STAIRCASE_DEBUG=1` to trace every internal command
- Run `staircase config runner claude-code` to change the default runner for the workspace
- Try `staircase case list` (no args) to see all cases across all vendors and projects at once
