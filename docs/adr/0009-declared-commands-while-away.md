# ADR 0009: Declared commands while away (design)

- Status: accepted and built (2026-10-14): the owner chose to build it as proposed. Built with "a sandbox without network" meaning
  `docker:...`, or `required` where sandbox-exec or bubblewrap runs (Landlock cannot cut the network); Gemini CLI and OpenCode are refused.
- Related: ADR 0006 (Away mode, section 9 item 4), the docker sandbox (`--sandbox docker:...`), `policy.Engine` ("shell commands must always
  reach a human operator").

## Context

An away run parks every shell command, because a command must reach a person and nobody is there. That is safe, and it makes the
commonest unattended work stall: an agent that wants to run `go generate`, `npm run build` or a formatter before it proposes a change
cannot. The checks already run unattended (they are the evidence), so the gap is commands the agent asks for.

What must not change: only bytes stAirCase derives from recorded decisions reach the branch, and the host is not exposed to a command
nobody authorized.

## Decision (proposed)

A run's owner may declare, when the run starts, the exact commands an away run may run without asking: `--away-command "go generate ./..."`
(repeatable). They are part of the run's frozen terms (saved, restored on resume, digest on `run_bound`), and:

1. **Exact commands only.** A proposed command is allowed only if it is byte-for-byte one of the declared commands, run in the worktree's
   root. No patterns, no arguments added, no shell composition (`;`, `&&`, pipes, redirections): a declared command is the whole command.
2. **Only in a strong sandbox.** Declared commands require `--sandbox docker:...` (or `required` with a sandbox that has no network);
   without one the run refuses to start rather than run them unsandboxed.
3. **Recorded as the owner's decision made in advance.** The decision's source is `declared`, naming the declaration's digest; the
   certificate counts it apart from a person's and a policy's decisions.
4. **What they change is reviewed after.** Files a declared command changes come to the run as changes made by a command, decided by
   evidence or parked like any other change, and the change reaches CAL 2 (as commands that change files do today).
5. **Not outside away runs.** The rule that every command reaches a person stays for runs with someone present; there, a person is asked.

## Why this does not weaken the promise

The branch still gets only derived, recorded bytes, and a command changes nothing on the branch by running: its file changes are decided
like an agent's. The host is exposed only to commands the owner wrote down, in a container that sees the worktree alone and has no
network. The person's decision moves from "when the command is proposed" to "when the run is agreed", and the record says so.

## Open

1. Is the declaration per run (`--away-command`) enough, or should a project keep a list?
2. Should a declared command's output (which goes back to the model) be bounded or redacted beyond what commands get today?
