<h1 align="center">stAirCase</h1>

<p align="center">
  <strong>The enforcement gate between AI plans and your codebase.</strong><br>
  Open, self-hostable, offline. Human authority over autonomous code agents — provable, not promised.
</p>

<p align="center">
  <a href="https://github.com/b070nd/stAirCase/actions/workflows/ci.yml"><img src="https://github.com/b070nd/stAirCase/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
  <img src="https://img.shields.io/badge/go-1.26-00ADD8.svg" alt="Go 1.26">
  <img src="https://img.shields.io/badge/status-pre--1.0-orange.svg" alt="pre-1.0">
</p>

---

AI coding agents are useful and increasingly autonomous. The risk isn't that they
write code — it's that they write code **you never saw, approved, or can prove
the provenance of afterward**. stAirCase is the control layer that sits between an
agent's plan and its execution: the agent proposes, a human approves, and every
decision lands in a signed, tamper-evident log. Nothing reaches your repository
without passing the gate.

It is **not** an agent framework. It is the governed boundary *around* agents —
self-hosted, with no data leaving your machine and no vendor in the loop.

```
        plan ─▶ ┌───────────── stAirCase control plane (Go) ─────────────┐
                │  quality gates → HITL approval → content-bound commit  │
                │           every decision → signed audit chain          │
                └───────────────────────────────────────────────────────┘ ─▶ governed change
                                          ▲
                                 agent runtime (Python/LangGraph)
                                  speaks an authenticated IPC protocol;
                                  is never trusted — the gate is
```

## See it in 60 seconds (no API key, fully offline)

```bash
git clone https://github.com/b070nd/stAirCase.git
cd stAirCase
make demo
```

`make demo` runs the whole governance loop against the real control plane using a
stub agent that speaks the genuine IPC protocol — so it's deterministic and needs
no API key or network. You'll watch an agent propose a change, block for human
approval, get committed only after the orchestrator confirms the bytes match what
was approved, and finish with a **verified** signed audit chain. Then:

```bash
./demo/run-demo.sh --tamper   # the agent writes different bytes AFTER approval
```

The run fails, nothing is committed, and the attempt is recorded as an
`approval_content_mismatch` event. That's the headline guarantee, demonstrated
failing closed.

## What it does

- **Human-in-the-loop approval** — agents *yield* before any file edit, branch
  commit, or shell command; a human (or an explicit policy rule) decides.
- **Content-bound approvals** — approval is bound to a SHA-256 of the exact bytes
  to be written. An agent cannot get a benign diff approved and then commit
  something else.
- **Signed, tamper-evident audit chain** — every event is hash-chained and
  Ed25519-signed; checkpoints can be anchored in a public
  [Rekor](https://docs.sigstore.dev/logging/overview/) transparency log for an
  external witness.
- **Git blast-radius isolation** — agent work happens on a dedicated
  `staircase/run-N` branch; only operator-approved files are staged, never a
  blanket `git add`.
- **Secret isolation** — secrets are decrypted in Go and delivered over the IPC
  boundary on request; the AES key never crosses into the agent runtime, and
  delivered values are scrubbed from every log and audit record.
- **Shell execution off by default** — `run_shell` is disabled unless you pass
  `--allow-shell-exec`, enforced at three independent layers.
- **Quality gates** — pluggable pre-run checks (signed `gates.json` manifests)
  that must pass before a run starts.

## Architecture

A Go CLI **control plane** is the single source of authority. It generates a
Python/[LangGraph](https://langchain-ai.github.io/langgraph/) **runtime** for each
run and talks to it over an authenticated Unix-domain-socket IPC protocol. The
trust boundary is the Go side: the agent runtime is treated as potentially
compromised, so every security decision — approval, path sandboxing, secret
delivery, audit logging — is enforced in Go, never delegated to Python.

State (runs, cases, topologies, encrypted secrets, the audit chain) lives in a
local SQLite workspace, separate from the repositories being changed — so agent
exhaust never pollutes your product repos.

Full design: [`docs/architecture.md`](docs/architecture.md). IPC wire protocol:
[`proto/ipc.v1.schema.json`](proto/ipc.v1.schema.json).

## Install

Pre-built, signed binaries are published per release (Linux/macOS/Windows; SBOM +
cosign signature over the checksums — see [QUICKSTART](QUICKSTART.md#verifying-release-artifacts)).
Or build from source:

```bash
CGO_ENABLED=0 go build -o staircase ./src/cmd/staircase/
```

A single static binary, no CGo, pure-Go SQLite — no shared libraries required.
The full walkthrough (workspace setup, a real run, HITL, evidence export) is in
**[QUICKSTART.md](QUICKSTART.md)**.

## Security

stAirCase is a security tool; its threat model and guarantees are documented in
**[SECURITY.md](SECURITY.md)**, including how to report a vulnerability. In short:
the Go control plane is the trust boundary, approvals are content-bound, the audit
chain is signed and externally anchorable, and shell execution is off by default.

## Status & roadmap

Pre-1.0 and under active development. The control plane, HITL flow, audit chain,
and offline demo are solid and well-tested (Go race-tested, IPC fuzzed, adversarial
E2E). On the roadmap toward 1.0:

- **DSSE audit envelopes** — adopt the Sigstore/in-toto envelope format so the
  audit chain interoperates with the broader supply-chain ecosystem.
- **Per-agent identity** — distinct identity per agent persona with per-tool
  credential scoping.
- **OS-level sandbox** for `--allow-shell-exec` runs (namespaces/seccomp).

## Contributing

Contributions welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for build/test
conventions (`make test-ci`, `make race`, `make demo`) and the project norms.

## License

[MIT](LICENSE) © 2026 Botond Biro
