# ADR 0008: Governing a session another runner starts (design)

- Status: accepted (2026-10-14), owner's request: "Cloudroom beside stAirCase, ONLY IF this is not weakening the stAirCase project
  position; take the best from all four options". The owner chose the first slice with the runner's MCP tools off. The thin slice is built (`staircase host claude`, the pinned Away
  brief); section 2's resumed sessions and the gated MCP tools are not.
- Related: ADR 0003 (hook bridge), ADR 0006 (Away mode), ADR 0007 (prototype bridging), the Cloudroom reuse assessment.

## Context

Cloudroom runs AI coding agents for people: it starts Claude Code (and others) with approvals switched off
(`--dangerously-skip-permissions`), keeps long sessions over a two-way stream-json protocol, and adds its own tools through an MCP server.
People who use it get agents that work unattended, and no record of what reached their branch or why. stAirCase is the opposite half:
every change decided before it is made, and only bytes derived by stAirCase and covered by a recorded decision reach the branch.

The question is whether stAirCase can govern a session Cloudroom starts, with no change to Cloudroom, and without giving up anything of
its promise.

What was found (sources at Cloudroom `d44dd8b`, probes on Claude Code 2.1.236):

- Cloudroom starts the binary its harness profile names (`profile.binary` in `src/runtime/claude.rs`), so another program can stand in
  front of Claude Code by configuration alone.
- It passes `--dangerously-skip-permissions`, its own `--settings` JSON (once or twice), `--mcp-config` with its `cloudroom` MCP server,
  `--disallowedTools`, `--append-system-prompt`, `--session-id` or `--resume`, and stream-json in and out.
- **Probe:** under `--dangerously-skip-permissions`, in headless mode and in two-way stream-json mode, a `PreToolUse` hook that denies
  still blocks the write ([record](../release-evidence/claude-code-2.1.236-hooks-under-skip-permissions.md)). Skipping the prompts
  does not skip the hooks.
- **Probe:** Claude Code 2.1.236 does not know `--permission-prompts`, which Cloudroom 0.1.57 passes: Cloudroom targets a newer Claude Code
  than the one stAirCase has evidence for. Any integration is qualified per version pair.

## Decision

### 1. The condition: what keeps stAirCase's position (non-negotiable)

The integration is built only on these terms; if one cannot hold, it refuses rather than governs less.

1. **stAirCase owns the run, its worktree and delivery.** The agent works in stAirCase's run worktree, not in the runner's workspace;
   only bytes stAirCase derives from recorded approvals reach `staircase/run-N`, with the certificate and ledger. The runner never commits
   on stAirCase's behalf, and nothing the runner says (a finished turn, a receipt, a chat message) is evidence or acceptance.
2. **Only stAirCase's hooks decide, and a session without them is refused.** The stand-in merges the runner's settings into stAirCase's,
   never the reverse: stAirCase's hooks, with `--setting-sources ""` (no user or project settings), and the `SessionStart` handshake
   as today. A session whose hooks never ran is not governed and delivers nothing.
3. **Every tool the agent can call goes through the gate, the runner's included.** stAirCase today allows no outside MCP server
   (`--strict-mcp-config`), because such tools act outside governance. With a runner's MCP server, every MCP tool call goes through
   `PreToolUse` like an edit or a command: decided by policy, parked while away, never silently allowed. If a runner's tools cannot be
   put behind the gate, they are switched off for the session, and the runner loses them, not stAirCase its promise. The certificate
   says which outside tools were present and how each call was decided.
4. **Away rules apply.** A runner session nobody watches is an away run (ADR 0006): approval on evidence or policy only, everything else
   parked, the end checked on the exact tree, and the change waits for `staircase decide`, with P to prototype what was parked (ADR 0007).
5. **No widening for the runner.** Its `--dangerously-skip-permissions` is kept (prompts are not stAirCase's gate; hooks are), but nothing
   the runner passes adds a permission, a credential or a tool past the gate.

On these terms the integration does not weaken stAirCase: it extends the promise to agents stAirCase does not start, and the runner's
"approvals off" becomes "decided by stAirCase, recorded, delivered only as derived". Where the terms cannot hold, the answer is no.

### 2. The stand-in: `staircase host claude`

A binary the runner names instead of `claude` (`staircase host claude`, or a one-line wrapper script). Given the runner's arguments it:

1. finds the repository (the runner's working directory) and starts or continues an away run for the runner's session: the session id
   (`--session-id` or `--resume`) maps to one stAirCase run, so a resumed runner session continues the same run (ADR 0005, native resume);
2. rewrites the arguments: stAirCase's hook settings and the runner's settings merged into one `--settings`, `--setting-sources ""`, the
   runner's MCP configuration kept only if its tools pass the gate (decision 1.3), the working directory replaced by the run's worktree;
3. starts the real Claude Code with them and passes stdin and stdout through untouched, so the runner's stream-json protocol is unchanged;
4. when Claude Code exits, ends the segment as an away run does (checks on the exact tree, `awaiting_review`), and exits with Claude Code's
   status.

The runner keeps its features that do not touch the branch (chat, history, its own views). Its view of the files is the run's worktree.

### 3. The best of the other three options, taken into this design

- **A pinned Away brief** (small, safe): the stand-in adds stAirCase's brief to the agent's system prompt, versioned, with its digest on
  the audit chain: "you work unattended under governance; a refused change is parked for the owner, do not retry it; hand off what you
  could not do". It changes no rule; it makes agents behave well under parking, in stAirCase's own away runs too.
- **A continuing prototype** (later, ADR 0007's next step): a parked proposal can start a second, throwaway session in the runner or in
  stAirCase, on its own branch, to show where the work leads. The stand-in is the piece that lets a runner host that second session.
- **Delivery by policy** (not taken now): delivering to the run branch without the owner's review weakens review-before-delivery, which this
  integration must not do. Recorded as considered: a later form could deliver to the run branch on evidence and move the owner's review to
  the pull request, where the required admission check (the ruleset fixture) still enforces the certificate; it needs its own ADR.

## Consequences

- Cloudroom needs no code change: a harness profile whose binary is the stand-in.
- The runner's MCP tools are gated or off; a runner whose features depend on ungated tools works less under stAirCase, by design.
- Each runner version and Claude Code version pair is qualified by a real probe before it is advertised.

## Open questions, to settle by probes before building

1. ~~Does `PreToolUse` fire for MCP tool calls (`mcp__*`) under the runner's flags, and can a deny block them?~~ Yes, probed on 2.1.236
   ([record](../release-evidence/claude-code-2.1.236-mcp-tools-under-hooks.md)); decision 1.3 is built: each call is a `tool_call` proposal,
   never approved by a policy rule, parked while away unless its exact name is declared (`--away-tool`), and an approved one caps the
   change at CAL 2. Not yet probed: an SDK-type server such as Cloudroom's own.
2. Does Claude Code merge two `--settings` flags or keep the last? (The stand-in merges them itself either way.)
3. Do Cloudroom's own features need its working directory to be its workspace (room-cli, child spawns, file views)?
4. Which Claude Code version does Cloudroom 0.1.57 need (`--permission-prompts`), and is it one stAirCase can qualify?

## Thin slice, when accepted

The stand-in for Claude Code only, with no runner MCP server (stAirCase's `--strict-mcp-config` kept), the pinned Away brief, one session
mapped to one away run, and a real probe through Cloudroom's exact arguments (from its source), ending in `staircase decide`.
