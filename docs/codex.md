# Governing Codex

> **Experimental.** Tested with codex-cli 0.155 (the version inside the ChatGPT app
> for macOS). It depends on how Codex runs hooks, which can change between
> versions.

stAirCase can run OpenAI's [Codex CLI](https://developers.openai.com/codex) as the
agent. Codex does the work with its own tools, and every change still goes through
stAirCase before it reaches your branch.

In any git repository, with nothing to set up first (it shows you the task, the
scope and how commands are handled, and asks you to confirm; `--yes` in scripts):

```bash
staircase codex "add a /health endpoint that returns 200 and the body ok"
staircase codex --model gpt-6-luna --allow 'src/**' "fix the failing date test"
```

## What you need

- Codex installed and logged in (`codex login status`). If `codex` is not on your
  `PATH`, stAirCase uses the one inside the ChatGPT app for macOS.
- macOS or Linux.

## How it works

Codex changes files in two ways, and stAirCase governs both:

| Codex does | What stAirCase does |
|---|---|
| an edit (`apply_patch`) | becomes a proposal that you (or your [rules](approvals.md)) decide **before** it is applied |
| a shell command (`ls`, `rg`, tests, a formatter) | runs without asking, inside Codex's sandbox: **no network**, and it can only write in the run's worktree and temporary folders. Any file it changed comes to you **afterwards**: approve it and it is kept; reject it and it is put back |
| anything else (MCP tools and similar) | refused |

After an approved edit, stAirCase writes the approved bytes again, so what is
committed is exactly what you approved. The final check of the run compares the
whole worktree with what was approved.

Codex skips a hook nobody has reviewed without saying so. stAirCase therefore starts
it with `--dangerously-bypass-hook-trust` for its own hooks, and checks that the
hooks really ran: if Codex finishes without ever calling them, the run fails and
nothing is committed.

## Limits

- A shell command can **read** files outside the repository (Codex's sandbox does not
  restrict reading), and what it reads goes to OpenAI with the conversation.
- Changes that commands make are reviewed after they happened, so a run that keeps
  such a change reaches [CAL 2](adr/0001-core-promise-and-assurance-levels.md), not
  CAL 3. The change certificate says so.
- Your own Codex hooks (`~/.codex`) still load next to stAirCase's, and the bypass
  flag lets them run without review during the session.
- Token usage and cost are not recorded yet for Codex runs.

## What is recorded

Every proposal and decision is on the [audit chain](audit.md), including which changes
a command made and whether you kept them. The run's commit carries
`Assisted-by: Codex` and a signed [change certificate](audit.md#the-change-certificate-on-every-commit).
