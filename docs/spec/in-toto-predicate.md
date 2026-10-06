# Draft: the change certificate as an in-toto predicate

A draft for proposing stAirCase's change certificate to the
[in-toto attestation framework](https://github.com/in-toto/attestation) as a
predicate type, following the framework's predicate template. The normative text
is the [specification](certificate-v1.md); this draft summarises it for the
framework's maintainers.

## Type URI

`https://github.com/b070nd/stAirCase/blob/master/docs/adr/0002-change-certificate.md#v1`

## Version

1

## Purpose

To state, for one git commit, how an AI coding agent's changes in it were proposed
and decided: which agents and models proposed them, who or what approved each
change, which checks ran on the result, and the assurance level the process
reached. It lets a repository, a CI system or an auditor require that commits
written with AI agents were governed, without seeing the code, prompts or reasoning.

## Use cases

- A maintainer's CI refuses a pull request whose agent-assisted commits lack a
  certificate at a required level.
- An auditor checks that changes to sensitive paths had two-party review (a second
  person's signature on the certificate).
- A team records that its tests passed on the exact commit an agent produced.

## Prerequisites

The in-toto Attestation Framework v1 and DSSE. For person signatures, the SSH
signature format (`ssh-keygen -Y sign`).

## Model

The **producer** is a governance tool that sits between an agent and a repository.
It decides every change before it reaches the branch (by a person, a rule or a
reviewer model), builds the commit from the decided bytes, and issues the
certificate. The **consumer** is a verifier in CI or a person, holding the
producer's public key. The subject is the commit; the certificate is carried as a
git note and may be anchored in a transparency log.

## Schema

The predicate is a JSON object. All fields are names, numbers and digests; the
predicate never contains code, prompts or command output. The normative text, with
meanings and the verification procedure, is
[section 3 of the specification](certificate-v1.md#3-predicate); the fields are:

| Field | Type | Required |
|---|---|---|
| `run` | integer | yes |
| `baseCommit` | string (commit id) | yes |
| `agents` | array of strings | yes |
| `decisions` | object, string to integer (how many proposals each source decided) | yes |
| `chainHead` | string (SHA-256 of the producer's audit chain) | yes |
| `cal` | integer 1 to 3 (the change assurance level; level 4 is established only by a verifier) | yes |
| `planDigest`, `blueprint` | string (what the agent was asked to do, and the blueprint it came from) | no |
| `notes` | array of strings (why the level is not higher) | no |
| `requestedBy` | string (the git identity the run was made under) | no |
| `initiator` | object `{principal, signature}` (the person who started the run, authenticated by an SSH signature) | no |
| `ledger` | string (SHA-256 of the ledger the commit's tree can be rebuilt from) | no |
| `policy` | string (SHA-256 of the policy the run decided under) | no |
| `signed` | object `{decisions, signers}` (decisions people signed; informative) | no |
| `attention` | object `{humanDecisions, medianSeconds, quickApprovals}` (how people decided; informative) | no |
| `checks` | array of `{command, exitCode, sandboxed, outputSha256}` (commands run on the commit) | no |

## Parsing rules

- Verifiers MUST accept the certificate only for the commit named in the subject.
- Verifiers MUST refuse a certificate that records a failed check.
- Verifiers SHOULD ignore unknown predicate fields.
- A change of meaning gets a new type URI ending in `#v2`.

## Example

Any accepted file in [`vectors/`](vectors), for example
[`01-valid-cal3.json`](vectors/01-valid-cal3.json), holds a complete envelope; its
decoded payload is the statement.

## Changelog and migrations

- v1 (2026): initial version. Fields added since, all optional: `requestedBy`, `ledger`, `policy`, `initiator`, `signed`,
  `attention`, `checks`, `notes`, `blueprint`. The type URI did not change: verifiers ignore unknown fields.
