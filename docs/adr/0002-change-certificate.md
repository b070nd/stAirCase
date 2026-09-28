# ADR 0002: The change certificate

- Status: accepted (format fixed when v1 ships)
- Date: 2026-09-28

## Context

Today a run's evidence is its hash-chained event log, exported as a signed checkpoint.
It is complete, but specific to stAirCase, large, and full of content: prompts,
reasoning and code. Anchoring it publicly would publish all of that (F87). The people
who need to trust a change - a CI check, a maintainer, an auditor - need something
small, standard and tied to the commit.

## Decision

**Format.** An [in-toto Statement v1](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md)
in a [DSSE envelope](https://github.com/secure-systems-lab/dsse). It is signed with the
workspace's Ed25519 key, using only the Go standard library. More signatures can be
added to the same envelope later: people's SSH keys, or keyless signing in CI.

**Subject.** The commit: `{"name": "commit", "digest": {"gitCommit": "<id>"}}`. It
carries no repository path, so a private repository's name is not leaked.

**Predicate type.** A versioned URI that resolves to the specification in this
repository. It is fixed when v1 first ships.

**Predicate** (v1 draft):

| Field | Content |
|---|---|
| `baseCommit` | the commit the run started from |
| `intent` | the case, plus digests of the PRD and the stories |
| `policy` | the digest of the policy in effect, or none |
| `agents` | each agent harness with its version, and each model |
| `decisions` | how many proposals each source decided (person, policy, validator, orchestrator) |
| `ledger` | the SHA-256 of the ledger: the ordered, approved proposals and their derived file digests |
| `chainHead` | the hash of the last audit event |
| `cal` | the assurance level reached ([ADR 0001](0001-core-promise-and-assurance-levels.md)) |
| `verifiers` | references to test-result attestations (later) |

**Rebuilding the commit.** Take the base commit and apply the ledger with the documented
derivation rules. The result must be exactly the commit's tree. The certificate holds
only digests; the ledger holds the content and stays private.

**Carriers.**
- The envelope is stored in the workspace and as a git note under
  `refs/notes/staircase`.
- The commit carries `Assisted-by: <agent> (<model>)` and
  `Staircase-Chain: sha256:<chain head>` trailers. The certificate names the commit,
  so it cannot live inside the commit itself.

**Public anchoring.** Only the envelope (digests) goes to a transparency log such as
Rekor. The ledger and the events never do. This fixes F87.

**Verification.** `staircase verify <commit|range>` checks:
- the signatures, against trusted keys;
- that the subject is the commit;
- when the ledger is available, that the commit rebuilds from it;
- that the level is at least the one required.

## Consequences

- No new dependency: the DSSE signing encoding and Ed25519 are a few lines of standard
  library code.
- The predicate is versioned. Changing a field means a new version, never a silent
  change.
- SLSA source provenance or a verification summary can be emitted next to the
  certificate later, for tools that only read those.
- The trailers can be turned off, but they are on by default: disclosure is the point.
