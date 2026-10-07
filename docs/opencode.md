# Governing OpenCode (experimental)

```bash
staircase opencode "add a /health endpoint"
```

Runs [OpenCode](https://opencode.ai) on the task in a separate worktree of your
repository, with every change decided by you, your rules or your reviewers, like
[Claude Code](claude-code.md).

**Status.** This adapter was built from OpenCode's published documentation and tested
against a stand-in that loads and runs the generated plugin under Node. It has been run
for real **once**, on OpenCode 1.18.35 with `openai/gpt-4.1-mini` through its own OpenAI
login (2026-10-07, [the record](release-evidence/opencode-1.18.35.md)): one governed run, and a
rejection followed by a retry, both committed with a valid certificate (CAL 3) that a fresh
clone verified and rebuilt. Only OpenCode's `write` tool was exercised, there is one version
and one model, and no control for a change nobody governed has been run. It has no native
session resume. Treat it as experimental.

## What happens

- stAirCase writes a small plugin for the run and points OpenCode at it
  (`OPENCODE_CONFIG_DIR`). The plugin hooks `tool.execute.before`: every tool call is
  posted to stAirCase, and a refusal, or any error at all, **throws**, which blocks the
  call.
- `edit`, `write` and `apply_patch` become proposals decided before they are applied,
  and the approved bytes are written again afterwards, so what OpenCode's editor did
  cannot change what is committed. `edit` must match exactly once.
- Reading and searching (`read`, `glob`, `grep`) is limited to the worktree.
  `todowrite` is allowed. Every other tool (`webfetch`, `task`, `websearch` and so on)
  is refused.
- `bash` is refused unless you start the session with `--allow-shell-exec`; then each
  command comes to you before it runs, and the files it changed come to you afterwards.
  OpenCode's commands run **without a sandbox**, so a run with commands reaches
  [CAL 2](adr/0001-core-promise-and-assurance-levels.md), not CAL 3.
- OpenCode's own permission prompts are off (`permission: "*": "allow"`, with
  `external_directory` and `doom_loop` denied), because stAirCase's plugin decides and a
  headless run would only stall on a prompt. Sharing and self-update are off.
- At the end, exactly the approved changes are committed on `staircase/run-N`, with
  `Assisted-by: OpenCode` and a signed change certificate. Your checkout is not touched.

All the options of `staircase claude` work (`--allow`, `--check`, `--validator`,
`--signal`, `--approve-in-scope`, `--sign-approvals`, `--approval-port`); `--model` takes
OpenCode's `provider/model`.

## Limits, from OpenCode's documentation

- **A plugin that fails to load lets calls through.** stAirCase's plugin calls the run when
  it loads; a session whose plugin never reaches the run fails and keeps nothing, and
  finalize refuses any change in the worktree that nobody approved. A command that did run
  in that window could still have had effects elsewhere.
- **Other plugins still load.** OpenCode has no way to load only stAirCase's plugin.
  stAirCase points `XDG_CONFIG_HOME` at an empty folder so your global config and plugins
  are not read, but plugins in the repository's `.opencode` folder still are. Do not run
  this on a repository whose `.opencode` folder you do not trust.
- **Login.** OpenCode must already have a provider (`opencode auth login`). stAirCase does
  not pass API keys to it, but OpenCode keeps its login in a file in your data folder
  (`~/.local/share/opencode/auth.json`) and the run reads it from there. A command it runs
  **without a sandbox** (`--allow-shell-exec`) can read that file, so use a key with a spending
  limit, and do not allow commands on a task you do not trust.
- **No company-wide template** yet: OpenCode's documentation describes no way to make a
  hook mandatory.
