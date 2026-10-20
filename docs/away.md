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
- **What needs you is parked, not asked.** A change without evidence, one outside the scope, a sensitive file, a guard hit, a change
  to where your checks are defined or a shell command would come to you; while you are away it is parked: the agent is told at once that it is kept for you and to go on with what
  it can do without it. The parked proposal is on the audit chain with why it needed you. It is never applied by itself.
- **A sensitive path is parked whatever your policy says.** An edit of a CI or build file, a dependency file, an `.env` or shell file,
  a `CODEOWNERS` file or Gemini's repository settings (`.gemini/`) is parked even when a rule of your policy approves it, as in a run you watch
  ([who decides, in order](approvals.md#who-decides-in-order)). The reason on the parked proposal names the path.
- **The checks cannot be weakened.** The script a check runs, the tests a test runner is given and `make`'s `Makefile` are protected
  with the run's terms, and so is what you name with `--protect` (for `go test ./...`, `--protect "**/*_test.go"`). A change to them
  is parked whatever your policy says, so the agent cannot make a failing check pass by editing or deleting the test it runs
  ([checks the agent cannot weaken](approvals.md#checks-the-agent-cannot-weaken)).
- **The end is checked, not announced.** When the agent stops, your checks run once more on the whole approved change. A change that
  does not pass ends the run failed. One that passes ends it **awaiting review**, with nothing committed and the run branch where it
  started. The process exits; nothing has to keep running.

## Declared commands

An away run parks every command, because a command needs a person. When the work needs a command you can name in advance (a code
generator, a formatter, a build), declare it:

```bash
staircase claude "regenerate the API client and fix what breaks" --allow "src/**" --away --check "go test ./..." \
  --allow-shell-exec --away-command "go generate ./..." --sandbox docker:image=golang:1.26-bookworm
```

A declared command runs without asking only if it is exactly what you wrote, in the worktree's root, and only in a sandbox without
network (`--sandbox docker:...`, or `required` where sandbox-exec or bubblewrap runs); anything else is parked as before. A declared
command must be a whole command (no `;`, `&`, `|`, redirection, `$` or second line). It is recorded as your decision made in advance
(source `declared`, the declaration's digest on `run_bound`), and the files it changes are decided like the agent's (on evidence, or
parked), so such a change reaches CAL 2. Gemini CLI and OpenCode run their commands without a sandbox, so they cannot be given declared
commands ([ADR 0009](adr/0009-declared-commands-while-away.md)). Seen in a real Claude Code run: the declared command ran, an undeclared one was
parked ([record](release-evidence/declared-commands-claude-code-2.1.236.md)).

### Declared MCP tools

A call of an MCP server's tool is decided like a command: never by a policy rule, and while away it is parked, unless you declared that
exact tool: `--away-tool mcp__<server>__<tool>` (repeatable). An approved tool call runs outside stAirCase's sandbox, so a change made
with one reaches CAL 2, and the certificate says how many calls were approved.

## When you are back: `staircase decide`

```text
Awaiting your review
└─ run #12   review 1a2b3c4d5e6f   from abc123abc123
   ├─ outcome    awaiting review: its change passed its checks on tree fedcba987654, with 1 proposal(s) parked: decide it with staircase decide
   ├─ attempts   1 (times the agent was launched: the run, then each continuation); 1 proposal(s) parked for you
   ├─ time       24m10s of 1h0m0s, 35m50s left
   ├─ tokens     120000 in, 30000 out, as the agents reported them (no token limit)
   ├─ cost       about $0.81 of $5.00 (the project's cap per run), about $4.19 left, estimated from list prices
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
| **P** | Prototype a parked change: pick one, and see what it would do on top of the change on offer, its diff and your checks on the result, in a checkout that is thrown away. One that passes can be adopted into the change as your approval; you then decide on the new review. |

Without a terminal: `staircase decide 12 --accept` or `--reject`, with `-m` for why, and `staircase decide 12 --adopt 4` to adopt
parked proposal #4 (as the tree numbers them) if your checks pass with it. A decision is bound to the review you saw:
`--shown <entry>` (the review entry the list prints) is refused if the run's review is another one.

## A run whose agent was lost

A run whose agent's last launch has no established end (its process was killed or crashed, or the agent did not stop when asked) is
parked, not launched again blindly, because nobody knows what the agent did, outside the worktree included. It shows as **blocked or
partial** (not as running, unless its process still holds the run) with the command to continue it after you looked:

```text
outcome    blocked or partial: launch 1 of its agent has no established end (its process was lost), so nobody knows what the agent did: check what it could have done, then continue it with staircase resume 12 --ack-unknown, or recover what it approved with staircase recover 12
attempts   1 (times the agent was launched: the run, then each continuation); 1 launch(es) ended without an established end
```

Once you acknowledge it with `--ack-unknown` the run goes on and counts one more attempt. A run whose launches ended without an
established end 3 times is **refused**: no acknowledgement continues it, and the status points to `staircase recover 12`.

## A run that parked everything

An away run in which every proposal was parked and nothing was approved does not end as a success. It is offered to you like any
other run: a review of an **empty change** (`change   empty: nothing was approved`), with the checks run on the unchanged tree and the
parked proposals listed with their numbers. Prototype (**P**) and adopt (`--adopt <n>`) work as above, and the adopted proposal is then
the change you decide on. Accepting the empty change itself delivers nothing: no commit, no certificate, the branch stays at its base,
`decide` says so, and the run's outcome reads failed ("you accepted a review of an empty change: nothing was delivered"). Reject it
to say you want none of it. If the project's checks fail on the unchanged tree itself, the empty change is still offered, and says so
(`checks   FAILED on this unchanged tree`), so the parked proposals are not lost: one of them may be what fixes the checks, and adopting
it requires the checks to pass with it.

## A run that did nothing

An away run in which no proposal was approved or parked has no change to offer, so it is not offered for review. It is not a silent
success either: the checks run once on the unchanged tree and the result is recorded (`phase_checked`). The agent may have made proposals
that were refused (a path outside the repository, a rule that rejects), so the message says how many it made, never "none".

- **Your checks fail on the unchanged tree.** The run ends **failed**: `staircase` exits non-zero and keeps the worktree. It says
  "Nothing to review, and your checks fail: no proposal was approved or parked (the agent made 2), and your checks fail on the unchanged
  tree (what failed). Nothing was committed. Read what the agent did with `staircase inspect log 12`." Either the project is broken at
  its base or the task needed changes the agent could not make.
- **Your checks pass.** The run stays a success, with no commit, and says "Nothing to review: no proposal was approved or parked (the
  agent made 2), and your checks pass on the unchanged tree. If you expected a change, read what the agent said with `staircase inspect
  log 12`." Its outcome is **no change**.

A run whose earlier segment approved files is not a run that did nothing: it is offered for review as always. A hosted session
(`staircase host`) is a conversation the runner ends, so it is not checked at its end. Accepting an empty review (above) still ends
failed.

## How each run stands

Every run has one **outcome**, read from its verified audit chain alone (never from the status column or a summary file):

| Outcome | What the chain says |
|---|---|
| **running** | its last segment has not ended: it is working, or its process died, which a crash cannot record (`resume` or `recover` it) |
| **awaiting review** | it offered its change, checked on the exact tree, and you have not finished deciding; a run that parked everything and approved nothing offers an empty change |
| **blocked or partial** | it stopped short and waits for you: its agent's last launch has no established end (see below), drift supervision halted it, it was stopped (cancelled, out of time or budget), or its commit was named and never delivered |
| **failed** | it ended without a delivery and nothing about it waits: its checks failed on the approved tree (or, when nothing was approved or parked, on the unchanged tree), its review was rejected, you accepted an empty change, or it failed |
| **no change** | it succeeded without a commit and without parked proposals: no proposal was approved or parked, and its checks pass on the unchanged tree, so there is nothing to deliver (a hosted session is not checked, and its reason says so) |
| **delivered** | its commit is on the run branch and its evidence is complete |
| **delivered with incomplete evidence** | its commit is on the run branch, and some of its ledger, certificate or notes could not be written (the reason is shown) |

With it come the **attempts** (how many times the agent was launched: the claims on the chain, the run then each continuation, a launch whose agent never started included), what was parked, and the
budget: the active time used against the time limit of the run's saved policy and plan, the tokens the agents reported (there is no
token limit), and the cost estimated from those tokens at list prices against the project's budget cap (`staircase project config
set --budget-cap`, as it is set now). `staircase decide` shows this for each run that waits, then lists your other away runs that are
still running or blocked; `staircase decide 12` shows it for a run that does not wait; `staircase inspect runs` has an outcome
column and `staircase inspect log 12` shows the status above the events.

## What it guarantees, and what not yet

- The commit holds exactly the files the chain says were approved, derived again from the base commit, and the tree must be the one
  whose checks passed. Accepting again after an interruption finishes the same commit; it never makes a second one.
- The certificate cites the checks that ran on that tree and says the change was made while you were away and accepted afterwards.
- `staircase resume` and `staircase recover` refuse a run that awaits review: it has ended, and `decide` is how it goes on.
- Seen with a real agent: one Claude Code run (2.1.236, haiku), from `--away` to `decide --accept` and a fresh-clone verify and rebuild
  ([record](release-evidence/away-claude-code-2.1.236.md)).
- Seen with a real agent too: P on what Claude Code parked, adopted through the real prompt, accepted and verified
  ([record](release-evidence/prototype-claude-code-2.1.236.md)).
- Adopting a parked proposal ([ADR 0007](adr/0007-prototype-bridging.md)) is recorded as what a live approval would have written, so the
  delivered commit verifies and rebuilds like any other; it is refused, with nothing recorded, when your checks fail with it.
- Not yet: a run that requires signed decisions cannot be away (the review in `decide` is not signed); a prototype in which the agent
  continues on top of a parked change; work of several dependent tasks; delivery by policy without your review.
