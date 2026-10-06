# Audit evidence

Every run keeps a record of everything that happened: what the agents proposed,
who decided, what was committed, and what went wrong. This page explains the record
and how to hand it to someone else as evidence.

## The audit chain

Each event of a run is stored with a hash of its content **and of the event before
it**. Changing, removing or reordering any event breaks every hash after it, so a
change anywhere is detected.

The first event of every run, `run_bound`, records what the run started from: the
commit, the topology version, the hash of the plan and, for a
[blueprint](blueprints.md) case, the blueprint's hash.

Events you will see:

| Event | Meaning |
|---|---|
| `run_bound` | what the run started from (commit, plan, blueprint) |
| `yield_request` | an agent proposed a change or a command |
| `yield_decided` | the decision: who decided (`policy`, `operator`, `validator:<model>`, …), the reason, and for an approved change the hash of the exact approved content |
| `shell_exec_rejected` | a shell command was refused because the run did not allow shell commands |
| `approval_path_escape` | a proposal tried to reach a path outside the repository |
| `approval_content_mismatch`, `unapproved_worktree_change`, `run_branch_moved` | at the end, the worktree was not exactly what was approved; nothing was committed |
| `drift_halt`, `drift_report` | [drift supervision](drift.md) stopped the run / its summary |
| `state_emit` | model usage of an agent step (tokens, cost) |
| `agent_unresponsive` | an agent did not stop when asked |
| `story_accepted`, `rolled_back` | a person accepted a story / discarded a run |
| `task_agreed` | who agreed to the task before a session started, and the plan's digest |
| `policy_snapshot` | the digest of the `policy.json` the run decided under, and whether its signature was valid (written before the first proposal) |
| `initiator_signed` | the person who started the run signed a request naming the run, base commit and plan: who, and the SSH signature (only when the run was started with a key) |
| `check_ran` | a `--check` command ran on the delivered commit: its command, exit code, whether it was sandboxed and the digest of its output |
| `shell_ran` | an approved shell command ran: the agent, the command and whether it ran in the sandbox |
| `signal_rated` | a decision model was asked about a change that was not decided by a person: the model, the probabilities it gave that the change is risky and that it serves a story, the kind of change and the cost, or the error when it could not answer. It can only send the change to a person |
| `recovery_final_review` | the final review of a recovered run, asked of a person before any commit was made: approved or not, and the digest of what was shown |
| `run_recovered` | `staircase recover` committed what the run had approved: the commit, how many proposals, whether a final review was given |
| `recovery_repaired` | a later `recover` finished the evidence (ledger, certificate, notes) of a commit that was already made, and which parts were still missing |
| `certificate_failed`, `evidence_failed` | the commit was made but its certificate, or other evidence, could not be written, and why (`--require-evidence` makes this a failure) |
| `run_resumed` | the run was continued (`staircase resume`): the segment number, the chain head it resumed from (`generation`), the approvals it stood on, the next proposal number, the policy and plan digests it continues under, how many requests of earlier segments were never decided, the time earlier segments used, whether drift was acknowledged, and how the agent's context was carried (`native_resume`: the vendor session was resumed; `fresh_grounded`: a new session grounded by the chain) |
| `agent_session` | an agent harness's own session, so a later segment can resume it: the harness, the vendor's session id and whether it was `started` or `resumed` (Claude Code and Gemini CLI get an id staircase chose, Codex's is read from its `thread.started` event) |
| `commit_prepared` | the run's commit exists and is named here (commit, base, tree, branch) before the run branch moves to it; if this cannot be recorded nothing is delivered, and a recovery after a crash keeps only a commit named here as the run's own |
| `certificate_issued` | the change certificate was signed for the run's commit |
| `run_path` | the phases the run went through, in order (see below) |

A run moves through fixed phases - checks (`PRE_FLIGHT`), its worktree
(`BRANCH_CREATE`), starting the agent (`AGENT_START`), deciding (`AGENT_LOOP`), the
final checks and commit (`FINALIZE`) and cleanup (`BRANCH_RESTORE`) - and only along
the moves the run's state machine allows. `run_path` records the way a run actually
took, so a run that stopped early shows where.

A decision is written to the chain **before** the agent learns it. Secrets are
removed from every event before it is stored.

### The hash chain

Each event has an `event_hash`, and a `hash_version` that says how it was computed.
Both are in the checkpoints you export and in the `staircase inspect log` output.

- **Version 2** (written by stAirCase from the release after v0.6.0): the SHA-256, in
  lowercase hex, of the ASCII text `staircase-chain-v2` and a line feed, then four
  fields in this order - the **event type**, the payload, the previous event's
  `event_hash` (empty for the first event) and the git commit hash the event carries
  (empty before the run's commit) - each written as its length in bytes (8 bytes,
  big-endian) followed by the bytes. Because the event type is covered, relabelling an
  event (say, turning a `yield_decided` into something else) breaks the chain; because
  each field is length-prefixed, moving bytes from one field to the next does too.
- **Version 1** (earlier runs, and an entry with no `hash_version`): the SHA-256 of the
  payload, the previous hash and the commit hash, joined without separators. It does not
  cover the event type: **an event of a version 1 run can be relabelled without breaking
  its chain**. Such runs still verify as they always did; the weakness is why a version 2
  exists, and an export of an old run is no stronger than it was.
- A run can hold both (it began before an upgrade and continued after). An entry cannot
  be passed off as another version: its stored hash only matches the version it was
  computed under. A `hash_version` this stAirCase does not know is refused.

Conformance vectors are in [`spec/audit-chain-vectors.json`](spec/audit-chain-vectors.json);
`python3 docs/spec/audit_chain_vectors.py` checks them with a program that shares no code
with stAirCase.

## The change certificate on every commit

Each run that commits leaves a **change certificate** about exactly that commit: a
small, signed statement of who helped, what was decided and by whom, and the
[assurance level](adr/0001-core-promise-and-assurance-levels.md) the change reached.
It holds digests only, never code or prompts.

- It is attached to the commit as a git note (`refs/notes/staircase`) and saved as
  `audit/run-N.certificate.json` in the workspace.
- The commit message itself names the agents that helped (`Assisted-by:`) and where
  the audit chain stood (`Staircase-Chain:`).

Check a commit, in the repository:

```bash
staircase verify staircase/run-7
staircase verify staircase/run-7 --min-cal 3     # fail below CAL 3
```

```
✅ Commit 8a4ae8705334: valid change certificate, CAL 3
   run #7 from 1ec6a66040cc, assisted by Claude Code
   3 decision(s) by operator
```

A run reaches CAL 3 when every action was decided before it ran; it drops to CAL 2
when an approved shell command ran without a sandbox (for example when a company's
managed settings exempt Claude Code's commands from its sandbox), or when changes were reviewed only after the agent made them.
Files written by a command that ran in the sandbox are decided before they reach
the commit, so they keep CAL 3. The
certificate format is described in [ADR 0002](adr/0002-change-certificate.md).

**For a reviewer on another machine:** notes are not pushed or fetched by default.
Push them with the branch:

```bash
git push origin staircase/run-7 refs/notes/staircase
```

The reviewer fetches them and verifies with your public key, the only file they need:

```bash
git fetch origin refs/notes/staircase:refs/notes/staircase
staircase verify <commit> --key signing.pub
```

## Rebuild a commit from its approvals

```bash
staircase rebuild staircase/run-7
```

`verify` shows who signed what was decided. `rebuild` shows that what was decided is
what is in the commit, byte for byte: it takes the run's ledger (the base commit and
every approved proposal, in order), replays it on the base commit with the same
rules a run uses, and the git tree it produces must be identical to the commit's
tree. A commit that holds anything nobody approved fails, even when its certificate
is validly signed.

The ledger holds the approved content, so it stays in your workspace
(`audit/run-7.ledger.json`, readable only by you); the certificate carries its
SHA-256. To let a reviewer rebuild, give them the ledger file, and they run
`staircase rebuild <commit> --ledger run-7.ledger.json`. Runs from before this feature have no ledger.

The ledger is JSON. Text, including NUL bytes, bare carriage returns, any Unicode and
files up to 200 KiB, round-trips exactly. A whole new file that is **not** valid UTF-8 (an
image, a Latin-1 source file, a compiled blob) travels as base64 in the change's
`content_b64`, up to 2 MiB, and makes the ledger **version 2** (a ledger is version 2 only
when it needs to be; a version 1 ledger that has `content_b64` is refused). A change of a file's mode (a new
script that is executable, or `chmod +x` of an existing one) is a whole-file change with the
change's `mode`, and also makes the ledger version 2. A person
deciding such a change is not shown unreadable bytes but the file's size and SHA-256, and a
rule, the agreed task or a model never approves it: it always comes to a person, with the
reason shown as `CHECK:`. Search-and-replace edits of a binary file are not supported; a
changed binary file is a whole-file change. A text proposal whose `replace_block` is not
valid UTF-8 is still refused, with the reason, before anyone decides it.

## When the evidence cannot be written

The commit is made first; the ledger and the signed certificate are written after it.
If either cannot be written (a full disk, an unwritable `audit/` directory, no signing
key), the commit is still on its run branch and the run says so plainly: the run
summary (`runs/<id>/summary.json`) has `"outcome": "delivered_without_evidence"` and
the reasons in `evidence_errors`, and the audit chain has an `evidence_failed` event.
A fully evidenced commit has `"outcome": "certified"`.

With `--require-evidence` the run also exits non-zero in that case, so a script or CI
job cannot mistake it for a complete result. The run is not relabelled as failed or
undelivered: the commit exists, and its hash is in the record.

## Keeping evidence: clean, archive and legal hold

`staircase clean --aggressive` tidies the workspace, but it does not throw evidence
away silently:

- A `staircase/run-N` branch is deleted only when it is older than 30 days **and
  merged into another branch** (local or remote-tracking). An unmerged run branch holds
  the only copy of its commit and is kept, with a message. A squash-merge does not count
  as merged, so those branches stay until you delete them yourself.
- Audit rows go to `archive/clean-<time>.jsonl` (readable only by you) **before** they
  are deleted: flagged cases with their runs and events, and the event-log rows of the
  oldest runs beyond a million rows. A run goes whole or not at all (cutting the front off
  a run's audit chain would leave one that can no longer be verified), so a run that is
  still running, or that has an entry newer than the cut such as a story accepted later, is
  kept, and fewer rows than the excess may go. If the archive cannot be written, nothing is
  deleted.
- A file named `legal-hold` in the workspace stops all of it: no audit rows, cases or
  run branches are deleted until you remove the file.

Certificates and ledgers under `audit/` are never touched by `clean`.

## Back up and restore the workspace

The workspace directory (`~/.staircase-workspace`) is the whole state: the database
(`workspace.db` with its `-wal` and `-shm` files), the encryption key (`.key`), the
signing keys, `policy.json`, and `audit/` (certificates, ledgers, checkpoints). Back
it up while no run is active: copy the directory, all of it. A restored copy verifies
its audit chains, decrypts its secrets and verifies its commits with the restored
signing key; this is tested. Without `.key`, the stored secrets cannot be read, so keep
it with the backup, somewhere only you can reach. Keep the certificates and ledgers in
git too (`refs/notes/staircase*`), so a commit's evidence does not depend on one disk.

## Review attention

A signed approval proves someone clicked approve, not that they read the change. So
the certificate also records how people decided: how many decisions they made,
their median time, and how many changes of 20 lines or more they approved in under
5 seconds. `staircase verify` prints it, and `staircase report` lists the certified
commits with such quick approvals so a team can look at them again. It is
information, not a gate: a quick approval can be right, for example after reading
the same change elsewhere.

## Checks on the commit

`--check` runs a command, such as your tests, on the commit a run made, and puts the
result in its certificate. It works with `run`, `claude`, `codex` and `review`, and
can be repeated:

```bash
staircase claude "fix the date parser" --check "go test ./..." --check "go vet ./..."
```

Each check runs on a clean checkout of exactly that commit, in the same OS sandbox
as approved commands (no network, writes only in the checkout and a temporary
folder), for at most 15 minutes (`--check-timeout 5m` sets another limit for every place checks run; a check that takes longer counts as not passed, and the certificate records it as one that could not run, exit code -1). The certificate records the command, its exit code,
whether it ran sandboxed and the SHA-256 of its output, never the output itself.

**The agent cannot finish until the checks pass.** In `staircase claude` and
`staircase codex` sessions, when the agent wants to end, its Stop hook runs the
checks on a copy of the approved changes so far. If one fails, the stop is refused
and the agent gets the failure to work on. After 3 refused stops it may end anyway,
and the result is recorded as it is. Each attempt is on the audit chain
(`done_checked`).

A failed check does not undo the commit, but `staircase verify` fails it, so a CI
check that requires certificates also requires passing checks. Checks are evidence
about the result; they do not change the level, which is about how the change was
decided. Commands that need the network (downloading dependencies) fail in the
sandbox: fetch them first, or pass `run --sandbox off` and say so in review.

## Report across repositories

```bash
staircase report ~/src/shop ~/src/billing --since 30.days --key team.pub
```

For each repository (default: the one you are in), `report` looks at the commits of
its current branch and shows how many were made by people and how many with an
agent, how many of those carry a valid certificate at each assurance level, which
agents wrote them, and each agent commit whose certificate is missing, invalid or
records a failed check. It decides certificates exactly as `staircase verify` does,
but never fails; `--json` prints the same for a dashboard or a spreadsheet. Fetch the
notes first (`git fetch origin refs/notes/staircase:refs/notes/staircase`).

## Two-person review (CAL 4)

For changes that need a second pair of eyes, a reviewer signs the run's certificate
with their SSH key after checking the change. It is the same mechanism git uses for
SSH-signed commits, so any key they already use for GitHub works.

```bash
# the reviewer, in the repository, after fetching the notes:
git fetch origin refs/notes/staircase:refs/notes/staircase
staircase sign staircase/run-7 --key ~/.ssh/id_ed25519.pub --as bob@example.com
git push origin refs/notes/staircase
```

Trusted reviewers are listed in git's `allowed_signers` format, one per line:
`bob@example.com ssh-ed25519 AAAA...`. Anyone checking the change then runs:

```bash
staircase verify staircase/run-7 --allowed-signers .github/allowed_signers --min-cal 4
```

A CAL 3 change counts as **CAL 4** when a trusted reviewer signed it **and** the
reviewer is not the person the run was made for: the certificate records the git
email (`user.email`) the run was made under, and that person's own signature does
not count as a second review.

**Who started the run.** By default the certificate records the git `user.email` of the
checkout as the requester, and anyone can set that to anything, so two different strings
are not proof of two different people. Start the run with your SSH key
(`--sign-approvals ~/.ssh/id_ed25519 --sign-as you@example.com`) and you also sign the
request: your name, the run, its base commit and its plan. The certificate then carries
that signature as its `initiator`, and `verify` checks it (a forged one fails the
certificate). A second party must be someone else: neither the git email, nor the
initiator's name, nor the initiator's key under another name counts.

```bash
staircase verify staircase/run-7 --allowed-signers .github/allowed_signers --min-cal 4 --require-initiator
```

With `--require-initiator`, CAL 4 counts only when the initiator signed and is listed in
`allowed_signers` (namespace `staircase-request`, which a plain `ssh-keygen` line covers
unless restricted): that is two different *trusted keys*. What that proves is that two
keys you trust were used, one to start the run and one to review it, not that two humans
hold them; back it with branch protection that requires a human approval. Without
`--require-initiator` (and for runs started without a key), read CAL 4 as "a trusted
reviewer signed it".

## Require certificates on pull requests

A CI check can refuse pull requests with a change that is not properly certified.
Commit your workspace's public key to the repository's protected branch (it is
public; copy `~/.staircase-workspace/.signing.pub`, for example to
`.github/staircase.pub`), and add a workflow:

```yaml
name: stAirCase
on: pull_request
permissions:
  contents: read
  attestations: read
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - uses: b070nd/stAirCase@v0.6.0   # the trust rules below need 0.6.0; 0.5.0 reads the key from the checkout
        with:
          key: .github/staircase.pub
          allowed-signers: .github/allowed_signers   # optional: trusted reviewers, for CAL 4
          min-cal: 3
          rebuild: true                              # optional: needs a release that keeps ledger notes
```

The check installs the release of `staircase` the action is used at (`@v0.7.1`), or the
release named by the `version` input, after verifying how it was built: the build attestation
must come from this repository's release workflow (`release.yml`) for that exact tag
(`gh attestation verify --signer-workflow … --source-ref refs/tags/<tag>`), and a binary that
was not verified is never run. The `version` input must be a release tag such as `v0.7.1`;
anything else is an error and never "the latest". Which verifier runs is decided in this order:
the `version` input if it is set; else the release tag the action is used at; else, when the action is
used at a ref that is not a release tag (`@main`, or a commit SHA with no `version` input), the **latest
release is installed and the step warns that the verifier is not pinned**. For a reproducible check
pin the verifier: use the action at a release tag, or at a commit SHA together with `version: v0.8.0`.
(That the unpinned case warns rather than refuses is current behaviour, not a promise of determinism.) It fetches the certificates (`refs/notes/staircase`) and checks **every commit** of the
pull request. Push the certificates together with the branch:

```bash
git push origin staircase/run-7 refs/notes/staircase refs/notes/staircase-ledger
```

**Where the trust comes from.** The key and the allowed signers are read from the
pull request's *base branch* (or `trust-ref`), never from the pull request's own
files: a pull request that replaced the key would otherwise vouch for itself. A key
that is only in the pull request is refused. Outside a pull request (a push, a merge
queue), set `range` and `trust-ref` to a protected branch or tag: without `trust-ref` the check
refuses to run rather than read the key from the checkout. The pull request's base is fetched
whatever `range` is, and stays the place the trust comes from. Protect the key and
signers paths with CODEOWNERS and a ruleset that requires review, and make this
check a required status check; a check that can be skipped or edited by the change it
judges is not a gate.

**Every commit, by default.** `all: false` checks only commits that name an agent in
an `Assisted-by:` trailer, and a run of `staircase verify` without `--all` says how
many commits it skipped. A commit an agent made without the trailer looks like a
person's, so selective checking is advisory. To exempt human commits reliably, do it
outside the change: a ruleset that requires signed commits from people, for example.

**Rebuild.** With `rebuild: true` (`staircase verify --rebuild`), each commit is also
replayed from its ledger, which a run attaches as a note
(`refs/notes/staircase-ledger`), and must have exactly the tree the ledger produces:
a validly signed certificate on a commit that holds other bytes fails. A commit with
no ledger fails, so turn it on once your runs come from a release that keeps them.

**Limits, stated plainly.** A certificate is about one commit id. Squash-merge and
rebase-merge make new commits that carry no certificate, so check the pull request's
own commits before merging, and merge with a merge commit, or certify the merged
result with `staircase seal`. A pull request from a fork has no notes in your
repository (the contributor cannot push them there) and fails closed. Notes are
ordinary refs: anyone who can push to them can overwrite a note, so restrict who may
push `refs/notes/staircase*`. None of this has been exercised against a real
organization ruleset; do that once with a test repository before relying on it.

The same check runs locally: `staircase verify main..HEAD --all --min-cal 3`.

## Look at a run

```bash
staircase inspect runs --case 1        # runs of case 1 and their status
staircase inspect log 7                # the events of run 7; also checks the chain
staircase inspect log 7 --full         # without shortening long payloads
staircase replay 7                     # just the decisions, in order, after checking the chain
```

## Export signed evidence

```bash
staircase audit export 7
staircase audit verify ~/.staircase-workspace/audit/run-7.checkpoint.json
```

`audit export` writes the run's events to `audit/run-7.checkpoint.json` in the
workspace and signs them with the workspace's Ed25519 signing key. Exporting again
adds a new record to the same file; older records are never overwritten.

## Let someone else verify it

Verification needs only two files, and neither is secret:

- the checkpoint file, `run-7.checkpoint.json`;
- your workspace's public signing key, `~/.staircase-workspace/.signing.pub`.

The other person puts `.signing.pub` in an empty folder and runs:

```bash
staircase --dir ./folder-with-the-public-key audit verify run-7.checkpoint.json
```

If any character of the evidence was changed, verification fails.

## Add an outside witness: Rekor

A signature proves the evidence was not changed after signing, but whoever holds the
signing key could create new, different evidence. To prove **when** the evidence
existed, anchor it in [Rekor](https://docs.sigstore.dev/logging/overview/), Sigstore's
public, append-only transparency log.

**Anchor the change certificate (recommended).** Only the certificate is sent: commit
hashes, digests, decision counts and the level. No code, prompts or reasoning leave
your machine.

```bash
staircase audit anchor 7                              # writes run-7.certificate.json.anchor
staircase verify staircase/run-7 --check-anchor
```

**Anchor the whole record.** This also covers every event of the run:

```bash
staircase audit export 7 --anchor                   # also writes run-7.checkpoint.json.anchor
staircase audit verify run-7.checkpoint.json --check-anchor
```

> **What leaves your machine with `--anchor`.** The whole signed record - every event of the run,
> including the agents' reasoning, file paths and the proposed changes - is sent to
> the Rekor service, which checks the signature. The public log then keeps, for
> good, only the record's SHA-256, the signature and your public key; it does not
> store the record itself (checked on rekor.sigstore.dev). If the record must not
> reach a third party at all, use `--rekor-url` with a Rekor instance you run
> yourself.

## Accepting stories is evidence too

`staircase story accept <story-id>` records who accepted the story on the audit chain
of the case's latest successful run. A commit alone never marks a story as done.
