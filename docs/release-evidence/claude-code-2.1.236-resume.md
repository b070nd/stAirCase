# Claude Code 2.1.236: a resumed run remembers its conversation (2026-10-14)

What was asked: when `staircase resume` continues a Claude Code run in Claude Code's own session (`--resume <id>`), does the agent
remember what it said before the run was killed? The mechanism (the session id on the audit chain, `--session-id` and `--resume`) had real
probes before; the memory had not been shown, for want of a working credential.

**Setup.** stAirCase built from `master` at `e2838e8`; Claude Code 2.1.236, model haiku, headless; its credential (a long-lived OAuth token
from `claude setup-token`) given only through `--pass-env CLAUDE_CODE_OAUTH_TOKEN`, with a home folder of its own so that nothing of the
owner's `~/.claude` was read or written. A new repository and workspace; no policy, so every proposal came to a person, answered through the
approval API.

**The task**, given once at the start:

> This is a memory test in four steps. Step 1: invent a random code word of exactly 8 lowercase letters that is not an English word, and say
> it once in your reply, written as CODEWORD: followed by the word. Never write the code word into any file, and do not repeat it, except in
> step 4. Step 2: create a.txt containing exactly the line: one. Step 3: create b.txt containing exactly the line: two. Step 4: create c.txt
> containing exactly the code word from step 1 and nothing else. [...]

**Segment 1** (`staircase claude --allow a.txt --allow b.txt --allow c.txt`). The agent's reply began `CODEWORD: flarxing`. It proposed
`a.txt` = `one` (approved) and then `b.txt` = `two`, which was held; with `b.txt` waiting, the whole process tree (stAirCase, Claude Code,
the hooks) was killed with `kill -9`. No process survived.

**Between the segments.** The word `flarxing` was searched for in stAirCase's whole workspace (the audit database, the journal, the plan, the
run's worktree; 17 files) and in the repository: it was in none of them. It was only in Claude Code's own session transcript
(`0e48f137-3c5e-48ae-b977-795c640e9f40.jsonl`), as the agent's reply. The worktree held `README.md` and the approved `a.txt` only.

**Segment 2** (`staircase resume 1`). The agent proposed `b.txt` = `two` and then `c.txt` = `flarxing`; both approved; the run committed
`README.md`, `a.txt`, `b.txt` and `c.txt`. A new session could only have guessed the word.

**The audit chain of the run** (18 entries; the checkpoint verifies):

| Entry | Event | What it says |
|---|---|---|
| 4 | `agent_session` | `claude-code`, id `0e48f137-3c5e-48ae-b977-795c640e9f40`, `started` |
| 5, 6 | `yield_request`, `yield_decided` | `a.txt` = `one`, approved by the operator |
| 7 | `yield_request` | `b.txt` = `two` (the held proposal; never decided in segment 1) |
| 8 | `run_resumed` | `context: native_resume`, carrying approval seq 1 |
| 9 | `agent_session` | the same id, `resumed` |
| 10 to 13 | `yield_request`, `yield_decided` | `b.txt` = `two`, then `c.txt` = `flarxing`, both approved |
| 14 to 18 | `state_emit`, `drift_report`, `commit_prepared`, `certificate_issued`, `run_path` | the commit and its certificate |

**A reviewer's check.** A fresh clone with only the public key: `staircase verify origin/staircase/run-1 --key .signing.pub --min-cal 2
--rebuild` gives `valid change certificate, CAL 3` and `rebuilt from 3 approved proposal(s): tree 057879d8d8cd is identical`.

**What it shows and what it does not.** It shows that a resumed Claude Code session carries its conversation across a `kill -9` of the run,
that stAirCase resumes the session the chain names, and that the chain, not the agent, decides what was approved (`a.txt` was not proposed again).
It is one run with one small model; it does not show how long a session stays resumable, nor what happens when Claude Code cannot find the
session (that path, the refusal and `--fresh-context`, is covered by tests against a stand-in). The check script read the code word from the
transcript; its first version matched the task's own words and was corrected before the resume, which does not change the run.
