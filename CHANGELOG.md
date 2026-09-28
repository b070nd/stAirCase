# Changelog

All notable changes to stAirCase are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). This project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `staircase codex "task"`: run OpenAI's Codex CLI under governance, with no
  setup. Its edits (`apply_patch`) are decided before they are applied; its
  shell commands run in Codex's sandbox (no network, writes only in the
  worktree) and the files they change are reviewed afterwards: kept if
  approved, reverted if not. A Codex run that never calls stAirCase's hooks
  fails. `run --agent codex`, `compile --agent codex`, `make smoke-codex`.
- Review after the fact: changes an agent's command made in the worktree come
  to a decision as one proposal (approved, they are kept; rejected, they are
  reverted). A run that keeps such changes reaches CAL 2.
- `--model` for `staircase claude`, `staircase codex` and `run` with an agent
  harness.
- **Change certificates.** Every run that commits signs a change certificate
  about exactly that commit (in-toto statement in a DSSE envelope, workspace
  Ed25519 key, digests only) and attaches it as a git note
  (`refs/notes/staircase`); the commit message names the agents that helped
  (`Assisted-by:`) and the audit chain's head (`Staircase-Chain:`). The
  certificate states the change assurance level reached (CAL 3, or CAL 2 when
  shell commands were approved).
- `staircase verify <commit>`: checks a commit's certificate against the
  trusted key, that it is about exactly this commit, and `--min-cal`.
- `staircase verify main..HEAD`: checks every commit in a range that names an
  agent (`Assisted-by:`), or every commit with `--all`.
- A GitHub Action, `uses: b070nd/stAirCase@<version>`, that requires valid
  change certificates on a pull request's agent commits. It installs the
  staircase release after checking its build attestation.
- `staircase audit anchor <run-id>`: anchors a run's change certificate in a
  Rekor transparency log. Only digests are sent, never code or reasoning;
  `staircase verify --check-anchor` confirms it.
- `staircase claude "task"`: run Claude Code on a task in the current git
  repository with no setup. The workspace, a project for the repository and a
  case for the task are created when missing; every change still comes to you,
  and exactly the approved changes land on a run branch.
- `staircase compile --agent claude-code`: a plan can name the agent harness
  that runs it. Such a case needs no topology and no provider key; the harness
  is recorded in the run's first audit event.
- `staircase hook <agent>`: one command that passes an agent's hook calls to the
  run that governs it, and blocks the call on every failure (exit code 2).
- `ROADMAP.md` and the first architecture decisions in `docs/adr/`.

### Changed

- Claude Code runs (`--agent claude-code`) call `staircase hook claude-code
  --governed` instead of `curl`. The run's token is no longer on a command line
  (it is in a file only you can read), and curl is no longer needed.

- Release checksums are signed into one Sigstore bundle,
  `checksums.txt.sigstore.json` (the format cosign 3 writes), instead of
  separate `.sig` and `.pem` files; verify with `cosign verify-blob --bundle`.
- The documentation is rewritten in plain English, with an index
  (`docs/README.md`), a page per task, troubleshooting, and a CLI reference
  generated from the program (`docs/cli.md`). `docs/plugin-gates.md` is now
  `docs/gates.md`, and `docs/project-use.md` is now `docs/safety.md`.
- `audit export --anchor` now says in its help what leaves the machine: the
  whole record is sent to the Rekor service, whose public log keeps its SHA-256,
  the signature and your public key.

### Fixed

- `audit verify --check-anchor` could not succeed against the real Rekor log:
  it expected the log to return the record, but the log keeps only the
  record's hash. It now checks the logged hash and signature.
- An edit to a Windows-style (CRLF) file no longer rewrites every line ending
  in the file: it changes only its own lines and keeps the file's line
  endings, so what you approve is what you see.
- A repository's own Claude Code settings (its `.claude` hooks, and user
  settings with their hooks, plugins and MCP servers) no longer load in a
  governed Claude Code run.

- The `runtime.plan_pinned` gate skips a case that does not exist, instead of
  failing with a database error.

## [0.2.0] - 2026-09-25

The Go rewrite (versioned 0.x, starting over from the Bash 1.x line). Highlights
since the first Go commits:

### Changed (breaking)

- **No Python.** Agents run in-process in the `staircase` binary: a supervisor and
  its agents call models over HTTPS (Anthropic, OpenAI, Gemini, xAI, or any model
  through an OpenAI-compatible LLM gateway). The generated LangGraph script, the
  venv and its dependency lock, and the IPC socket are gone. `init --skip-venv`,
  `init --offline-wheels` and `doctor --fix-venv` are accepted but do nothing.
- **`compile` writes a plan** (`tmp/plan_case<N>.json` + sha256), not a script;
  `run` refuses a plan that was edited, is for another case or is of another
  version. Unknown extra tools and models without a provider fail at compile.
- **Routing:** the supervisor starts; each agent ends its turn with
  `ROUTE: <next>` (the supervisor may say `END`); runs stop after 25 agent steps
  or 100 model calls. Documented topologies now finish instead of looping.
- **Every run gets its own git worktree**; the developer's checkout is never
  touched. `run --auto-stash` and `--force` do nothing.
- **Approvals are bound to bytes the orchestrator derives**, decisions are
  audited before the agent sees them, and the commit is built from the approved
  bytes; unapproved changes, index edits or agent commits fail the run.
- **`policy.json` fails closed**: unparsable or unknown fields stop the run
  (they used to be ignored with a warning). `limits.max_run_duration`, never
  enforced before, is now `max_run_secs` (the name blueprints use) and is
  enforced; the old name is refused with that hint. A reject rule scoped by
  `agent_names` no longer counts as a blanket deny.
- **Unpriced models are not free**: they count at the highest known rate
  against the budget cap; `provider/model` names are priced by their model.
- **Webhook approvals** must echo the request's fresh `yield_id` and
  `request_sha256` when a secret is set.

- **Releases** ship Linux and macOS (amd64, arm64) and an experimental,
  untested Windows amd64 build (`run_shell` and `--agent claude-code` need a
  POSIX shell). They are published from `b070nd/stAirCase` (the cosign identity
  in QUICKSTART's verification steps is corrected to match).

### Security

- Built with Go 1.26.8 and updated dependencies with published fixes that
  stAirCase's code reaches: go-git 5.19.2 (path traversal via reference
  names, worktree symlinks, crafted repositories), golang.org/x/crypto 0.56.0
  (SSH client), google.golang.org/grpc 1.83.1 (HTTP/2 transport, used by the
  trace exporter) and goldmark 1.7.17 (terminal approval rendering). Go 1.26.5+
  also fixes an os.Root symlink escape (GO-2026-4970). govulncheck reports no
  reachable vulnerabilities.

### Added

- `--record-llm` / `--replay-llm` record every model exchange of a run and replay
  it offline, failing loudly on any request not in the recording.
- `story accept` records acceptance on the audit chain and completes the case.
- A stuck agent no longer hangs a run (`agent_unresponsive` is audited).
- **Blueprints** (`docs/blueprints.md`): `blueprint import` snapshots a
  blueprint directory by content hash, `project bind` creates its topology and
  cases in a project, and the `runtime.plan_pinned` gate blocks runs of bound
  cases whose plan drifted from the blueprint. Plans (version 2) carry the
  case's stories and blueprint; recompile plans from earlier builds.
- `run_bound` records the plan's sha256 and blueprint, and runs record the
  topology version they executed rather than the latest one.
- **Drift supervision** (`docs/drift.md`): `story scope` (or a blueprint's
  scope) limits the paths a run may change; out-of-scope proposals, file limits
  and checkpoints go to a human with a `DRIFT:` reason; too many scope
  violations or `max_run_secs` halt the run (`drift_halt`) until
  `run --ack-drift`; every run ends with a `drift_report`. Agents are told
  their stories and scope.
- `run --validator <model>`: an automated reviewer decides in-scope file edits
  the policy leaves open, from the derived change and the stories only;
  sensitive paths, rejection streaks, unreadable verdicts and every 5th
  approval go to a human, and a human approves the run's final change once.
- `secret set` on an existing key replaces its value (e.g. a rotated API key)
  and counts its version; it used to fail with a UNIQUE constraint error.
- `make demo` is the offline end-to-end acceptance run: blueprint imported
  from its own repository → bind → run with create, edit and delete approved →
  run branch holds exactly those changes → developer checkout byte-identical →
  stories accepted → audit verified; plus `--tamper` and `--drift` paths.
  `make smoke` / `make smoke-claude` run the same flow against a real model
  (record, then replay offline) and Claude Code; they skip without credentials.
- **Install and verify**: `go install github.com/b070nd/stAirCase/src/cmd/staircase@latest`
  (the module path is now the repository path), Homebrew with bash/zsh/fish
  completions, and release archives that are byte-reproducible from their tag,
  with signed checksums, SBOMs and GitHub build-provenance attestations
  (`gh attestation verify`). `staircase version` reports the module version for
  `go install` builds.
- Project: code of conduct, issue forms, pull request template, Dependabot,
  CodeQL and OpenSSF Scorecard; release notes come from this changelog.
- `run --agent claude-code` (experimental): Claude Code does the work, and
  hooks route every tool call through the same approvals - edits and shell
  commands are proposals, reads stay in the worktree, other tools are denied.

## [1.2.0] - 2026-03-12

### Core model shift

v1.2 drops the per-project `.staircase/` tree entirely. All PRD (context) files now live flat in the workspace under `.staircase/prd/`. Project directories on the filesystem are either symlinks to real source or plain stub directories - never contain `.staircase/` metadata. Multiple cases per project coexist permanently; there is no "active context" concept.

### Added

- **`staircase migrate`** - migrates a v1.1 workspace to v1.2 in-place. Reads `vendor/project/.staircase/tasks/*/context.json` and `active/context.json`, writes them as flat PRD files under `.staircase/prd/`. Reads runner from per-project `config.json` and stores it in the manifest. Converts `activeCase` → `lastCase`. If a project has a source path set, removes the stub directory and creates a symlink. Removes per-project `.staircase/` directories. Bumps manifest and config versions to `"1.2"`.
- **`staircase case delete <v[/p]> <case-id>`** - removes the PRD file for a case. Clears `lastCase` in the manifest if it pointed to the deleted case. Supports `--dry-run`.
- **Vendor-scope cases** - `case new`, `case info`, `case delete`, and `run` all accept a bare vendor name (no `/project` segment). Vendor-level PRDs are stored as `<vendor>.<case-id>.json` and carry `"scope": "vendor"`.
- **`project add <v/p> [source-path]`** - optional second argument: if given, the project directory is created as a symlink `ln -s <canon> workspace/v/p` rather than a stub directory.
- **`.staircase/prd/` directory** - created by `init` and `doctor --fix`.
- **`_prd_dir()`, `_prd_ns()`, `_prd_file()`, `_last_case()`, `_parse_vp_or_v()` helpers** - new internal functions for PRD path construction and vendor-or-project parsing.
- **`_build_prd()` helper** - replaces `_build_context()`. Produces the new PRD schema with `id`, `vendor`, `project` (null for vendor-scope), `scope`, `components`, `created`, `modified`, `stories`, `files`, `gitDiff`.

### Changed

- **Manifest schema** - version bumped to `"1.2"`. `activeCase` replaced by `lastCase` (display-only, not required for running). `runner` field added to project entries (null = inherit workspace default). `source` key absent when not linked.
- **PRD file schema** - replaces `context.json` / `caseId` schema. New fields: `id` (namespaced: `vendor.project.case-id`), `scope` (`"project"` or `"vendor"`), `modified`.
- **`_runner()`** - reads runner from manifest project entry (`.vendors[$v].projects[$p].runner`) instead of per-project `config.json`.
- **`cmd_run`** - case-id is now a required positional argument (`run <v/p|v> <case-id>`). No longer reads `active/context.json`; resolves PRD file from `_prd_file`. Updates `lastCase` in manifest after successful launch. `--config` merges into a temp copy of the PRD (original is never mutated). Accepts bare vendor for vendor-scope runs.
- **`cmd_case_new`** - accepts `<v/p|v>` (vendor-or-project). Writes to `.staircase/prd/` instead of per-project directory. Updates `lastCase` for project-scope cases.
- **`cmd_case_list`** - optional `[v[/p]]` argument. Without argument lists all PRD files. With vendor lists `<vendor>.*` files. With `v/p` lists `<vendor>.<project>.*` files. Marks `lastCase` with `*`.
- **`cmd_case_info`** - requires both `v[/p]` and `case-id` arguments. Reads from PRD file.
- **`cmd_project_add`** - no longer creates per-project `.staircase/` directories or `config.json`. Manifest entry uses `lastCase` instead of `activeCase`.
- **`cmd_project_remove`** - removes all `$v.$p.*.json` PRD files, removes the symlink (`rm -f`) or stub directory (`rm -rf`).
- **`cmd_project_link`** - now also manages filesystem symlink: empty stub dir → replaced with symlink; existing symlink → updated atomically; dir with contents → manifest only + warning.
- **`cmd_project_unlink`** - no longer writes per-project `config.json`. If `$ws/$v/$p` is a symlink, replaces it with a stub directory.
- **`cmd_project_info`** - shows `last case` instead of `active case`. Shows cases count from PRD files. Removed per-project `config.json` runner lookup.
- **`cmd_project_list`** - shows `last:` instead of `case:`.
- **`cmd_component_add`** - no longer writes per-project `config.json`. Skips `mkdir` when project dir is a symlink (component dirs live in the real source).
- **`cmd_component_remove`** - no longer writes per-project `config.json`.
- **`cmd_ls`** - detects symlinks with `-L`, shows `→ target` via `readlink`. Shows cases count per project from PRD dir. Shows vendor-level case count. Shows `last case` instead of `case`.
- **`cmd_status`** - columns updated to `VENDOR  PROJECT  LAST CASE  CASES  SRC  MODIFIED`. CASES = count of PRD files for the project. MODIFIED = mtime of lastCase PRD file.
- **`cmd_doctor`** - checks `.staircase/prd/` exists. Validates each PRD file (valid JSON, id matches filename). Checks symlink targets exist. Warns if per-project `.staircase/` dirs are found (suggests `migrate`). Removed checks for `active/` and `tasks/` dirs. `--fix` creates `prd/` dir and vendor dirs; does NOT remove stale symlinks.
- **`cmd_export`** - reads all `$v.$p.*` PRD files. Output schema: `{vendor, project, exported_at, manifest, cases: {<case-id>: <prd-content>}}`. Tar archives only the filtered PRD files.
- **`staircase --version`** now reports `1.2.0`.

### Removed

- **`staircase case switch`** - removed. Cases are permanent; switch by specifying `case-id` in `run`.
- **Per-project `config.json`** - no longer created or read. Runner moves to manifest project entry.
- **`active/context.json` and `tasks/*/context.json`** - replaced by flat PRD files in `.staircase/prd/`.
- **`_build_context()` helper** - replaced by `_build_prd()`.

### Migration

Workspaces from v1.1 can be migrated with `staircase migrate`. The command is idempotent and non-destructive until it removes the per-project `.staircase/` directories at the end.

---

## [1.1.0] - 2026-03-11

### Added

- **`staircase project link <v/p> <path>`** - associates an external source directory with a project. The path is resolved to its canonical absolute form (`pwd -P`) and stored in both the workspace manifest and the project's `.staircase/config.json`. Supports `--dry-run`.
- **`staircase project unlink <v/p>`** - removes the source link from manifest and project config. Idempotent: safe to call on a project that was never linked. Supports `--dry-run`.
- **`_source_path()` helper** - internal function that reads `.vendors[$v].projects[$p].source` from the manifest, returning an empty string when absent. Used by `run`, `ls`, `status`, `project info`, and `doctor`.
- **`cmd_run` source-aware execution** - when a project has a linked source, `run` `cd`s into the source directory instead of the stub project directory and passes the absolute context path to the runner (`--prd /abs/path/to/.staircase/active/context.json`). Unlinked projects behave identically to v1.0.0.
- **`project info` source display** - shows `source: /path/...` or `(not linked)` as the first field in the project info block.
- **`ls` link indicator** - appends `[→ linked]` (cyan) next to the component count for linked projects.
- **`status` SRC column** - new `SRC` column with `✓` for linked projects and `-` for unlinked.
- **`doctor` stale-source check** - warns when a project's source path is configured but the directory no longer exists. Does not auto-fix (the path may be on an unmounted volume); resolve with `project unlink` or by remounting.

### Changed

- **Manifest schema** - `.vendors[$v].projects[$p]` gains an optional `source` field (string, absolute path). No migration needed; absent field is treated as unlinked.
- **Project config schema** - `vendor/project/.staircase/config.json` gains an optional `source` field, mirroring the manifest.
- **`staircase --version`** now reports `1.1.0`.

---

## [1.0.0] - 2026-03-09

Initial release.

### Workspace

- **`staircase init [--name <n>]`** - scaffolds `.staircase/config.json`, `.staircase/manifest.json`, and `.staircase/tmp/`. Idempotent.
- **`staircase config [<key>] [<value>]`** - get/set workspace config values. `--list` shows all resolved values with their source (config, env, or default).

### Structure

- **`staircase vendor add|remove|list`** - manage vendor namespaces. `remove` requires all projects to be removed first.
- **`staircase project add|remove|list|info`** - manage projects (`vendor/project`). `add` auto-creates the vendor if it doesn't exist. `remove` leaves the directory in place. `info` shows active case, runner, and components.
- **`staircase component add|remove|list`** - manage component subdirectories within a project. `add` accepts multiple component names. `list` marks missing directories with `!`.

### Cases

- **`staircase case new <v/p> <case-id>`** - creates a task directory, writes `active/context.json`, and sets the case as active in the manifest. Case IDs with special characters (quotes, slashes) are handled safely via `jq -n`.
- **`staircase case switch <v/p> <case-id>`** - saves current `active/context.json` to the previous case's directory, loads the target context, and updates the manifest. All writes are atomic (`mktemp` + `mv`).
- **`staircase case list <v/p>`** - lists all cases with `*` marking the active one and last-modified timestamps.
- **`staircase case info <v/p> [case-id]`** - prints the active (or named) context as formatted JSON.

### Agent Runner

- **`staircase run <v/p> [--runner <r>] [--config '{}']`** - resolves the runner through the config cascade, changes into the project directory, and launches `<runner> --prd .staircase/active/context.json`. Optional `--config` JSON is merged into the context before launch.
- **Runner resolution order** (highest wins): `--runner` flag → `STAIRCASE_RUNNER` env → project config → workspace config → `ralph-tui`.
- Post-run hook: fires `hooks.d/99-post-run.sh` if present and executable.

### Inspection

- **`staircase ls`** - color-coded tree view of vendors, projects, active cases, and component counts.
- **`staircase status [--json]`** - tabular view with vendor, project, active case, component count, and last-modified timestamp. `--json` outputs the raw manifest.

### Health

- **`staircase doctor [--fix]`** - checks for missing/invalid config and manifest, missing vendor directories, missing project `.staircase/` directories, active cases without `context.json`, missing component directories, and unwritable tmp. `--fix` repairs everything it can.
- **`staircase export <v/p> [--format json|tar]`** - JSON export includes manifest entry, active context, and all saved case contexts. `--format tar` creates a `.tar.gz` archive.

### Git Hooks

- **`staircase hooks install <v/p>`** - creates `hooks.d/` stubs and installs `pre-commit` (formatter) and `post-merge` (auto-doctor) hooks into the project root and all component repos that have `.git`. Idempotent via guard comments.

### Flags & Environment

- **`--dry-run`** - every command supports dry-run mode, printing intended actions without touching disk.
- **`--no-color`** / **`NO_COLOR`** - disables ANSI output for CI environments.
- **`STAIRCASE_DEBUG`** - enables `set -x` tracing.
- **`STAIRCASE_DIR`**, **`STAIRCASE_TMP_DIR`**, **`STAIRCASE_HOOKS_DIR`**, **`STAIRCASE_RUNNER`** - override workspace root, tmp directory, hooks directory, and agent runner respectively.

### Technical Notes

- Zero Python dependencies - pure Bash + `jq`.
- Cross-platform: macOS, Linux, WSL, Docker.
- All JSON mutations use atomic `mktemp` + `mv` writes.
- Context JSON built with `jq -n` - special characters in case IDs are always safe.

[1.2.0]: https://github.com/b070nd/staircase/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/b070nd/staircase/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/b070nd/staircase/releases/tag/v1.0.0
