# Change sets

A change that spans a backend, a middleware and a frontend is several runs, one per repository. Each run stays what it always is: one
repository, its own worktree, audit chain and change certificate. A **change set** is the id that ties them together, and one command
that checks the whole.

```bash
staircase changeset create checkout-v2

# in each repository
staircase claude --change-set checkout-v2 "add the order endpoint"

staircase changeset add checkout-v2 12 13 14          # the run ids
staircase changeset contract checkout-v2 backend:api/openapi.yaml
staircase changeset ack checkout-v2 backend:api/openapi.yaml --by frontend
staircase changeset verify checkout-v2
```

A project is named by its id or its name. `staircase changeset show <id>` lists the runs, the contracts and the acknowledgements, and
`staircase changeset list` the sets of the workspace.

## What the set holds

| | |
|---|---|
| **Id** | chosen by you with `create`, so a run can name it before the run exists. Lower case letters, digits, `.`, `-`, `_`. |
| **Runs** | one per project. Adding another run of the same project replaces the first (the command says so); a redone run is the usual reason. |
| **Contracts** | files that more than one repository depends on: an API description, a schema. A literal path from the repository's root. |
| **Acknowledgements** | a record that a person looked at a member's change to a contract and another project accepts it. |

The set is a file in your workspace (`changesets/<id>.json`). Nothing of it is in any repository, so a client's code never shows it.

## What a run does with the id

`--change-set <id>` on a session command (`claude`, `codex`, `gemini`, `opencode`) or on `staircase run` is refused when the set does
not exist, because the id is written on the certificate. The id goes on the run's record (`change_set` on `run_bound`) and on its
certificate (the optional `changeSet` field). It is the id and nothing more: a set's members change as runs join it, so a certificate
that carried the list would go stale. Anyone holding one certificate can find the set; the set lists the rest. A verifier that does not
know the field ignores it, and the certificate's predicate version does not change.

Nothing else about a run changes: its worktree, branch, chain and the commit that carries its certificate are as for a run without a set.

## Contracts and acknowledgements

When a member's run changed a contract file of its own project (the file's content at the run's commit differs from its content at the
run's base), every **other** member must have acknowledged that exact content. `ack` records the content (the git blob the change
produced), so a later change to the file needs a new acknowledgement, and an acknowledgement of an older version counts for nothing. A
project cannot acknowledge its own change, and an acknowledgement is refused when the run did not change the file.

To make sure a contract is never changed without a person, make its path sensitive in the project's profile (`staircase project profile
set <id> --sensitive <path>`); `changeset contract` says when it is not. See [a project's profile](profile.md).

## What `verify` checks

For every member run:

1. the run was **delivered**, by its own verified history (the same outcome `staircase status` shows), not by a status column;
2. its **change certificate** verifies against the workspace's signing key, is about the run's commit, reaches the level asked
   (`--min-cal`, default none), and records no failed check;
3. the certificate **names this set**;
4. every contract of the member's project that the run changed is **acknowledged by every other member**.

A failing member is named, with the reason; a missing acknowledgement names the contract, the project that changed it and the project
that has not accepted it, and the command that records it. The command exits non-zero when anything fails: accept the set or nothing.

## What it does not do

- An acknowledgement is a record in the workspace file, not a signature. Whoever can write the workspace can write one, as for a
  profile. It tells a reviewer that a person looked and which identity (the project's git `user.email`) said so; it is not proof.
- Verification uses the key of the workspace that made the runs. Verifying a set on another machine, with someone else's keys, is not
  built.
- One agent does not work in several repositories in one session. A person or an orchestrating agent starts one run per repository.
- The set does not order the runs. A project's dependencies and the dependency gate do that: [gates](gates.md).
