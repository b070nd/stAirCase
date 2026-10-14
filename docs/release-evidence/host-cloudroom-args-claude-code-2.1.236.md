# `staircase host claude` through Cloudroom's arguments, with Claude Code 2.1.236 (2026-10-14)

What was asked ([ADR 0008](../adr/0008-governing-another-runner.md)): run the way a runner starts Claude Code, is a session governed as
an away run, with the runner's protocol intact and nothing widened?

**Setup.** stAirCase built from `feature/SC-111-prototype-real-run` (the stand-in uncommitted at the time of the run, committed with this
record); Claude Code 2.1.236; a new repository and workspace; `staircase host setup --allow "HEALTH.md" --check "grep -qx ok HEALTH.md"
--pass-env CLAUDE_CODE_OAUTH_TOKEN`. The stand-in was started with **Cloudroom 0.1.57's own argument list for a new Claude Code session**,
taken from its `src/runtime/claude.rs` at `d44dd8b`: its two `--settings` JSONs, `-p`, stream-json in and out, `--verbose`,
`--include-partial-messages`, `--replay-user-messages`, `--dangerously-skip-permissions`, `--permission-prompts none`, `--no-chrome`,
`--model haiku`, `--append-system-prompt`, `--disallowedTools`, `--mcp-config` with its `cloudroom` server, and a `--session-id`. One
stream-json user message on stdin asked for `HEALTH.md` ("ok") and then `deploy.sh`. Cloudroom itself was not run.

**Result** (8 seconds):

- stdout, the runner's stream, held 237 lines, every one JSON, ending in Claude Code's `result`; none of stAirCase's own output.
- `HEALTH.md` was approved on evidence; `deploy.sh`, outside the scope, was parked; the run ended `AWAITING_REVIEW`.
- On the chain: `run_bound` with the Away brief (version 1), `agent_session` with the runner's id `6f1c2d3e-4a5b-4c6d-8e9f-0a1b2c3d4e5f`,
  the evidence decision, the parked decision (`outside the stories' scope: deploy.sh`), `phase_checked`, `awaiting_review`.
- `staircase decide 1 --accept` committed `730f13def687` (`HEALTH.md`, `README.md`). A fresh clone with only the public key: `valid change
  certificate, CAL 3`, `rebuilt from 1 approved proposal(s): tree 14a3e5a52c74 is identical`.

What the stand-in did with the runner's arguments (as tested in `TestHost_governs_a_session_a_runner_starts`): the MCP configuration and
`--permission-prompts` were dropped (2.1.236 does not know the flag), the two settings JSONs were reduced to their harmless keys under
stAirCase's own settings, and `--setting-sources ""` and `--strict-mcp-config` were added.

**What it does not show:** Cloudroom itself starting the stand-in, a long multi-turn session, a resumed session, Cloudroom's tools (off), or a
Claude Code version that knows `--permission-prompts`.
