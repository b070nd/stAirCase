# Project Use and Current Safety Boundary

stAirCase is pre-1.0. Runs work in their own git worktree, so your checkout is
never touched; still, evaluate on a project you can afford to experiment with, and
keep `STAIRCASE_DIR` outside it. Agents act only through proposals the control
plane decides, but approved shell commands (`--allow-shell-exec`) run as your OS
user without a sandbox — use a restricted container or VM for untrusted work,
without mounting your home directory or unrelated credentials.

## Verified Run Semantics

- `SUCCESS` means the agents finished and finalization succeeded: the worktree
  held exactly the approved state, and the commit on `staircase/run-N` was built
  from the approved bytes. A run with no approved changes succeeds without one.
- A run fails if its worktree holds anything that was not approved (including
  changes made by approved shell commands), if its branch moved, or if an agent
  fails; the reason is on the audit chain. The worktree is kept for inspection.
- Setup, verify, commit, and summary-write failures return a nonzero exit and
  attempt to record `FAILED`. Run and case terminal states are updated together
  in a SQLite transaction. If the database is unavailable, the command still
  fails; its last persisted state may require later recovery.
- A commit hash is retained when delivery succeeded but a later operation failed.
  Inspect it before retrying to avoid delivering the same change twice.
- Successful execution does not prove story acceptance. Pending or invalidated
  stories are not automatically marked implemented; their case remains pending.
- After reviewing the actual changes and running the project's own checks, use
  `staircase story accept <story-id>` to record acceptance. This is an operator
  declaration, not an automatic validator. It requires a successful run of the
  case, is appended to that run's audit chain (bound to its commit), and marks
  the case COMPLETED once every story is accepted.
- Existing `staircase/run-N` branches are never overwritten by a new run.
  `run --reconcile` reports inactive branches for inspection but no longer deletes
  them, even when the associated run failed or its record is missing.
- Blueprints ([docs/blueprints.md](blueprints.md)) are imported as content-hash
  snapshots; a bound case runs only as its blueprint defines it
  (`runtime.plan_pinned`), and `run_bound` records the plan digest and blueprint.
- Drift supervision ([docs/drift.md](drift.md)): changes outside the stories'
  scope, past file limits or at checkpoints go to a human; too many scope
  violations or too long a run halt it until acknowledged; a broken
  `policy.json` stops the run.

## Still Required Before Trusted Project Use

- An OS sandbox for approved shell commands, which today run as your user.
- Acceptance runs against real models and projects. The offline demo exercises
  the real runtime with a stand-in model; it says nothing about model quality or
  complete project acceptance.

No enterprise-readiness or compromised-runtime containment claim should be
inferred from the current test suite.
