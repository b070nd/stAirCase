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
when shell commands were approved, because they run without a sandbox. The
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
public, append-only transparency log:

```bash
staircase audit export 7 --anchor                   # also writes run-7.checkpoint.json.anchor
staircase audit verify run-7.checkpoint.json --check-anchor
```

> **What leaves your machine.** The whole signed record - every event of the run,
> including the agents' reasoning, file paths and the proposed changes - is sent to
> the Rekor service, which checks the signature. The public log then keeps, for
> good, only the record's SHA-256, the signature and your public key; it does not
> store the record itself (checked on rekor.sigstore.dev). If the record must not
> reach a third party at all, use `--rekor-url` with a Rekor instance you run
> yourself.

## Accepting stories is evidence too

`staircase story accept <story-id>` records who accepted the story on the audit chain
of the case's latest successful run. A commit alone never marks a story as done.
