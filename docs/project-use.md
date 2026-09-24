# Project Use and Current Safety Boundary

stAirCase is pre-1.0. Use a disposable clone of a project for evaluation, not your
only working copy or a checkout containing valuable uncommitted work. Keep
`STAIRCASE_DIR` outside that clone. The agent currently runs as your OS user;
a Python virtual environment is not a security sandbox. Use a separately
restricted container or VM for untrusted runtime code, without mounting your
primary checkout, home directory, or unrelated credentials.

## Verified Run Semantics

- `SUCCESS` means the runtime exited successfully and orchestrator finalization
  succeeded. If approved edits changed the index, their commit must succeed.
  A no-op can succeed without creating a commit.
- Setup, staging, commit, and summary-write failures return a nonzero exit and
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

## Still Required Before Trusted Project Use

- An external execution checkout that does not switch or modify the developer's
  active checkout, plus explicit delivery and concurrent-run ownership rules.
- A sandbox if protection against a compromised runtime is required. IPC checks
  cannot prevent same-user Python code from accessing files directly.
- Approval content derived and verified by the control plane. Current hashes come
  from the agent, are optional, and do not independently prove that the displayed
  search/replace proposal describes all committed bytes. Git-index and symlink
  handling also require hardening.
- Immutable, independently versioned blueprints with pinned project bindings;
  a mutable SQLite topology version is not that artifact.
- End-to-end acceptance tests of the generated LangGraph runtime against a real
  project. The offline stub demo verifies selected protocol paths, not model
  quality or complete project acceptance.

No enterprise-readiness or compromised-runtime containment claim should be
inferred from the current test suite.
