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
- **What needs you is parked, not asked.** These would come to you in a normal run: a change without evidence, one outside the scope,
  a sensitive file, a guard hit, a change to where your checks are defined, and a shell command. While you are away they are parked. The
  agent is told at once that the change is kept for you, and to carry on with what it can do without it. The parked proposal is on the
  audit chain with the reason it needed you, and it is never applied by itself.
- **A sensitive path is parked whatever your policy says.** Edits to CI and build files, dependency files, `.env` and shell files,
  `CODEOWNERS` files and Gemini's repository settings (`.gemini/`) are parked even when a rule of your policy approves them, as in a run
  you watch ([who decides, in order](approvals.md#who-decides-in-order)). The reason names the path.
- **The checks cannot be weakened.** The script a check runs, the tests a test runner is given and `make`'s `Makefile` are protected
  with the run's terms, and so is anything you name with `--protect` (for `go test ./...`, use `--protect "**/*_test.go"`). A change
  to them is parked whatever your policy says, so the agent cannot make a failing check pass by editing or deleting the test it runs
  ([checks the agent cannot weaken](approvals.md#checks-the-agent-cannot-weaken)).
- **Delivery by policy: rules you signed can deliver a change to the run branch without your review.** Use `--deliver policy` with
  `--deliver-path <glob>` (repeatable). It is meant for work whose correctness a rule already decides: documentation, changelog entries,
  generated or formatted files, and the places you name with a rule's `paths`. The run is refused unless it is away, has no `--validator`,
  names a delivery scope, signs (`--sign-approvals`) and declares where its checks are defined (`--protect`, or checks that name a file
  of their own). It is also refused at the start if the policy is not signed (`staircase policy sign`). The agreement says "no person
  reviews the whole change".

  When the agent stops and your checks pass, the run delivers by itself **only if all seven of these hold**. They are read from the run's
  verified history, never from live state:
  1. The terms say policy, and the policy was signed.
  2. Every change was approved by a rule of that policy: none by the agreed task, evidence, a reviewer model or a person.
  3. Nothing was parked. One parked proposal sends the whole run to you, because the approved part may depend on it.
  4. Every approved path is inside your delivery scope, and is neither a sensitive path nor one that holds a check's definition, even if
     a rule approves it.
  5. The checks passed on exactly the approved tree, and the run branch is still where the run started.
  6. The protected paths are declared.
  7. You signed as the initiator, so the certificate names who armed unattended delivery.

  The run then records `delivery_authorized` on the audit chain and commits like a run you watch, with its ledger and certificate. If any
  condition fails, or is in doubt, nothing is committed. The run waits for your review, and `staircase decide` shows which condition sent
  it to you. That is a normal outcome, not an error. Changes approved on evidence, which is most code in an ordinary away run, still wait
  for your review.

  The mode only moves the run branch. It never pushes, publishes, releases or deploys. The certificate keeps the level it computes, says
  that every approved change was approved by a rule of your signed policy (by its digest) and that no person reviewed the whole change,
  and has the optional field `delivery: policy`, so a verifier's own policy can refuse such certificates. A run killed after
  `delivery_authorized` is finished with `staircase recover`, never continued ([ADR 0012](adr/0012-delivering-by-policy.md)).
- **The end is checked, not announced.** When the agent stops, your checks run once more on the whole approved change. A change that
  does not pass ends the run failed. One that passes ends it **awaiting review**, with nothing committed and the run branch where it
  started (a run delivering by policy that meets every condition commits instead, as above). The process exits; nothing has to keep
  running.

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

Sometimes the agent's last launch has no known end: its process was killed or crashed, or it did not stop when asked. Nobody knows what
the agent did, including outside the worktree, so the run is parked and not launched again blindly. It shows as **blocked or partial**
(not as running, unless its process still holds the run), with the commands to go on once you have looked:

```text
outcome    blocked or partial: launch 1 of its agent has no established end (its process was lost), so nobody knows what the agent did: check what it could have done, then say you looked with staircase acknowledge 12 (it launches nothing) and resume it, or continue it at once with staircase resume 12 --ack-unknown, or recover what it approved with staircase recover 12
attempts   1 (times the agent was launched: the run, then each continuation); 1 launch(es) ended without an established end
```

`staircase acknowledge 12` records on the audit chain that you looked at that exact launch. It launches nothing. A later `staircase resume
12` (or a hosted runner's own resume, which never acknowledges by itself) then goes on. `staircase resume 12 --ack-unknown` does both at
once. Either way the run counts one more attempt.

If a run's launches ended without a known end 3 times, it is **refused**. No acknowledgement continues it, and the status points to
`staircase recover 12`.

## A run that parked everything

If every proposal was parked and nothing was approved, the run does not end as a success. It is offered to you as a review of an **empty
change** (`change   empty: nothing was approved`). The checks run on the unchanged tree, and the parked proposals are listed with their
numbers. Prototype (**P**) and adopt (`--adopt <n>`) work as above, and an adopted proposal becomes the change you decide on.

Accepting the empty change delivers nothing: no commit, no certificate, and the branch stays at its base. `decide` says so, and the run's
outcome reads failed ("you accepted a review of an empty change: nothing was delivered"). Reject it if you want none of it.

If the project's checks already fail on the unchanged tree, the empty change is still offered and says so (`checks   FAILED on this
unchanged tree`), so the parked proposals are not lost. One of them may be what fixes the checks, and adopting it needs the checks to pass
with it.

## A run that did nothing

If no proposal was approved or parked, there is no change to offer. The run is not a silent success either: the checks run once on the
unchanged tree and the result is recorded (`phase_checked`). The agent may have made proposals that were refused (a path outside the
repository, a rule that rejects), so the message says how many it made, never "none".

- **Your checks fail on the unchanged tree.** The run ends **failed**: `staircase` exits non-zero and keeps the worktree. The message
  says: "Nothing to review, and your checks fail: no proposal was approved or parked (the agent made 2), and your checks fail on the
  unchanged tree (what failed). Nothing was committed. Read what the agent did with `staircase inspect log 12`." Either the project is
  broken at its base, or the task needed changes the agent could not make.
- **Your checks pass.** The run stays a success with no commit, and the message says: "Nothing to review: no proposal was approved or
  parked (the agent made 2), and your checks pass on the unchanged tree. If you expected a change, read what the agent said with
  `staircase inspect log 12`." Its outcome is **no change**.

A run whose earlier segment approved files did not do nothing: it is offered for review as usual. A hosted session (`staircase host`) is a
conversation the runner ends, so it is not checked at the end. Accepting an empty review (above) still ends failed.

## How each run stands

Every run has one **outcome**, read from its verified audit chain alone, never from the status column or a summary file:

| Outcome | What the chain says |
|---|---|
| **running** | The last segment has not ended. The run is working, or its process died (a crash cannot be recorded), in which case `resume` or `recover` it. |
| **awaiting review** | It offered its change, checked on the exact tree, and you have not finished deciding. A run that parked everything and approved nothing offers an empty change. |
| **blocked or partial** | It stopped short and waits for you: the agent's last launch has no known end (see above), drift supervision halted it, it was stopped (cancelled, out of time or budget), or its commit was named and never delivered. |
| **failed** | It ended without a delivery and nothing waits on it: the checks failed on the approved tree (or on the unchanged tree, when nothing was approved or parked), the review was rejected, you accepted an empty change, or the run failed. |
| **no change** | It succeeded without a commit or parked proposals. Nothing was approved or parked, and the checks pass on the unchanged tree, so there is nothing to deliver. A hosted session is not checked, and the reason says so. |
| **delivered** | The commit is on the run branch and the evidence is complete. |
| **delivered with incomplete evidence** | The commit is on the run branch, but some of the ledger, certificate or notes could not be written. The reason is shown. |

With the outcome come the **attempts** (how many times the agent was launched: the claims on the chain, the run then each continuation,
including a launch whose agent never started), what was parked, and the budget:

- the active time used, against the time limit of the run's saved policy and plan;
- the tokens the agents reported (there is no token limit);
- the cost estimated from those tokens at list prices, against the project's budget cap (`staircase project config set --budget-cap`).

`staircase decide` shows this for each run that waits, then lists your other away runs that are still running or blocked. `staircase decide
12` shows it for a run that does not wait. `staircase inspect runs` has an outcome column, and `staircase inspect log 12` shows the status
above the events.

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
  continues on top of a parked change; work of several dependent tasks.
