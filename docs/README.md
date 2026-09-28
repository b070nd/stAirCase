# stAirCase documentation

New here? Read [Concepts](concepts.md), then [Getting started](../QUICKSTART.md).

## Start here

| Page | What you learn |
|---|---|
| [Concepts](concepts.md) | the idea, how a run works, and the words the docs use |
| [Install](install.md) | install, verify a download, shell completion |
| [Getting started](../QUICKSTART.md) | the offline demo, then your first real run |
| [Safety boundary](safety.md) | what stAirCase guarantees, and what it does not |

## How to

| Page | What you learn |
|---|---|
| [Approvals](approvals.md) | approve in the terminal, from a script, from a service; rules; a reviewer model |
| [Models and API keys](models.md) | providers, storing keys, budget caps, record and replay |
| [Blueprints](blueprints.md) | keep your agent setup as files in its own repository |
| [Drift supervision](drift.md) | keep runs on their stories with scope and limits |
| [Gates](gates.md) | the checks before a run, and how to add your own |
| [Audit evidence](audit.md) | inspect runs, change certificates, [require them in CI](audit.md#require-certificates-on-pull-requests), let others verify evidence |
| [Governing Claude Code](claude-code.md) | use Claude Code as the agent (experimental) |
| [Troubleshooting](troubleshooting.md) | common messages and what to do |

## Reference

| Page | What you find |
|---|---|
| [CLI reference](cli.md) | every command and flag (generated from the program) |
| [Security](../SECURITY.md) | the security model, known limits, reporting a vulnerability |
| [Changelog](../CHANGELOG.md) | what changed in each release |
| [Roadmap](../ROADMAP.md) | where stAirCase is going, and why |

## For contributors

| Page | What you find |
|---|---|
| [Architecture](architecture.md) | how the program is built inside |
| [Architecture decisions](adr/) | the decisions that shape it: [the core promise and assurance levels](adr/0001-core-promise-and-assurance-levels.md), [the change certificate](adr/0002-change-certificate.md), [the hook bridge](adr/0003-hook-bridge.md) |
| [Testing](testing.md) | the test layers and `make check` |
| [Contributing](../CONTRIBUTING.md) | how to propose a change, and how to write docs |
| [Demo](../demo/README.md) | what the offline demo checks, step by step |
| [Compliance checklist](compliance-checklist.md) | a historical audit record from the earlier runtime (archive) |
