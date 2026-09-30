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
| `certificate_issued` | the change certificate was signed for the run's commit |
| `run_path` | the phases the run went through, in order (see below) |

A run moves through fixed phases - checks (`PRE_FLIGHT`), its worktree
(`BRANCH_CREATE`), starting the agent (`AGENT_START`), deciding (`AGENT_LOOP`), the
final checks and commit (`FINALIZE`) and cleanup (`BRANCH_RESTORE`) - and only along
the moves the run's state machine allows. `run_path` records the way a run actually
took, so a run that stopped early shows where.

A decision is written to the chain **before** the agent learns it. Secrets are
removed from every event before it is stored.

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
folder), for at most 15 minutes. The certificate records the command, its exit code,
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

What this proves, and what it does not: CAL 4 means a reviewer whose key you trust
signed the change and is not recorded as the requester. The requester is the git
`user.email` of the checkout, which anyone can set to anything, so two different
strings are not proof of two different people. Until the requester is authenticated
too (an initiator signature is planned), read CAL 4 as "a trusted reviewer signed
it", not as proven two-person control, and back it with branch protection that
requires a human approval.

## Require certificates on pull requests

A CI check can refuse pull requests with an agent's commit that is not properly
certified. Commit your workspace's public key to the repository (it is public; copy
`~/.staircase-workspace/.signing.pub`, for example to `.github/staircase.pub`), and
add a workflow:

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
      - uses: b070nd/stAirCase@v0.5.0
        with:
          key: .github/staircase.pub
          min-cal: 3
```

The check installs that release of `staircase` after verifying how it was built,
fetches the certificates (`refs/notes/staircase`) and checks every commit of the pull
request that names an agent in an `Assisted-by:` trailer. With `all: true`, every
commit needs a certificate. Push the certificates together with the branch:

```bash
git push origin staircase/run-7 refs/notes/staircase
```

What it cannot do: a commit made with an agent but not marked `Assisted-by:` looks
like a person's commit. Use `all: true` where every change must come through
stAirCase.

The same check runs locally: `staircase verify main..HEAD --min-cal 3`.

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
