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

> **Warning: anchoring makes the evidence public, permanently.** The whole signed
> record is uploaded to the log - every event of the run, including the agents'
> reasoning, file paths and the proposed changes themselves. The public Rekor log
> cannot delete entries. Anchor only runs whose content may be public (for example
> open-source work), or use `--rekor-url` to anchor in a Rekor instance you run
> yourself.

## Accepting stories is evidence too

`staircase story accept <story-id>` records who accepted the story on the audit chain
of the case's latest successful run. A commit alone never marks a story as done.
