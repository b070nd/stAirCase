# Change certificate v1: specification

This document specifies the change certificate that stAirCase attaches to every
commit a run makes, precisely enough for another tool to produce or verify one.
The design and its reasons are in [ADR 0002](../adr/0002-change-certificate.md); the
assurance levels are in [ADR 0001](../adr/0001-core-promise-and-assurance-levels.md).

The key words MUST, MUST NOT, SHOULD and MAY are used as in RFC 2119.

## 1. Envelope

A certificate is a [DSSE](https://github.com/secure-systems-lab/dsse) envelope,
encoded as JSON:

| Field | Value |
|---|---|
| `payloadType` | exactly `application/vnd.in-toto+json` |
| `payload` | the statement (section 2), UTF-8 JSON, encoded in standard base64 with padding |
| `signatures` | an array of `{"keyid": string, "sig": string}` |

**What is signed.** Every signature is over the DSSE pre-authentication encoding
(PAE) of the payload type and the *decoded* payload bytes:

```
"DSSEv1" SP LEN(payloadType) SP payloadType SP LEN(payload) SP payload
```

where `SP` is one space (0x20) and `LEN` is the byte length as ASCII decimal without
leading zeros.

## 2. Statement

The payload is an [in-toto Statement v1](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md):

| Field | Value |
|---|---|
| `_type` | exactly `https://in-toto.io/Statement/v1` |
| `subject` | exactly one element: `{"name": "commit", "digest": {"gitCommit": "<commit id>"}}` |
| `predicateType` | exactly `https://github.com/b070nd/stAirCase/blob/master/docs/adr/0002-change-certificate.md#v1` |
| `predicate` | the predicate (section 3) |

The commit id is the full hexadecimal git object id, in lower case. The subject name
is always `commit`, so the certificate does not reveal the repository's name.

## 3. Predicate

| Field | JSON type | Required | Meaning |
|---|---|---|---|
| `run` | integer | yes | the run's number in the producer's workspace |
| `baseCommit` | string | yes | the commit the run started from |
| `planDigest` | string | no | hex SHA-256 of the compiled plan (task, stories and scope, agents) |
| `blueprint` | string | no | the blueprint the plan was bound from |
| `agents` | array of strings | yes | the agent harness (for example `Claude Code`) or the models of the built-in agents |
| `decisions` | object: string → integer | yes | how many proposals each source decided: `operator`, `policy`, `task`, `validator:<model>[+<model>…]`, `orchestrator`, `drift` |
| `chainHead` | string | yes | hex SHA-256 of the producer's audit chain when the commit was made; the commit's `Staircase-Chain:` trailer carries the same value |
| `cal` | integer 1 to 3 | yes | the change assurance level the run reached (level 4 is only ever established by a verifier, section 5) |
| `notes` | array of strings | no | why the level is not higher |
| `requestedBy` | string | no | the git identity (e-mail) the run was made under |
| `ledger` | string | no | hex SHA-256 of the run's ledger file (section 7): the base commit and every approved proposal, in order. With it the commit's tree can be rebuilt |
| `policy` | string | no | hex SHA-256 of the `policy.json` in effect for the run |
| `attention` | object | no | how people decided, when any did: `{"humanDecisions": integer, "medianSeconds": number, "quickApprovals": integer}`; a quick approval is a change of 20 or more lines approved in under 5 seconds. Informative: it does not change the level |
| `checks` | array | no | commands run on the commit after it was made, each `{"command": string, "exitCode": integer, "sandboxed": boolean, "outputSha256": string}`; `exitCode` -1 means it could not run |

Producers MUST NOT put code, prompts or command output in the predicate: it holds
digests and names only. Verifiers SHOULD ignore fields they do not know.

## 4. Signatures

**The producer's signature.** Ed25519 over the PAE. `keyid` is the hex SHA-256 of
the raw 32-byte public key; `sig` is the standard base64 of the 64-byte signature.

**A person's signature** (two-person review, CAL 4). `keyid` is `sshsig:` followed by
the signer's principal (their e-mail); `sig` is the standard base64 of an armored SSH
signature over the PAE in the namespace `staircase-certificate`, as made by
`ssh-keygen -Y sign -n staircase-certificate` (the format git uses for SSH-signed
commits).

A verifier MUST NOT rely on `keyid` to find the trusted signature: it tries the
signatures until one verifies.

## 5. Verification

Given an envelope, a trusted Ed25519 public key, the commit being checked, a required
minimum level (0 for none) and, optionally, an `allowed_signers` file of trusted
people, a verifier MUST:

1. Refuse the envelope unless `payloadType` is exactly `application/vnd.in-toto+json`.
2. Decode `payload`, and refuse it unless at least one signature verifies with the
   trusted key over the PAE (section 1).
3. Parse the statement, and refuse it unless `_type` and `predicateType` are exactly
   as in section 2 and the subject names a commit.
4. Refuse it unless the subject's commit is exactly the commit being checked.
5. Take the level as `cal`. If an `allowed_signers` file is given and `cal` is at
   least 3, the level is 4 when at least one person's signature (section 4) verifies
   against that file for its principal and namespace, and that principal is not
   `requestedBy`.
6. Refuse it when the level is below the required minimum.
7. Refuse it when any `checks` entry has an `exitCode` other than 0.

Otherwise the commit is accepted at that level.

**Ranges.** When checking a range of commits (for example a pull request), every
commit that declares an agent in an `Assisted-by:` trailer MUST carry a certificate
that is accepted. A verifier MAY require a certificate on every commit.

## 6. Carriers

- **Git note.** The envelope JSON is the note on the commit under
  `refs/notes/staircase`. Notes are not pushed or fetched by default; share them with
  `git push origin refs/notes/staircase` and
  `git fetch origin refs/notes/staircase:refs/notes/staircase`.
- **Commit trailers.** The commit carries `Assisted-by: <agent>` for each agent and
  `Staircase-Chain: sha256:<chainHead>`.
- **Transparency log.** The envelope MAY be anchored in a Rekor log. Only the
  envelope's digest is logged.

## 7. Rebuilding a commit

A certificate with a `ledger` lets anyone who has the ledger file check that the
commit holds exactly what was approved. The ledger is JSON (kept by the author; it
holds the approved content):

```
{"version": 1, "base": "<commit id>",
 "proposals": [{"seq": 1, "source": "operator",
                "edits": [{"file": "...", "search_block": "...", "replace_block": "..."}]}]}
```

**Checking.** A verifier MUST: (1) verify the certificate as in section 5; (2) refuse
unless the SHA-256 of the ledger file's bytes equals `predicate.ledger`; (3) refuse
unless `base` equals the certificate's `baseCommit` and the commit's parent; (4)
replay the proposals as below on the tree of `base`; (5) accept only if the resulting
git tree id equals the commit's tree id.

**Replaying.** Keep a state of files, path to (bytes, mode), starting from the base
tree (regular files only). For each proposal in order, apply its edits in order to a
copy that sees the earlier edits of the same proposal; if any edit is refused, the
ledger is refused. Then merge the copy into the state.

For an edit, clean `file` first: refuse if it is empty or starts with `/`; take its
normalized form (as `posixpath.normpath`); refuse if that is `.`, `..` or starts with
`../`; refuse if any component equals `.git` ignoring case. Then, by `search_block`:

- `(new file)`: the file becomes the UTF-8 bytes of `replace_block` (at most 204800
  bytes), keeping the mode of the file it overwrites, else `100644`.
- `(delete file)`: the file must exist, and is removed.
- otherwise: the file must exist. Read both blocks with `\r\n` as `\n`. If the file
  has no carriage return, use it and the blocks as they are. If every line end in
  the file is `\r\n` (and there is at least one), turn `\n` in both blocks into
  `\r\n`. Otherwise (mixed line ends) read the file with `\r\n` and `\r` as `\n`,
  and save it with `\n` throughout. Refuse if the search text is not in the file.
  Replace its first occurrence.

The tree is built from the state: an entry is mode `100755` if the file is
executable and `100644` otherwise, blobs and trees are hashed as git does (SHA-1
object ids), directories that end up empty do not exist.

## 8. Conformance vectors

[`vectors/`](vectors) holds test cases, one JSON file each:

| Field | Meaning |
|---|---|
| `description` | what the case shows |
| `publicKey` | the trusted key: standard base64 of the raw 32-byte Ed25519 public key |
| `commit` | the commit being checked |
| `minCal` | the required minimum level |
| `envelope` | the certificate |
| `valid` | whether it is accepted |
| `cal` | for an accepted case, the level |
| `error` | for a refused case, a phrase stAirCase's reason contains (informative) |

An implementation conforms when it decides every vector as `valid` says, at level
`cal`. [`rebuild-vectors/`](rebuild-vectors) does the same for section 7: each file
has a base tree, approved proposals and either the git tree they produce or a phrase
a refusal contains; [`rebuild_vectors.py`](rebuild_vectors.py) is an independent
implementation (standard library only) that reproduces all of them. The vectors do not cover person signatures (step 5), which need `ssh-keygen`.
stAirCase checks itself against these files in its tests, and
[`verify_vectors.py`](verify_vectors.py) is an independent verifier written from this
document alone that decides them the same way.

## 9. Versions

This is version 1. A change to the meaning of a field, or a new required field, gets
a new predicate type ending in `#v2`, and verifiers keep accepting v1.
