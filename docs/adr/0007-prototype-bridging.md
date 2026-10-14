# ADR 0007: Bridging a parked proposal with a prototype (design)

- Status: accepted and built for v1.1, owner's request of 2026-10-14 ("if approval needs to be done, a disposable prototype could bridge it").
- Date: 2026-10-14
- Builds on: ADR 0006 (Away mode), section 9 item 1.

## Context

An away run parks what needs the owner: a change without evidence, outside the scope, a sensitive file. On return the owner sees each
parked proposal and why it needed them, but deciding on it means guessing its consequence, and an accepted run leaves it out: to have it,
the owner starts another run. The owner asked for the bridge: see what the parked change would do, then take it in, without blocking.

## Decision

### 1. A prototype is the parked change on top of the change on offer, checked, and thrown away

`P` in `staircase decide` (and `Runner.Prototype`) builds, in a disposable checkout, the base commit with the approved changes and the
parked proposal's edits on top, derived exactly as an approval would derive them, and runs the run's own checks there. The owner sees the
files it touches, the diff against the change on offer, and each check's result. Nothing is written to the run, its branch or its chain:
a prototype decides nothing. A proposal that no longer applies on top (its search text is gone) says so. A parked command cannot be
prototyped (it would have to run); it says that too.

### 2. Adopting makes the parked proposal an ordinary approval, by the owner

After a prototype the owner may adopt the proposal into the change. `Runner.Adopt` derives the new state again and runs the checks on it;
if they do not pass, nothing is recorded and the adoption is refused with why. If they pass, the chain gets what a live approval would
have written, so recovery, the ledger, rebuild and verify need no new rule:

1. a `yield_request` with the parked proposal's edits, naming the parked decision it adopts (`adopts`: its number);
2. its line in the approval journal;
3. a `yield_decided` approved by `operator` (the owner), with the digest of exactly the new approved state;
4. a `phase_checked` of the new tree and a new `awaiting_review` naming it.

The edits come from the parked proposal's request on the chain, which is what the owner was shown. The proposal is decided by a person
before its bytes are applied anywhere, so the level of the change does not drop. A review entry shown before the adoption is stale
afterwards: a decision on it is refused (ADR 0006, section 6). A proposal can be adopted once.

### 3. Interrupted adoption

If the process dies after the approval and before the new `awaiting_review`, the approvals derive a tree that the last review does not
offer; accepting that review is refused (the trees differ). Adopting the same proposal again sees its approval on the chain and only checks
the tree and records the review.

## Consequences

- No new event type: an adoption is a request, a decision, a check and a review, with `adopts` on the request.
- The owner's adoption is a person's decision in the certificate (`decisions.operator`), with its time to decide.
- Not built: a prototype that lets the agent continue on top of the parked change (an agent run on a throwaway branch). The deterministic
  prototype of the proposal itself comes first; a continuing prototype needs a second run and its own ADR.

## Acceptance, mapped to tests

| Criterion | Test |
|---|---|
| A prototype shows the files, the diff and the checks of the parked change on top, and writes nothing | prototype of a passing and a failing parked proposal; chain length unchanged |
| Adopting a passing proposal records an owner's approval and a new review; the old review is stale | adopt, then decide with the old and the new entry |
| Accepting after an adoption delivers both, and the commit verifies and rebuilds | accept, certificate, ledger of three proposals |
| A proposal whose checks fail is not adopted, and nothing is recorded | adopt the failing one |
| A proposal is adopted once; a command cannot be prototyped | refusals |
