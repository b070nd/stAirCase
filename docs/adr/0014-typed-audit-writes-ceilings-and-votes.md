# ADR 0014: Typed audit writes, entries that are never cut, and reviewer votes on the chain

- Status: proposed (2026-10-22). Built so far: section 1 (the entry that is never cut, the two ceilings; the early refusal of a
  proposal that would make the offered review too big is still to come) and section 2 (one `history.Writer`, every event a type, the
  agent host's path merged into it, the fixture reproduced byte for byte). Sections 3 and 4 follow, and the status changes to
  accepted when the last of them is built.
- Related: ADR 0004 (audit chain version 2), ADR 0010 (one replay of the audit chain, Q3 and Q13, "writers fill typed structs"),
  ADR 0012 (delivering by policy, the grammar after `delivery_authorized`), ADR 0002 (change certificate).

## Context

ADR 0010 gave the chain one reader, and decided that writers fill typed structs. Twenty-one of the 47 places that write an entry
still built a map by hand, and `awaiting_review` was written in two ways. Renaming a key in the hand-built one broke no test:
the replay ignores keys it does not know (ADR 0010, Q9), so only the write side can keep the shape.

The chain also cut every entry at 64 KiB without a word. A cut entry still hashes and verifies, so it stands as the record of
something the run did not write, and the replay then refuses the whole run as "its payload is unreadable". The entries that list
a change's files grow with the change, and a decision on a proposal of about 700 files, or the review an away run offers for
them, passed the limit.

The reviewer models of `--validator` decide by unanimity, and the chain recorded only the outcome as one decision and a line of
text: not each model's verdict, nothing at all of a disagreement or a model that failed, so nobody could recount what the decision
rested on.

## Decision

### 1. An entry is written whole or refused

An entry holds at most 1 MiB (about 10,000 files at some 100 bytes of path and digest each). Over that the chain refuses it with an
error that names the event and the size, and writes nothing. What an agent supplies keeps its own, lower limit of 64 KiB, enforced
by the agent host before the chain is reached, and is recorded as a short note of what was left out (`truncated`, the kind, the
size and the digest), as before. The limit is applied when an entry is written and nowhere else: the hash covers whatever bytes were
written, so chains written before read and verify as they did, and an older release reads a longer entry as it reads any other.

A proposal that would make the review an away run offers pass the ceiling is rejected for the agent, with a message to stop or split
the work and the source `orchestrator`, so that the run does not do all its work and then fail at its last step. The refusal at
write time stays as the backstop.

### 2. One writer, typed events

Events are written through one `history.Writer`, whose `Append` takes a typed event (`AppendHash` returns the new entry's hash and
`AppendIf` checks a condition under the append lock). The string-and-`any` form is deleted and has no replacement, so a new event
needs a type first. All thirty-eight event types have one, including the twelve that had none. A golden
test runs on what the writer produces, and a fixture chain written by the code before this change is reproduced byte for byte: no
hash changes. Scrubbing and the debug sink stay where they are, as settings of the writer the agent host uses, which also writes the value as it
marshals (`Plain`), as it always has. One entry does not pass through the writer: the acceptance of a story (`story_accepted`), which
is written inside the transaction that accepts it.

### 3. Reviewer votes

Each reviewer model's verdict on a proposal is written as `validator_voted` before the decision: the model, the request it judged
(`request_entry`), approve, reject or error, its reason, and the digest of its raw reply. Disagreements, errors and the verdicts that
came before a failed model are recorded. A run with a panel records the models on `run_bound` (`validator_models`). Where that field
is present the replay requires every decision whose source is `validator:` to follow one vote per listed model on that request, all
agreeing with it, and refuses the chain, naming the entry, when they do not. Votes without a validator decision are allowed (a
decision a person took, as in the sampled review), and votes never use up a request. A chain without the field reads as before.

### 4. The votes are not signed

A model has no key. A signature by the workspace key would add little over the hash chain and the signed certificate, and would
suggest an independence that does not exist. A compromised orchestrator could forge votes; this is stated in the documentation.
`inspect`, `status` and `decide` show the votes. The change certificate does not change (ADR 0002): a field would be a public
contract, and a verifier in CI has the certificate and ledger but not the chain.

## Considered options

- **Cut, but say so.** Rejected: any marker in a cut entry is itself part of a record that no longer parses.
- **A larger single ceiling for every entry.** Rejected: an agent's own proposal is the one input outside the orchestrator's
  control, and its short note already exists.
- **Record only a digest of the files of a large review.** Not taken: `decide` shows the files from the entry, and the delivery
  checks compare against it. Reviews of more than the ceiling can be recorded differently later, as a change of the format of its
  own.
- **A threshold of the panel (a majority) in place of unanimity.** Not taken: the measured weakness of the reviewer models is that
  they agree on a wrong "safe", not that they disagree (docs/evaluation).

## Consequences

- A run of a change over the ceiling fails with an error instead of leaving a run nobody can read.
- The replay grammar gains a rule that only runs which declare a panel are held to, which moves decisions toward a person only
  (the compatibility rule in docs/compatibility.md).
- `persistence` keeps rows; hashing, verification and the ceilings move to a package of their own that the store calls on insert.
