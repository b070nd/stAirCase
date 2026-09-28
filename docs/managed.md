# Governing every session in a company

A company can make every Claude Code or Codex session on its machines go through
stAirCase. It deploys one hook, the way it deploys any managed setting: the hook
**blocks** every tool call outside a governed session and **governs** the calls inside
one. Developers then use the agents through `staircase claude` and `staircase codex`.

## Claude Code

Print the managed settings, with the path where staircase is installed on the
developers' machines:

```bash
staircase hook-template claude-code --bin /opt/homebrew/bin/staircase > managed-settings.json
```

Deploy the file as Claude Code's
[managed settings](https://code.claude.com/docs/en/managed-settings):

- macOS: `/Library/Application Support/ClaudeCode/managed-settings.json`, or a
  configuration profile for the `com.anthropic.claudecode` domain;
- Linux and WSL: `/etc/claude-code/managed-settings.json`;
- Windows: the JSON as the `Settings` value under `HKLM\SOFTWARE\Policies\ClaudeCode`.

The file also sets `allowManagedHooksOnly`, so developers' own and repositories' hooks
do not load. The managed hook alone governs a session; when a session's own hook runs
too, each tool call is still decided once.

## Codex

```bash
staircase hook-template codex --bin /opt/homebrew/bin/staircase > codex-hooks.toml
```

This prints the `[hooks]` block for Codex's managed configuration (Codex runs hooks
from managed sources without asking anyone to review them). The block was checked to
load with codex-cli 0.155; where a company deploys managed Codex configuration depends
on its Codex setup, see OpenAI's Codex documentation.

## What developers see

- `claude` or `codex` started directly: every tool call is refused with *"this
  organisation runs … only through stAirCase: start a session with staircase claude"*.
- `staircase claude "task"` or `staircase codex "task"`: the session works as usual,
  with every change decided and certified.

## Limits

This stops accidental ungoverned sessions. It is not a wall against someone set on
getting around it: a developer who is an administrator on their machine can change
managed settings, and the hook trusts the session file named in its environment. Use
the [CI check](audit.md#require-certificates-on-pull-requests) as the second line:
what reaches your repositories must carry a valid change certificate.
