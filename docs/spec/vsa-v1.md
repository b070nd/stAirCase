# stAirCase verification summary (SLSA VSA v1)

`staircase verify <commit | range> --vsa-out <dir>` writes, for every commit that
passes, a [SLSA Verification Summary Attestation](https://slsa.dev/spec/v1.2/verification_summary)
(predicate type `https://slsa.dev/verification_summary/v1`, SLSA v1.2) as a DSSE envelope
in `<dir>/<commit>.vsa.json`. A CI job or a release process can then depend on one small
signed statement ("this commit was verified, at this level, under this policy") instead
of repeating the whole verification. It is written only for a commit that passed; a
failed verification writes nothing.

## What it says, and what it does not

- **It is stAirCase's own verdict, not a SLSA level.** A change assurance level is not a
  SLSA Source level ([standards](../standards.md)), and SLSA reserves `SLSA_*` for its own
  levels, so `verifiedLevels` holds values of ours (below) and, as the explicit no-claim
  SLSA defines, `SLSA_SOURCE_LEVEL_UNEVALUATED`. It never contains
  `SLSA_SOURCE_LEVEL_1` to `3` (those are a source control system's claims) and never
  `SLSA_SOURCE_TWO_PARTY_REVIEWED` (CAL 4 is a different property: a trusted reviewer's
  SSH signature, see [audit](../audit.md#two-person-review-cal-4)).
- **It is only as trustworthy as its signer.** The VSA is signed with the verifier's
  Ed25519 key (`--vsa-key`, else the workspace key). As SLSA says, a consumer accepts
  only specific (signer, `verifier.id`) pairs, and a VSA does not protect against a
  compromised verifier.

## The statement

| Field | Value |
|---|---|
| `subject[0].digest` | `gitCommit` (the verified commit) and `gitTree` (its tree, the one a rebuild reproduces) |
| `predicate.verifier.id` | `https://github.com/b070nd/stAirCase`, or `--verifier-id` (name your CI's verifier) |
| `predicate.verifier.version` | `{"staircase": "<version>"}` |
| `predicate.timeVerified` | RFC 3339, UTC |
| `predicate.resourceUri` | the repository as `git+https://host/org/repo`: `--resource-uri`, else the `origin` remote (a local path or a URL with a password is refused) |
| `predicate.policy` | `uri`: `--policy-uri`, else the [policy](#policy) section below; `digest.sha256`: of the parameters the verification ran with |
| `predicate.inputAttestations` | the change certificate (`name` `refs/notes/staircase`, `digest.sha256` of the note as `git notes show` prints it) and, when `--rebuild` ran, the ledger (`refs/notes/staircase-ledger`, the digest the certificate records) |
| `predicate.verificationResult` | `PASSED` |
| `predicate.verifiedLevels` | see below |
| `predicate.slsaVersion` | `1.2` |
| `predicate["https://github.com/b070nd/stAirCase/vsa-parameters/v1"]` | the parameters themselves (extension field, named by a URI as SLSA requires) |

`dependencyLevels` is absent: stAirCase makes no claim about dependencies.

### `verifiedLevels`

| Value | Meaning |
|---|---|
| `SLSA_SOURCE_LEVEL_UNEVALUATED` | no SLSA source level is claimed |
| `STAIRCASE_CAL_<n>` | the change assurance level the verification reached, 1 to 4 (ADR 0001) |
| `STAIRCASE_REBUILT` | the commit's tree was reproduced from its ledger (`--rebuild`) |
| `STAIRCASE_CHECKS_PASSED` | the certificate records checks, and all passed |
| `STAIRCASE_INITIATOR_AUTHENTICATED` | the run's initiator signed the request with a key the trusted signers list |

## Policy

The policy digest is the SHA-256 of this JSON, with the fields in this order and no
white space, made from what the verification ran with:

```
{"minCal":3,"all":true,"rebuild":true,"requireInitiator":false,"keys":["<id>",…],"signers":"<sha256 or empty>"}
```

`keys` are the IDs (hex SHA-256) of the trusted producer keys, sorted; `signers` is the
SHA-256 of the `allowed_signers` file used, or `""`. Two VSAs with the same digest were
made under the same rules; a consumer that wants a particular policy compares the digest.

## What a consumer checks

1. The envelope's signature, with a key it has configured for this verifier.
2. `predicateType` is the VSA type; `verificationResult` is `PASSED`.
3. `subject[0].digest.gitCommit` is the commit in question; `resourceUri` is the repository it expects.
4. `verifier.id` is the one paired with that key.
5. `verifiedLevels` contains what it requires (for example `STAIRCASE_CAL_3` and `STAIRCASE_REBUILT`),
   and `policy.digest.sha256` is the policy it expects.

Not covered: emitting a VSA from the GitHub Action (it would need a signing key in CI),
`FAILED` summaries, and `subject.uri` / `annotations.sourceRefs`.
