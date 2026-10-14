# Working while you are away

Start a task, leave, and come back to one decision. `--away` lets the agent work without asking you anything: what it can do on
evidence it does, what needs you it parks, and when it stops the change waits for your review. Nothing is committed before you
accept it. The design is [ADR 0006](adr/0006-away-mode.md).

```bash
staircase claude "add a /health endpoint with a test" --allow "src/**" --away --check "go test ./..."
# ... later
staircase decide
```

## While you are away

- **Approved on evidence only.** A change inside `--allow` is approved when every `--check` passes on the exact state it would
  produce, as with [`--approve-on-evidence`](approvals.md#approving-on-evidence). Your policy rules still apply. `--away` needs at
  least one `--check`.
- **What needs you is parked, not asked.** A change without evidence, one outside the scope, a sensitive file, a guard hit or a shell
  command would come to you; while you are away it is parked: the agent is told at once that it is kept for you and to go on with what
  it can do without it. The parked proposal is on the audit chain with why it needed you. It is never applied by itself.
- **The end is checked, not announced.** When the agent stops, your checks run once more on the whole approved change. A change that
  does not pass ends the run failed. One that passes ends it **awaiting review**, with nothing committed and the run branch where it
  started. The process exits; nothing has to keep running.

## When you are back: `staircase decide`

```text
Awaiting your review
└─ run #12   review 1a2b3c4d5e6f   from abc123abc123
   ├─ change   2 file(s): src/health.go, src/health_test.go
   ├─ checks   passed on exactly this change (tree fedcba987654): go test ./... (exit 0)
   └─ parked   1 proposal(s) that needed you:
      └─ go.mod: check "go test ./..." failed (exit 1): ...

Keys: Y or I accept, N or O reject, D defer and show the alternatives, Z clarify
```

| Key | What happens |
|---|---|
| **Y** or **I** | Accept: exactly the change on offer is committed on `staircase/run-12` with its certificate. The agent is not started again. |
| **N** or **O** | Reject: nothing is committed. |
| **D** | Defer: nothing is decided, the run keeps waiting; you see what was parked and the command for the whole diff. |
| **Z** | Clarify: Claude Code starts in plan mode (read-only) in a separate directory that holds only this change, its evidence and what was parked. Ask what you need; nothing said there decides anything. You come back to Y, N or D. |

Without a terminal: `staircase decide 12 --accept` or `--reject`, with `-m` for why. A decision is bound to the review you saw:
`--shown <entry>` (the review entry the list prints) is refused if the run's review is another one.

## What it guarantees, and what not yet

- The commit holds exactly the files the chain says were approved, derived again from the base commit, and the tree must be the one
  whose checks passed. Accepting again after an interruption finishes the same commit; it never makes a second one.
- The certificate cites the checks that ran on that tree and says the change was made while you were away and accepted afterwards.
- `staircase resume` and `staircase recover` refuse a run that awaits review: it has ended, and `decide` is how it goes on.
- Seen with a real agent: one Claude Code run (2.1.236, haiku), from `--away` to `decide --accept` and a fresh-clone verify and rebuild
  ([record](release-evidence/away-claude-code-2.1.236.md)).
- Not yet: a run that requires signed decisions cannot be away (the review in `decide` is not signed); a parked proposal cannot be
  turned into a follow-up run from the inbox; work of several dependent tasks; delivery by policy without your review.
