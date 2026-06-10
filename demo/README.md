# stAirCase Demo

A 60-second, **fully offline** walkthrough of what stAirCase actually guarantees:
an AI agent cannot change your code without a human approving it — and cannot
change anything *other* than what was approved. Every decision lands in a signed,
tamper-evident audit log.

```bash
./demo/run-demo.sh          # interactive — you approve the change yourself
./demo/run-demo.sh --auto   # hands-free (approves via curl); used by CI
./demo/run-demo.sh --tamper # proves a tampered edit is blocked and audited
```

**Requirements:** `go`, `python3`, `git`, `curl`. No API key. No network. Nothing
is installed globally — the demo builds the binary and works in a disposable
temp workspace that is deleted on exit.

## What it does

1. Builds `staircase` and creates a throwaway workspace + target git repo.
2. Registers a vendor, project, topology, and case with real CLI commands.
3. Starts a run with the **HTTP approval server** (`--approval-port`).
4. An agent proposes creating a file and **blocks for human approval**. The
   request carries a SHA-256 of the exact content that will be written.
5. You approve (browser or `curl`). The orchestrator applies the edit, then
   **re-hashes the file before commit** and refuses to commit if the bytes
   differ from what you approved.
6. It prints the committed diff and **verifies the signed audit chain**.

### The agent is a stub — on purpose

`demo/agent_stub.py` speaks the **real** stAirCase IPC protocol (the same wire
format the generated LangGraph runtime uses) but contains no LLM call. That is
what makes the demo deterministic and key-free. The governance layer it
exercises — HITL approval, approval-content binding, the signed audit chain,
the git blast-radius branch — is the real production code, unchanged.

### Want a real model?

Use `demo/record-replay.sh` to record a real conversation once (needs
`ANTHROPIC_API_KEY` and a full `staircase init`), then replay it offline and
deterministically with `staircase run --replay-llm demo/replay.json`.

## The `--tamper` proof

With `--tamper`, the stub writes **different bytes after approval** than it got
approved for. The run fails, nothing is committed, and an
`approval_content_mismatch` event is written to the audit chain:

```
✓ Run FAILED as expected — the post-approval edit was rejected.
✓ Nothing committed.
    event: approval_content_mismatch  file=GREETING.md
    approved_hash=86af7b957840e128…  actual_hash=03827a12d3128356…
```

This is the headline security control: **approval is bound to content, not to a
preview.** An agent cannot get a benign diff approved and then write something
else.
