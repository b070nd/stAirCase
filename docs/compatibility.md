# Compatibility

Agents, models and stAirCase's own internals change often. Six interfaces are what
other tools, scripts and your CI build on, so they change carefully. This page says
what each one is and what you can rely on.

## The promise, from 1.0.0

From v1.0.0 all six follow semantic versioning (releases before 1.0 are listed in the [CHANGELOG](../CHANGELOG.md)). A breaking
change to one of them needs a new major version, or a new version of the format that is accepted alongside the old one for at
least one major version. The change certificate v1 has been stable since it was introduced: stAirCase keeps producing it, or keeps
accepting it after a v2 exists. Every change to the six is listed in the CHANGELOG with what to do.

The promise is about these six interfaces and nothing else. The agent adapters are not part of it: they follow other programs'
versions, and Gemini CLI and OpenCode are experimental (see below for what has been run).

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
| Version exercised | 0.159 (2026-09-30), earlier 0.155 and 0.158 | 2.1.236 (2026-10-05, headless, model haiku, its credential passed with `--pass-env`) | 0.46.0 (2026-10-06, headless, model gemini-3.5-flash-lite, its key passed with `--pass-env`) | 1.18.35 (2026-10-07, headless, model openai/gpt-4.1-mini, its own OpenAI login, two runs for $0.0125) |
| Its hooks run, and a file write is governed (the one tool a real run exercised) | yes (real run) | yes (real run: `make smoke-claude`) | yes (real run: `./demo/smoke.sh gemini`) | yes (real run: `./demo/smoke.sh opencode`) |
| Session refused when its hooks never ran | stand-in only | refused when SessionStart never arrives (the session is not governed, nothing is kept); SessionStart firing in headless `-p` with `--settings` was probed on 2.1.236 (real), the refusal itself is tested with a synthetic host | stand-in only | stand-in only |
| A hook answer that is empty, cut off or not a decision, a hook call that is unreadable or for an unregistered event, a hook program that is missing, crashes or is killed | shared bridge tests only (`staircase hook`, each agent's reply shape): the hook turns each into a block (exit 2); no Codex host was simulated | shared bridge tests, and a synthetic Claude Code host (`hook_host_test.go`, a fake that follows the documented behaviour: only exit 2 or a deny stops a tool): not a real run | shared bridge tests only | not applicable: the plugin asks the run itself |
| The host gives up on a slow hook | not run | synthetic Claude Code host only (`hook_host_test.go`): the tool runs, the end-of-run check commits nothing unapproved; not a real run | not run | not run |
| An approval held longer than a hook's default timeout | yes: held 70 s, then answered | yes (real run): held 40 s, then answered | yes (real run): held 40 s, then answered | not run |
| A rejection, then the agent's retry | yes: rejected once, the retry approved | yes (real run): rejected once, the retry approved; the committed bytes are the retry's | yes (real run): rejected once, the retry approved; the committed bytes are the retry's | yes (real run): rejected once, the retry approved; the committed bytes are the retry's |
| Stop refused until a failing check passes | yes: the first attempt to stop blocked by the check, the second allowed | stand-in only | not applicable | not applicable |
| Commit verified and rebuilt by a fresh clone with only the public key | yes | yes (real run, both runs above: CAL 3, tree identical on rebuild) | yes (real run, both runs: CAL 3, tree identical on rebuild) | yes (real run, both runs: CAL 3, tree identical on rebuild) |
| Its own session is resumed after a kill (`staircase resume`, native) | session id read from `thread.started` of `codex exec --json`, resumed with `codex exec resume <id>`: real probe on 0.159 (the resume is refused for about a minute after a kill while the dead run holds the thread, so stAirCase retries for up to three minutes); adapter tests against a stand-in | `--session-id` and `--resume` accepted by 2.1.236 and the session file persists after a kill -9 (real probe); that the resumed agent remembers its conversation is **not verified** (no valid credential at the time), adapter tests against a stand-in | yes (real run on 0.46.0): killed mid-run, resumed with `--resume <id>` and the agent remembered its context | no native resume: refused, `--fresh-context` only |
| A file written around the hooks is not committed (the test writes it into the worktree during the run) | not run | not run | yes (real run, 0.46.0, 2026-10-07): the run failed with `unapproved_worktree_change` and committed nothing; `recover` delivered only the approved file; fresh-clone verify and rebuild passed | not run |
| Commands: no network, no credentials | Codex's own sandbox plus stAirCase's checks; not probed with a real model | not run | not run | not run |
| Only staircase's settings are read (a repository's own hooks are ignored) | not applicable | yes, logged out | not run | not run |

The evidence behind a cell is one of four kinds, from weakest to strongest: a **shared bridge test** (the
`staircase hook` program and its reply checks, the same code for every agent), a **stand-in agent** (a fake
program that speaks the agent's hook protocol, driven by stAirCase's own tests), a **synthetic host** (a fake that
follows what the host documents, so it tests our reaction to the documented behaviour), and a **real run** of the
real program. No fixture here was captured from a real host, and an agent with no real run stays experimental.
Two rows above are easy to misread. "Synthetic host" means a fake program that follows what the
host documents (Claude Code: only exit status 2, or a "deny" answer, stops a tool; any other failure of a
hook is a non-blocking error and the tool runs), not a real run. And a host that gives up on a slow hook
runs the tool whatever stAirCase does: what holds then is that **nothing unapproved is committed**, not that
the tool did not run. A hook that cannot answer therefore ends as a block when it can (the hook command ends
in `|| exit 2`, so even a missing program blocks), and the end-of-run check is the backstop when it cannot.

How each host's tool calls are read (names and arguments) is pinned by fixtures in
`internal/agent/normalize_test.go`, each marked with its host version and where it came from. All of them are
**synthetic** so far (written from the host's documentation, Gemini CLI 0.46.0 for Gemini, OpenCode 1.18.35 for
OpenCode, whose real run exercised only its `write` tool); none was captured from a real session. When a host names one argument twice with different
values (for example `absolute_path` and `file_path`), the call is refused as ambiguous rather than guessed.

What a cell means is the same everywhere: "yes" cites a run whose result is in the
audit chain of that run and that anyone can repeat; everything else is unverified.
Two more limits. Historical: an early probe of the real Gemini CLI without a login refused
before any hook fired; since then it has been run for real (the table), so that probe says
nothing about the current adapter. And the real runs used cheap models on one-line tasks: they
show the mechanics work (an approval, a held approval, a rejection and its retry, a verify and a
rebuild in a fresh clone), not how well a model does real work, and not that every tool call of
a host is governed. Each real run exercised one tool, a file write. The other tools are covered by
stand-in agents and synthetic hosts. One real run (Gemini CLI 0.46.0) had a file written into its
worktree around the hooks (an ungated change), which is what the end-of-run check is for; the file was
written by the test, not by the agent, and none has been done for Claude Code, Codex or OpenCode.

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
