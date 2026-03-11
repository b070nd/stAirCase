# Changelog

All notable changes to stAirCase are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[1.1.0]: https://github.com/b070nd/staircase/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/b070nd/staircase/releases/tag/v1.0.0
