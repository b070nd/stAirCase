# Change sets

A change that spans a backend, a middleware and a frontend is several runs, one per repository. Each run stays what it always is: one
repository, with its own worktree, audit chain and change certificate. A **change set** is an id that ties them together, plus one
command that checks all of them.

```bash
staircase changeset create checkout-v2

# in each repository
staircase claude --change-set checkout-v2 "add the order endpoint"

staircase changeset add checkout-v2 12 13 14          # the run ids
staircase changeset contract checkout-v2 backend:api/openapi.yaml
staircase changeset ack checkout-v2 backend:api/openapi.yaml --by frontend
staircase changeset verify checkout-v2
```

Name a project by its id or its name. `staircase changeset show <id>` lists the runs, contracts and acknowledgements. `staircase changeset
list` lists the sets in the workspace.

## What a set holds

| | |
|---|---|
| **Id** | You choose it with `create`, so a run can name it before the run exists. Lower case letters, digits, `.`, `-` and `_`. |
| **Runs** | One per project. Adding another run of the same project replaces the first, and the command tells you. |
| **Contracts** | Files that more than one repository depends on, such as an API description or a schema. A literal path from the repository root. |
| **Acknowledgements** | A record that someone looked at a member's change to a contract and another project accepts it. |

The set is a file in your workspace (`changesets/<id>.json`). Nothing of it is in any repository.

## What a run does with the id

`--change-set <id>` works on the session commands (`claude`, `codex`, `gemini`, `opencode`) and on `staircase run`. It is refused if the
set does not exist, because the id ends up on the certificate. The id goes on the run's record (`change_set` on `run_bound`) and on its
certificate (the optional `changeSet` field).

The certificate holds the id and nothing more. Members join a set over time, so a list on the certificate would go stale. Anyone holding
one certificate can find the set, and the set lists the rest. A verifier that does not know the field ignores it, and the predicate
version does not change.

Nothing else about a run changes: its worktree, branch, chain and certificate commit are the same as for a run without a set.

## Contracts and acknowledgements

When a member's run changed a contract file in its own project (the file differs between the run's base and its commit), every
**other** member must have acknowledged that exact content.

- `ack` records the content (the git blob the change produced). A later change to the file needs a new acknowledgement, and an
  acknowledgement of an older version counts for nothing.
- A project cannot acknowledge its own change.
- An acknowledgement is refused if the run did not change the file.

To make sure nobody changes a contract without a person, add its path to `sensitive` in the project's profile (`staircase project profile
set <id> --sensitive <path>`). `changeset contract` tells you when it is not there yet. See [a project's profile](profile.md).

## What `verify` checks

For every member run:

1. The run was **delivered**, according to its own verified history (the same outcome `staircase status` shows).
2. Its **certificate** verifies against the workspace signing key, is about the run's commit, reaches the level asked (`--min-cal`,
   none by default) and records no failed check.
3. The certificate **names this set**.
4. Every contract in the member's project that the run changed is **acknowledged by every other member**.

A failing member is named with the reason. A missing acknowledgement names the contract, the project that changed it, the project that has
not accepted it, and the command that records it. The command exits non-zero if anything fails, so you accept the whole set or nothing.

## What it does not do

- An acknowledgement is a record in the workspace file, not a signature. Whoever can write to the workspace can write one, as with a
  profile. It tells a reviewer that someone looked, and under which git `user.email`, but it is not proof.
- Verification uses the signing key of the workspace that made the runs. Verifying on another machine with other keys is not built.
- One agent does not work in several repositories in one session. A person, or an orchestrating agent, starts one run per repository.
- A set does not order the runs. Project dependencies and the dependency gate do that ([gates](gates.md)).
