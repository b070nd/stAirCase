# ADR 0002: The change certificate

- Status: accepted; v1 shipped (fields below); the ledger, policy digest, verifiers
  and ranges come in later versions
- Date: 2026-09-28

## Context

Today a run's evidence is its hash-chained event log, exported as a signed checkpoint.
It is complete, but specific to stAirCase, large, and full of content: prompts,
reasoning and code. Anchoring it sends all of that to the Rekor service (F87). The people
who need to trust a change - a CI check, a maintainer, an auditor - need something
small, standard and tied to the commit.

## Decision

**Format.** An [in-toto Statement v1](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md)
in a [DSSE envelope](https://github.com/secure-systems-lab/dsse). It is signed with the
workspace's Ed25519 key, using only the Go standard library. More signatures can be
added to the same envelope later: people's SSH keys, or keyless signing in CI.

**Subject.** The commit: `{"name": "commit", "digest": {"gitCommit": "<id>"}}`. It
carries no repository path, so a private repository's name is not leaked.

**Predicate type.** `https://github.com/b070nd/stAirCase/blob/master/docs/adr/0002-change-certificate.md#v1`.
A change to any field means a new version in this URI.

**Predicate v1** (what a run writes today):

| Field | Content |
|---|---|
| `run` | the run's number in the workspace |
| `baseCommit` | the commit the run started from |
| `planDigest` | the SHA-256 of the compiled plan: the task, the stories and their scope, the agents |
| `blueprint` | the blueprint the case was bound from, if any |
| `agents` | the agent harness (for example `Claude Code`), or the models of the built-in agents |
| `decisions` | how many proposals each source decided (`operator`, `policy`, `validator:<model>`, `orchestrator`) |
| `chainHead` | the hash of the last audit event when the commit was made |
| `cal` | the assurance level reached ([ADR 0001](0001-core-promise-and-assurance-levels.md)) |
| `notes` | why the level is not higher, for example approved shell commands without a sandbox |

**Planned for v2:** `ledger` (the SHA-256 of the ordered, approved proposals and their
derived file digests, so a verifier can rebuild the commit), `policy` (the digest of
the policy in effect) and `verifiers` (references to test-result attestations).

**Rebuilding the commit** (v2). Take the base commit and apply the ledger with the
documented derivation rules. The result must be exactly the commit's tree. The certificate holds
only digests; the ledger holds the content and stays private.

**Carriers.**
- The envelope is stored in the workspace and as a git note under
  `refs/notes/staircase`.
- The commit carries `Assisted-by: <agent>` (for example `Assisted-by: Claude Code`
  or `Assisted-by: stAirCase (claude-sonnet-4-6)`) and
  `Staircase-Chain: sha256:<chain head>` trailers. The certificate names the commit,
  so it cannot live inside the commit itself.

**Public anchoring.** Only the envelope (digests) goes to a transparency log such as
Rekor. The ledger and the events never do. (Correction, checked on
rekor.sigstore.dev: the log never stored records, only their hashes, but
`audit export --anchor` still sends the whole record to the Rekor service.
Anchoring the certificate instead keeps the content on your machine.)

**Verification.** `staircase verify <commit>` checks:
- the signature, against the trusted key;
- that the subject is exactly this commit;
- that the level is at least the one required (`--min-cal`).

Later versions add commit ranges (for a CI check on a pull request) and, with the
ledger, rebuilding the commit.

## Consequences

- No new dependency: the DSSE signing encoding and Ed25519 are a few lines of standard
  library code.
- The predicate is versioned. Changing a field means a new version, never a silent
  change.
- SLSA source provenance or a verification summary can be emitted next to the
  certificate later, for tools that only read those.
- The trailers can be turned off, but they are on by default: disclosure is the point.
