# Compatibility

Agents, models and stAirCase's own internals change often. Six interfaces are what
other tools, scripts and your CI build on, so they change carefully. This page says
what each one is and what you can rely on.

## Before and after 1.0

stAirCase is before 1.0 (see [CHANGELOG](../CHANGELOG.md)).

- **Now:** the change certificate v1 is stable. stAirCase will keep producing it,
  or keep accepting it after a v2 exists. The other five may still change in a
  minor version. Every change to them is listed in the CHANGELOG with what to do.
- **From 1.0:** all six follow semantic versioning. A breaking change needs a new
  major version, or a new version of the format that is accepted alongside the old
  one for at least one major version.

## The six contracts

| Contract | What it is today | What you can rely on |
|---|---|---|
| **Change certificate** | in-toto Statement v1 in a DSSE envelope, predicate type `…/0002-change-certificate.md#v1` ([specification](spec/certificate-v1.md), [test vectors](spec/vectors)) | Stable now. New optional fields may be added; verifiers ignore unknown fields. A change of meaning gets `#v2`, and v1 stays accepted. |
| **Hook bridge** | `staircase hook <agent> [--governed \| --require]`: the agent's hook JSON on stdin, the answer on stdout, exit 2 blocks; the session is found through `STAIRCASE_HOOK_FILE` ([ADR 0003](adr/0003-hook-bridge.md)) | Command, flags, exit codes and the environment variable keep working; new agents are added as new `<agent>` names. |
| **Proposal and decision records** | The audit chain's events, such as `yield_request` and `yield_decided`, and the signed checkpoint `staircase audit export` writes ([audit evidence](audit.md)) | A checkpoint that verifies today verifies with later versions. Events may gain fields; existing fields keep their meaning. Each event names the hash-chain format it was written under (`hash_version`; version 2 covers the event type, see [the hash chain](audit.md#the-hash-chain) and [ADR 0004](adr/0004-audit-chain-version-2.md)); every version stays verifiable, and a new one is added alongside the old, never in place of it. |
| **Decision-model interface** | Typed questions (`boolean`, `choice`, `score`) about a change, answered with probabilities, through the gateway's `/v1/evaluate` (`--signal`) | Signals can only send a change to a person. That rule does not loosen without a new, opt-in flag. |
| **Verifier results** | `checks` in the certificate: command, exit code, sandboxed, output digest (`--check`) | Existing fields keep their meaning. Linking full test reports as in-toto test-result attestations is planned, as an addition. |
| **Policy file** | `policy.json`: rules that approve or reject proposals, and limits ([approvals](approvals.md)) | A valid policy file keeps its meaning. `staircase policy test` shows what a policy decides on your past runs before you rely on a change. |

Everything else, including the command-line output, the workspace layout, the
database and the agents' prompts, can change in any version.

## Which agents have actually been run

A stand-in agent that speaks an agent's hook protocol proves stAirCase's side of the
bridge. It says nothing about what a real version of that agent does. This is what has
been run against the real thing, with the version and the date, and how to repeat it.
"Stand-in only" means the behaviour is tested against a fake that stAirCase's own
tests drive, not against the real program.

| Scenario | Codex CLI | Claude Code | Gemini CLI | OpenCode |
|---|---|---|---|---|
| Version exercised | 0.159 (2026-09-30), earlier 0.155 and 0.158 | 2.1.236 (logged out) | 0.46.0 (no login) | not installed |
| Its hooks run, every tool call is governed | yes (real run) | not run: needs a login | stand-in only | stand-in only |
| Session refused when its hooks never ran | stand-in only | not implemented: a run whose hooks never ran is caught by the end-of-run check, which commits nothing unapproved | stand-in only | stand-in only |
| A hook answer that is empty, cut off or not a decision, a hook call that is unreadable or for an unregistered event, a hook program that is missing, crashes or is killed | stand-in host only: the hook turns each into a block (exit 2); a host that carries on after any other exit status is modelled on its documented behaviour | stand-in host only (same) | stand-in host only (same) | not applicable: the plugin asks the run itself |
| The host gives up on a slow hook | stand-in only: the tool runs, the end-of-run check commits nothing unapproved | not run | not run | not run |
| An approval held longer than a hook's default timeout | yes: held 70 s, then answered | not run | not run | not run |
| A rejection, then the agent's retry | yes: rejected once, the retry approved | not run | not run | not run |
| Stop refused until a failing check passes | yes: the first attempt to stop blocked by the check, the second allowed | stand-in only | not applicable | not applicable |
| Commit verified and rebuilt by a fresh clone with only the public key | yes | not run | not run | not run |
| Commands: no network, no credentials | Codex's own sandbox plus stAirCase's checks; not probed with a real model | not run | not run | not run |
| Only staircase's settings are read (a repository's own hooks are ignored) | not applicable | yes, logged out | not run | not run |

Two rows above are easy to misread. "Stand-in host only" means a fake program that follows what the
host documents (Claude Code: only exit status 2, or a "deny" answer, stops a tool; any other failure of a
hook is a non-blocking error and the tool runs), not a real run. And a host that gives up on a slow hook
runs the tool whatever stAirCase does: what holds then is that **nothing unapproved is committed**, not that
the tool did not run. A hook that cannot answer therefore ends as a block when it can (the hook command ends
in `|| exit 2`, so even a missing program blocks), and the end-of-run check is the backstop when it cannot.

How each host's tool calls are read (names and arguments) is pinned by fixtures in
`internal/agent/normalize_test.go`, each marked with its host version and where it came from. All of them are
**synthetic** so far (written from the host's documentation, Gemini CLI 0.46.0 for Gemini; OpenCode is not
installed); none was captured from a real session. When a host names one argument twice with different
values (for example `absolute_path` and `file_path`), the call is refused as ambiguous rather than guessed.

What a cell means is the same everywhere: "yes" cites a run whose result is in the
audit chain of that run and that anyone can repeat; everything else is unverified.
Two more limits. The real Gemini CLI, run without a login, refuses before any hook
fires, so its hooks and our settings file are untested beyond being accepted. And the
Codex run used a cheap model on a one-line task: it shows the mechanics work, not how
well a model does real work.

**Repeat them.** Each needs the agent installed and logged in, costs a few cents with a
small model, and cleans up after itself:

```bash
make smoke-codex                 # edits decided first, command changes reviewed after
./demo/smoke.sh codex-stop       # held and rejected approval, refused Stop, fresh-clone verify
make smoke-claude                # Claude Code, first approval held 40 s
SMOKE_CODEX_MODEL=nonexistent ./demo/smoke.sh codex-stop   # free: stops at the model call
```

`SMOKE_KEEP=1` keeps the scratch directory (and prints its path) so you can look at
the audit chain afterwards. Agent versions change often: a newer version than the one
listed may behave differently, so run the smoke test for your agent after upgrading it.
