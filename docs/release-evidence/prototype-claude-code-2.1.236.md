# A real prototype and adoption with Claude Code 2.1.236 (2026-10-14)

What was asked: does P in `staircase decide` ([ADR 0007](../adr/0007-prototype-bridging.md)) work on what a real agent parked, through the real
prompt, to a commit a reviewer can verify?

**Setup.** stAirCase built from `master` at `7538871`; Claude Code 2.1.236, model haiku, headless, its OAuth token given only through
`--pass-env`, with a home folder of its own; a new repository and workspace, no policy file. The same away task as the
[earlier record](away-claude-code-2.1.236.md): `src/greeting.txt` inside the scope (`--allow "src/**"`, `--check "grep -qx hello src/greeting.txt"`)
and `notes.txt` outside it.

**The run** took 14 seconds: `src/greeting.txt` approved on evidence, `notes.txt` parked as outside the scope, the run awaiting review.

**The decision**, through the interactive prompt of `staircase decide` (keys typed: P, 1, y, Y):

```text
└─ run #1   review 089ec786e4c0   from 59581f81d35f
   ├─ change   1 file(s): src/greeting.txt
   ├─ checks   passed on exactly this change (tree 1efc9e1693d1): grep -qx hello src/greeting.txt (exit 0)
   └─ parked   1 proposal(s) that needed you:
      └─ #2 notes.txt: outside the stories' scope: notes.txt
run #1: ... > p
   1) notes.txt: outside the stories' scope: notes.txt
   which one? > 1
   prototype of notes.txt on top of the change:
   check grep -qx hello src/greeting.txt: exit 0
   | +remember to deploy
   adopt it into the change? [y/N] > y
   adopted: the change now includes notes.txt; this is a new review
└─ run #1   review 213695fb9e7f   from 59581f81d35f
   ├─ change   2 file(s): notes.txt, src/greeting.txt
   ├─ checks   passed on exactly this change (tree 4eaae02a58b4): grep -qx hello src/greeting.txt (exit 0)
   └─ parked   nothing
run #1: ... > y
✅ Run #1 accepted: committed fa6000038647 on staircase/run-1
```

The commit holds `README.md`, `notes.txt` and `src/greeting.txt`. A fresh clone with only the public key: `valid change certificate, CAL 3`,
`rebuilt from 2 approved proposal(s): tree 4eaae02a58b4 is identical`, decisions: 1 by evidence, 1 by operator, 1 parked.

**The audit chain:** evidence (6), parked (8), `phase_checked` and `awaiting_review` of the first tree (12, 13), then the adoption: a
`yield_request` naming parked proposal 2 (15), the owner's `yield_decided` (16, source `operator`, `adopts` 2), `phase_checked` and
`awaiting_review` of the new tree (17, 18), `review_accepted` (19), `certificate_issued` (20), `review_delivered` (21).

**What it does not show:** one run, one small model, a parked change whose prototype passed. A failing prototype, a parked command and an
interrupted adoption are covered by tests, not by a real run.
