# Safety boundary

Read this before you point stAirCase at a project you care about. It says plainly
what stAirCase guarantees, what it does not, and how to use it safely today.

stAirCase is **pre-1.0**.

## What stAirCase guarantees

- **Your checkout is not touched.** Each run works in its own git worktree on its
  own branch, `staircase/run-N`. An existing branch with that name is never
  overwritten.
- **Only approved bytes are committed.** stAirCase computes itself what each
  approved change produces. At the end of a run, the worktree must hold exactly
  that; anything else — a file nobody approved, a change after approval, a commit
  made by the agent — fails the run and nothing is committed.
- **Every decision is on record.** Each proposal and decision is written to a
  hash-chained audit log before the agent learns the answer, and can be exported
  with a signature. See [Audit evidence](audit.md).
- **Shell commands are off by default.** Agents can only propose shell commands
  when you start a run with `--allow-shell-exec`, and a person must approve each
  one; rules never approve them automatically.
- **Keys stay out of the model's reach.** API keys are stored encrypted, used only
  to call the provider, and removed from every log and audit record.

## What stAirCase does not do

- **It is not a sandbox.** stAirCase and its agents run as **your user**. An
  approved shell command can read and change anything you can, outside the
  repository too. Only the changes it makes *inside* the worktree are caught (they
  fail the run).
- **It does not judge quality.** A successful run means "exactly what was approved
  was committed", not "the story is done". That is your decision:
  `staircase story accept`.
- **It does not understand meaning.** [Drift supervision](drift.md) checks paths,
  file counts and time. An in-scope change can still do something no story asked
  for. The [validator](approvals.md#letting-a-model-review-changes-the-validator)
  is a model and can be wrong.
- **It sends code to your model provider.** A real run sends the PRD, the stories,
  a map of the repository and the files the agents read. Nothing else leaves the
  machine — unless you anchor evidence in the public Rekor log, which publishes the
  whole record ([details](audit.md#add-an-outside-witness-rekor)).

## How to use it safely today

1. **Try it on a project you can afford to experiment with.** Keep the workspace
   (`~/.staircase-workspace`) outside the repository.
2. **Leave shell commands off** unless you need them. If you turn them on for
   untrusted work, run stAirCase in a container or VM that does not mount your home
   folder or unrelated credentials.
3. **Give stories a scope** so changes elsewhere come to you (`staircase story scope`).
4. **Set a budget cap** per project (`staircase project config set --budget-cap`).
5. **Review the branch** before you merge it, and run your project's own tests.

## What a run's result means

| Result | Meaning |
|---|---|
| `SUCCESS` | the agents finished, and the commit on `staircase/run-N` holds exactly the approved changes. A run with no approved changes succeeds without a commit. |
| `FAILED` | an agent failed, or the worktree held something that was not approved, or the branch moved. Nothing was committed. The reason is on the audit chain and the worktree is kept for you to look at. |
| `KILLED` | the run was stopped: by you, by the budget cap, or by drift supervision. |

If something fails **after** the commit was made (for example writing the run
summary), the commit is kept and shown. Look at it before you run again, so you do
not deliver the same change twice.

`staircase run <case-id> --reconcile` lists `staircase/run-*` branches that no run
is using any more (it never deletes them) and marks runs that are still recorded as
running, but no longer are, as stopped.

## Still missing before trusted use

- **An OS sandbox** for approved shell commands.
- **Experience with real projects.** The tests and the offline demo check the
  safety mechanics with a stand-in model. They say nothing about how well a given
  model does real work.

Nothing in the current tests should be read as a claim of enterprise readiness.
