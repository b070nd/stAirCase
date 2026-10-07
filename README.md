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
  <img src="https://img.shields.io/badge/status-1.0-green.svg" alt="1.0">
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
  carries a signed **change certificate** that anyone can verify, and **rebuild**: from
  the base commit and the approved proposals, anyone can reproduce the commit's exact
  tree.

It is one program with no dependencies except git (and `ssh-keygen`, if you sign
decisions or reviews with SSH keys). Your agent setup, plans and evidence live in a
workspace outside your repositories.

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
✅ Commit 33279b77f9ba: valid change certificate, CAL 3
   requested by dev@example.com (the git email of the checkout: unauthenticated)
   run #1 from 188a7dda520e, assisted by Claude Code
   1 decision(s) by operator
```

Add `--rebuild` and it also replays the approved proposals and checks that the commit
holds exactly what they produce. A pull request can be required to carry such
certificates (and that rebuild) with the
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
| **Evidence you can hand over** | a hash-chained audit log, Ed25519-signed exports, a signed certificate per commit, a [SLSA verification summary](docs/spec/vsa-v1.md), optional anchoring in the public Rekor log. |
| **Reproducible, not just signed** | `staircase rebuild` (or `verify --rebuild`) reproduces a commit's tree from its approved proposals; the [specification](docs/spec/certificate-v1.md) has test vectors and an independent implementation. |
| **Approve your way** | in the terminal, in a browser page, from a script (local HTTP API), from a service (signed webhook), with rules, or with a reviewer model; sign your decisions with an SSH key. |
| **Approve the task, on evidence** | agree a task and its scope once, and let your checks and reviewer models decide each change inside it instead of a sample; you approve the whole change at the end. |
| **Survives a crash** | each approval is kept before the agent hears it; `staircase resume` carries on with an interrupted run (Claude Code, Gemini CLI and Codex pick up their own session); `staircase recover` commits exactly what it had approved. |
| **For teams** | rules, trusted keys and blueprints from one governance repository, pinned in every workspace; a CI check that requires certificates, from the protected branch. |
| **Runs stay on task** | give each story the paths it may change; anything else comes to you, and a run that keeps wandering is stopped. |
| **Setup as code** | keep agents, prompts, cases and limits as a blueprint in its own repository; runs are pinned to its exact content. |
| **Any major model** | Anthropic, OpenAI, Google, xAI, or any model through an OpenAI-compatible gateway - mixed in one team. Budget caps per project. |
| **Shell off by default** | agents can only ask to run commands when you allow it, and a person approves each one. |
| **Claude Code and Codex, governed** | `staircase claude "task"` or `staircase codex "task"`: the agents you already use, with every change going through the same approvals (experimental); Gemini CLI and OpenCode are there too (`staircase gemini`, `staircase opencode`; both have run against a real login once or twice, OpenCode is still experimental), and any agent that edits your checkout (Cursor) is covered by `staircase seal`. |

## Know the limits

stAirCase controls **what reaches your repository**, and confines what agents do
on your machine:

| | Where it runs |
|---|---|
| Edits and reads | only through stAirCase's tools, only inside the run's own worktree |
| Shell commands and checks | an OS sandbox (macOS `sandbox-exec`, Linux bubblewrap or Landlock, Claude Code's own sandbox): no network, writes only in the worktree, no access to your credentials or stAirCase's keys; `--sandbox required` refuses to run without one |
| The agent program itself | as your user (Claude Code, Codex, or stAirCase's runtime); for stronger isolation, run stAirCase in a container or VM |
| Agents that edit your checkout (Cursor) | outside stAirCase until you `seal` their staged changes (CAL 2) |
| What the agent reads, and what the model provider receives | not confined: a run sends code to your model provider, and a sandboxed command can read files you can; pick a [confidentiality profile](docs/safety.md#confidentiality-pick-a-profile) |

Read the [safety boundary](docs/safety.md) before you use it on a project you care
about. stAirCase is at 1.0: its six interfaces follow semantic versioning ([compatibility](docs/compatibility.md)); the agent
adapters are not part of that promise and Gemini CLI and OpenCode are still experimental.

## Documentation

Everything is in **[docs/](docs/README.md)** - start with [Concepts](docs/concepts.md)
(five minutes) and [Getting started](QUICKSTART.md).

## Security

How stAirCase protects your code, its known limits, and how to report a
vulnerability privately: [SECURITY.md](SECURITY.md).

## Roadmap

Where stAirCase is going: every change an AI agent makes carries a signed, verifiable
**change certificate**, and the repository can require it. Built so far: zero-setup
sessions for Claude Code, Codex and Gemini CLI (OpenCode experimental), the certificate and its
specification, rebuild from a ledger, signed decisions, approval on evidence, recovery,
team governance and the CI check. Next is what only users can show: real pilots on
sensitive repositories, and more real runs of every agent. The full
plan, what has been verified against real agents and what has not, and its reasoning are in
**[ROADMAP.md](ROADMAP.md)** and the [compatibility table](docs/compatibility.md#which-agents-have-actually-been-run).

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md); `make check` is
the test every change must pass.

## License

[MIT](LICENSE) © 2026 Botond Biro
