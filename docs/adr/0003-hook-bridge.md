# ADR 0003: One hook bridge for every agent

- Status: accepted
- Date: 2026-09-28

## Context

Coding agents change every few months, but most now call a *hook* before and after
each tool call. A hook can block the call:
- Claude Code: `PreToolUse`/`PostToolUse`;
- Codex CLI: the same;
- Gemini CLI: `BeforeTool`/`AfterTool`;
- OpenCode: `tool.execute.before`;
- Cursor: `preToolUse`, `beforeShellExecution`.

In v0.2.0 each Claude Code run writes this hook command into a settings file:
`curl … -H 'Authorization: Bearer <token>' … || exit 2`. That has three problems:
- the token is on the command line (F81);
- it needs curl and a POSIX shell;
- the command changes every run, and Codex trusts a hook by the hash of its definition,
  so every run would need a new review.

## Decision

**One stable command connects every agent's hooks to the run:**

```
staircase hook <agent> [--governed]
```

- It reads the agent's own hook input (JSON) on standard input and writes the answer in
  the agent's own format on standard output. **Exit code 2 blocks** the tool call, the
  convention these agents share.
- It finds the run through the `STAIRCASE_HOOK_FILE` environment variable. The variable
  names a file, readable only by you, that holds the run's local endpoint and token.
  The file is removed when the run ends. stAirCase starts the agent with the variable
  set, and the agent passes it on to its hooks.
- **It fails closed:** any error blocks the tool call.
- When stAirCase starts the agent itself, the hook always carries `--governed`, and
  without the run's file it blocks.
- Without `--governed` (a hook installed once, for a user or a company), it lets calls
  through when no governed session is running.
- It is always called by the absolute path of the `staircase` program. The command line
  is the same on every run, so trust-by-hash holds, no secret appears on it, and neither
  curl nor a shell is needed.

**A small dialect per agent** maps its tools to proposals:
- reads are allowed only inside the worktree;
- edits and writes become file proposals;
- shell commands become shell proposals;
- anything else is refused.

**Agents without a hook before edits** (Cursor) use *review-after* capture:
- the edit lands in the isolated worktree and becomes a proposal;
- if it is rejected, it is reverted;
- commands are refused while an edit waits for a decision.

Such runs reach at most CAL 2 ([ADR 0001](0001-core-promise-and-assurance-levels.md)).

**Later**, the same command will talk to a local daemon over a unix socket. The agents'
configuration will not change.

## Consequences

- Supporting a new agent means one dialect plus tests that drive it with a fake agent
  binary.
- The same command can be deployed through the vendors' managed (MDM) hook settings, so
  a company can govern every session centrally.
- The run's hook server keeps checking the token, so a process without the file cannot
  submit proposals.
