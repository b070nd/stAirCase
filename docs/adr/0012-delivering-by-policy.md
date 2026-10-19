# ADR 0012: Delivering by policy, the second delivery mode (design)

- Status: proposed (2026-10-18), for the owner to decide. Nothing here is built.
- Related: the v1.1 candidate V11-C01 ("Accepted delivery choice - select before every phase starts", in the maintainer's protocol);
  ADR 0001 (core promise, CAL), ADR 0006 (Away mode, section 5 and section 9 item 2), ADR 0008 (section 3, delivery by policy "not taken
  now"), ADR 0010 (one replay), ADR 0011 (work phases).

## Context

V11-C01 names two delivery modes, chosen before a phase starts. **Review before delivery** keeps the approved state and its evidence
for the owner, and the run branch does not move until the owner accepts those bytes. **Policy-based local delivery** lets Go deliver
once every required check and gate passes, when the owner authorized that narrowly in advance; a mandatory human review still parks the
work, and the mode never authorizes push, publication, release, deployment or any other change outside the repository. The first mode
is built (ADR 0006). This ADR designs the second.

What delivery is, as the code is now (`finalize` in `orchestrator/runphases.go`):

- An away run whose approved tree passes its checks goes to `awaitReview` (`away.go`): `phase_checked` (the tree, each check's result),
  then `awaiting_review`, then the run ends `AWAITING_REVIEW` with the run branch at its base. `staircase decide --accept` records
  `review_accepted` and finishes through `Recover`: the files derived again from the chain, the tree compared with the one offered,
  `commit_prepared`, the move of the branch, the evidence, `review_delivered`.
- A run that is not away asks a person for a **final review** of the whole change when `val.unreviewed || dec.taskApproved`: when a
  change was approved as part of the task or on evidence (both set `taskApproved`, "a person still reviews the whole change at the end"),
  or a validator model left something unreviewed. A run whose approvals were all a person's or a policy rule's is not asked, and
  delivers at the level it computes (CAL 3 when nothing else lowers it).
- Delivering is `preparedCommit.deliver`: `git update-ref refs/heads/staircase/run-N <commit> <base>` in a private index, with the
  orchestrator's identity and no hooks, and only if the branch still points at the base. Then the checks run on the commit, and the
  ledger and the certificate are written; what cannot be written is recorded as `evidence_failed`, the commit stays delivered, and with
  `RequireEvidence` the run ends with `ErrEvidenceIncomplete`.
- Nothing in a run pushes. `GitRepo.Push` is called by `staircase push`, a command a person runs.

While away, an in-scope change is approved on evidence (`NewTerms` turns `Away` into `ApproveOnEvidence`), so nearly every away change
is one a live run would have put to a person's final review. The certificate (`certify.go`) does not record whether a final review
happened: today it does not need to, because a CAL 3 certificate on an evidence-approved change always had one. The pull-request check
in [audit evidence](../audit.md#require-certificates-on-pull-requests) is shown with `min-cal: 3`.

ADR 0008 recorded delivery by policy as weakening review-before-delivery and left it to its own ADR. V11-C01 accepts it as a design
direction with the conditions above.

## Decision (proposed)

### 1. Chosen before the run, frozen in its terms

`--deliver review|policy` on an away run, default `review`. It is a term: `Terms.Deliver` (`json:"deliver,omitempty"`), where empty
means review, so every `options.json` saved so far reads as review. It is saved and bound by `terms_sha256` like every term;
`ResumeOptions` has no field for it, so a continuation cannot change it; in a phase (ADR 0011) it is in the contract and every task has
it. A stAirCase that predates the field ignores it when it reads the saved terms (`loadTerms` does not refuse unknown keys) and treats
the run as review mode: the stricter mode, which a test pins.

`NewTerms` refuses `policy`:

- without `--away`;
- with `--validator` models: an approval in which a model agreed is recorded with source `evidence` (`onEvidence` adds the validator's
  name to the evidence), and ADR 0001 says a model's approvals count only when a person reviews the whole change;
- with signed decisions (already refused for away runs);
- without a **delivery scope**: `--deliver-path <glob>`, repeatable, the paths the owner lets reach the run branch without them.

### 2. When Go delivers, and when it parks instead

When the agent stops and `phase_checked` passes, Go delivers without a person only if every one of these holds, read from the run's
verified history (one replay, ADR 0010), not from the live state:

1. the bound terms say `policy`;
2. every approval's source is `evidence`, `policy` or `declared`: none is a person's, the agreed task's alone, or a model's;
3. no proposal was parked;
4. every approved path is inside the delivery scope;
5. `phase_checked` passed on exactly the tree the approvals derive, with the checks the terms name;
6. the run branch still points at the base (the compare-and-swap of `deliver`).

Otherwise the run ends awaiting review, exactly as in review mode, and says which condition sent it to the owner. Falling back to the
owner is a normal outcome, never a failure.

**What still reaches a person.** Everything the deciders send to a person is parked while away and so stops delivery by condition 3:
a guard (dependencies, a file made executable, a file that is not text, hidden Unicode, what looks like a secret), a sensitive path
under the agreed task, drift, a scope violation, a change to a path that holds a check's definition (`Terms.ProtectedPaths`, decided
after drift and before the limits and the policy, whatever the policy says), a limit or checkpoint, missing or failing evidence, a signal's escalation, and every
command or tool call that was not declared. The mode answers none of them, and it never applies a parked proposal.

### 3. What is recorded

Before `commit_prepared`, a new event, `delivery_authorized`: the `phase_checked` entry, the tree, the approved files' digest, the
terms' digest and `mode: policy`. The history grammar gains its rules: it follows a `phase_checked` of the same tree; no request or
decision follows it; only `commit_prepared` and the evidence may. Then delivery is the live run's: `commit_prepared`, the move of the
branch, the checks on the commit, the ledger, the certificate. The run ends as a live run that delivered does, with `evidence_failed`
when evidence could not be written; no new run status.

### 4. What the certificate says

- A note: delivered by policy while the owner was away; no person reviewed the whole change; the owner authorized local delivery to this
  run branch in advance, under the terms the chain binds.
- **The level is capped at CAL 2 when the delivery stood in for a final review a live run would have asked** (an approval on evidence or
  by the task). A gate that requires CAL 3 today gets, for every evidence-approved change, a person's review of the whole change; a
  delivery by policy must not satisfy it silently. A run whose approvals were all policy rules or declared commands keeps the level it
  computes, as a live run with only rule approvals does today.
- The predicate type stays `#v1` and gains no field: the note and the `decisions` counts (`evidence`, `policy`, `declared`, no
  `operator`) say how it was decided. The meaning of each level is unchanged; the cap applies ADR 0001 section 3 to a new case.

### 5. What it never does

- Push, open a pull request, tag, release, publish, deploy, call out to act, or spend beyond the terms' budget. `staircase push` stays a
  person's command.
- Move any ref but `refs/heads/staircase/run-N`, and that one only from its base. The developer's branch and checkout are never touched,
  as today.
- Deliver bytes not derived from approvals: the commit is made by `approvals.prepare`, as for every run.
- Start a dependent task on a delivery with incomplete evidence (ADR 0011, section 5).
- Change mode during a run, or apply to a run started in review mode.

Each line is a test: a stand-in agent in policy mode, with a remote configured, ends with the remote untouched and only the run branch
moved.

### 6. Crashes

The kill points are the existing barriers plus one: after `phase_checked`, after `delivery_authorized`, after `commit_prepared`, after
the compare-and-swap, after the evidence. Killed before `delivery_authorized`, the run is an interrupted away run, as today. Killed
after it, `staircase recover` finishes the delivery without a person only when the tree it derives is the authorized tree and the terms
still say `policy`; otherwise it asks for a final review, as recovery does today after an approval no person or rule made (ADR 0010,
Q23). After `commit_prepared`, recovery's existing repair applies: one commit, one certificate.

## Options considered

1. **Advance authorization, conditions, fallback to review, CAL capped** (proposed).
2. **Deliver only when no final review would be asked today**: every approval by a policy rule or a declared command. No new authority
   and no cap, but while away nearly every change is approved on evidence, so it would rarely deliver. A safe first step.
3. **Deliver on evidence and move the review to the pull request** (ADR 0008's recorded alternative), with the required admission check
   enforcing the certificate. The whole-change review would then be the forge's, not on stAirCase's chain, and it needs a push, which
   this mode must never do.
4. **Record the authorization as a final review** (`yield_decided`, action `final_review`, a new source such as `delivery_policy`).
   No new event, but a final review is a person's decision (glossary), and the rules that read it (which final review counts, Q18;
   when recovery asks, Q23) were written for persons.
5. **Do not build it**: review before delivery stays the only mode.

## Consequences

- New: `Terms.Deliver`, `--deliver` and `--deliver-path`, the `delivery_authorized` event and its grammar rule, the certificate note and
  cap, and the message that says why a run fell back to review.
- `docs/away.md` loses "delivery by policy without your review" from "Not yet" and explains the cap. The policy file, the hook bridge
  and the certificate predicate type are unchanged (`docs/compatibility.md`).
- Acceptance, from V11-C01 criterion 3: an authorized passing run yields one owned local commit, a certificate that verifies and a
  ledger that rebuilds the tree; anything parked sends it to the owner; nothing outside the repository changes. Criterion 7: kills at
  each point above, then recover: one commit.

## Open questions for the owner

1. **May an advance authorization stand in for the review of evidence-approved changes at all** (option 1), or should the first version
   deliver only rule-approved and declared changes (option 2)?
2. **The level**: capped at CAL 2 (proposed), or as computed with a note only? And should a person's later review (`staircase sign`, or
   accepting in `staircase decide`) lift the cap?
3. **Parked proposals**: does one parked proposal send the whole run to the owner (proposed), or may the approved part be delivered with
   the parked ones listed for later?
4. **The delivery scope**: its own `--deliver-path` list (proposed), or the stories' scope the run already has?
