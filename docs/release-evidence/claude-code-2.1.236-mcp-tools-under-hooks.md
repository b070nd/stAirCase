# Claude Code 2.1.236: a PreToolUse hook decides MCP tool calls too (2026-10-14)

What was asked ([ADR 0008](../adr/0008-governing-another-runner.md), open question 1): when a runner gives Claude Code an MCP server, do
stAirCase's hooks see the calls of its tools, and can a refusal stop them?

**Probe.** Claude Code 2.1.236, model haiku, headless, `--dangerously-skip-permissions`, `--strict-mcp-config`, `--setting-sources ""`, a home
folder of its own. A minimal MCP server over stdio (written for the probe: one tool, `write_note`, that writes a marker file on the host)
given with `--mcp-config`. Prompt: "Use the write_note tool of the probe MCP server to write the text hello". In the first run a
`PreToolUse` hook (matcher `*`) refuses every `mcp__*` tool and allows the rest; the second run has no hook.

| Run | What the hook saw | The tool | Claude Code reported |
|---|---|---|---|
| with the hook | `ToolSearch` (allowed), then `mcp__probe__write_note` (refused) | did not run (no marker file) | `permission_denials` lists `mcp__probe__write_note` |
| without a hook | | ran (marker file written) | "Done." |

A first attempt refused every tool, `ToolSearch` included; Claude Code then never reached the MCP tool, so that run showed nothing about
MCP calls and is not counted.

**What it shows:** on this version an MCP tool call goes through `PreToolUse` under its full name (`mcp__<server>__<tool>`) with its input,
and a refusal stops it, also with the permission prompts skipped. stAirCase can therefore decide each call instead of switching the tools off.

**What it does not show:** an SDK-type MCP server (Cloudroom's `cloudroom` server is `"type":"sdk"`, served by the host program), other
versions, or tools that act before the hook is asked.
