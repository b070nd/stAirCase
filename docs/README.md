# stAirCase documentation

New here? Read [Concepts](concepts.md), then [Getting started](../QUICKSTART.md).

## Start here

| Page | What you learn |
|---|---|
| [Concepts](concepts.md) | the idea, how a run works, and the words the docs use |
| [Install](install.md) | install, verify a download, shell completion |
| [Getting started](../QUICKSTART.md) | the offline demo, then your first real run |
| [Safety boundary](safety.md) | what stAirCase guarantees and what it does not, the confidentiality profiles, and recovering an interrupted run |

## How to

| Page | What you learn |
|---|---|
| [Approvals](approvals.md) | approve in the terminal, a browser page, a script or a service; rules; a reviewer model; approve on evidence; sign your decisions |
| [Models and API keys](models.md) | providers, storing keys, budget caps, record and replay |
| [Blueprints](blueprints.md) | keep your agent setup as files in its own repository |
| [Drift supervision](drift.md) | keep runs on their stories with scope and limits |
| [Gates](gates.md) | the checks before a run, and how to add your own |
| [Audit evidence](audit.md) | inspect runs, change certificates, [require them in CI](audit.md#require-certificates-on-pull-requests), let others verify evidence |
| [Governing Claude Code](claude-code.md) | use Claude Code as the agent (experimental) |
| [Governing Codex](codex.md) | use OpenAI's Codex as the agent (experimental) |
| [Governing OpenCode](opencode.md) | use OpenCode as the agent (work in progress: not yet run against a real login) |
| [Governing Gemini CLI](gemini.md) | use Google's Gemini CLI as the agent (work in progress: not yet run against a real login) |
| [Agents in your own checkout](attach.md) | Cursor and other tools that edit your files directly: seal what you approve |
| [Reviewing changes made elsewhere](review.md) | certify what you accept from a cloud agent's pull request |
| [Working as a team](governance.md) | one repository for the team's rules, trusted keys and blueprints, pinned in every workspace |
| [Governing every session in a company](managed.md) | managed settings that send every Claude Code or Codex session through stAirCase |
| [Troubleshooting](troubleshooting.md) | common messages and what to do |

## Reference

| Page | What you find |
|---|---|
| [CLI reference](cli.md) | every command and flag (generated from the program) |
| [Verification summary (SLSA VSA)](spec/vsa-v1.md) | the signed summary `verify --vsa-out` writes for a verified commit, its levels (none of them a SLSA level) and what a consumer checks |
| [Change certificate specification](spec/certificate-v1.md) | the certificate format, how to verify it and how to rebuild a commit from its ledger, with [test vectors](spec/vectors) for other implementations |
| [Security standards](standards.md) | how stAirCase relates to SLSA's source track, the OWASP Top 10 for LLM applications and NIST's SSDF |
| [Change certificate in-toto proposal](spec/in-toto-predicate.md) | the draft proposal of the certificate as an in-toto predicate |
| [Compatibility](compatibility.md) | the six interfaces other tools build on, what you can rely on, and which agents have actually been run |
| [Security](../SECURITY.md) | the security model, known limits, reporting a vulnerability |
| [Changelog](../CHANGELOG.md) | what changed in each release |
| [Roadmap](../ROADMAP.md) | where stAirCase is going, and why |

## For contributors

| Page | What you find |
|---|---|
| [Architecture](architecture.md) | how the program is built inside |
| [Architecture decisions](adr/) | the decisions that shape it: [the core promise and assurance levels](adr/0001-core-promise-and-assurance-levels.md), [the change certificate](adr/0002-change-certificate.md), [the hook bridge](adr/0003-hook-bridge.md), [the audit chain covers the event type](adr/0004-audit-chain-version-2.md), [continuing an interrupted run (design)](adr/0005-durable-continuation.md) |
| [Testing](testing.md) | the test layers and `make check` |
| [Contributing](../CONTRIBUTING.md) | how to propose a change, and how to write docs |
| [Demo](../demo/README.md) | what the offline demo checks, step by step |
| [Compliance checklist](compliance-checklist.md) | a historical audit record from the earlier runtime (archive) |
