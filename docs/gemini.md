# Governing Gemini CLI (work in progress)

```bash
staircase gemini "add a /health endpoint"
```

Runs Google's [Gemini CLI](https://geminicli.com) on the task in a separate worktree of
your repository, with every change decided by you, your rules or your reviewers, like
[Claude Code](claude-code.md).

**Status.** This adapter was built from Gemini CLI's published documentation and tested
against a stand-in that behaves as the documentation says. It has been run once for real (see [compatibility](compatibility.md)): a governed run, a held approval, a rejection and its retry, and a fresh-clone verify and rebuild. Treat it as experimental still: one version, one model, a small task.

## What happens

- stAirCase gives Gemini a settings file (through `GEMINI_CLI_SYSTEM_SETTINGS_PATH`)
  with hooks on `SessionStart`, `BeforeTool` and `AfterTool`. Every tool call reaches
  stAirCase before it runs.
- `write_file` and `replace` become proposals decided before they are applied, and the
  approved bytes are written again afterwards, so what Gemini's editor did cannot change
  what is committed. `replace` must match exactly once.
- Reading and searching (`read_file`, `list_directory`, `glob`, `grep_search`) is limited
  to the worktree. `write_todos` is allowed. Every other tool (web access, `ask_user`,
  MCP tools and so on) is refused.
- `run_shell_command` is refused unless you start the session with `--allow-shell-exec`;
  then each command comes to you before it runs, and the files it changed come to you
  afterwards. Gemini's commands run **without a sandbox**, so a run with commands reaches
  [CAL 2](adr/0001-core-promise-and-assurance-levels.md), not CAL 3.
- Gemini's own approval prompts are switched off (`--approval-mode=yolo`), because
  stAirCase's hooks decide and a headless run would only stall on a prompt.
- At the end, exactly the approved changes are committed on `staircase/run-N`, with
  `Assisted-by: Gemini CLI` and a signed change certificate. Your checkout is not touched.

All the options of `staircase claude` work (`--allow`, `--check`, `--validator`,
`--signal`, `--approve-in-scope`, `--sign-approvals`, `--approval-port`).

## Limits, from Gemini's documentation

- **Hooks fail open.** Gemini lets a tool call run when a hook fails with any exit code
  but 2, or prints anything but JSON. stAirCase's hook exits 2 on every failure, and a
  session whose `SessionStart` hook never reaches stAirCase fails and keeps nothing.
- **Other hooks still run.** Gemini has no way to ignore the hooks in your own
  `~/.gemini/settings.json` or in the repository's `.gemini/settings.json`; they run beside
  stAirCase's. A hook of theirs that rewrites a tool's arguments could change what
  stAirCase saw. Do not run this on a repository whose `.gemini` settings you do not trust.
- **Login.** Gemini must already be logged in (run `gemini` once), or you hand it its own key by name:
  `staircase gemini --pass-env GEMINI_API_KEY "task"` passes that one variable from your environment and
  nothing else. Gemini, and any command it runs with your approval, can read the value: use a key meant for this.
- **Trusted folder.** A headless Gemini refuses to start in a folder it was not told to trust, and the run's
  worktree is a new folder every time, so stAirCase starts it with `--skip-trust`. That adds nothing to the
  warning above: the repository's `.gemini` settings already ran beside stAirCase's.

## If the run is interrupted

`staircase resume N` carries on with the same run and resumes Gemini CLI's own session (`--resume <session id>`; stAirCase chose the id and
put it on the audit chain). A real run killed mid-way and resumed remembered its context. `staircase recover N` commits what was approved
instead. See [troubleshooting](troubleshooting.md#a-run-was-interrupted).

## Company-wide

`staircase hook-template gemini --bin /opt/homebrew/bin/staircase` prints a `settings.json`
for Gemini CLI's *system* settings (`/etc/gemini-cli/settings.json`, or
`/Library/Application Support/GeminiCli/settings.json` on macOS). Its hook refuses tool
calls outside a governed session, like [the other agents' templates](managed.md). Gemini's
documentation does not say hooks can be locked against being disabled, so this stops
accidental ungoverned sessions only.
