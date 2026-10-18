# Glossary

The words stAirCase's code and documentation use for its own concepts. [Concepts](docs/concepts.md) explains them for a user;
this page fixes what each word means, so a module can be named after one.

**Run**: one attempt at a case. Its agents work in a worktree on the branch `staircase/run-N`. A run that is continued has more
than one **segment**, each starting at `run_bound` or `run_resumed`.

**Audit chain**: a run's events in the order they were written, each hashed over the one before (ADR 0004). It is the only
authority on what a run decided (ADR 0005).

**Verified**: describes a chain that was read and hash-checked as one read (`ListVerifiedEventLogs`). Nothing decides, signs or
delivers from rows that were not verified. Only display commands show raw rows.

**Event**: one entry of the chain. It has a type (`yield_decided`, `run_bound` and so on) and a JSON payload, and it is named by
its hash, its **entry**.

**Proposal**: something an agent asks to do: change files (`file_edit`), run a command (`shell_exec`) or call an outside tool
(`tool_call`). It is audited as a `yield_request` and numbered by the decision that answers it (`seq`).

**Decision**: the answer to a proposal, audited as a `yield_decided` before the agent hears it. Its **source** says who decided:

- `operator`: a person.
- `policy`: a policy rule.
- `task`: the agreed task covers the proposal.
- `evidence`: the run's checks passed on it.
- `validator:<model>`: a reviewer model.
- `declared`: a command the owner declared in advance (ADR 0009).
- `parked`: nobody was there to ask (ADR 0006).
- `orchestrator`: stAirCase refused it, or the run ended first.

**Final review**: a person's decision on a run's whole change, a `yield_decided` of action `final_review`. No request precedes
it.

**Approval**: an approved `file_edit` decision. It carries the digest of exactly the files it approved, and its request is kept
in the approval journal. Only bytes derived from approvals reach the branch.

**Approved state**: the bytes a run's approvals derive. They are derived again from the base commit, in an empty directory, proposal
by proposal in chain order, each checked against the digest the chain recorded for it. What is on disk has no say in it. Recovery,
resume, Prototype and Adopt get it the same way: they open the run (`openRun`) and derive.

**Consumed**: describes an approval whose decision is on the audit chain. Only then does the agent hear of it and may its bytes
reach the branch. It is kept in the journal first, and once the run has ended nothing more is consumed: the decision is recorded as a
rejection (`unconsumed` when its journal line was already written). The live run and an owner's adoption consume the same way
(`consume`, ADR 0005).

**Approval journal**: the requests of a run's approvals, written before their decision is audited
(`journal/run-N.approved.jsonl`). Recovery pairs it with the chain.

**Parked**: a proposal that needed a person while the owner was away. It is rejected for the agent and kept for the owner's
review.

**Adopted**: a parked proposal the owner took into the change after a prototype (ADR 0007). It is recorded as an ordinary
`operator` approval that names the proposal (`adopts`).

**Offered review**: what an away run puts to its owner when it ends. This is the exact tree, its files' digests, and the checks
that passed on that tree (`awaiting_review`, resting on `phase_checked`). It is decided with `staircase decide`.

**Sealed**: describes a chain after which nothing more may be decided. `commit_prepared` ends the agent's part.
`certificate_issued`, `run_recovered` and `review_delivered` end the chain.

**History**: what one replay of a verified chain says about a run: its binding, every decision with its request, the parked and
adopted proposals, the offered review, the continuation's counters, and what sealed it (`internal/history`, ADR 0010). A chain that
no run can have written has no history. It is refused with the entry that breaks the grammar.

**Terms**: what a run decides under, set when it starts and kept by every continuation. They say:
- who agreed to the task;
- how changes are approved;
- where commands and checks run, the checks, and the paths where they are defined (protected);
- what signs;
- what an away run may do without asking.

They are plain data (`orchestrator.Terms`), made only through `NewTerms`, which normalizes them once and refuses what no run can
decide under. A run saves them as its `options.json`, bound by digest on `run_bound`. A continuation also keeps the saved plan and
policy, each checked against the chain the same way. Where a run listens, how it is shown and which agent carries it out are not
terms.
