# stAirCase Demo

A 60-second, **fully offline** walkthrough of what stAirCase actually guarantees:
an AI agent cannot change your code without a human approving it — and cannot
change anything *other* than what was approved. Every decision lands in a signed,
tamper-evident audit log.

```bash
./demo/run-demo.sh          # interactive — you approve each proposal yourself
./demo/run-demo.sh --auto   # hands-free (approves via curl); used by CI
./demo/run-demo.sh --tamper # proves a change made after approval is refused
./demo/run-demo.sh --drift  # proves an agent wandering off its stories is stopped
```

**Requirements:** `go`, `git`, `curl`. No API key. No network. Nothing is
installed globally — the demo builds the binaries and works in a disposable temp
workspace that is deleted on exit.

## What it does

It is the project's offline end-to-end acceptance run (`make demo` runs all
three modes in CI), and it checks every step instead of just printing it:

1. Builds `staircase` and creates a product repository with a README, an
   obsolete notes file and some **uncommitted work** of the developer's.
2. Puts the [`hello` blueprint](../examples/blueprints/hello/blueprint.yaml) in
   **its own git repository**, imports it (a content-hash snapshot with its
   source commit) and **binds** it to the project: a topology, one case, two
   stories, each with the paths it may change.
3. Compiles the case (the `runtime.plan_pinned` gate confirms the plan is the
   blueprint's) and starts a run with the **HTTP approval server**. The run works
   in its own git worktree.
4. The supervisor hands the work to the coder, who **creates** `GREETING.md`,
   **edits** `README.md` and **deletes** `OLD_NOTES.md` — each call blocks for
   your approval and shows exactly what would be applied.
5. Finalize checks the worktree against the approvals and commits exactly the
   approved bytes. The demo checks the run branch holds exactly those three
   changes, that your checkout — HEAD, branch, index, status, every file — is
   byte-for-byte as before, and that the product repository gained nothing but
   the run branch.
6. You **accept both stories**; the case is COMPLETED. The signed audit chain is
   exported and **verified**.

### The model is a stand-in — on purpose

`demo/demotool serve` is a tiny OpenAI-compatible gateway that plays the two
agents deterministically. Everything else is the real product: the Go agent
runtime and its tools, the model client, encrypted workspace secrets, the
approval server, the worktree, finalize and the audit chain.

### Want a real model?

Store a provider key as a workspace secret and use `demo/record-replay.sh` to
record a real run once; replay it offline with `staircase run --replay-llm`.

## The `--tamper` proof

With `--tamper`, after its file is approved the coder asks to run a shell
command that appends to the file. You approve the command — and the run still
fails: the file no longer holds the bytes you approved, so nothing is committed
and the refusal is written to the audit chain:

```
✓ Run FAILED as expected — the post-approval change was refused.
✓ Nothing committed. The refusal is on the audit chain below.
    event: approval_content_mismatch  file=GREETING.md
    approved sha256 1ae04447…, found bf6d6ad6…
```

This is the headline security control: **approval is bound to content, not to a
preview or a command.** Only bytes a human approved reach the repository.

## The `--drift` proof

With `--drift`, after its stories' work the coder keeps proposing files under
`src/`, outside both stories' scope. Even where a policy would auto-approve,
each goes to you marked `DRIFT: outside the stories' scope`; the blueprint
allows 2 such proposals, so the 3rd halts the run (`drift_halt`), and the case
does not run again until you pass `--ack-drift`:

```
🧭 Drift: more than 2 proposals reached outside the stories' scope — halting run #1
✓ run halted (KILLED) and exited 1
✓ the case does not run again until 'staircase run 1 --ack-drift'
```
