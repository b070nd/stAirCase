# stAirCase

**AI workspace orchestrator** — structure your repos, manage agent context, run cases against your agent.

stAirCase gives your AI coding agents a workspace where projects are organized under vendors, broken into components, and every unit of work gets its own *case* — a PRD file that can be passed to any runner. Multiple cases per project coexist permanently. Think of it like Composer for AI agent workflows.

```
my-workspace/
├── .staircase/
│   ├── config.json        ← workspace config
│   ├── manifest.json      ← all vendor/project metadata
│   ├── prd/               ← ALL case files (flat)
│   │   ├── acme.webshop.SPRINT-1.json
│   │   ├── acme.webshop.BUG-042.json
│   │   └── acme.SYNC-001.json    ← vendor-scope case
│   └── tmp/
├── acme/
│   ├── webshop  →  /real/src/webshop   ← symlink (when linked)
│   └── cms/                            ← stub dir (when unlinked)
└── hooks.d/               ← lifecycle hooks
```

## Install

```bash
# macOS (Homebrew)
brew tap b070nd/staircase
brew install staircase

# Any platform
curl -sL https://raw.githubusercontent.com/b070nd/stAIrcase/main/staircase \
  -o /usr/local/bin/staircase && chmod +x /usr/local/bin/staircase
```

**Requirements:** `bash` 4+, `jq`

## Quick Start

```bash
mkdir my-workspace && cd my-workspace

staircase init                                          # create workspace
staircase project add acme/webshop                      # add project (stub dir)
staircase component add acme/webshop storefront api     # add components
staircase case new acme/webshop SPRINT-1                # create a case
staircase run acme/webshop SPRINT-1                     # launch your agent
```

If your source code lives elsewhere, link it:

```bash
staircase project add acme/webshop ~/src/webshop        # add with source → creates symlink
# or link after the fact:
staircase project link acme/webshop ~/src/webshop       # converts stub to symlink
staircase run acme/webshop SPRINT-1                     # runner executes inside ~/src/webshop
```

## Migrating from v1.1

```bash
cd my-workspace
staircase migrate
```

This converts all per-project `.staircase/tasks/*/context.json` files into flat PRD files under `.staircase/prd/`, moves runner config from project `config.json` to the manifest, replaces stub dirs with symlinks where a source path is set, and bumps the schema version to `1.2`.

## Concepts

**Vendor** — a namespace grouping (`acme`, `client-x`). Just a directory.

**Project** — a product or service (`acme/webshop`). The unit of work. Project dirs are either symlinks to real source code or empty stub directories — no `.staircase/` metadata lives inside them.

**Component** — a subproject within a project (`storefront`, `checkout-api`). The agent sees all components when running against a project.

**Case** — a unit of work scoped to a project (or a whole vendor). Each case is a PRD JSON file stored flat in `.staircase/prd/`. Multiple cases coexist — run any of them by name. Cases can also be vendor-scope (not tied to a specific project).

**Source link** — when set, the project directory becomes a symlink to the real codebase, and `run` `cd`s into the real source.

## Commands

### Workspace

```bash
staircase init [--name <n>]             # Create workspace in current directory
staircase migrate                       # Migrate v1.1 workspace to v1.2
staircase config runner claude-code     # Set default agent runner
staircase config --list                 # Show resolved config with sources
```

`init` creates `.staircase/config.json`, `.staircase/manifest.json`, `.staircase/prd/`, and `.staircase/tmp/`.

### Structure

```bash
staircase vendor add acme                                   # create vendor
staircase vendor list                                       # list vendors
staircase vendor remove acme                                # remove (must be empty)

staircase project add acme/webshop                          # create stub project
staircase project add acme/webshop ~/src/webshop            # create project + symlink to source
staircase project list                                      # list all projects
staircase project list acme                                 # filter by vendor
staircase project info acme/webshop                         # show details
staircase project remove acme/webshop                       # remove from manifest + filesystem

staircase component add acme/webshop storefront api admin   # add components (multi)
staircase component list acme/webshop                       # list components
staircase component remove acme/webshop admin               # remove one
```

`project add` auto-creates the vendor if it doesn't exist yet. When a source path is provided, the project directory is created as a symlink immediately.

### Source Linking

Associate a real codebase with a project:

```bash
staircase project link acme/webshop ~/src/webshop           # link (or update) symlink
staircase project link acme/api ~/src/api                   # each project links independently
staircase project unlink acme/webshop                       # remove link, restore stub dir
```

The path is resolved to its canonical absolute form at link time. When `project add` is called with a source path, the symlink is created immediately. `project link` on an already-linked project updates the symlink atomically.

When a source is linked:
- The project directory in the workspace is a symlink (`readlink` shows the target)
- `staircase run` `cd`s into the source directory and passes the absolute PRD path to the runner
- Your source repos stay clean — no `.staircase/` metadata bleeds in
- `staircase project info` shows the linked path
- `staircase ls` shows `→ target` next to the project
- `staircase status` shows `✓` in the `SRC` column
- `staircase doctor` warns if the symlink target goes missing

### Cases

```bash
staircase case new acme/webshop SPRINT-1        # create a project-scope case
staircase case new acme/webshop BUG-042         # create another — both persist
staircase case new acme SYNC-001                # create a vendor-scope case

staircase case list                             # list all cases in workspace
staircase case list acme                        # list all acme cases
staircase case list acme/webshop               # list webshop cases (* = lastCase)

staircase case info acme/webshop SPRINT-1      # show PRD JSON
staircase case info acme SYNC-001              # show vendor-scope PRD

staircase case delete acme/webshop BUG-042     # delete a case
```

There is no "active case" concept. All cases coexist. Run any case by name with `run`.

### Running Your Agent

```bash
staircase run acme/webshop SPRINT-1                         # launch for a project case
staircase run acme SYNC-001                                 # launch for a vendor-scope case
staircase run acme/webshop SPRINT-1 --runner claude-code    # override runner
staircase run acme/webshop SPRINT-1 --config '{"model":"gpt4"}'  # inject config (temp copy)
```

`run` resolves the runner, then:

- **Project-scope, linked**: `cd`s into the source directory, launches `<runner> --prd /abs/path/to/prd.json`
- **Project-scope, unlinked**: `cd`s into `workspace/acme/webshop/`, launches `<runner> --prd /abs/path/to/prd.json`
- **Vendor-scope**: `cd`s into `workspace/acme/`, launches `<runner> --prd /abs/path/to/prd.json`

`--config` merges the provided JSON into a temp copy of the PRD before passing it to the runner. The original PRD file is never mutated.

After a successful launch, `lastCase` in the manifest is updated to the case-id.

**Runner resolution order** (highest wins): `--runner` flag → `STAIRCASE_RUNNER` env → project manifest entry → workspace config → `ralph-tui`

### Inspection

```bash
staircase ls              # tree view
staircase status          # table view
staircase status --json   # machine-readable manifest
```

**`ls` output:**
```
my-workspace  (/home/user/my-workspace)
├── acme  (1 vendor case)
│   ├── cms/
│   │   ├── last case:  BUG-042
│   │   ├── cases:      2
│   │   └── components: 2
│   └── webshop
│       ├── last case:  SPRINT-1
│       ├── cases:      3
│       └── components: 3 [→ /real/src/webshop]
└── clientx
    └── landing/
        ├── last case:  (none)
        ├── cases:      0
        └── components: 1
```

**`status` output:**
```
VENDOR        PROJECT               LAST CASE         CASES  SRC  MODIFIED
------------  --------------------  ----------------  -----  ---  -------------------
acme          cms                   BUG-042           2      -    2026-03-12 10:05:12
acme          webshop               SPRINT-1          3      ✓    2026-03-12 10:01:23
clientx       landing               (none)            0      -    -
```

### Health Checks

```bash
staircase doctor          # check workspace health
staircase doctor --fix    # auto-repair issues
```

Doctor v1.2 checks: missing/invalid config and manifest, missing `prd/` dir, invalid PRD JSON files, id-filename mismatches in PRD files, missing vendor directories, stale symlink targets, stale source paths (source set but dir missing), missing component directories, and per-project `.staircase/` dirs (suggests `migrate`). `--fix` creates `prd/` dir and vendor dirs; symlink staleness is not auto-fixed.

### Export

```bash
staircase export acme/webshop               # JSON to stdout
staircase export acme/webshop --format tar  # .tar.gz archive
```

JSON export includes manifest entry and all case PRDs as `{vendor, project, exported_at, manifest, cases: {<case-id>: <prd>}}`.

### Git Hooks

```bash
staircase hooks install acme/webshop
```

Scans the project root and all component directories for `.git` repos and installs:

- **pre-commit** — runs `hooks.d/01-format.sh` (stub for your formatter)
- **post-merge** — runs `staircase doctor --fix` silently

### Dry Run

Every command supports `--dry-run`:

```bash
staircase --dry-run project add acme/new-service
staircase --dry-run project link acme/webshop ~/src/webshop
staircase --dry-run case new acme/webshop SPRINT-99
staircase --dry-run case delete acme/webshop SPRINT-99
staircase --dry-run component add acme/webshop payments
staircase --dry-run run acme/webshop SPRINT-1
```

## PRD File Schema

Each case produces a PRD JSON file in `.staircase/prd/`:

```json
{
  "id": "acme.webshop.SPRINT-1",
  "vendor": "acme",
  "project": "webshop",
  "scope": "project",
  "components": ["storefront", "checkout-api", "admin-panel"],
  "created": "2026-03-12T10:00:00Z",
  "modified": "2026-03-12T10:00:00Z",
  "stories": [],
  "files": [],
  "gitDiff": ""
}
```

For vendor-scope cases: `"project": null, "scope": "vendor"`.

Naming convention: `<vendor>.<project>.<case-id>.json` (project-scope) or `<vendor>.<case-id>.json` (vendor-scope).

## Manifest Schema v1.2

```json
{
  "version": "1.2",
  "vendors": {
    "acme": {
      "registered_at": "2026-03-12T10:00:00Z",
      "projects": {
        "webshop": {
          "registered_at": "2026-03-12T10:00:00Z",
          "source": "/real/src/webshop",
          "components": ["storefront", "checkout-api"],
          "runner": null,
          "lastCase": "SPRINT-1"
        }
      }
    }
  }
}
```

`source` key is absent when not linked. `runner: null` means inherit workspace default. `lastCase` is display-only — it records the most recently created or run case, but any case can be run at any time.

## Configuration

**Workspace config** (`.staircase/config.json`):
```json
{
  "version": "1.2",
  "name": "my-workspace",
  "runner": "ralph-tui",
  "hooks_dir": "hooks.d"
}
```

Per-project config files no longer exist. Runner overrides are stored in the manifest project entry.

**`config --list`** shows resolved values with sources:

```
KEY               VALUE                 SOURCE
----------------  --------------------  --------
runner            ralph-tui             config
hooks_dir         hooks.d               config
tmp_dir           .staircase/tmp        default
name              my-workspace          config
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `STAIRCASE_DIR` | Auto-detect upward | Workspace root override |
| `STAIRCASE_TMP_DIR` | `.staircase/tmp/` | Temp dir for atomic writes |
| `STAIRCASE_HOOKS_DIR` | `hooks.d/` | Hooks directory |
| `STAIRCASE_RUNNER` | `ralph-tui` | Agent runner command |
| `STAIRCASE_DEBUG` | *(unset)* | Enable debug logging |
| `NO_COLOR` | *(unset)* | Disable colored output |
| `DRY_RUN` | *(unset)* | Same as `--dry-run` flag |

```bash
STAIRCASE_RUNNER=claude-code staircase run acme/webshop SPRINT-1
STAIRCASE_DIR=/opt/workspaces/client staircase ls
```

## Design Decisions

**Why flat PRD files instead of per-project context dirs?**
Multiple cases coexist simultaneously. A flat directory makes it trivial to list, diff, archive, or inspect all cases across all projects without traversing a tree. Filename namespacing (`vendor.project.case-id.json`) gives you both uniqueness and fast glob-based queries.

**Why symlinks instead of source paths in config?**
v1.1 kept a source path in a config file and `cd`d to it at runtime. v1.2 makes the filesystem the source of truth: the project dir IS the symlink. `ls`, `realpath`, and any shell tool can navigate directly without going through staircase. The runner still gets an absolute PRD path.

**Why remove "active case"?**
The active-case concept implied that you could only work one case at a time. In practice, CI pipelines, parallel agents, and long-lived feature branches all need multiple cases open simultaneously. Removing the concept simplifies the model: create as many cases as you need, run any one of them by name.

**Why is `lastCase` in the manifest at all?**
It's a convenience for humans inspecting `ls` and `status` output. It records which case was most recently created or run, making it easy to resume where you left off without remembering the case ID.

**Why vendor-scope cases?**
Some work spans all projects under a vendor — cross-cutting refactors, dependency upgrades, architectural reviews. A vendor-scope case avoids forcing the user to pick an arbitrary project.

**Why atomic writes?**
Every JSON mutation goes through `mktemp` + `mv`. If your machine crashes mid-write, you get either the old file or the new file — never a corrupted half-write.

**Why `jq`?**
The alternative is parsing JSON in pure bash with `sed`, which breaks the moment someone uses a quote in a case ID. `jq` handles edge cases correctly and is available in every package manager.

## License

[MIT](LICENSE) © Botond Biro
