<h1 align="center">stAirCase</h1>

<p align="center">
  <strong>AI agents propose. You approve. Only what you approved is committed - and you can prove it.</strong>
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

AI coding agents are fast, but it is hard to know exactly what they changed, who
agreed to it, and whether what was committed is what was reviewed.

**stAirCase** sits between AI agents and your repository:

- the agents work in a **separate copy** of your repository, never in your checkout;
- every change they want to make is a **proposal** that waits for a decision - by
  you, or by a rule you wrote;
- at the end, stAirCase commits **exactly the approved bytes** on a new branch, and
  nothing else;
- every decision is recorded on a **tamper-evident audit chain**, and the commit
  carries a signed **change certificate** that anyone can verify.

It is one program with no dependencies except git. Your agent setup, plans and
evidence live in a workspace outside your repositories.

## Put `staircase` in front of your agent

In any git repository, with nothing to set up:

```bash
staircase claude "add a /health endpoint that returns 200"   # or: staircase codex "..."
```

Every change the agent wants to make comes to you first. At the end, exactly what
you approved is on a new branch, with a certificate you can check:

```bash
staircase verify staircase/run-1
```

```
✅ Commit 8a4ae8705334: valid change certificate, CAL 3
   run #1 from 1ec6a66040cc, assisted by Claude Code
```

A pull request can be required to carry such certificates with the
[stAirCase GitHub Action](docs/audit.md#require-certificates-on-pull-requests).

## See it work in one minute

No API key and no network needed - the demo uses a stand-in model:

```bash
git clone https://github.com/b070nd/stAirCase.git
cd stAirCase
./demo/run-demo.sh
```

An agent proposes to create, edit and delete a file, and waits for you each time.
At the end the demo proves that the new branch holds exactly those changes and that
your checkout did not change. Two more modes show the safety checks at work:

```bash
./demo/run-demo.sh --tamper   # a command changes an approved file → you decide, it is put back
./demo/run-demo.sh --drift    # the agent works outside its task → the run is stopped
```

## Install

```bash
brew install b070nd/staircase/staircase
```

Or `go install github.com/b070nd/stAirCase/src/cmd/staircase@latest`, or download a
signed archive for Linux, macOS or Windows (experimental). See [Install](docs/install.md),
including how to verify a download.

Then follow **[Getting started](QUICKSTART.md)**: your first real run in about 15
minutes.

## What you get

| | |
|---|---|
| **Approval bound to bytes** | stAirCase computes itself what each approved change produces, and commits only that. Anything else fails the run. |
| **Your checkout untouched** | every run works in its own git worktree, on its own `staircase/run-N` branch. |
| **Evidence you can hand over** | a hash-chained audit log, Ed25519-signed exports, optional anchoring in the public Rekor log. |
| **Approve your way** | in the terminal, from a script (local HTTP API), from a service (signed webhook), with rules, or with a reviewer model. |
| **Runs stay on task** | give each story the paths it may change; anything else comes to you, and a run that keeps wandering is stopped. |
| **Setup as code** | keep agents, prompts, cases and limits as a blueprint in its own repository; runs are pinned to its exact content. |
| **Any major model** | Anthropic, OpenAI, Google, xAI, or any model through an OpenAI-compatible gateway - mixed in one team. Budget caps per project. |
| **Shell off by default** | agents can only ask to run commands when you allow it, and a person approves each one. |
| **Claude Code and Codex, governed** | `staircase claude "task"` or `staircase codex "task"`: the agents you already use, with every change going through the same approvals (experimental). |

## Know the limits

stAirCase controls **what reaches your repository**. It is **not a sandbox** for the
agents themselves: they run as your user. Approved shell commands of built-in agents
run in an OS sandbox where the machine has one. Read the
[safety boundary](docs/safety.md) before you use it on a project you care about.
stAirCase is pre-1.0.

## Documentation

Everything is in **[docs/](docs/README.md)** - start with [Concepts](docs/concepts.md)
(five minutes) and [Getting started](QUICKSTART.md).

## Security

How stAirCase protects your code, its known limits, and how to report a
vulnerability privately: [SECURITY.md](SECURITY.md).

## Roadmap

Where stAirCase is going: every change an AI agent makes carries a signed, verifiable
**change certificate**. Next up: `staircase claude "task"` with no setup, more agents
(Codex, Gemini CLI, OpenCode, Cursor), and a CI check that requires certificates. The
full plan and its reasoning are in **[ROADMAP.md](ROADMAP.md)**.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md); `make check` is
the test every change must pass.

## License

[MIT](LICENSE) © 2026 Botond Biro
