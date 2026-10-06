# Proposal text for the in-toto attestation framework (prepared, not sent)

This is the text to send, and where. It has **not** been sent: submitting it is an outward-facing act that the
maintainer decides, and acceptance by the framework is not promised or assumed.

**Where:** the in-toto attestation repository, as a discussion or an issue first (the framework's predicate
contribution guide asks for a proposal and a use case before a pull request that adds a predicate document),
and then a pull request adding `spec/predicates/` text if the maintainers want one.

**Title:** Proposal: a predicate for the governance of AI-assisted source changes (stAirCase change certificate v1)

**Body:**

> stAirCase is an open-source control plane that sits between an AI coding agent and a Git repository: every change
> the agent proposes is decided by a person, a rule or a reviewer model, and the commit is built from the decided
> bytes. It issues a signed attestation per commit saying how the change was proposed and decided. We would like to
> propose that attestation as a predicate type.
>
> **Use cases:** a CI check that refuses a pull request whose agent-assisted commits lack the attestation at a
> required level; an auditor checking that sensitive paths had a second person's signature; a team recording that its
> tests passed on the exact commit an agent produced. It states process, not correctness: it does not claim the code is
> right or safe.
>
> **What it is:** an in-toto Statement v1 in a DSSE envelope, subject a Git commit, predicate type
> `https://github.com/b070nd/stAirCase/blob/master/docs/adr/0002-change-certificate.md#v1`. The predicate holds
> names, counts and digests only (never code, prompts or command output): the agents and models involved, how many
> proposals each kind of decider made, the audit chain's head, the assurance level reached, the checks run on the
> commit, and, optionally, an SSH-signed initiator, the ledger digest (from which the commit's tree is rebuilt) and the
> policy digest.
>
> **What exists:** a written specification with a normative verification procedure
> (`docs/spec/certificate-v1.md`), 13 test vectors and a second implementation written from the specification alone
> (Python, standard library plus `cryptography`), 25 rebuild vectors, a verifier (`staircase verify`), a GitHub Action,
> and a signed SLSA Verification Summary Attestation writer (`verify --vsa-out`). Every release since v0.3.0 is
> read by today's verifier (a fixture per release in `docs/spec/historical`).
>
> **What we ask:** whether the framework would list this as a predicate, and what changes to the predicate document
> or the type URI the maintainers want first (for example a stable URI outside a GitHub blob link). We have drafted the
> document in the framework's predicate template: `docs/spec/in-toto-predicate.md`.
>
> **Limits we state:** the specification is maintained by one project; the second implementation is by the same
> authors; the producer's key is trusted by configuration, not by an identity system. We would value review on the
> assurance-level definitions.

**Before sending:** check that the type URI resolves (the repository must be public and the path stable), that the
vectors and the historical fixtures are in the tagged release being pointed at, and that the draft's field table still
matches the predicate (`TestContract_the_in_toto_draft_names_every_certificate_field` fails the build when it does not).
