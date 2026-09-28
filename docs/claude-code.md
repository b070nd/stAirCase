# Governing Claude Code

> **Experimental.** This works and is tested, but depends on how Claude Code runs
> hooks, which can change between Claude Code versions.

Instead of stAirCase's own agents, a run can use
[Claude Code](https://docs.anthropic.com/en/docs/claude-code) as its agent. Claude
Code does the work with its own tools, but **every tool call is decided by
stAirCase first**, with the same approvals, audit chain and final checks as any
other run.

In any git repository, with nothing to set up first:

```bash
staircase claude "add a /health endpoint that returns 200 and the body ok"
staircase claude --allow 'src/**' "fix the failing date test"     # limit where it may change files
```

This creates the workspace, a project for the repository and a case for the task
when they are missing, then runs it. Claude Code needs no API key in stAirCase: it
uses its own login.

For a case you set up yourself (stories, scope, blueprint), compile it for Claude
Code and run it as usual:

```bash
staircase compile 1 --agent claude-code
staircase run 1
```

## What you need

- the `claude` command on your `PATH`;
- Claude Code **already logged in** on this machine. stAirCase starts it with a
  clean environment that holds no API keys, so it uses its own login;
- macOS or Linux (Windows is untested).

## How it works

stAirCase starts Claude Code in the run's worktree with a settings file (kept
outside the repository). Its hooks call `staircase hook claude-code --governed`
before and after every tool call, which asks the run and passes on its answer
([how the hook bridge works](adr/0003-hook-bridge.md)):

| Claude Code tool | What stAirCase does |
|---|---|
| `Read`, `Glob`, `Grep`, `LS` | allowed, but only inside the repository |
| `Write`, `Edit` | becomes a proposal that you (or your [rules](approvals.md)) decide |
| `Bash` | becomes a shell proposal; refused unless you pass `--allow-shell-exec` |
| `TodoWrite` | allowed (it only keeps Claude Code's task list) |
| anything else | refused |

After an approved edit, stAirCase writes the **approved** bytes again, so what is
committed is exactly what you approved, whatever Claude Code's edit did. The final
check of the run still compares the whole worktree with what was approved.

Some edits are refused before you see them:

- an `Edit` whose text to replace does not appear **exactly once** in the file;
- an `Edit` with `replace_all` - Claude Code is asked to change each place on its
  own.

If the hook cannot reach the run, or anything else goes wrong, the tool call is
blocked, never allowed. The run's address and token are in a file only you can
read, removed when the run ends; they never appear on a command line.

## Limits

- stAirCase starts Claude Code with **only its own settings**: your user settings
  and the repository's own `.claude` settings are not loaded, so their hooks,
  plugins and MCP servers cannot act outside governance. Settings your company
  manages centrally still apply.
- The repository's `CLAUDE.md` files are still read. They are instructions for the
  model and cannot run anything themselves.
- A shell command you approve runs as your user, without a sandbox, as in any run
  with `--allow-shell-exec` (see the [safety boundary](safety.md)).

## What is recorded

The run is recorded like any other: every proposal and decision on the
[audit chain](audit.md). Claude Code's token usage is recorded when it reports it.
Its price is not known, so a [budget cap](models.md#limit-what-a-run-may-spend)
counts it at the highest known rate.

The prompt Claude Code receives is the compiled case: the PRD, the stories and
their scope.
