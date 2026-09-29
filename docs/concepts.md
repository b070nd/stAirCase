# Concepts

This page explains the ideas behind stAirCase and the words the other pages use.
Read it once; the rest of the documentation assumes it.

## The idea in one paragraph

AI agents can write code. stAirCase makes sure that **no change reaches your
repository unless a person (or a rule you wrote) approved it**, and that you can
**prove afterwards** what was approved, by whom, and what was committed. The agents
work in a separate copy of your repository. Every change they want to make is a
*proposal*. stAirCase decides each proposal, records the decision, and at the end
commits exactly the approved bytes - nothing else - on a new branch.

## How a run works

```
 you describe the work      stAirCase plans it        agents work, you approve         you get
 ─────────────────────▶   ─────────────────────▶   ─────────────────────────────▶   ─────────────────
 case + stories + PRD        compile → plan            proposals → decisions            a branch with
 (or a blueprint)            gates check it            (recorded, signed)               only approved bytes
```

1. **Describe the work.** A *case* holds a PRD (what to build) and *stories* (what
   "done" means). You can write these by hand or load them from a *blueprint*.
2. **Compile.** `staircase compile` turns the case and its agent *topology* into a
   *plan* - one file that says exactly what will run.
3. **Check.** *Gates* check that everything needed is in place (keys, plan, git).
4. **Run.** `staircase run` creates a *worktree* (a separate copy of your
   repository) and starts the agents there. Each time an agent wants to change a
   file or run a command, it sends a *proposal* and waits.
5. **Decide.** stAirCase refuses proposals it cannot apply safely, then asks - in
   order - *drift supervision*, your *policy*, an optional *validator* model, and
   finally a person. Every decision is written to the *audit chain* before the agent
   sees it.
6. **Commit.** When the agents finish, stAirCase checks that the worktree holds
   exactly what was approved and commits it on a branch named `staircase/run-N`.
   Your own checkout is never touched.
7. **Accept.** You review the branch and *accept* each story. When all stories are
   accepted, the case is *completed*. You can export the signed audit chain as
   evidence.

## Glossary

**Workspace** - the folder where stAirCase keeps its state: a SQLite database,
encryption and signing keys, plans, worktrees and logs. It lives outside your
repositories. Default: `~/.staircase-workspace` (change it with `--dir` or the
`STAIRCASE_DIR` environment variable).

**Vendor** - a name that groups projects, for example a client or a team.

**Project** - one repository that agents work on. It belongs to a vendor and points
to the repository on disk (`--source`).

**Component** - an optional named part of a project, for organising agents.

**Project dependency** - "project A depends on project B". A gate warns when B
has no successful run with its current topology yet. See `staircase dag viz`.

**Case** - one unit of work in a project: a PRD plus stories. Status: `PENDING`,
`RUNNING`, `COMPLETED` or `FAILED`.

**PRD** - the product requirements: plain text (or JSON) that tells the agents what
to build.

**Story** - one thing that must be true when the work is done. Status: `PENDING`,
`IMPLEMENTED` (a person accepted it) or `INVALIDATED` (it must be checked again).
A story can have a *scope*: the file paths its work may change.

**Topology** - the team of agents for a project: a *supervisor* that starts and
routes the work, the other *agents*, and the *edges* that say who may hand work to
whom. Topologies are versioned; a new version never changes an old one.

**Agent** - one model with a role (its instructions). Each agent can use a different
model provider.

**Blueprint** - a topology, cases and stories kept as files in **their own**
repository, imported into stAirCase as a snapshot named by its content hash.
Binding a project to a blueprint creates its cases; a bound case can only run as the
blueprint defines it. See [Blueprints](blueprints.md).

**Plan** - the compiled form of a case (`tmp/plan_case<N>.json` plus a checksum).
A run executes the plan exactly; if the plan is changed after compiling, the run
refuses to start.

**Gate** - a check that must pass before a run starts. There are built-in gates and
your own *plugin gates*. See [Gates](gates.md).

**Run** - one execution of a plan. Status: `RUNNING`, `SUCCESS`, `FAILED` or
`KILLED`.

**Worktree** - a separate git checkout of your repository that a run works in, on
its own branch. Your checkout, index and uncommitted files are never touched.

**Proposal** (in the code: *yield*) - an agent's request to change a file (create,
edit or delete) or to run a shell command. The agent waits for the decision.

**Approval** - the decision on a proposal. An approval is bound to **exact bytes**:
stAirCase itself computes what the approved change produces, and only those bytes
can be committed.

**Policy** - rules in `policy.json` that approve or reject some proposals
automatically, plus limits. Shell commands are never approved by policy.

**Drift supervision** - checks that keep a run on its stories: changes outside the
stories' scope, too many files, or regular checkpoints go to a person; too many
out-of-scope attempts or too long a run stop it. See [Drift supervision](drift.md).

**Validator** - an optional model that reviews in-scope file changes in place of a
person, with a person approving the final result. See [Approvals](approvals.md).

**Audit chain** - the list of every event of a run (proposals, decisions,
violations). Each event includes the hash of the one before, so a change anywhere
breaks the chain. `staircase audit export` writes a signed copy. See
[Audit evidence](audit.md).

**Secret** - a value such as an API key, stored encrypted in the workspace and given
only to the code that needs it. Secrets never appear in logs or in the audit chain.

## What stAirCase is not

- **Not a sandbox.** Agents run inside the `staircase` process as your user;
  only their approved shell commands run in an OS sandbox. stAirCase controls what reaches your repository, not
  what a process can do on your machine. See [Safety boundary](safety.md).
- **Not a code reviewer.** It makes sure *you* reviewed and approved every change,
  and it proves that you did. Whether the change is good is still your judgement.
