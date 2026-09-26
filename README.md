<h1 align="center">stAirCase</h1>

<p align="center">
  <strong>The enforcement gate between AI plans and your codebase.</strong><br>
  Open, self-hostable, with an offline demo. External workspace state, approval workflows, and auditable runs.
</p>

<p align="center">
  <a href="https://github.com/b070nd/stAirCase/actions/workflows/ci.yml"><img src="https://github.com/b070nd/stAirCase/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/b070nd/stAirCase/actions/workflows/codeql.yml"><img src="https://github.com/b070nd/stAirCase/actions/workflows/codeql.yml/badge.svg" alt="CodeQL"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/b070nd/stAirCase"><img src="https://api.securityscorecards.dev/projects/github.com/b070nd/stAirCase/badge" alt="OpenSSF Scorecard"></a>
  <a href="https://github.com/b070nd/stAirCase/releases/latest"><img src="https://img.shields.io/github/v/release/b070nd/stAirCase?include_prereleases&sort=semver" alt="Latest release"></a>
  <a href="https://pkg.go.dev/github.com/b070nd/stAirCase"><img src="https://pkg.go.dev/badge/github.com/b070nd/stAirCase.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/b070nd/stAirCase"><img src="https://goreportcard.com/badge/github.com/b070nd/stAirCase" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  <img src="https://img.shields.io/badge/status-pre--1.0-orange.svg" alt="pre-1.0">
</p>

---

AI coding agents are useful and increasingly autonomous. The risk isn't that they
write code — it's that they write code **you never saw, approved, or can prove
the provenance of afterward**. stAirCase is the control layer that sits between an
agent's plan and its execution: the agent proposes, a human approves, and every
decision is recorded for audit. It is not an OS sandbox: the agent's tools and
approved shell commands run as your user (see the safety boundary).

**Before using a project:** read [Project Use and Current Safety Boundary](docs/project-use.md).
Runs work in their own git worktree, so your checkout is never touched.

It is a single Go binary with a small built-in agent runtime, and the governed
boundary *around* it — self-hosted. Real model calls send project context to the
configured provider; the offline demo uses a stand-in model and makes none.

```
        plan ─▶ ┌───────────── stAirCase control plane (Go) ─────────────┐
                │  quality gates → HITL approval → content-bound commit  │
                │           every decision → signed audit chain          │
                └───────────────────────────────────────────────────────┘ ─▶ governed change
                                          ▲
                              agents (built in, in-process)
                         every edit or command is a proposal the
                         control plane decides — the gate is the boundary
```

## See it in 60 seconds (no API key, fully offline)

```bash
git clone https://github.com/b070nd/stAirCase.git
cd stAirCase
make demo
```

`make demo` runs the whole governance loop — the real control plane and agent
runtime — against a stand-in model, so it's deterministic and needs no API key
or network. A blueprint from its own repository is bound to a project; a coder
creates, edits and deletes files, each blocking for your approval; exactly the
approved bytes land on the run's own branch while your checkout stays
byte-identical; you accept the stories; and the signed audit chain is
**verified**. Then:

```bash
./demo/run-demo.sh --tamper   # an approved shell command changes the file AFTER its approval
./demo/run-demo.sh --drift    # the agent wanders outside its stories' scope
```

With `--tamper` the run fails, nothing is committed, and the refusal is recorded
as an `approval_content_mismatch` event — the headline guarantee, demonstrated
failing closed. With `--drift` the out-of-scope proposals come to you marked
`DRIFT`, the run is halted, and the case waits for `--ack-drift`.

## What it does

- **Human-in-the-loop approval** — agents *yield* before any file edit, branch
  commit, or shell command; a human (or an explicit policy rule) decides.
- **Approval bound to bytes** — the control plane derives exactly what each
  approved edit produces and commits only that: the commit is built from the
  approved bytes, and a run whose worktree holds anything else fails.
- **Signed, tamper-evident audit chain** — every event is hash-chained; exported
  checkpoints of the chain are Ed25519-signed and can be anchored in a public
  [Rekor](https://docs.sigstore.dev/logging/overview/) transparency log for an
  external witness.
- **Separate worktree per run** — each run works in its own git worktree on a
  `staircase/run-N` branch; your checkout is never touched.
- **Secret handling** — secrets are encrypted at rest; model API keys are
  decrypted only to call the model, are never shown to it, and are scrubbed from
  every log and audit record. Shell commands get a minimal environment.
- **Shell execution off by default** — `run_shell` is disabled unless you pass
  `--allow-shell-exec`, enforced at three independent layers.
- **Quality gates** — pluggable pre-run checks (signed `gates.json` manifests)
  that must pass before a run starts.
- **Blueprints** — a project's automation versioned in its own repository,
  imported as a content-hash snapshot and pinned: a bound case runs only as its
  blueprint defines it, and every run records the plan and blueprint it
  executed ([docs/blueprints.md](docs/blueprints.md)).
- **Drift supervision** — changes outside the stories' scope, past file limits
  or at checkpoints go to a human; too many violations or too long a run halt
  it until acknowledged; an optional model reviewer decides in-scope edits,
  with a human approving each run's final change ([docs/drift.md](docs/drift.md)).

## Architecture

One Go binary. `staircase compile` turns a case (PRD, stories, agent topology)
into a checksummed plan; `staircase run` executes it with the built-in agent
runtime — a supervisor and its agents calling models over HTTPS (Anthropic,
OpenAI, Gemini, xAI, or any model through an OpenAI-compatible LLM gateway).
The agents' tools cannot change the repository themselves: every edit is a
proposal the control plane decides and audits, and tools write only the bytes it
derived. There is no Python and no separate runtime process.

State (runs, cases, topologies, encrypted secrets, the audit chain) lives in a
local SQLite workspace, separate from the repositories being changed — so agent
exhaust never pollutes your product repos.

How it fits together: [`docs/architecture.md`](docs/architecture.md).

## Install

```bash
brew install b070nd/staircase/staircase                           # macOS / Linux, with shell completions
go install github.com/b070nd/stAirCase/src/cmd/staircase@latest   # Go 1.26+
```

Or download an archive from the [latest release](https://github.com/b070nd/stAirCase/releases/latest):
Linux and macOS (amd64, arm64), and an **experimental** Windows (amd64) build —
untested, and `run_shell` and `--agent claude-code` need a POSIX shell. Every
release is built reproducibly from its tag, with checksums signed by a keyless
cosign signature, an SBOM per archive, and GitHub build-provenance attestations;
[verify a download](QUICKSTART.md#verifying-release-artifacts) before running it.

A single static binary, no CGo, pure-Go SQLite — no shared libraries, no Python.
The full walkthrough (workspace setup, a real run, HITL, evidence export) is in
**[QUICKSTART.md](QUICKSTART.md)**.

## Security

stAirCase is a security tool; its threat model and guarantees are documented in
**[SECURITY.md](SECURITY.md)**, including how to report a vulnerability. In short:
only bytes a human (or an explicit policy rule) approved can be committed, every
decision is on a signed and externally anchorable audit chain, and the shell tool
is off by default. Without an OS sandbox, approved shell commands still run as
your user — that is the main limitation.

## Status & roadmap

Pre-1.0 and under active development. The control plane, agent runtime, HITL
flow, audit chain and offline demo have automated tests (including race and
adversarial checks), but those tests do not establish enterprise readiness. On
the roadmap:

- **DSSE audit envelopes** — adopt the Sigstore/in-toto envelope format so the
  audit chain interoperates with the broader supply-chain ecosystem.
- **Per-agent identity** — distinct identity per agent persona with per-tool
  credential scoping.
- **OS-level sandbox** for untrusted runtimes, not only shell-enabled runs.

## Contributing

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for build/test
conventions (`make check` is the gate) and the project norms.

## License

[MIT](LICENSE) © 2026 Botond Biro
