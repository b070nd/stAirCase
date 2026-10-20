# ADR 0011: Work phases of several dependent tasks (design)

- Status: accepted (2026-10-20) for the decisions below (the queue, several predecessors, one writer per repository, `--away`
  unchanged); proposed on 2026-10-18. Nothing here is built: the queue is built at the queue ticket, and the single-task phase needs
  only the contract and the optional `phase` field on `run_bound`.
- Related: the v1.1 candidate V11-C01, implementation scope 1 (immutable phase contract) and 4 (durable progress, queue and parking),
  and Cloudroom pattern 2 (in the maintainer's protocol); ADR 0005 (continuation), ADR 0006 (Away mode, section 1 and section 9 item 3),
  ADR 0009 (declared commands), ADR 0010 (one replay), ADR 0012 (delivering by policy).

## Context

A **work phase** is a package of work the owner agrees to and then leaves: several tasks, some depending on others, each ending in a
change the owner reviews or that is delivered by policy (ADR 0012). It is not a `RunPhase` (`PhasePreFlight`, `PhaseFinalize` and the
other steps of a run's state machine in `runphases.go`). Today one work phase is one run: `--away` makes one run with no task graph and
no scheduler (ADR 0006, section 1).

What a phase builds on, as the code is now:

- **Terms** (`orchestrator/terms.go`). What a run decides under is plain data, made only through `NewTerms`, saved as `options.json`
  (`termsVersion` 1) and bound on `run_bound` by `terms_sha256`. `loadTerms` refuses saved bytes the chain does not name. The plan and
  the policy a run started with are saved and checked the same way, so a continuation never decides under today's files.
- **Segments** (`orchestrator/segment.go`). A run is one or more segments. A continued segment starts from the run's own base, branch
  and worktree, writes `run_resumed` (segment, chain head, approvals, next number, undecided requests, elapsed time), and carries over
  what the earlier segments used: run time (`spent`), model use (`carry` into the tracker) and the deciders' counts (`resume`). Limits
  are for the whole run; a continuation does not replenish them.
- **Consumption** (`orchestrator/consume.go`). An approval is journaled, then put on the chain, and the run's liveness is checked under
  the chain's append lock. `keepApproval` lets a line with the same number and the same bytes stand and refuses the same number with
  other bytes. That is content-bound retry identity, at the level of one approval.
- **History** (`internal/history`). One fold over a verified chain, with one grammar: `run_bound` first, every decision uses up a
  request, numbers increase, `commit_prepared` seals the agent's part, `certificate_issued`, `run_recovered` and `review_delivered`
  end the chain. It knows the offered review (`awaiting_review` resting on `phase_checked`) and whether it was decided.
- **Ownership.** A run is driven by the process holding `journal/run-N.owner.lock` (`claimRun`), and recovery runs under its own lock
  and an operation record (`recover_op.go`). Two processes cannot drive one run.
- **Dependencies between projects** exist only as gates: `deps.no_cycle` blocks a cycle, `deps.deps_completed` warns when a project
  this one depends on has no successful run at its current topology. That is about projects, not tasks, it warns rather than blocks,
  and "a successful run" is not bound to any content.

What is missing: there is no record above a run. Nothing orders runs, nothing says a run may start only after another run's change was
reviewed or delivered, and nothing bounds automatic restarts: `staircase resume` and `staircase recover` are started by a person, and
nothing counts their attempts. `MaxDoneAttempts` (3, in `done.go`) only bounds how often one agent is sent back before it may stop;
it is permission to end a loop, not acceptance.

**Cloudroom pattern 2** (reference snapshot `d44dd8b`, source reviewed, not run) has what a queue needs: `Manager::retry` refuses an id
reused with other input; accepting a prompt and inserting it in the queue is one durable record; a manual stop persists as a pause
across a restart; queue edits are checked against a revision; dispatch intent is persisted before a turn starts; one turn per session
is active. Its limits, as the assessment records them: a FIFO of prompts is not a dependency-aware scheduler; one turn per session is
not one writer per repository (sessions can share a workspace); parking for a person, eligibility and writer ownership must be Go
rules; an edited queue item must not revise a frozen contract or replenish limits.

## Decision

### 1. A phase is a contract above runs, and each task is one run

The **phase contract** is a new, versioned file the owner is shown and agrees to before anything starts. It holds:

- the phase's identity and the base commit it starts from;
- the tasks: for each, an id, its case and plan (by digest), its scope (the stories' allowed paths) and its checks;
- the dependencies: a task names the task it depends on (`depends_on`); the graph must have no cycle;
- the delivery mode, `review` or `policy` (ADR 0012), one for the whole phase;
- the limits: the phase's wall time, attempts per task and for the phase, the model-use budget for the whole phase, and a finite
  number of tasks;
- the terms every task shares: sandbox, declared commands and tools (ADR 0008, ADR 0009), signing.

Each task runs as one away run whose `Terms` are made from the contract by `NewTerms`, so a contract cannot express what no run can
decide under. Its `run_bound` gains an optional `phase` field: the phase id, the contract's digest, the task id and the attempt. It is
omitted when empty, so a plain away run's chain is unchanged; a golden test in `history/payload_test.go` pins the key.

The contract is immutable. Changing a task, a dependency, the mode, a check, a capability or a limit is a new contract with a new phase
id; the old phase's tasks stay as they ended, and nothing they approved is carried over by the new one except through delivered
commits. When the person who starts the phase has a signing key, they sign the contract's digest, as `InitiatorText` binds a run's
initiator to its run, base and plan.

### 2. The queue is the phase's own audit chain

A phase has a hash chain of its own, in the workspace store, in the format of a run's chain (ADR 0004, version 2), read only through a
verified fold beside `internal/history`, with the same rules: unverified rows are never read, and a log no phase can have written is
refused with the entry that breaks its grammar. Its events:

| Event | Written | Says |
|---|---|---|
| `phase_bound` | before any task | the contract's digest and the base commit |
| `task_claimed` | before the task's run is created | the task, the attempt, its retry key (section 3) and its base commit |
| `task_started` | after the run's `run_bound` | the run that carries the claim |
| `task_settled` | after the run ends | the outcome and the run's entry it rests on (its `awaiting_review`, its `certificate_issued`, ...) |
| `phase_paused`, `phase_continued` | when the owner pauses or continues | a pause survives a restart until the owner ends it |
| `phase_ended` | once | why: all tasks settled, a limit reached, or the owner stopped it |

**Storage (decided).** The phase chain is stored as rows in the workspace database, `phase_event_logs(id, phase_id, seq, event_type,
payload, event_hash, hash_version, timestamp)`, with a unique `(phase_id, seq)`, so a second writer cannot fork it. The claim is
written in the same transaction that creates the run row (`runs` gains the nullable columns `phase_id`, `task_id` and `attempt`), so
the two commit together or not at all (SQLite's atomic commit; the database already runs with `synchronous=FULL`). After a restart,
a claim whose run has no `run_bound` entry never started its agent: it is closed by a `task_abandoned` event, its attempt stays
consumed, and a new claim for the same key may follow. This is built when the queue ticket starts, not before.

The phase chain records intent and order. The runs' chains stay the only authority on what was decided: a `task_settled` counts only
when the run's own verified history says the same thing (delivered means the run's chain names the commit; awaiting review means its
last `awaiting_review` is undecided). A Markdown handoff is a generated view of both, never an input.

### 3. A retry is bound to its content

A task's **retry key** is the SHA-256 of the contract's digest, the task id, the task's base commit and the deliveries of its
predecessor. Then:

- A claim for a key that already has a run continues that run (`Runner.Resume`, same terms, cumulative time and model use) or recovers
  it, and counts an attempt. It never makes a second run of the same key while the first can still go on.
- The same task id with another key (another base, a predecessor delivered differently) is refused, as `keepApproval` refuses a
  number reused with other bytes and `Decide` refuses a decision made on another `awaiting_review` entry.
- An outcome that cannot be established is not guessed. A claim with no `task_started` and no run record, or a run with no
  `run_bound`, never started its agent (`run_bound` is audited in `checkout`, before the agent is set up), so it may be claimed again.
  A run whose owner lock is free was interrupted and is continued under its saved terms, with the checks `Resume` makes today. A run
  whose ownership cannot be known (`RunOwner` returns `known == false`, as on Windows) stops the phase and is shown to the owner.

Attempts are counted from claims, and a claim carries the writer's generation (a random id per phase process, as `launch_claimed`
carries one), so a settle from another generation is refused.

### 4. One writer per repository

A phase is driven by one process, which holds the phase's owner lock (`journal/phase-N.owner.lock`, taken as `claimRun` takes a
run's). It has at most one task run going at a time: one agent writes, in its run's own worktree, on its run's own branch. The
developer's checkout and branches are never written, as today.

The writer is one per repository, not per phase: while a task runs, the phase also holds a repository lock
(`journal/repo-<hash of the repository's real path>.writer.lock`, besides its own owner lock), released when the task's run ends. Two
phases on one repository therefore wait for each other. Relaxing this to one writer per phase later is backward compatible;
tightening it later would not be. Where locks exclude nothing (Windows) a phase is refused.

### 5. Dependent tasks wait for a reviewed or delivered predecessor

A task has at most one `depends_on` in contract version 1, and a contract with a task that names more than one is refused when it
loads. Fan-out (two tasks depending on one) is allowed. A diamond is modelled as a chain until a later join task exists: a task whose
base is a merge commit stAirCase made (for example with `git merge-tree --write-tree`), recorded on the chain with its tree and both
parents, conflicts parked, the checks run on the merge tree, and a person's review before any dependent starts. Merging the
predecessors without that review would deliver bytes nobody approved.

A task is **eligible** when its predecessor's change was delivered (review mode: the owner accepted it and `review_delivered` is on its
chain; policy mode: Go delivered it under ADR 0012), with complete evidence; the phase is not paused; and attempts and budget remain.
A predecessor that awaits review, failed, ended with an unknown outcome or was delivered with incomplete evidence keeps its dependents
blocked. Incomplete evidence is read from the chain: no `certificate_issued` names the commit, or a live run recorded `evidence_failed`
(a delivery through recovery reports missing evidence in its result and records no such event today, so the phase needs it recorded). An independent task goes on while another waits for the owner. A dependent task's base commit is its
predecessor's delivered commit, as the predecessor's chain names it, never a branch's current tip.

What needs a person is parked in the task's run, as in every away run (`deciders.human` while away), and never answered automatically.
The task then ends awaiting review with its parked proposals, and its dependents wait.

When nothing is eligible, the phase's process records why and exits, as an away run exits awaiting review. `staircase phase continue`
starts it again after the owner has decided; deciding a task's review does not start the next task by itself.

### 6. Limits belong to the phase, and are cumulative

Each task run decides under the stricter of the task's limits and what remains of the phase's, as `Limits.Tighter` combines a policy's
and a plan's limits today. What remains is the contract's limit minus what the phase chain and the runs' chains say was used: wall time
from the segments, model use from the usage records a continuation already replays, attempts from the claims. A retry, a pause or a
restart never gives any of it back.

### 7. A one-task phase is today's away run

A contract with one task, no dependency and review mode makes exactly the run `--away` makes now: the same terms, the same chain with
`phase` on its `run_bound`, the same end in `AWAITING_REVIEW`, the same `staircase decide`. `--away` without a contract keeps working
unchanged. A test pins that the two chains differ only in that field and that the certificate is the same.

### 8. Cloudroom pattern 2: what is taken

| Pattern | Here |
|---|---|
| Retry refuses an id reused with other input | the retry key (section 3) |
| Acceptance and queue insertion in one durable record | `task_claimed` on the phase chain before the run exists |
| Pause persisted across restart | `phase_paused` until `phase_continued` |
| Dispatch intent persisted before the turn | `task_claimed` before, `task_started` after `run_bound` |
| One active turn per session | one task run per phase (section 4) |

Not taken: FIFO order as the schedule (eligibility is the dependency rule), queue edits (a change is a new contract), and anything that
answers a permission request. No code is copied; Cloudroom is Apache-2.0 and any later port carries its notices.

## Options considered

1. **The phase's own hash chain** (chosen, stored as in section 2). Durable claims before launch, pauses and order, tamper-evident
   like a run's chain.
   - **Rejected variant: the same chain written in two steps** (the run row first, the claim after, no unique key). There is a window
     between the two writes in which a crash leaves a run without a claim or a claim without a run, and without a unique key two
     processes could fork the chain. The single transaction and the unique `(phase_id, seq)` close both.
2. **No phase state, derived from the runs' chains** (each run tagged with its phase on `run_bound`). Less to build, but a crash between
   creating a run and its `run_bound` loses the attempt, and a pause has nowhere to live.
3. **A journal file**, fsynced, like the approval journal. Simpler than a chain, but not tamper-evident; the approval journal is
   trusted only together with the chain, and a phase journal would have no chain to pair with.
4. **Tasks as segments of one run.** Reuses segments, but a run seals at `commit_prepared` and offers one review for its whole change,
   so a task cannot be reviewed or delivered before the next one starts.
5. **Port Cloudroom's queue.** Another language, a FIFO with no dependency rule, and no verified history: patterns only.

## Consequences

- New: the phase contract format, the phase chain and its fold, an optional `phase` field on `run_bound`, and `staircase phase start`,
  `status`, `pause` and `continue`; `staircase decide` shows a task's phase and what waits on it.
- Run chains, the certificate predicate and the CAL rules do not change. A task's commit has its run's certificate; it may add a note
  naming the phase.
- `KillStaleRuns` marks `RUNNING` runs of a case older than two hours killed when a new run of the case starts; with one writer a phase
  never starts a run while another of its runs is live, and a test pins it.
- Acceptance, from V11-C01: agreement restored unchanged and tampered contracts refused (criterion 1); parking, eligibility and one
  writer (6); kills around claim, start and settle (7); one accepted item per key, same id with other content refused, pause surviving
  restart (9); attempts claimed before launch and bounded (10).

## Failure analysis (why the queue is a chain whose claim shares the run's transaction)

| Failure | What the chosen design does |
|---|---|
| A crash between the claim and the dispatch | The claim and the run row commit together or not at all. After a restart a claim whose run has no `run_bound` never started its agent: `task_abandoned` closes it, the attempt stays consumed, and a new claim follows for the same key. |
| A duplicate claim (`phase continue` twice, a retry, two terminals) | The key is looked up inside the claiming transaction, and a second claim for the same key continues the first run. The unique `(phase_id, seq)` makes a fork of the phase chain impossible. |
| A stale writer that thinks it still owns the phase | The owner lock, dropped by the operating system when its process dies, stops a second live writer. The unique key is a second fence, and the claim's generation refuses a settle from another process. |
| A changed or truncated record | A changed row breaks the hash. A truncated tail is not detected without an exported checkpoint, the same limit as a run's chain (ADR 0004). |

Two other places the queue would have lived were weighed and rejected. State derived only from the runs' chains plus columns on `runs`
keeps pauses and attempt counts in plain rows that no hash covers. A fsynced journal file cannot be updated atomically with the run
row, so a crash between the two can leave a second run possible for one key.

A related hazard is separate from phases and is fixed on its own (decision 8 of the owner's decisions): two runs of one repository that
deliver at the same moment can silently lose one run's certificate or ledger note, because `git notes add` run in parallel on one
notes reference keeps only the last writer's tree. Writing those notes under a repository lock, reading them back and reporting an
unconfirmed note as an evidence failure serves phases too.

## Decisions on the open questions

1. **Where the queue lives**: the phase's own chain, stored as rows in the workspace database (section 2).
2. **Several predecessors**: refused in version 1 of the contract (section 5).
3. **One writer per phase or per repository**: per repository, while a phase runs (section 4).
4. **`--away` itself**: unchanged (section 7).

## Open questions for the owner

5. **Attempts**: the default number per task, and whether a native resume of the agent's own session counts as one.
6. **Compatibility**: should the phase contract join the six contracts of `docs/compatibility.md` now, or after a release has used it?
