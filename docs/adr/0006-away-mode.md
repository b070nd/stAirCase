# ADR 0006: Away mode - unattended work that ends in a durable review (design)

- Status: accepted for v1.1 (the thin slice below), with the owner's decisions of 2026-10-14: build the thin slice, approve on
  evidence while away, take Cloudroom's good patterns, offer a Y/N/D/Z decision inbox. Not yet built.
- Date: 2026-10-14
- Related: the v1.1 candidate V11-C01 and the Cloudroom reuse assessment (in the maintainer's protocol), ADR 0005 (continuation).

## Context

A person who wants to leave an agent working cannot today. A run stops at every decision that needs a person, and a run that approved
changes by evidence or by the agreed task waits, at the end, for the final review of the whole change. Nothing the agent can still do
while it waits gets done, and nothing survives the run's process: close the terminal and the run is killed (it can be recovered).

What exists and is reused, not rebuilt:

- **Approval on evidence** (`--approve-on-evidence`): an in-scope change is approved when the `--check` commands pass on the state it
  would produce (and the `--validator` models agree); otherwise it goes to a person.
- **Finalizing from the chain without the agent** (`staircase recover`): the approved files are derived again from the base commit and the
  verified audit chain, the final review is asked, and the commit goes through `commit_prepared`, the base-ref compare-and-swap and the
  evidence, under a per-run lock and an operation record that make it happen once.
- **Saved terms** (ADR 0005): a run saves its policy, plan and options when it starts, and a continuation runs under exactly those.

## Decision

### 1. Away is a property of a run, frozen in its terms

`--away` on `staircase claude`, `gemini`, `codex` and `run`. It is saved with the run's terms when the run starts and cannot be changed by
`staircase resume` or by anything the agent says. For v1.1 one work phase is one run: no task graph, no scheduler (section 9).

### 2. While away, a change is approved only on evidence or by policy

`--away` requires at least one `--check` and implies `--approve-on-evidence`. Policy rules still apply. The agreed task alone approves
nothing (no `--approve-in-scope` sampling), and nothing is approved because a person did not answer.

### 3. Nothing waits for a person: what needs one is parked

A proposal that would go to a person (evidence missing, out of scope, a shell command, a sensitive file) is **parked**: the agent is
answered at once with a refusal whose feedback says the proposal is kept for the owner and to go on with what it can do without it, and the
chain records `proposal_parked` with the request's hash. The rule that every shell command needs a person is unchanged; while away such a
command is parked, not run. A parked proposal is never applied later by itself: it is information for the owner (section 6), and a later run
can be started from it.

### 4. Acceptance is bound to the exact tree, not to the agent saying it is done

When the agent stops, the required checks run once more, in the sandbox, on the exact approved tree. The chain records `phase_checked`:
the tree's hash, each check's definition digest, exit status and duration. The agent ending its turn, a Stop hook allowing it, or the
retries running out is not acceptance. A check result is usable only for the tree and the check definitions it names.

### 5. The run ends in a durable "awaiting review", and its process exits

With approved changes and passing checks, the run records `awaiting_review` (the approved files' digest, the tree, the `phase_checked`
entry it rests on, the number of parked proposals), ends with the new status `AWAITING_REVIEW` and exits. Nothing is committed and the run
branch stays at its base. With failing checks the run ends `FAILED`, with its record; with nothing approved it ends as today. Delivery mode
in v1.1 is **review before delivery** only; policy-based local delivery is deferred (section 9).

### 6. The decision inbox: `staircase decide`, with Y, N, D and Z

`staircase decide` lists what waits for the owner as a tree: project, then run, then its change (files, lines, check results) and its
parked proposals. On a run awaiting review:

| Key | Meaning |
|---|---|
| **Y** or **I** | Accept: finalize the run (below). |
| **N** or **O** | Reject: record `review_rejected`; the run ends `FAILED`; nothing is committed. |
| **D** | Defer and show alternatives: nothing is recorded, the run stays awaiting review; the parked proposals and the per-file diff are shown. |
| **Z** | Clarify: open a session with a restricted context (only this run's change, its evidence, its parked proposals and its task) in a disposable directory with no write access to the repository. Nothing said there decides anything; the owner comes back to Y, N or D. |

The same decisions without a terminal: `staircase decide <run> --accept | --reject`, and the approval API.

**Finalize** is the recovery path entered from an awaiting run: the approved files are derived again from the chain; the derived tree must
equal the tree of `awaiting_review`, and the `phase_checked` it names must be for that tree; the owner's decision is recorded (signed if the
run requires signed decisions) as the run's final review; then `commit_prepared`, the compare-and-swap and the evidence, under the recovery
lock and operation record. The agent is not started again. The certificate says the run finished and was reviewed after it ended.

**A decision is bound to what was shown.** The inbox shows the hash of the `awaiting_review` entry; a decision names it, and is refused if the
run's chain no longer ends in that entry (a stale screen, a second inbox, a concurrent `recover`). Same identity, other content: refused.

### 7. Crashes and repetition

The kill points of finalizing are the existing barriers (commit prepared, compare-and-swap, evidence published). A Y given again after a
crash finishes the same operation (as `recover` repairs), never a second commit. A `resume` or `recover` of a run awaiting review is refused:
it has ended, and `decide` is how it goes on.

### 8. What is taken from Cloudroom, and what is not

Taken as patterns (no code is copied; Cloudroom is Apache-2.0 and any later port carries its notices):

| Cloudroom pattern | Here |
|---|---|
| Durable receipts with explicit uncertainty | `proposal_parked`, `phase_checked`, `awaiting_review` and the decisions are chain entries written before the state they describe is acted on; an outcome that cannot be established is reported as unknown, not as success |
| Content-bound retry identity | a decision names the `awaiting_review` entry it was shown; same identity with other content refuses |
| Claim before launch, bounded recovery | finalize takes the recovery lock and writes its operation record before git moves |
| Lifecycle separate from acceptance | the agent's exit is lifecycle; acceptance is `phase_checked` on the exact tree |
| Views separate from work | closing the inbox, or a viewer disconnecting, decides nothing and stops nothing |
| Fault-oriented scenarios | kill during finalize at each barrier, stale decision, decision on a changed chain, checks for another tree |

Not taken: approvals switched off for the agent (Cloudroom runs Claude Code with `--dangerously-skip-permissions` and Codex with
`approvalPolicy: never`), skills that warn and continue when missing, a snapshot trusted as a shortcut past the full history, and a record
counted as matching without comparing its bytes.

**Cloudroom beside stAirCase.** Work done in Cloudroom can be governed today with no new code: `staircase review <branch> --by "Cloudroom
agent"` brings the branch in one file at a time and certifies what the owner approves (CAL 2, changes made elsewhere). A tighter integration,
Cloudroom launching its harnesses with stAirCase's hook settings so that each change is decided before it is made, is a follow-on: whether
Claude Code's hooks still decide under `--dangerously-skip-permissions` has to be probed on a real host first.

### 9. Deferred, in order of value

1. **Bridging a parked decision with a disposable prototype**: a parked proposal can start a throwaway run on its own branch so the owner
   sees the consequence before deciding.
2. Policy-based local delivery (the second delivery mode).
3. Phases of several dependent tasks, one writer, and a durable queue with parking per task.
4. Declared command capabilities while away (this reverses the all-shell-human rule and needs its own ADR and a sandbox on every supported OS).
5. A pinned, versioned Away brief for the agent, with its digest in the terms.
6. Dashboard and hosted views (cursor catch-up, acknowledged replication).

## Consequences

- New audit events (`proposal_parked`, `phase_checked`, `awaiting_review`, `review_accepted`, `review_rejected`) and a new run status.
  The audited history (`auditedHistory`) learns them; a chain from an earlier version is read as before. The certificate predicate and the
  CAL rules do not change: a change approved by evidence and reviewed by a person reaches the level it reaches today with a live final review.
- `staircase recover` and `staircase resume` refuse a run awaiting review and name `staircase decide`.
- The existing interactive final review is unchanged for runs that are not away.

## Acceptance, mapped to tests

| Criterion | Test |
|---|---|
| `--away` is saved with the terms; `resume` cannot change it; it needs a `--check` | terms round trip; refusal without `--check` |
| A proposal that needs a person is parked, the agent is answered at once, the run goes on | stand-in agent proposes in scope (approved on evidence), out of scope (parked), then ends |
| Checks run on the exact approved tree; failing checks end `FAILED` with nothing committed | stand-in with a passing and a failing check |
| The run ends `AWAITING_REVIEW` with the branch at its base and the process gone | run ends, status, branch, no commit |
| Y finalizes once without the agent: commit, certificate, ledger; the commit verifies and rebuilds | `decide --accept`, then verify and rebuild |
| N records the rejection and commits nothing | `decide --reject` |
| A decision for another `awaiting_review` entry, a changed tree or checks for another tree is refused | tampered and stale cases |
| Kill during finalize at each barrier, then Y again: one commit | barrier drill |
| `resume` and `recover` refuse an awaiting run | refusal messages |
