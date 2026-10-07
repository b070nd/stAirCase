# Troubleshooting

Start with these two commands. They find most problems and say how to fix them:

```bash
staircase doctor      # the workspace: keys, database, git
staircase gate 1      # everything a run of case 1 needs
```

Below are the messages you are most likely to see, and what to do.

## Before the run starts

**`quality gate check failed - N BLOCK failure(s)`**

A check failed. Run `staircase gate <case-id>`; each failed line says how to fix
it. See [Gates](gates.md).

**`ANTHROPIC_API_KEY missing - printf 'value' | staircase secret set ANTHROPIC_API_KEY`**

A model in the team has no key. Store it as shown. Each model needs its provider's
key: see [Models and API keys](models.md).

**`model "…" (agent "…") has no known provider`**

The model name does not start with a known prefix. Use `claude-…`, `gpt-…`,
`o1-…`/`o3-…`/`o4-…`, `gemini-…`, `grok-…`, or `provider/model` for a gateway.

**`no compiled plan - run 'staircase compile 1'`**

Compile the case. Compile again after every change to the case, its stories or
its topology (`staircase compile 1 --force`).

**`case #1 drifted from blueprint …`** or **`plan was not compiled from blueprint …`**

The case no longer matches its blueprint. Change the blueprint instead, import it
and bind it again. See [Blueprints](blueprints.md).

**`run #N of case #1 was halted for drift`**

The last run was stopped by [drift supervision](drift.md). Look at what happened
(`staircase inspect log N`), then run with `--ack-drift`.

**`run branch "staircase/run-N" already exists`**

The repository already has a branch with this name, often from another workspace.
stAirCase never overwrites it. Look at the branch, then rename or delete it
yourself.

**`load policy: …`**

`policy.json` does not parse, or has a field stAirCase does not know. Fix the
file; the message names the problem. See [Approvals](approvals.md#approving-automatically-with-rules).

**`policy integrity check failed`**

`policy.json` changed after it was signed. If you made the change, sign it again:
`staircase policy sign`.

**`workspace key is locked`** or **`workspace is locked by another process`**

Another `staircase` command (a run, `secret set` or `secret rotate`) is using the
workspace. Wait for it to finish.

**`a key rotation was interrupted`**

`staircase secret rotate` was stopped part way (a crash, a power cut). Whether your
secrets are under the old or the new key cannot be told safely, so nothing reads or
writes them until it is resolved, rather than risk storing a secret under a key that is
about to be dropped. Run `staircase secret rotate` again: it finishes the rotation if
the secrets were already re-encrypted, and otherwise rolls the attempt back and rotates
afresh. No secret is lost either way.

## During the run

**`… has uncommitted changes; the agent works on commit … without them`**

Not an error. The agents work on a separate copy of your **last commit**. Commit
first if they should see your latest changes.

**`Budget cap exceeded - killing run #N`**

The estimated cost reached the project's cap. Raise it with
`staircase project config set <project-id> --budget-cap <dollars>`, or use a
cheaper model. See [Models](models.md#limit-what-a-run-may-spend).

**`webhook approvals are UNAUTHENTICATED`**

The project has a webhook but no shared secret, so an approval could be forged.
Store one: see [Approvals](approvals.md#from-a-service-a-webhook).

**`replay: no recorded response for this … request`**

A run with `--replay-llm` asked something the recording does not contain,
because the prompts, the plan or the files changed. Record again.

## After the run

**The run ended `FAILED` with `approval_content_mismatch`, `unapproved_worktree_change` or `run_branch_moved`**

At the end, the worktree was not exactly what was approved, so nothing was
committed. This is the safety check working. `staircase inspect log <run-id>` shows
which file differed. The failed run's worktree is kept in the workspace
(`worktrees/run-N`) for you to look at.

**The case stays `PENDING` after a successful run**

A commit does not mark a story as done - you do. Accept each story with
`staircase story accept <story-id>`.

**I want to throw a run away**

`staircase case rollback <case-id>` removes the run's branch and worktree and keeps
its audit record.

## A run was interrupted

A crash, a closed terminal or a `kill -9` ends a run before it commits. Nothing unapproved is ever committed, and everything you approved is kept.
Two commands, with different jobs:

- **`staircase resume N`** carries on with the same run: the same task, branch and worktree, under the policy and options it saved when it
  started. What was approved stays approved, and its limits and the time already used still count. Claude Code, Gemini CLI and Codex pick up
  their own session, so the agent remembers its conversation. It changes nothing unless every check passes.
- **`staircase recover N`** does not run the agent again: it commits exactly what the run had approved and stops.

`resume` says why when it refuses, and changes nothing:

| It says | What to do |
|---|---|
| `the worktree of run #N holds changes nobody approved: …` | The agent changed files after its last approval. Look at them, then `staircase resume N --discard-unapproved` puts the worktree back to the approved state. They are never committed either way. |
| `the audit chain of run #N names no … session` or `… whose own session cannot be resumed by staircase` | There is no agent session to pick up (OpenCode has none). `--fresh-context` continues with a new session that is told by the audit chain what is already approved; the chain records that it was a new session. Or `staircase recover N`. |
| `run #N has used up its time limit … only recovered` | `staircase recover N`. |
| `run #N was halted for drift: …` | Read it (`staircase inspect log N`), then `--ack-drift`. |
| `the run branch … is not at the run's base` | The branch moved or was deleted after the run began; `recover` reports what it can. |
| `run #N is still running: its process holds the run's lock, so it cannot be continued` | The run's process is alive. Wait for it, or stop it, then try again. |
| `run #N saved no options` (or `no plan`, `no policy`) `when it started … recover it instead` | The run began before runs could be continued. Only `staircase recover N` is possible. |

## Still stuck?

Run the command again with `--debug` (for `staircase run`) to log every agent
message to the `log/` folder of the workspace, and
[open an issue](https://github.com/b070nd/stAirCase/issues). Remove API keys and
private code from anything you paste. For a security problem, follow
[SECURITY.md](../SECURITY.md) instead.
