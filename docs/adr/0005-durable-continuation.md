# ADR 0005: Continuing an interrupted run (design)

- Status: accepted, with the owner's decisions below. Built for the Go-owned state and the built-in agent (US-008: `staircase resume`, `Runner.Resume`), and for agent harnesses with a fresh grounded context (`--fresh-context`); native session resume (US-009) is not built.
- Date: 2026-10-06

## Context

Today an interrupted run can be **recovered**: `staircase recover` commits exactly the approvals the run had
given, from the audit chain and the approval journal. It cannot be **continued**: the agent's work after the
last approval is lost, and the person starts a new run. The roadmap's phase 4 names the gap ("continuing the
agent itself after a restart is still open").

What exists and must not be weakened:

- the run's state machine, `runMoves`: `PRE_FLIGHT, BRANCH_CREATE, AGENT_START, AGENT_LOOP, FINALIZE,
  BRANCH_RESTORE`, with every other move a failure, and the phases a run went through recorded as `run_path`;
- the **audit chain** (verified as one read, `ListVerifiedEventLogs`) and the **approval journal**, which
  `auditedHistory` and `reconcile` already turn into "what was approved, in order, with its request";
- the **operation identity** of delivery: a commit is named on the chain (`commit_prepared`) before the branch
  moves, recovery has its own operation record and per-run lock, and a commit nothing names is a conflict;
- cancellation (`waitCtx`) and the consumption point of an approval (its decision on the chain, checked under
  the chain's write lock);
- a worktree that must equal the approved state byte for byte before anything is committed.

## Decision

### 1. The chain is the only authority; state is derived by replay, never restored from a snapshot

Everything a run's decisions depend on in memory is already a function of the verified chain plus the journal.
A continuation does not save a second copy of it; it **replays** the chain through one pure function (the one
recovery uses, extended) and builds the state from the result:

| In-memory state | Derived from |
|---|---|
| next proposal number (`deciders.total`) | the highest `seq` of a `yield_decided` on the chain, plus one |
| approved files (`approvals.files`), the ledger | the approvals of the chain, in chain order, derived against the base commit from the journal's requests, each checked against the digest the chain recorded (what recovery does) |
| `autoApproved` (the policy's `max_auto_approved` count) | the count of approved decisions whose `source` is `policy` |
| drift supervisor (yields, distinct files, scope violations, halt) | the `drift` and `files` of the decisions, and `drift_halt` |
| `taskApproved`, validator `unreviewed` | the sources of the decisions (`task`, `validator:*`, `evidence`) and whether an approved final review follows |
| token and cost totals | the `state_emit` usage events |
| elapsed run time (`max_run_secs`) | the sum of the active length of each segment (section 3), a segment ending at its last chain event |
| what the person agreed to, who started it | `task_agreed`, `initiator_signed` (kept, never signed again) |
| the policy | the bytes the run started under (section 2) |

A replay that fails (the chain does not verify, the history is not one a run writes, an approval has no request,
a digest differs) **refuses**, with the same errors recovery gives. Resume and recovery share the replay; they
cannot disagree about what a run approved.

### 2. What a run must save at its start, so that a continuation is under the same terms

Two things are only digests on the chain today and live in files that change:

- the **policy**: `policy.json` is re-read by whoever looks next. A run saves the exact bytes it loaded
  (`journal/run-N.policy.json`, written before `policy_snapshot` is audited, and checked against that digest);
- the **plan** the agent executes: `tmp/plan_caseN.json` is replaced by the next compile. A run saves its bytes the
  same way and checks the digest `run_bound` recorded.

A continuation runs under the saved bytes. A policy that has changed since is a notice, not a change of terms. If
the owner wants a changed policy to apply, the run is not continued: it is recovered and a new run is started. (A
later option may allow only a *stricter* policy; that is a separate decision and is not assumed.)

### 3. Legal moves: a segment of a run

A run is a sequence of **segments**. Segment 1 is the run as it is today. A continuation adds a segment to the
**same run** (same run number, branch, worktree, base commit). The state machine gains one phase and keeps all its
moves:

```
PRE_FLIGHT -> BRANCH_CREATE -> AGENT_START -> AGENT_LOOP -> FINALIZE -> BRANCH_RESTORE      (segment 1, as today)
RESUME     -> AGENT_START   -> AGENT_LOOP   -> FINALIZE -> BRANCH_RESTORE                   (a later segment)
RESUME     -> BRANCH_RESTORE                                                                (the checks failed: refused)
```

`RESUME` is entered only by `staircase resume <run>`. Its `run_path` records `RESUME` where segment 1 records
`PRE_FLIGHT`. There is **no pause phase**: an interrupted run is a run whose last segment has no `FINALIZE`, and
that is derived (the chain has no `run_path`, `certificate_issued` or `run_recovered`), because a crash cannot record
anything. A clean pause (`staircase pause`, a signal) is only the cooperative way to stop the agent and write one
`run_paused` record first; resume treats a paused run and a crashed run identically.

`run_resumed` is audited when the checks pass and before the agent starts: the segment number, the chain head it
resumed from (the **approved generation**), the fingerprint of the approvals, the policy and plan digests, the next
sequence number, the remaining budgets, how the agent's context is carried (section 5), and the number of requests
that were never decided.

### 4. Preconditions: every one is checked, any failure refuses and changes nothing

Resume takes the run's lock (the same per-run lock as recovery, never waiting), then:

1. the run is `RUNNING` (with `--force`, as for recovery, because its process is known to be gone) or `KILLED`, has no
   commit, and the chain has no `certificate_issued`, `run_recovered` or `run_path` after its last segment;
2. **no recovery operation exists** for the run (`journal/run-N.recovery.json`): a run that recovery has begun is
   recovered, not continued; and a recovery of a run with a later segment is refused while resume holds the lock;
3. the chain verifies as one read, the history is legal (`auditedHistory`, extended to accept `run_resumed` and
   `run_paused` after `run_bound`, never after a seal), and `reconcile` pairs every approval with its request;
4. the saved policy and plan exist and match their digests;
5. the branch is still at the base commit and the base commit exists;
6. the worktree **equals the approved state byte for byte**. A file the agent changed after its last approval, or a
   file nobody approved, is not adopted: resume refuses and says which, and `--discard-unapproved` puts the worktree back
   to the approved state first (the same restore a rejected command gets). Resume never stages, adds or commits
   anything it finds;
7. a run that was **halted for drift** is not resumed without the same acknowledgement a new run needs
   (`--ack-drift`); a `max_run_secs` already used up, or a `max_auto_approved` already reached, resumes into the same
   limit (the run sends the next proposal to a person or ends), never into a fresh one;
8. signing keys, allowed signers and sandbox availability that the run's options require are present: a run that
   required signed approvals does not continue without them.

### 5. The agent's context: two models, labelled

The Go side is authoritative: the approved state and the sequence are derived (section 1). What the **agent** remembers
is only the model's memory, and is never trusted as state.

- **Fresh grounded continuation** (always available, any agent, the only one for the built-in graph agent): a new
  agent session starts in the worktree at the approved state with a brief built by Go: the task and plan, the files
  already approved (the ledger's list, not their content unless asked), the feedback of rejected proposals, and what
  remains. It does not claim to be the old conversation.
- **Native resume** (external agents whose program can do it): Claude Code `--resume <session id>` (and `--session-id`
  to choose the id at the start), Gemini CLI `--resume` (`--session-id`), Codex `exec resume <id>`. All three are in the
  installed versions' `--help`. Where the program lets stAirCase choose the session id, it is chosen and audited at the
  start of each segment (`agent_session`); otherwise it is read from the program's own output while the segment runs
  (Codex: to be probed before it is promised). If the id is not on the chain, native resume is not offered.
  The resumed session gets a **new** hook endpoint and token, and the governed settings again: every tool call is
  decided as before, and a program that does not run the hooks is refused as in a first segment.

`run_resumed` says which was used. A native resume that fails to start falls back to a fresh grounded continuation only
if the person asked for the fallback (`--fresh-context`); it is never silent.

### 6. Stale work and old challenges

- **Challenges.** Anything the dead segment had pending (a webhook request, an entry in the approval API, a review
  page, a signed-approval challenge) is dead with it. The approval API's key is new for each segment, a session registers under a new instance id, and a proposal's id is unguessable and
  belongs to the segment that made it (not a number that restarts): a decision that arrives for a request of an earlier segment is refused (`404`),
  as a late answer after the run's end is today. A signature a person made over a proposal is good for that proposal in
  that segment only (it already names the proposal's request hash).
- **Requests with no decision** are legal and counted in `run_resumed`; they are never turned into decisions.
- **Stale agent results.** A hook call or a result that names a tool call of an earlier segment is ignored, because the
  pending table starts empty and the endpoint is new. A repeated effect (the same proposal sent twice) is a new proposal
  with a new sequence number and a new decision; it is never matched to the old one.
- **Sequence.** Journal lines with a number above the chain's last decision are what a crash leaves; they are ignored
  and never become approvals (as in recovery), and the next proposal takes the next number after the chain's, so a number
  is never reused for a different request.

### 7. Delivery and recovery are unchanged

Delivery is the same code with the same identities: `commit_prepared` names the commit before the branch moves, evidence
is written after, `recover` can still finish a run whose later segment died, and a run that is resumed and then killed
again is recoverable from the same journal. The certificate of a run with more than one segment records the
number of segments (`segments`) and the fact that the run was interrupted and continued; CAL is not lowered by a
continuation (every approval is still decided before it is applied), but a segment boundary is shown, as the end-of-run
checks of the first segment's agent did not run.

## Fail closed: what resume does not do

| Case | Result |
|---|---|
| chain does not verify, history illegal, approval without request, digest differs | refused, nothing changed |
| the run has a commit, a certificate, a recovery record, or is `SUCCESS` | refused: nothing to continue |
| worktree differs from the approved state | refused, naming the files; `--discard-unapproved` restores first |
| branch moved, base missing | refused |
| saved policy or plan missing or not matching its digest | refused |
| another resume or a recovery holds the run's lock | refused, never waits |
| native session id not on the chain | native resume not offered; `--fresh-context` is explicit |
| the agent's hooks never ran in the new segment | the segment fails, nothing it did is kept, as in segment 1 |
| a run started by an older version (no saved policy or plan) | not continued: recovered |

## Consequences

- Runs started by this version onward can be continued; earlier runs can only be recovered. This is stated, not hidden.
- The saved policy and plan add two small files per run under `journal/`; `staircase clean` prunes them with the run.
- The replay is the one place that says what a run approved; making resume and recovery share it removes a way for them
  to disagree.
- Continuation does not make an agent's conversation durable; it makes the **governed state** durable. How much of the
  agent's own context survives is a property of the agent program, and the run says which kind it used.
- The local background coordinator (US-007) needs only this interface: "list runs that are interrupted", "take a run's
  lock", "resume with these options", "state of the segment". It adds no authority of its own.

## Acceptance: what building this must pass

Each is a test through `Runner.Run` / `Runner.Resume` with a real repository and store, using the barrier points where a
process is killed:

1. **Replay equals recovery:** for every interrupted history the tests already build, resume's derived approved state and
   recovery's are identical (same files, same tree).
2. **Cumulative limits:** a run with `max_auto_approved: 2` that auto-approved 2 before the kill sends its next proposal
   to a person after resume; `max_run_secs` and cost already used are subtracted; a drift-halted run needs `--ack-drift`.
3. **Sequence:** proposal numbers continue from the chain; an unconsumed journal line above the chain's last decision is
   never an approval and its number is not reused for another request.
4. **Worktree:** after a kill, unapproved changes in the worktree refuse the resume, and `--discard-unapproved` restores
   exactly the approved state; nothing is ever staged by resume.
5. **Stale and replayed:** a decision for an earlier segment's request, a repeated proposal, and a hook call naming an old
   tool call are each refused, and none changes the approved state.
6. **Kill at every boundary** (the named barrier points, plus `resume-checked` and `resume-started`): after a kill the run
   is resumable or recoverable, exactly once, with one owned commit and verifiable evidence; a second resume while the first
   holds the lock is refused.
7. **Delivery:** a resumed run that finishes delivers a commit the chain names, a certificate that verifies and a ledger that
   rebuilds the same tree; a resumed run that is killed again is recoverable.
8. **Context:** a fresh grounded continuation works for each adapter (stand-in), and a native resume of a real Claude Code
   and Gemini CLI session continues a task with new approvals governed (owner-authorized runs); Codex is probed before its
   native resume is promised.
9. **Conflicts:** an existing recovery record, an older run without the saved files, a changed policy file, a moved branch
   and a missing base each refuse with the stated message and change nothing.

## Decisions of the owner (2026-10-06)

1. **Native resume that cannot be performed refuses.** The command says why and changes nothing. Falling back to a fresh
   grounded continuation needs an explicit `--fresh-context`: a silent fallback would let the operator believe the
   agent's conversation continued when it did not.
2. **A stricter policy never applies to a continuation automatically.** A run was started under one policy digest; changing
   the terms mid-run, even to stricter ones, breaks the "same terms" guarantee the saved policy exists for. To tighten,
   recover the approvals and start a new run. An explicit `--allow-stricter-policy` may be added later if a real need
   appears (explicit, audited, never the default); **it is not part of the first version of resume.**
3. **The segment count is recorded on the audit chain only** (`run_resumed` carries the segment number and the approved
   generation). The signed certificate predicate does not change: that would be a contract change for every verifier, the
   rebuild, the independent implementation and the Action. A versioned certificate change can follow real use.
4. **Codex native resume is offered only if a real probe proves it.** Until it is shown that Codex exposes a
   machine-readable session identifier that can be learned and later resumed, Codex gets fresh grounded continuation only.

## What was built (US-008)

`Runner.Resume` and `staircase resume <run>`. A run saves, when it starts, its policy bytes, its plan bytes and its options
(`journal/run-N.policy.json`, `.plan.json`, `.options.json`); a run without them, or whose saved bytes are not the ones its chain names,
is recovered, not continued. The replay (`resume_state.go`) derives the next sequence number, the policy-approved count, the task flag,
the supervisor's decisions (each decision on the chain now records the `paths` it touched), the validator's counts (`validator_state`),
token and cost, and the time earlier segments used. The worktree is checked against the approved state; an approval whose change the
agent had not written yet (it died between the approval and the write, the file still as at the base) is applied as approved,
anything else that differs refuses, naming the files, unless `--discard-unapproved`. A run whose time limit the earlier segments used up
is refused. One phase (`RESUME`) was added to the state machine. Tests: `resume_test.go` (continuation, equality with recovery,
cumulative limits, supervisor, time, worktree, repeated proposals, refusals) and `tests/resume_drill.bats` (real kills at the
named boundaries of the first segment and of the continuation).
