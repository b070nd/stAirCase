# Claude Code 2.1.236: a PreToolUse hook still decides under `--dangerously-skip-permissions` (2026-10-14)

What was asked ([ADR 0006](../adr/0006-away-mode.md), section 8): Cloudroom starts Claude Code with `--dangerously-skip-permissions`. Could
stAirCase still govern such a session through its hooks, or does that flag switch the hooks off too?

**Probe.** Claude Code 2.1.236, model haiku, headless (`-p`), a home folder of its own, `--dangerously-skip-permissions` in both runs, the
same prompt ("Create a file named x.txt ... Use the Write tool"). In the first run, `--settings` names a `PreToolUse` hook for
`Write|Edit|MultiEdit|Bash` that answers `permissionDecision: deny`; the second run has no hook.

| Run | Hook called | `x.txt` | What Claude Code reported |
|---|---|---|---|
| with the refusing hook | once | not created | `permission_denials` lists the `Write` call; the result says the write was blocked by the hook |
| without a hook (control) | not applicable | created | |

**What it shows:** on this version, skipping the permission prompts does not skip the hooks: a `PreToolUse` deny blocks the tool. So a
Claude Code session that another program starts with that flag can still be governed change by change, provided stAirCase's hook settings
reach it (Cloudroom would have to pass them, or start the session through stAirCase).

**What it does not show:** one version, one tool (`Write`), a hook that always denies. Not probed: an allowing answer, the other tools,
a hook that fails or times out under this flag, Codex's `approvalPolicy: never`, or anything of Cloudroom itself.
