# Governing sessions another program starts

Some tools start Claude Code for you: a runner such as Cloudroom keeps agents working in its own sessions, with Claude Code's approvals
switched off. You can put stAirCase in front of it without changing the runner: name `staircase host claude` as the runner's Claude Code
program. Each session it starts is then an [away run](away.md): nobody is asked, a change is approved only when your checks pass on it,
anything that needs you is parked, and the change waits for `staircase decide`. The design is [ADR 0008](adr/0008-governing-another-runner.md).

## Set up

```bash
cd your-repository
staircase host setup --allow "src/**" --check "go test ./..." --pass-env CLAUDE_CODE_OAUTH_TOKEN
```

Then, in the runner's settings for Claude Code, set the program to `staircase host claude` (or a script that runs it). The runner's
working directory must be your repository; `--pass-env` names the variable that carries Claude Code's credential (the runner usually sets
it).

## What the runner keeps, and what it does not

- **Its protocol.** The runner's stdin and stdout reach Claude Code untouched (stream-json in and out); stAirCase's own output goes to
  `host/<session>.log` in the workspace, never into the runner's stream.
- **Its session id**, on the audit chain as the agent's session.
- **Its instructions**, with stAirCase's Away brief after them (versioned; its digest is on the chain).
- **Its harmless settings** (`fastMode`, `enableWorkflows`, `attribution`), under stAirCase's own.
- **Its MCP server, behind the gate.** Every call of the runner's tools is a proposal stAirCase decides: never by a policy rule; while away
  it is parked unless you declared its exact name (`staircase host setup --away-tool mcp__<server>__<tool>`). A change made with an approved
  tool call reaches CAL 2: the tool ran outside stAirCase's sandbox. Probed on Claude Code 2.1.236 with a stdio server
  ([record](release-evidence/claude-code-2.1.236-mcp-tools-under-hooks.md)); an SDK-type server like Cloudroom's own is not probed yet.
- **Not an argument stAirCase does not know.** An unknown argument could widen what the agent may do, so the session is refused, naming it.
- **Not its working copy.** The agent works in stAirCase's run worktree; what reaches `staircase/run-N` is only what stAirCase derived from
  recorded decisions, with its certificate.

## What it guarantees, and what not yet

- Claude Code's hooks still decide when a runner switches its permission prompts off: probed on 2.1.236
  ([record](release-evidence/claude-code-2.1.236-hooks-under-skip-permissions.md)). A session in which stAirCase's hooks never ran keeps nothing.
- Seen with a real agent through Cloudroom 0.1.57's own argument list ([record](release-evidence/host-cloudroom-args-claude-code-2.1.236.md)).
- Not yet: a resumed or forked runner session (refused, naming the reason), other agents than Claude Code, and a run inside Cloudroom itself.
