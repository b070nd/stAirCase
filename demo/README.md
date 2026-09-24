# stAirCase Demo

A 60-second, **fully offline** walkthrough of what stAirCase actually guarantees:
an AI agent cannot change your code without a human approving it — and cannot
change anything *other* than what was approved. Every decision lands in a signed,
tamper-evident audit log.

```bash
./demo/run-demo.sh          # interactive — you approve each proposal yourself
./demo/run-demo.sh --auto   # hands-free (approves via curl); used by CI
./demo/run-demo.sh --tamper # proves a change made after approval is refused
```

**Requirements:** `go`, `git`, `curl`. No API key. No network. Nothing is
installed globally — the demo builds the binaries and works in a disposable temp
workspace that is deleted on exit.

## What it does

1. Builds `staircase` and creates a throwaway workspace + target git repo.
2. Registers a vendor, project, a supervisor ⇄ coder topology and a case with
   real CLI commands, and compiles the case to a plan.
3. Starts a run with the **HTTP approval server** (`--approval-port`). The run
   works in its own git worktree; your checkout is never touched.
4. The supervisor hands the task to the coder, whose `create_file` call
   **blocks for human approval**. You see the complete content that would be
   written — the orchestrator derived it and binds the approval to those bytes.
5. You approve (Enter, or `curl`). The tool writes exactly the approved bytes;
   finalize checks the worktree against the approvals and builds the commit from
   them. The supervisor then ends the run.
6. It prints the committed diff and **verifies the signed audit chain**.

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
