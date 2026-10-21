# ADR 0012: Delivering by policy, the second delivery mode (design)

- Status: accepted (2026-10-20), option 2 tightened, as the owner's decisions below; proposed on 2026-10-18 with option 1, which is
  rejected. Built: the terms (the delivery mode and scope, every refusal that the terms alone decide, the refusal of an unsigned
  policy at the start of the run), the command-line choice and the agreement screen, and the delivery itself (section 2's seven
  conditions judged from the verified history, the `delivery_authorized` event with its replay grammar, typed payload and barrier
  point, the certificate note and the optional field `delivery`, the message that says why a run fell back to the owner, and
  recovery as section 7 describes). Not built, by decision: a cap on the level, any push, and anything in a phase (ADR 0011).
- Related: the v1.1 candidate V11-C01 ("Accepted delivery choice - select before every phase starts", in the maintainer's protocol);
  ADR 0001 (core promise, CAL), ADR 0006 (Away mode, section 5 and section 9 item 2), ADR 0008 (section 3, delivery by policy "not taken
  now"), ADR 0010 (one replay), ADR 0011 (work phases), ADR 0013 (typed policy conditions, which let a rule name paths).

## Context

V11-C01 names two delivery modes, chosen before a phase starts. **Review before delivery** keeps the approved state and its evidence
for the owner, and the run branch does not move until the owner accepts those bytes. **Policy-based local delivery** lets Go deliver
once every required check and gate passes, when the owner authorized that narrowly in advance; the specification says a mandatory human
review still parks the work and that this selection cannot waive it, and the mode never authorizes push, publication, release,
deployment or any other change outside the repository. The first mode is built (ADR 0006). This ADR designs the second.

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
happened. The pull-request check in [audit evidence](../audit.md#require-certificates-on-pull-requests) is shown with `min-cal: 3`.

Two facts from the research that decided the design (`decisions.md`, item A):

- **The final review of the whole change is a person's decision**, in the specification, the glossary, the approvals guide and ADR 0001
  (a model's approvals count only when a person reviews the whole change). An advance authorization that replaces it contradicts the
  specification line, however it is labelled.
- **The level has never meant "a person looked at the whole change".** A live run whose approvals were all policy rules is not asked for
  a final review and already gets CAL 3. So a delivery by policy that rests on rules alone adds no new weakness to the level, and a cap
  on it would be the first place the code made the level mean something it does not mean elsewhere.

ADR 0008 recorded delivery by policy as weakening review-before-delivery and left it to its own ADR. V11-C01 accepts it as a design
direction with the conditions above.

## Decision

**Deliver without a person only what rules of the owner's signed policy approved, never what evidence or the agreed task alone
approved.** Anything else, and any doubt, sends the run to the owner's review exactly as in review mode.

### 1. Chosen before the run, frozen in its terms

`--deliver review|policy` on an away run, default `review`. It is a term: `Terms.Deliver` (`json:"deliver,omitempty"`), where empty
means review, so every `options.json` saved so far reads as review and the saved form of a review run is byte for byte what it was
(the terms golden test is unchanged for the old keys). It is saved and bound by `terms_sha256` like every term; `ResumeOptions` has no
field for it, so a continuation cannot change it; in a phase (ADR 0011) it is in the contract and every task has it. A stAirCase that
predates the field ignores it when it reads the saved terms (`loadTerms` does not refuse unknown keys) and treats the run as review
mode: the stricter mode (a documented limitation, not a tested one: an older binary cannot be run here).

`--deliver-path <glob>`, repeatable, is the **delivery scope**: the paths the owner lets reach the run branch without them
(`Terms.DeliverPaths`, `json:"deliver_paths,omitempty"`).

`NewTerms` refuses `policy`:

- without `--away`;
- with `--validator` models: an approval in which a model agreed is recorded with source `evidence` (`onEvidence` adds the validator's
  name to the evidence), and ADR 0001 says a model's approvals count only when a person reviews the whole change;
- with signed decisions (already refused for away runs);
- without a delivery scope (`--deliver-path`);
- without a signing key (`--sign-approvals`): the initiator signs, so the certificate names who armed unattended delivery. This is
  accountability, not review;
- without a **complete protected-paths declaration**: the checks name a file of their own, or `--protect` was given. For
  `go test ./...` the owner passes `--protect "**/*_test.go"`. Otherwise an agent can edit a test under a rule that approves its file
  type and make the final check pass, and the final check is what stands in for the review.

It also refuses `--deliver-path` without `--deliver policy`, a mode that is neither `review` nor `policy`, and a delivery path outside
the repository.

At the start of the run's first segment, an **unsigned policy snapshot** refuses the run (`policy_snapshot.signed`, written by
`staircase policy sign`): an edited policy file must not arm unattended delivery. This is the one condition the terms alone cannot
decide, because the policy is read when the run starts.

### 2. When Go delivers, and when it parks instead

When the agent stops and `phase_checked` passes, Go delivers without a person only if every one of these holds, read from the run's
verified history (one replay, ADR 0010), not from the live state:

1. the bound terms say `policy` and the run's policy snapshot was signed;
2. every file approval has source `policy`. The source `declared` is allowed for commands and tools only, which carry no files; the
   files such a command writes are decided like the agent's (ADR 0009), so they are `evidence` or parked and the run falls back to
   review. No `operator`, `task`, `evidence` or `validator:*` approval exists;
3. nothing was parked: **one parked proposal sends the whole run to the owner**. The specification says a mandatory review parks the
   work, not the proposal, and delivering the approved part while the parked part (which may be what the approved part depends on)
   waits can deliver an incoherent tree;
4. every approved path is inside the delivery scope, and **none matches the sensitive list or the run's protected paths** (this
   repeats the sensitive-path bar and the protected-path rule on purpose: delivery must not depend on a rule having been written
   carefully);
5. `phase_checked` passed on exactly the approved tree, with the checks the terms name, and the run branch still points at the base
   (the compare-and-swap of `deliver`);
6. the protected-paths declaration is complete (section 1 refuses the terms otherwise);
7. the initiator signed (section 1 refuses the terms otherwise), and the chain's `initiator_signed` signature is a valid SSH
   signature of exactly this run's request (run, base commit, plan and principal), checked when the conditions are judged: the
   chain's hashes do not cover it, and a name beside junk is not an initiator. Whose key it is stays for `staircase verify`.

Otherwise the run ends awaiting review, exactly as in review mode, and says which condition sent it to the owner. Falling back to the
owner is a normal outcome, never a failure.

**What still reaches a person.** Everything the deciders send to a person is parked while away and so stops delivery by condition 3:
a guard (dependencies, a file made executable, a file that is not text, hidden Unicode, what looks like a secret), a sensitive path
(barred in every run, whatever the policy says), drift, a scope violation, a change to a path that holds a check's definition
(`Terms.ProtectedPaths`), a limit or checkpoint, missing or failing evidence, a signal's escalation, and every command or tool call
that was not declared. The mode answers none of them, and it never applies a parked proposal.

**What it delivers in practice.** A rule says "every changed file is under these paths" through the typed conditions of ADR 0013, or
approves one narrow file type. Unattended runs that deliver are therefore documentation, changelog entries, generated or formatted
files, and the areas the owner names with path-aware rules. Code changes in an ordinary away run are approved on evidence, so they
still wait for the morning review exactly as in review mode. Delivery by policy is for work whose correctness the owner already decided
in a rule; evidence-approved code is not that.

### 3. What is recorded

Before `commit_prepared`, a new event, `delivery_authorized`: the `phase_checked` entry, the tree, the approved files' digest, the
terms' digest, the policy's digest and `mode: policy`. The history grammar gains its rules: it follows a `phase_checked` of the same
tree; no request or decision follows it; only the commit-naming and evidence events may. It has a typed payload and a golden test, and
a new barrier point sits at the authorization. Then delivery is the live run's: `commit_prepared`, the move of the branch, the checks
on the commit, the ledger, the certificate. The run ends as a live run that delivered does, with `evidence_failed` when evidence could
not be written; no new run status.

### 4. What the certificate says

- **The level is as computed** (CAL 3 when nothing else lowers it), with no cap. A policy-only live run already gets that level, and
  a cap would make a later person's signature unable to lift the certificate: the verifier lifts a CAL 3 certificate to CAL 4 only
  when a trusted person other than the requester or initiator signed it, and only `cal >= 3` can be lifted.
- A note beside the existing away note: every approved change was approved by a rule of the owner's signed policy (digest), none by a
  person, by the agreed task, by a reviewer model or on evidence alone; the checks passed on the exact tree; **no person reviewed the
  whole change**; only the run branch moved and nothing was pushed.
- One **optional** predicate field, `delivery`, with the value `policy` (omitted for review). The compatibility page allows new
  optional fields; old verifiers ignore it, and a later verifier option can refuse such certificates. The predicate type stays
  version 1, and the specification's certificate section documents the field.
- `decisions` already shows `policy: N` and no `operator`.

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

### 6. The command line and the agreement screen

The command line gains `--deliver review|policy` and a repeatable `--deliver-path`. The agreement screen of a policy run shows the
mode, the delivery scope, the digest of the policy whose rules decide (and says plainly when it is not signed, so the run would be
refused), and the sentence "no person reviews the whole change". A review run's screen is unchanged.

### 7. Crashes

The kill points are the existing barriers plus one: after `phase_checked`, after `delivery_authorized`, after `commit_prepared`, after
the compare-and-swap, after the evidence. Killed before `delivery_authorized`, the run is an interrupted away run, as today. Killed
after it, `staircase recover` finishes the delivery without a person only when the tree it derives is the authorized tree and no
approval needs a person's review; otherwise it refuses and delivers nothing. (As first drafted it asked for a final review, as
recovery does after an approval no person or rule made, ADR 0010, Q23. That cannot be: the grammar allows no decision after the
authorization, so a review could not be recorded, and refusing is the safe side.) The replay also checks that the authorization names
the terms the run was bound to and the signed policy it loaded, so the terms "still say policy" by construction. A run killed
after the authorization is not continued (`staircase resume` refuses and names `recover`); its outcome is blocked. The certificate a
recovery issues for it carries the same `delivery: policy` field and note, at the level a recovery gives (CAL 2 at most, as for any
recovered run). After `commit_prepared`, recovery's existing repair applies: one commit, one certificate.

## Options considered

1. **Advance authorization, conditions, fallback to review, CAL capped.** Rejected: an advance authorization that replaces the final
   review of evidence-approved changes contradicts the specification line that policy delivery cannot waive a mandatory human review,
   and the owner's own documents call that review a person's decision. The cap is not a gate in the default configuration (the Action's
   default `min-cal` is 2), and a capped certificate can never be lifted by a person's signature.
2. **Deliver only what a signed rule approved, with the bars above.** Chosen, tightened with conditions 1, 4 and 7, the complete
   protected-paths declaration, the whole run parking when anything is parked, and no cap. No new authority over what a live run with
   rule approvals already has; it delivers little until rules can name paths (ADR 0013), which is intended.
3. **Deliver on evidence and move the review to the pull request** (ADR 0008's recorded alternative), with the required admission check
   enforcing the certificate. The whole-change review would then be the forge's, not on stAirCase's chain, and it needs a push, which
   this mode must never do.
4. **Record the authorization as a final review** (`yield_decided`, action `final_review`, a new source such as `delivery_policy`).
   No new event, but a final review is a person's decision (glossary), and the rules that read it (which final review counts, Q18;
   when recovery asks, Q23) were written for persons. It has the effect of option 1 under another label.
5. **Do not build it**: review before delivery stays the only mode.

A separate idea, staging the offered review as a commit on a hidden reference with no certificate so a reviewer can use ordinary diff
tools, then promoting it on acceptance, is not part of this decision; it can be a ticket of its own.

## Consequences

- New: `Terms.Deliver` and `Terms.DeliverPaths`, `--deliver` and `--deliver-path`, the refusals above, the agreement lines; and, when
  the delivery is built, the `delivery_authorized` event and its grammar rule, the certificate note and optional field, and the message
  that says why a run fell back to review.
- `docs/away.md` loses "delivery by policy without your review" from "Not yet" when the delivery is built, and says what it is for. The
  policy file, the hook bridge and the certificate predicate type are unchanged (`docs/compatibility.md`).
- Acceptance, from V11-C01 criterion 3: an authorized passing run yields one owned local commit, a certificate that verifies and a
  ledger that rebuilds the tree; anything parked sends it to the owner; nothing outside the repository changes. Criterion 7: kills at
  each point above, then recover: one commit.

## Open questions, answered by the owner (2026-10-20)

1. **May an advance authorization stand in for the review of evidence-approved changes at all?** No. Only what rules of the signed
   policy approved is delivered (option 2).
2. **The level.** As computed, with no cap. A later `staircase sign` lifts it as for any CAL 3 certificate.
3. **Parked proposals.** One parked proposal sends the whole run to the owner.
4. **The delivery scope.** Its own `--deliver-path` list, intersected with the sensitive and protected bars.
