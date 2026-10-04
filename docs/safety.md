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
  that; anything else - a file nobody approved, a change after approval, a commit
  made by the agent - fails the run and nothing is committed.
- **Every decision is on record.** Each proposal and decision is written to a
  hash-chained audit log before the agent learns the answer, and can be exported
  with a signature. See [Audit evidence](audit.md).
- **Shell commands are off by default.** Agents can only propose shell commands
  when you start a run with `--allow-shell-exec`, and a person must approve each
  one; rules never approve them automatically.
- **Keys stay out of the model's reach.** API keys are stored encrypted, used only
  to call the provider, and removed from every log and audit record.

## What stAirCase does not do

- **It does not sandbox the agent program itself.** stAirCase, Claude Code, Codex
  and stAirCase's own runtime run as **your user**. What the agents *do* is
  confined: reads and edits only through stAirCase's tools, inside the run's
  worktree, and shell commands and checks in an OS sandbox (`--sandbox auto`, the
  default: macOS `sandbox-exec`, Linux bubblewrap or Landlock; in `staircase claude`
  sessions Claude Code's own sandbox, turned on strictly; Codex's workspace-write
  sandbox). In it a command can write only in the worktree and its own temporary
  folder, has no network, and cannot read the stAirCase workspace or common
  credential folders (`~/.ssh`, `~/.aws`, `~/.config/gh`, the agents' own login stores
  such as `~/.claude`, `~/.codex`, `~/.gemini` and OpenCode's, and others, `~/.vault-token`
  and shell history among them). On macOS and with bubblewrap it cannot signal your other
  processes (with bubblewrap it does not see them at all); the Landlock engine does not yet
  restrict that, so a command there can signal and inspect your other processes. It can read
  the rest of your files, and what it prints goes to the model. Where no sandbox is
  found, `auto` runs commands as you and says so; `--sandbox required` refuses
  them. Files a command changes come to you afterwards: approved, they are kept;
  rejected, they are reverted.
- **It does not judge quality.** A successful run means "exactly what was approved
  was committed", not "the story is done". That is your decision:
  `staircase story accept`.
- **It does not see inside the agent.** A model whose context was manipulated (a
  prompt injection in a file, an issue or a web page it read) keeps proposing
  changes that may look harmless one by one. stAirCase decides each change and flags
  some risky ones ([guards](approvals.md#who-decides-in-order)), but it cannot tell why the agent wants it.
- **It does not understand meaning.** [Drift supervision](drift.md) checks paths,
  file counts and time. An in-scope change can still do something no story asked
  for. The [validator](approvals.md#letting-a-model-review-changes-the-validator)
  is a model and can be wrong.
- **It sends code to your model provider.** A real run sends the PRD, the stories,
  a map of the repository and the files the agents read. Nothing else leaves the
  machine - unless you anchor evidence in the public Rekor log: the record is sent
  to that service, which keeps its hash and your public key for good
  ([details](audit.md#add-an-outside-witness-rekor)).

## How to use it safely today

1. **Try it on a project you can afford to experiment with.** Keep the workspace
   (`~/.staircase-workspace`) outside the repository.
2. **Leave shell commands off** unless you need them. When you turn them on, use
   `--sandbox required` so no command runs outside the sandbox. For untrusted work,
   run stAirCase itself in a container or VM that does not mount your home folder
   or unrelated credentials: then the agent program is contained too.
3. **Give stories a scope** so changes elsewhere come to you (`staircase story scope`).
4. **Set a budget cap** per project (`staircase project config set --budget-cap`).
5. **Let checks do the tedious part.** `--check` runs your tests, and security
   scanners such as `gosec ./...`, `semgrep scan --error` or `npm audit`, on the
   exact commit in the sandbox; in Claude Code and Codex sessions the agent cannot
   finish until they pass ([checks](audit.md#checks-on-the-commit)). Scanners that
   download rules need their rules available offline, as the sandbox has no
   network.
6. **Review the branch** before you merge it.

## Confidentiality: pick a profile

Commit integrity (only approved bytes reach the branch) is not confidentiality. What
a run can *read*, and what leaves your machine, depends on how you run it:

| Profile | How | What it protects | What it does not |
|---|---|---|---|
| **Default** | `staircase claude "..."` | commits, worktree isolation, commands without network and without your credential folders and agent logins | the agent program runs as you with the provider's network access; a command can read the rest of your files, and its output goes to the model |
| **Strict commands** | add `--sandbox required`, leave shell commands off | no command runs unsandboxed; on Linux a kernel whose Landlock is older than ABI 3 (before Linux 6.2) counts as having no Landlock sandbox, because it cannot stop a command truncating your files, so install bubblewrap there | the agent program itself; what the agent reads through its own tools still goes to the provider |
| **Contained** | run stAirCase inside a container or VM that mounts only the project and a scratch workspace, with egress limited to the model provider | the agent program and everything it can read | anything you mount, and whatever the provider receives |

The credential list is a deny-list, not an allow-list: a tool that keeps its tokens
somewhere else is readable by a sandboxed command. If that matters to you, use the
contained profile. Nothing here stops what the model is sent from leaving for the
provider: that is what a model run is. Decide per repository whether that is
acceptable before a run, not after.

## What a run's result means

| Result | Meaning |
|---|---|
| `SUCCESS` | the agents finished, and the commit on `staircase/run-N` holds exactly the approved changes. A run with no approved changes succeeds without a commit. |
| `FAILED` | an agent failed, or the worktree held something that was not approved, or the branch moved. Nothing was committed. The reason is on the audit chain and the worktree is kept for you to look at. |
| `KILLED` | the run was stopped: by you, by the budget cap, or by drift supervision. |

If something fails **after** the commit was made (for example writing the run
summary), the commit is kept and shown. Look at it before you run again, so you do
not deliver the same change twice.

**If a run is interrupted** (a crash, a power cut, a killed terminal), the changes it had
approved are not lost: a run keeps each approval, before the agent is told the answer,
and `staircase recover <run-id>` commits exactly those. It takes only what the run's
audit chain also records (a line added to the journal is ignored), derives the files
again from the base commit, and moves the branch only if it is still where the run
started. It verifies the audit chain first, applies the approvals in the order the chain
recorded them (not the order of the journal's lines), and refuses, committing nothing, when
the two records do not fit: an approval the chain holds that the journal lacks or holds with
other bytes, a repeated or corrupt journal line followed by others, a journal that cannot be
read, or a history no run could have written (a decision before the run began, numbers that do
not increase, a decision after the certificate). The one thing it tolerates is what a crash
leaves: a last journal line that was cut short, or written but never audited and so never
released. (A run written before audit chain version 2 can have an event relabelled without the
chain noticing; see [the hash chain](audit.md#the-hash-chain).) If part of the change was approved as part of the agreed task, by evidence or by
reviewer models, you approve the whole change once first, as a finished run would have
asked. The certificate says the run did not finish (CAL 2 at most), the run's record
keeps its KILLED status with the commit named, and the commit can be verified and rebuilt
like any other. `recover` can itself be interrupted: run it again and it finishes (it
recognizes its own commit, by the name it gave the operation in the commit, the base as the
only parent and the tree, and writes what was still missing; a commit it did not make is
never adopted, and never lets the final review be skipped). The commit is made after a
record of the operation is on disk, so a failure to write the ledger, the certificate, the
notes, the audit record or the run's record leaves a delivered commit and an honest list of what is
missing (`--require-evidence` makes that a failure), and running `recover` again repairs it.
The certificate of a recovered run names the policy and the signed request of the person who started
it as the run recorded them, not whatever policy.json holds by then. What recovery cannot do: continue the agent's conversation, or commit
something the run was about to propose.

`staircase run <case-id> --reconcile` lists `staircase/run-*` branches that no run
is using any more (it never deletes them) and marks runs that are still recorded as
running, but no longer are, as stopped.

## Still missing before trusted use

- **Experience with real projects.** The tests and the offline demo check the
  safety mechanics with a stand-in model. They say nothing about how well a given
  model does real work.

Nothing in the current tests should be read as a claim of enterprise readiness.
