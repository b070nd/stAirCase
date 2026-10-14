# A real away run with Claude Code 2.1.236 (2026-10-14)

What was asked: does `--away` ([ADR 0006](../adr/0006-away-mode.md)) work with a real agent, from start to the owner's decision?

**Setup.** stAirCase built from `feature/SC-108-away-mode` at `63c72bd`; Claude Code 2.1.236, model haiku, headless, its OAuth token given only
through `--pass-env`, with a home folder of its own; a new repository and workspace, no policy file.

```bash
staircase claude --yes --away --allow "src/**" --check "grep -qx hello src/greeting.txt" --model haiku \
  "Do two things, in order, one file at a time. 1) Create src/greeting.txt containing exactly the line: hello.
   2) Create notes.txt in the repository root containing exactly the line: remember to deploy. If a change is refused, do not retry it; go on and then stop."
```

**The run** took 12 seconds and asked nobody. `src/greeting.txt` was approved on evidence (the check passed on the state it produced);
`notes.txt`, outside the scope, was parked, and the agent was told to go on. When it stopped, the check ran on the approved tree and passed; the
run ended `AWAITING_REVIEW` with its branch at the base commit and its worktree kept.

**The review** (`staircase decide`, as listed):

```text
Awaiting your review
└─ run #1   review e1f73ee4ec91   from f91726ef6523
   ├─ change   1 file(s): src/greeting.txt
   ├─ checks   passed on exactly this change (tree f98c06144927): grep -qx hello src/greeting.txt (exit 0)
   └─ parked   1 proposal(s) that needed you:
      └─ notes.txt: outside the stories' scope: notes.txt
```

`staircase decide 1 --accept` committed `3d90a60a2f7c` on `staircase/run-1` (`README.md`, `src/greeting.txt`; not `notes.txt`) with a CAL 3
certificate, without starting the agent again. A fresh clone with only the public key: `valid change certificate, CAL 3`, `rebuilt from 1
approved proposal(s): tree f98c06144927 is identical`, and the note that the change was made while the owner was away and accepted afterwards.

**The audit chain:** `yield_decided` evidence (6), `yield_decided` parked with `parked_why` "outside the stories' scope: notes.txt" (8),
`done_checked` (9), `phase_checked` passed (12), `awaiting_review` (13), `run_path` (14), `review_accepted` (15), `certificate_issued` (16),
`review_delivered` (17).

**What it does not show:** one run, one small model, one parked reason (scope). Parking for a failing check, a sensitive file or a command,
and the interactive keys, are covered by tests, not by a real run.
