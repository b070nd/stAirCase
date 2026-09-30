# stAirCase and security standards

How stAirCase's evidence and controls relate to three standards teams are asked about
when AI agents write their code. This is a mapping to help you argue your own case,
**not a claim of compliance**: every standard below also requires things stAirCase
does not do, and your organisation decides what counts.

## SLSA v1.2, Source track

The [Source track](https://slsa.dev/spec/v1.2/source-requirements) rates how a
revision was produced. Its levels are enforced by the source control system (SCS),
such as GitHub. stAirCase is not an SCS: it runs before a change reaches one, and its
[change certificate](spec/certificate-v1.md) is evidence the SCS or CI can require.

| SLSA Source level | What stAirCase adds for agent-written commits |
|---|---|
| L1 Version controlled | Nothing new: runs work in git and commit on a branch. |
| L2 History & Provenance | A signed certificate per agent-assisted commit, about exactly that commit: who and what proposed and decided each change. It complements the SCS's own provenance; it is not a SLSA Source Provenance Attestation. |
| L3 Continuous technical controls | The [CI check](audit.md#require-certificates-on-pull-requests) is a technical control you can require on protected branches: agent commits need a valid certificate, a minimum CAL and passing checks. |
| L4 Two-party review | CAL 4: a trusted reviewer whose key you list, and who is not the recorded requester, signs the certificate (`staircase sign`); the requester is an unauthenticated git email, so this is not proven two-person control unless the run's initiator signed the request and `--require-initiator` is used (two different trusted keys). SLSA L4 asks the SCS to enforce two-party review of *every* change; CAL 4 covers the agent's change and is enforced by your CI check. |

A Source Verification Summary Attestation (VSA) is not issued yet; `staircase verify`
makes the same kind of decision, and emitting a VSA is on the roadmap.

## OWASP Top 10 for LLM Applications (2025)

The [list](https://genai.owasp.org/llm-top-10/) is written for applications built on
language models. For a coding agent, "the application" is the agent working on your
repository.

| Risk | What stAirCase does | What it does not do |
|---|---|---|
| LLM01 Prompt Injection | Limits what an injected agent can do: every change is decided before it reaches the branch, validators never see the agent's reasoning and treat the change as quoted data, guards flag hidden Unicode, a signal model can flag text addressed to a reviewer. | Prevent the model from being injected ([safety boundary](safety.md)). |
| LLM02 Sensitive Information Disclosure | Keys are stored encrypted and removed from every log; commands run in a sandbox that cannot read the workspace or credential folders; the secret guard flags keys written into code; certificates and Rekor entries hold digests only. | Stop an agent from reading ordinary repository files it was given. |
| LLM03 Supply Chain | Changes to dependency manifests and lock files always go to a person; stAirCase's own releases are signed, with checksums and build provenance. | Vet the dependencies themselves. |
| LLM04 Data and Model Poisoning | Lessons from rejections come only from a person's rejections with reasons. | Anything about training data; stAirCase trains nothing. |
| LLM05 Improper Output Handling | The model's output never runs or lands unchecked: stAirCase derives the exact bytes from each proposal, commits only decided bytes, and runs approved commands in a sandbox. | |
| LLM06 Excessive Agency | The core of stAirCase: agents act only through proposals, shell commands are off by default, the task and its scope are agreed first, drift outside the scope goes to a person, commands are sandboxed. | |
| LLM07 System Prompt Leakage | Keys are never put in prompts. | Keep the agent's instructions secret. |
| LLM08 Vector and Embedding Weaknesses | Not applicable: stAirCase uses no retrieval store. | |
| LLM09 Misinformation | Checks run on the exact commit, reviewer panels must agree, a person approves the result. | Judge whether code is correct; that is your review. |
| LLM10 Unbounded Consumption | A budget cap, a run time limit, a limit on files changed, and time limits for commands and checks. | |

## NIST Secure Software Development Framework

[SP 800-218A](https://csrc.nist.gov/pubs/sp/800/218/a/final) (July 2024) is the SSDF
community profile for generative AI. It is written for those who **produce** AI
models and AI systems. When you **use** AI agents to write ordinary software, the
base SSDF ([SP 800-218](https://csrc.nist.gov/pubs/sp/800/218/final)) practices are
the ones that apply to that code:

| SSDF practice | Evidence stAirCase gives |
|---|---|
| PO.3 Implement Supporting Toolchains | The governed session itself: one tool that records who and what decided each change. |
| PO.5 Implement and Maintain Secure Environments for Software Development | Isolated worktrees, sandboxed commands, credentials hidden from commands and models. |
| PS.1 Protect All Forms of Code from Unauthorized Access and Tampering | Only decided bytes are committed; the audit chain is hash-linked and signed; the certificate is tied to the exact commit. (The SSDF's own examples include commit signing and having changes reviewed and approved by someone else.) |
| PS.2 Provide a Mechanism for Verifying Software Release Integrity | `staircase verify` and the CI check, for agent-written commits (release signing stays your build's job). |
| PW.7 Review and/or Analyze Human-Readable Code to Identify Vulnerabilities and Verify Compliance with Security Requirements | Every change is decided before it lands; guards, reviewer models and a person's final review; CAL 4 for a second person. |
| PW.8 Test Executable Code to Identify Vulnerabilities and Verify Compliance with Security Requirements | `--check` runs your tests on the exact commit, in a sandbox, and records the result. |

## Where to go from here

- The certificate format: [specification](spec/certificate-v1.md) and
  [ADR 0002](adr/0002-change-certificate.md).
- The assurance levels: [ADR 0001](adr/0001-core-promise-and-assurance-levels.md).
- What stAirCase does not protect against: [safety boundary](safety.md).
