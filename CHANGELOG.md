# Changelog

All notable changes to stAirCase are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.2.0] — 2026-03-12

### Core model shift

v1.2 drops the per-project `.staircase/` tree entirely. All PRD (context) files now live flat in the workspace under `.staircase/prd/`. Project directories on the filesystem are either symlinks to real source or plain stub directories — never contain `.staircase/` metadata. Multiple cases per project coexist permanently; there is no "active context" concept.

### Added

- **`staircase migrate`** — migrates a v1.1 workspace to v1.2 in-place. Reads `vendor/project/.staircase/tasks/*/context.json` and `active/context.json`, writes them as flat PRD files under `.staircase/prd/`. Reads runner from per-project `config.json` and stores it in the manifest. Converts `activeCase` → `lastCase`. If a project has a source path set, removes the stub directory and creates a symlink. Removes per-project `.staircase/` directories. Bumps manifest and config versions to `"1.2"`.
- **`staircase case delete <v[/p]> <case-id>`** — removes the PRD file for a case. Clears `lastCase` in the manifest if it pointed to the deleted case. Supports `--dry-run`.
- **Vendor-scope cases** — `case new`, `case info`, `case delete`, and `run` all accept a bare vendor name (no `/project` segment). Vendor-level PRDs are stored as `<vendor>.<case-id>.json` and carry `"scope": "vendor"`.
- **`project add <v/p> [source-path]`** — optional second argument: if given, the project directory is created as a symlink `ln -s <canon> workspace/v/p` rather than a stub directory.
- **`.staircase/prd/` directory** — created by `init` and `doctor --fix`.
- **`_prd_dir()`, `_prd_ns()`, `_prd_file()`, `_last_case()`, `_parse_vp_or_v()` helpers** — new internal functions for PRD path construction and vendor-or-project parsing.
- **`_build_prd()` helper** — replaces `_build_context()`. Produces the new PRD schema with `id`, `vendor`, `project` (null for vendor-scope), `scope`, `components`, `created`, `modified`, `stories`, `files`, `gitDiff`.

### Changed

- **Manifest schema** — version bumped to `"1.2"`. `activeCase` replaced by `lastCase` (display-only, not required for running). `runner` field added to project entries (null = inherit workspace default). `source` key absent when not linked.
- **PRD file schema** — replaces `context.json` / `caseId` schema. New fields: `id` (namespaced: `vendor.project.case-id`), `scope` (`"project"` or `"vendor"`), `modified`.
- **`_runner()`** — reads runner from manifest project entry (`.vendors[$v].projects[$p].runner`) instead of per-project `config.json`.
- **`cmd_run`** — case-id is now a required positional argument (`run <v/p|v> <case-id>`). No longer reads `active/context.json`; resolves PRD file from `_prd_file`. Updates `lastCase` in manifest after successful launch. `--config` merges into a temp copy of the PRD (original is never mutated). Accepts bare vendor for vendor-scope runs.
- **`cmd_case_new`** — accepts `<v/p|v>` (vendor-or-project). Writes to `.staircase/prd/` instead of per-project directory. Updates `lastCase` for project-scope cases.
- **`cmd_case_list`** — optional `[v[/p]]` argument. Without argument lists all PRD files. With vendor lists `<vendor>.*` files. With `v/p` lists `<vendor>.<project>.*` files. Marks `lastCase` with `*`.
- **`cmd_case_info`** — requires both `v[/p]` and `case-id` arguments. Reads from PRD file.
- **`cmd_project_add`** — no longer creates per-project `.staircase/` directories or `config.json`. Manifest entry uses `lastCase` instead of `activeCase`.
- **`cmd_project_remove`** — removes all `$v.$p.*.json` PRD files, removes the symlink (`rm -f`) or stub directory (`rm -rf`).
- **`cmd_project_link`** — now also manages filesystem symlink: empty stub dir → replaced with symlink; existing symlink → updated atomically; dir with contents → manifest only + warning.
- **`cmd_project_unlink`** — no longer writes per-project `config.json`. If `$ws/$v/$p` is a symlink, replaces it with a stub directory.
- **`cmd_project_info`** — shows `last case` instead of `active case`. Shows cases count from PRD files. Removed per-project `config.json` runner lookup.
- **`cmd_project_list`** — shows `last:` instead of `case:`.
- **`cmd_component_add`** — no longer writes per-project `config.json`. Skips `mkdir` when project dir is a symlink (component dirs live in the real source).
- **`cmd_component_remove`** — no longer writes per-project `config.json`.
- **`cmd_ls`** — detects symlinks with `-L`, shows `→ target` via `readlink`. Shows cases count per project from PRD dir. Shows vendor-level case count. Shows `last case` instead of `case`.
- **`cmd_status`** — columns updated to `VENDOR  PROJECT  LAST CASE  CASES  SRC  MODIFIED`. CASES = count of PRD files for the project. MODIFIED = mtime of lastCase PRD file.
- **`cmd_doctor`** — checks `.staircase/prd/` exists. Validates each PRD file (valid JSON, id matches filename). Checks symlink targets exist. Warns if per-project `.staircase/` dirs are found (suggests `migrate`). Removed checks for `active/` and `tasks/` dirs. `--fix` creates `prd/` dir and vendor dirs; does NOT remove stale symlinks.
- **`cmd_export`** — reads all `$v.$p.*` PRD files. Output schema: `{vendor, project, exported_at, manifest, cases: {<case-id>: <prd-content>}}`. Tar archives only the filtered PRD files.
- **`staircase --version`** now reports `1.2.0`.

### Removed

- **`staircase case switch`** — removed. Cases are permanent; switch by specifying `case-id` in `run`.
- **Per-project `config.json`** — no longer created or read. Runner moves to manifest project entry.
- **`active/context.json` and `tasks/*/context.json`** — replaced by flat PRD files in `.staircase/prd/`.
- **`_build_context()` helper** — replaced by `_build_prd()`.

### Migration

Workspaces from v1.1 can be migrated with `staircase migrate`. The command is idempotent and non-destructive until it removes the per-project `.staircase/` directories at the end.

---

## [1.1.0] — 2026-03-11

### Added

- **`staircase project link <v/p> <path>`** — associates an external source directory with a project. The path is resolved to its canonical absolute form (`pwd -P`) and stored in both the workspace manifest and the project's `.staircase/config.json`. Supports `--dry-run`.
- **`staircase project unlink <v/p>`** — removes the source link from manifest and project config. Idempotent: safe to call on a project that was never linked. Supports `--dry-run`.
- **`_source_path()` helper** — internal function that reads `.vendors[$v].projects[$p].source` from the manifest, returning an empty string when absent. Used by `run`, `ls`, `status`, `project info`, and `doctor`.
- **`cmd_run` source-aware execution** — when a project has a linked source, `run` `cd`s into the source directory instead of the stub project directory and passes the absolute context path to the runner (`--prd /abs/path/to/.staircase/active/context.json`). Unlinked projects behave identically to v1.0.0.
- **`project info` source display** — shows `source: /path/...` or `(not linked)` as the first field in the project info block.
- **`ls` link indicator** — appends `[→ linked]` (cyan) next to the component count for linked projects.
- **`status` SRC column** — new `SRC` column with `✓` for linked projects and `-` for unlinked.
- **`doctor` stale-source check** — warns when a project's source path is configured but the directory no longer exists. Does not auto-fix (the path may be on an unmounted volume); resolve with `project unlink` or by remounting.

### Changed

- **Manifest schema** — `.vendors[$v].projects[$p]` gains an optional `source` field (string, absolute path). No migration needed; absent field is treated as unlinked.
- **Project config schema** — `vendor/project/.staircase/config.json` gains an optional `source` field, mirroring the manifest.
- **`staircase --version`** now reports `1.1.0`.

---

## [1.0.0] — 2026-03-09

Initial release.

### Workspace

- **`staircase init [--name <n>]`** — scaffolds `.staircase/config.json`, `.staircase/manifest.json`, and `.staircase/tmp/`. Idempotent.
- **`staircase config [<key>] [<value>]`** — get/set workspace config values. `--list` shows all resolved values with their source (config, env, or default).

### Structure

- **`staircase vendor add|remove|list`** — manage vendor namespaces. `remove` requires all projects to be removed first.
- **`staircase project add|remove|list|info`** — manage projects (`vendor/project`). `add` auto-creates the vendor if it doesn't exist. `remove` leaves the directory in place. `info` shows active case, runner, and components.
- **`staircase component add|remove|list`** — manage component subdirectories within a project. `add` accepts multiple component names. `list` marks missing directories with `!`.

### Cases

- **`staircase case new <v/p> <case-id>`** — creates a task directory, writes `active/context.json`, and sets the case as active in the manifest. Case IDs with special characters (quotes, slashes) are handled safely via `jq -n`.
- **`staircase case switch <v/p> <case-id>`** — saves current `active/context.json` to the previous case's directory, loads the target context, and updates the manifest. All writes are atomic (`mktemp` + `mv`).
- **`staircase case list <v/p>`** — lists all cases with `*` marking the active one and last-modified timestamps.
- **`staircase case info <v/p> [case-id]`** — prints the active (or named) context as formatted JSON.

### Agent Runner

- **`staircase run <v/p> [--runner <r>] [--config '{}']`** — resolves the runner through the config cascade, changes into the project directory, and launches `<runner> --prd .staircase/active/context.json`. Optional `--config` JSON is merged into the context before launch.
- **Runner resolution order** (highest wins): `--runner` flag → `STAIRCASE_RUNNER` env → project config → workspace config → `ralph-tui`.
- Post-run hook: fires `hooks.d/99-post-run.sh` if present and executable.

### Inspection

- **`staircase ls`** — color-coded tree view of vendors, projects, active cases, and component counts.
- **`staircase status [--json]`** — tabular view with vendor, project, active case, component count, and last-modified timestamp. `--json` outputs the raw manifest.

### Health

- **`staircase doctor [--fix]`** — checks for missing/invalid config and manifest, missing vendor directories, missing project `.staircase/` directories, active cases without `context.json`, missing component directories, and unwritable tmp. `--fix` repairs everything it can.
- **`staircase export <v/p> [--format json|tar]`** — JSON export includes manifest entry, active context, and all saved case contexts. `--format tar` creates a `.tar.gz` archive.

### Git Hooks

- **`staircase hooks install <v/p>`** — creates `hooks.d/` stubs and installs `pre-commit` (formatter) and `post-merge` (auto-doctor) hooks into the project root and all component repos that have `.git`. Idempotent via guard comments.

### Flags & Environment

- **`--dry-run`** — every command supports dry-run mode, printing intended actions without touching disk.
- **`--no-color`** / **`NO_COLOR`** — disables ANSI output for CI environments.
- **`STAIRCASE_DEBUG`** — enables `set -x` tracing.
- **`STAIRCASE_DIR`**, **`STAIRCASE_TMP_DIR`**, **`STAIRCASE_HOOKS_DIR`**, **`STAIRCASE_RUNNER`** — override workspace root, tmp directory, hooks directory, and agent runner respectively.

### Technical Notes

- Zero Python dependencies — pure Bash + `jq`.
- Cross-platform: macOS, Linux, WSL, Docker.
- All JSON mutations use atomic `mktemp` + `mv` writes.
- Context JSON built with `jq -n` — special characters in case IDs are always safe.

[1.2.0]: https://github.com/b070nd/staircase/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/b070nd/staircase/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/b070nd/staircase/releases/tag/v1.0.0
